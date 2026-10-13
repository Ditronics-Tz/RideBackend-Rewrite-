package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func localForTest(t *testing.T) *Local {
	t.Helper()
	dir := t.TempDir()
	local, err := NewLocal(LocalConfig{PublicDir: filepath.Join(dir, "public"), PrivateDir: filepath.Join(dir, "private"), PublicBaseURL: "/api/v1/uploads"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	return local
}

func TestLocalStorageIsolationAndLifecycle(t *testing.T) {
	l := localForTest(t)
	ctx := context.Background()
	key := "documents/owner/document/file.pdf"
	payload := "%PDF-1.7\nprivate document"
	info, err := l.Put(ctx, Private, key, Upload{Body: strings.NewReader(payload), Size: int64(len(payload)), ContentType: "application/pdf"})
	if err != nil || info.Size != int64(len(payload)) {
		t.Fatalf("put: %+v %v", info, err)
	}
	if _, err := l.Head(ctx, Public, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private object visible publicly: %v", err)
	}
	body, info, err := l.Get(ctx, Private, key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	body.Close()
	if err != nil || string(data) != payload || info.ContentType != "application/pdf" {
		t.Fatalf("get: %+v %v", info, err)
	}
	info, err = l.Head(ctx, Private, key)
	if err != nil || info.Size != int64(len(payload)) || info.ContentType != "application/pdf" {
		t.Fatalf("head: %+v %v", info, err)
	}
	if err := l.Delete(ctx, Private, key); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, Private, key); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Head(ctx, Private, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted object: %v", err)
	}
	if _, err := l.PresignPut(ctx, Private, key, "application/pdf", DefaultPresignTTL); !errors.Is(err, ErrPresignUnsupported) {
		t.Fatal(err)
	}
	if _, err := l.PresignGet(ctx, Private, key, DefaultPresignTTL); !errors.Is(err, ErrPresignUnsupported) {
		t.Fatal(err)
	}
	publicURL, err := l.PublicURL("profile/user/image.jpg")
	if err != nil || publicURL != "/api/v1/uploads/profile/user/image.jpg" {
		t.Fatalf("public URL: %q %v", publicURL, err)
	}
}

func TestLocalRejectsInvalidUploadsWithoutReplacingFile(t *testing.T) {
	l := localForTest(t)
	ctx := context.Background()
	key := "profile/user/image.jpg"
	for _, u := range []Upload{{Body: strings.NewReader("abc"), Size: 2, ContentType: "image/jpeg"}, {Body: strings.NewReader("abc"), Size: 4, ContentType: "image/jpeg"}, {Body: strings.NewReader("abc"), Size: 3, ContentType: "image/jpeg\r\nInjected: header"}} {
		if _, err := l.Put(ctx, Public, key, u); !errors.Is(err, ErrInvalidUpload) {
			t.Fatalf("invalid upload: %v", err)
		}
		if _, err := l.Head(ctx, Public, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid upload was persisted: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Put(canceled, Public, key, Upload{Body: strings.NewReader("abc"), Size: 3, ContentType: "image/jpeg"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestKeyAndVisibilityValidation(t *testing.T) {
	l := localForTest(t)
	ctx := context.Background()
	for _, key := range []string{"", "../secret", "a/../secret", "/absolute", "a//b", "a/./b", `a\b`, "a/%2e%2e/secret", "https://bucket/object", "a/b?signature=x", "a/b#x", "a/b\x00", "a/b."} {
		t.Run(key, func(t *testing.T) {
			if _, err := l.Head(ctx, Private, key); !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("accepted key: %v", err)
			}
		})
	}
	if _, err := l.Head(ctx, Visibility("backups"), "safe/object"); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatal(err)
	}
	if _, err := l.PresignGet(ctx, Private, "safe/object", 24*time.Hour); !errors.Is(err, ErrInvalidTTL) {
		t.Fatal(err)
	}
}

func TestLocalRejectsSymlinkEscapes(t *testing.T) {
	l := localForTest(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(l.public.Name(), "link")); err != nil {
		t.Skipf("OS cannot create symlinks: %v", err)
	}
	ctx := context.Background()
	key := "link/secret.txt"
	if _, err := l.Head(ctx, Public, key); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("head symlink: %v", err)
	}
	if _, err := l.Put(ctx, Public, key, Upload{Body: strings.NewReader("overwrite"), Size: 9, ContentType: "text/plain"}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("put symlink: %v", err)
	}
	if err := l.Delete(ctx, Public, key); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("delete symlink: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	if err != nil || string(data) != "private" {
		t.Fatal("outside file changed")
	}
}

func TestLocalRejectsOverlappingDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, dirs := range [][2]string{{dir, dir}, {dir, filepath.Join(dir, "private")}, {filepath.Join(dir, "public"), dir}} {
		l, err := NewLocal(LocalConfig{PublicDir: dirs[0], PrivateDir: dirs[1]})
		if err == nil {
			l.Close()
			t.Fatal("overlapping directories accepted")
		}
	}
}

type fakeObject struct {
	body        []byte
	contentType string
}

func fakeS3(t *testing.T) *httptest.Server {
	t.Helper()
	objects := make(map[string]fakeObject)
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/app-public/") && !strings.HasPrefix(r.URL.Path, "/app-private/") {
			t.Errorf("unexpected bucket path: %s", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("X-Amz-Signature") == "" {
			t.Error("request is not signed")
		}
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			objects[r.URL.Path] = fakeObject{body: body, contentType: r.Header.Get("Content-Type")}
			w.Header().Set("ETag", `"fake-etag"`)
		case http.MethodGet, http.MethodHead:
			obj, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				if r.Method != http.MethodHead {
					io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
				}
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
			w.Header().Set("Content-Type", obj.contentType)
			w.Header().Set("ETag", `"fake-etag"`)
			if r.Method == http.MethodGet {
				w.Write(obj.body)
			}
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testR2Config(endpoint string) R2Config {
	return R2Config{AccountID: "fake-account", AccessKeyID: "fake-access", SecretAccessKey: "fake-secret", PublicBucket: "app-public", PrivateBucket: "app-private", PublicBaseURL: "https://images.example.test", Endpoint: endpoint}
}

func TestR2FakeServerLifecycleAndPresign(t *testing.T) {
	srv := fakeS3(t)
	r2, err := NewR2(testR2Config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "documents/user/doc/image.jpg"
	data := "fake image bytes"
	info, err := r2.Put(ctx, Private, key, Upload{Body: strings.NewReader(data), Size: int64(len(data)), ContentType: "image/jpeg"})
	if err != nil || info.ETag != `"fake-etag"` {
		t.Fatalf("put: %+v %v", info, err)
	}
	info, err = r2.Head(ctx, Private, key)
	if err != nil || info.Size != int64(len(data)) || info.ContentType != "image/jpeg" || info.ETag != `"fake-etag"` {
		t.Fatalf("head: %+v %v", info, err)
	}
	if _, err := r2.Head(ctx, Public, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private object in public bucket: %v", err)
	}
	// Confirmation copies a streaming Get response into a new immutable key.
	stream, _, err := r2.Get(ctx, Private, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r2.Put(ctx, Private, "documents/user/doc/final.jpg", Upload{Body: stream, Size: int64(len(data)), ContentType: "image/jpeg"})
	stream.Close()
	if err != nil {
		t.Fatalf("streaming object copy: %v", err)
	}
	body, info, err := r2.Get(ctx, Private, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil || string(got) != data || info.Size != int64(len(data)) {
		t.Fatalf("get: %+v %v", info, err)
	}
	for _, visibility := range []Visibility{Public, Private} {
		put, err := r2.PresignPut(ctx, visibility, key, "image/jpeg", DefaultPresignTTL)
		if err != nil {
			t.Fatal(err)
		}
		get, err := r2.PresignGet(ctx, visibility, key, DefaultPresignTTL)
		if err != nil {
			t.Fatal(err)
		}
		for _, signed := range []PresignedURL{put, get} {
			u, err := url.Parse(signed.URL)
			if err != nil {
				t.Fatal(err)
			}
			bucket := "app-private"
			if visibility == Public {
				bucket = "app-public"
			}
			if u.Path != "/"+bucket+"/"+key || u.Query().Get("X-Amz-Expires") != "300" || u.Query().Get("X-Amz-Signature") == "" {
				t.Fatalf("incorrect presign: %+v", signed)
			}
			if signed.Method == http.MethodPut && !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "content-type") {
				t.Fatal("PUT content type must be bound to the signature")
			}
			if time.Until(signed.ExpiresAt) < 4*time.Minute || time.Until(signed.ExpiresAt) > 5*time.Minute {
				t.Fatal("incorrect presign expiry")
			}
		}
		if put.Method != http.MethodPut || put.Headers["Content-Type"] != "image/jpeg" || get.Method != http.MethodGet {
			t.Fatal("incorrect presign methods or headers")
		}
	}
	if _, err := r2.PresignPut(ctx, Private, key, "image/jpeg", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatal(err)
	}
	if _, err := r2.PresignGet(ctx, Visibility("backups"), key, DefaultPresignTTL); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatal(err)
	}
	if _, err := r2.PresignGet(ctx, Private, "../backups", DefaultPresignTTL); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
	if err := r2.Delete(ctx, Private, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r2.Get(ctx, Private, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted object: %v", err)
	}
	public, err := r2.PublicURL("profile/user/image.jpg")
	if err != nil || public != "https://images.example.test/profile/user/image.jpg" {
		t.Fatalf("public URL: %q %v", public, err)
	}
}

func TestR2RejectsUnsafeConfiguration(t *testing.T) {
	for _, change := range []func(*R2Config){func(c *R2Config) { c.PrivateBucket = c.PublicBucket }, func(c *R2Config) { c.PublicBucket = "../backup" }, func(c *R2Config) { c.AccessKeyID = "" }, func(c *R2Config) { c.AccountID = "unsafe/path" }, func(c *R2Config) { c.PublicBaseURL = "https://user:password@images.example.test" }, func(c *R2Config) { c.Endpoint = "http://remote.example.test" }, func(c *R2Config) { c.Endpoint = "https://endpoint.example.test/path" }} {
		cfg := testR2Config("")
		change(&cfg)
		if _, err := NewR2(cfg); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
}

func TestR2ErrorsDoNotExposeProviderResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>sensitive-provider-data</Message></Error>")
	}))
	defer srv.Close()
	r2, err := NewR2(testR2Config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r2.Head(context.Background(), Private, "documents/user/file.jpg")
	if err == nil || strings.Contains(err.Error(), "sensitive-provider-data") || strings.Contains(err.Error(), "fake-access") {
		t.Fatalf("unsafe storage error: %v", err)
	}
}
