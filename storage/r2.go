package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
)

type R2Config struct {
	AccountID, AccessKeyID, SecretAccessKey    string
	PublicBucket, PrivateBucket, PublicBaseURL string
	// Endpoint overrides the account endpoint for local S3-compatible tests only.
	Endpoint string
}

type R2 struct {
	client                                     *s3.Client
	presigner                                  *s3.PresignClient
	publicBucket, privateBucket, publicBaseURL string
}

var _ Storage = (*R2)(nil)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func NewR2(cfg R2Config) (*R2, error) {
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("R2 credentials are required")
	}
	if !bucketPattern.MatchString(cfg.PublicBucket) || !bucketPattern.MatchString(cfg.PrivateBucket) || cfg.PublicBucket == cfg.PrivateBucket {
		return nil, errors.New("distinct valid R2 public and private buckets are required")
	}
	if err := validateBaseURL(cfg.PublicBaseURL, false); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(cfg.PublicBaseURL, "https://") {
		return nil, errors.New("R2 public base URL requires HTTPS")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		if !accountPattern.MatchString(cfg.AccountID) {
			return nil, errors.New("R2 account ID is required and must be a safe endpoint component")
		}
		endpoint = "https://" + cfg.AccountID + ".r2.cloudflarestorage.com"
	} else {
		u, err := url.Parse(endpoint)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Host == "" {
			return nil, errors.New("invalid R2 endpoint")
		}
		ip := net.ParseIP(u.Hostname())
		local := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return nil, errors.New("R2 endpoint requires HTTPS except for loopback tests")
		}
	}
	client := s3.NewFromConfig(aws.Config{
		Region:                     "auto",
		Credentials:                aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}, func(opts *s3.Options) { opts.BaseEndpoint = aws.String(endpoint); opts.UsePathStyle = true })
	return &R2{client: client, presigner: s3.NewPresignClient(client), publicBucket: cfg.PublicBucket, privateBucket: cfg.PrivateBucket, publicBaseURL: cfg.PublicBaseURL}, nil
}

func (r *R2) PublicURL(key string) (string, error) { return publicURL(r.publicBaseURL, key) }
func (r *R2) bucket(visibility Visibility, key string) (string, error) {
	if err := validate(visibility, key); err != nil {
		return "", err
	}
	if visibility == Public {
		return r.publicBucket, nil
	}
	return r.privateBucket, nil
}

func r2Error(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "404") {
		return ErrNotFound
	}
	// Keep signed URLs, credential identifiers and response bodies out of errors.
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("R2 %s failed", operation)
}

func (r *R2) Put(ctx context.Context, visibility Visibility, key string, upload Upload) (ObjectInfo, error) {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if upload.Body == nil || upload.Size < 0 {
		return ObjectInfo{}, ErrInvalidUpload
	}
	if err := validateContentType(upload.ContentType); err != nil {
		return ObjectInfo{}, err
	}
	// R2's HTTPS transport protects streaming payloads. Signing an unsigned
	// payload also permits copying a non-seekable Get body without buffering it.
	out, err := r.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: upload.Body, ContentLength: aws.Int64(upload.Size), ContentType: aws.String(upload.ContentType)}, func(opts *s3.Options) {
		opts.APIOptions = append(opts.APIOptions, func(stack *middleware.Stack) error {
			return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
		})
	})
	if err != nil {
		return ObjectInfo{}, r2Error("put", err)
	}
	return ObjectInfo{Key: key, Size: upload.Size, ContentType: upload.ContentType, ETag: aws.ToString(out.ETag)}, nil
}

func (r *R2) Get(ctx context.Context, visibility Visibility, key string) (io.ReadCloser, ObjectInfo, error) {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, ObjectInfo{}, r2Error("get", err)
	}
	return out.Body, ObjectInfo{Key: key, Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), ETag: aws.ToString(out.ETag)}, nil
}

func (r *R2) Delete(ctx context.Context, visibility Visibility, key string) error {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return err
	}
	_, err = r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	return r2Error("delete", err)
}

func (r *R2) Head(ctx context.Context, visibility Visibility, key string) (ObjectInfo, error) {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	out, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return ObjectInfo{}, r2Error("head", err)
	}
	return ObjectInfo{Key: key, Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), ETag: aws.ToString(out.ETag)}, nil
}

func (r *R2) PresignPut(ctx context.Context, visibility Visibility, key, contentType string, ttl time.Duration) (PresignedURL, error) {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return PresignedURL{}, err
	}
	if err := validateTTL(ttl); err != nil {
		return PresignedURL{}, err
	}
	if err := validateContentType(contentType); err != nil {
		return PresignedURL{}, err
	}
	out, err := r.presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), ContentType: aws.String(contentType)}, func(opts *s3.PresignOptions) { opts.Expires = ttl })
	if err != nil {
		return PresignedURL{}, r2Error("presign put", err)
	}
	headers := make(map[string]string)
	for key, values := range out.SignedHeader {
		headers[key] = strings.Join(values, ",")
	}
	// SDK versions may exclude Content-Type from signed headers, but callers must
	// send it and confirmation must check Head metadata before submitting the row.
	headers["Content-Type"] = contentType
	return PresignedURL{URL: out.URL, Method: out.Method, Headers: headers, ExpiresAt: time.Now().UTC().Add(ttl)}, nil
}

func (r *R2) PresignGet(ctx context.Context, visibility Visibility, key string, ttl time.Duration) (PresignedURL, error) {
	bucket, err := r.bucket(visibility, key)
	if err != nil {
		return PresignedURL{}, err
	}
	if err := validateTTL(ttl); err != nil {
		return PresignedURL{}, err
	}
	out, err := r.presigner.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, func(opts *s3.PresignOptions) { opts.Expires = ttl })
	if err != nil {
		return PresignedURL{}, r2Error("presign get", err)
	}
	headers := make(map[string]string)
	for key, values := range out.SignedHeader {
		headers[key] = strings.Join(values, ",")
	}
	return PresignedURL{URL: out.URL, Method: out.Method, Headers: headers, ExpiresAt: time.Now().UTC().Add(ttl)}, nil
}
