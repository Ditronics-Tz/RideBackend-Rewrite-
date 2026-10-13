package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type LocalConfig struct{ PublicDir, PrivateDir, PublicBaseURL string }

// Local uses confined directory handles, which reject symlink escapes even if a
// path changes concurrently. Local file reads detect content type from bytes.
type Local struct {
	public, private *os.Root
	publicBaseURL   string
}

var _ Storage = (*Local)(nil)

func NewLocal(cfg LocalConfig) (*Local, error) {
	if cfg.PublicDir == "" || cfg.PrivateDir == "" {
		return nil, errors.New("public and private storage directories are required")
	}
	if cfg.PublicBaseURL != "" {
		if err := validateBaseURL(cfg.PublicBaseURL, true); err != nil {
			return nil, err
		}
	}
	dirs := []string{cfg.PublicDir, cfg.PrivateDir}
	for i, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return nil, err
		}
		dirs[i], err = filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, err
		}
	}
	for i := range dirs {
		rel, err := filepath.Rel(dirs[i], dirs[1-i])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("public and private storage directories must not overlap")
		}
	}
	pub, err := os.OpenRoot(dirs[0])
	if err != nil {
		return nil, err
	}
	priv, err := os.OpenRoot(dirs[1])
	if err != nil {
		pub.Close()
		return nil, err
	}
	return &Local{public: pub, private: priv, publicBaseURL: cfg.PublicBaseURL}, nil
}

func (l *Local) Close() error                         { return errors.Join(l.public.Close(), l.private.Close()) }
func (l *Local) PublicURL(key string) (string, error) { return publicURL(l.publicBaseURL, key) }

func (l *Local) root(visibility Visibility, key string) (*os.Root, error) {
	if err := validate(visibility, key); err != nil {
		return nil, err
	}
	if visibility == Public {
		return l.public, nil
	}
	return l.private, nil
}

func localError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// checkPath rejects symlinks rather than allowing aliases between keys.
func checkPath(root *os.Root, key string) error {
	parts := strings.Split(key, "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidKey
		}
	}
	return nil
}

func (l *Local) Put(ctx context.Context, visibility Visibility, key string, upload Upload) (ObjectInfo, error) {
	root, err := l.root(visibility, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if upload.Body == nil || upload.Size < 0 {
		return ObjectInfo{}, ErrInvalidUpload
	}
	if err := validateContentType(upload.ContentType); err != nil {
		return ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if err := checkPath(root, key); err != nil {
		return ObjectInfo{}, err
	}
	parent := filepath.Dir(filepath.FromSlash(key))
	if err := root.MkdirAll(parent, 0o700); err != nil {
		return ObjectInfo{}, err
	}
	tmp := filepath.Join(parent, ".upload-"+uuid.NewString())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer root.Remove(tmp)
	written, copyErr := io.Copy(f, io.LimitReader(upload.Body, upload.Size))
	var extra [1]byte
	if copyErr == nil {
		n, readErr := io.ReadFull(upload.Body, extra[:])
		if n != 0 || readErr != io.EOF {
			copyErr = ErrInvalidUpload
		}
	}
	closeErr := f.Close()
	if copyErr != nil {
		return ObjectInfo{}, copyErr
	}
	if closeErr != nil {
		return ObjectInfo{}, closeErr
	}
	if written != upload.Size {
		return ObjectInfo{}, ErrInvalidUpload
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if err := checkPath(root, key); err != nil {
		return ObjectInfo{}, err
	}
	if err := root.Rename(tmp, filepath.FromSlash(key)); err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Key: key, Size: written, ContentType: upload.ContentType}, nil
}

func (l *Local) Get(ctx context.Context, visibility Visibility, key string) (io.ReadCloser, ObjectInfo, error) {
	root, err := l.root(visibility, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	if err := checkPath(root, key); err != nil {
		return nil, ObjectInfo{}, localError(err)
	}
	f, err := root.Open(filepath.FromSlash(key))
	if err != nil {
		return nil, ObjectInfo{}, localError(err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ObjectInfo{}, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, ObjectInfo{}, fmt.Errorf("object is not a regular file")
	}
	var prefix [512]byte
	n, err := f.Read(prefix[:])
	if err != nil && err != io.EOF {
		f.Close()
		return nil, ObjectInfo{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, ObjectInfo{}, err
	}
	return f, ObjectInfo{Key: key, Size: info.Size(), ContentType: http.DetectContentType(prefix[:n])}, nil
}

func (l *Local) Head(ctx context.Context, visibility Visibility, key string) (ObjectInfo, error) {
	f, info, err := l.Get(ctx, visibility, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	return info, f.Close()
}

func (l *Local) Delete(ctx context.Context, visibility Visibility, key string) error {
	root, err := l.root(visibility, key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPath(root, key); err != nil {
		return err
	}
	info, err := root.Lstat(filepath.FromSlash(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidKey
	}
	return localError(root.Remove(filepath.FromSlash(key)))
}

func (l *Local) PresignPut(ctx context.Context, visibility Visibility, key, contentType string, ttl time.Duration) (PresignedURL, error) {
	if err := validate(visibility, key); err != nil {
		return PresignedURL{}, err
	}
	if err := validateTTL(ttl); err != nil {
		return PresignedURL{}, err
	}
	if err := validateContentType(contentType); err != nil {
		return PresignedURL{}, err
	}
	if err := ctx.Err(); err != nil {
		return PresignedURL{}, err
	}
	return PresignedURL{}, ErrPresignUnsupported
}

func (l *Local) PresignGet(ctx context.Context, visibility Visibility, key string, ttl time.Duration) (PresignedURL, error) {
	if err := validate(visibility, key); err != nil {
		return PresignedURL{}, err
	}
	if err := validateTTL(ttl); err != nil {
		return PresignedURL{}, err
	}
	if err := ctx.Err(); err != nil {
		return PresignedURL{}, err
	}
	return PresignedURL{}, ErrPresignUnsupported
}
