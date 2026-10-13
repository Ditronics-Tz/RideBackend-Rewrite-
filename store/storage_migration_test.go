package store_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/storage"
	"ride-backend/store"
	"ride-backend/testutil"
)

type migrationStorage struct {
	objects       map[string]storage.ObjectInfo
	puts          []string
	headCalls     int
	wrongHead     bool
	providerError bool
	onPut         func()
}

func (m *migrationStorage) Put(ctx context.Context, v storage.Visibility, key string, u storage.Upload) (storage.ObjectInfo, error) {
	m.puts = append(m.puts, string(v)+":"+key)
	if m.providerError {
		return storage.ObjectInfo{}, errors.New("private-source-name and credentials")
	}
	body, err := io.ReadAll(u.Body)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info := storage.ObjectInfo{Key: key, Size: int64(len(body)), ContentType: u.ContentType}
	if m.objects == nil {
		m.objects = make(map[string]storage.ObjectInfo)
	}
	m.objects[string(v)+":"+key] = info
	if m.onPut != nil {
		m.onPut()
	}
	return info, nil
}
func (m *migrationStorage) Head(ctx context.Context, v storage.Visibility, key string) (storage.ObjectInfo, error) {
	m.headCalls++
	info, ok := m.objects[string(v)+":"+key]
	if !ok {
		return info, storage.ErrNotFound
	}
	if m.wrongHead {
		info.Size++
	}
	return info, nil
}
func (*migrationStorage) Get(context.Context, storage.Visibility, string) (io.ReadCloser, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, errors.New("unexpected Get")
}
func (*migrationStorage) Delete(context.Context, storage.Visibility, string) error {
	return errors.New("unexpected Delete")
}
func (*migrationStorage) PresignPut(context.Context, storage.Visibility, string, string, time.Duration) (storage.PresignedURL, error) {
	return storage.PresignedURL{}, errors.New("unexpected PresignPut")
}
func (*migrationStorage) PresignGet(context.Context, storage.Visibility, string, time.Duration) (storage.PresignedURL, error) {
	return storage.PresignedURL{}, errors.New("unexpected PresignGet")
}

func migrationStore(t *testing.T) *store.Store {
	t.Helper()
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := s.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return s
}

func migrationFile(t *testing.T, root, rel string, body []byte) string {
	t.Helper()
	target := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func migrationDocument(t *testing.T, s *store.Store, path string) models.DriverDocument {
	t.Helper()
	doc := models.DriverDocument{ID: uuid.NewString(), DriverID: uuid.NewString(), Type: "driving_licence", FilePath: path, FileURL: "/api/v1/files/documents/legacy", Status: "submitted", IsCurrent: true}
	if err := s.DB().Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	return doc
}

// Magic bytes identify this fixture as a PNG; the source filename is irrelevant.
var migrationPNG = []byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 0}

func TestFilesToR2MigrationCopiesAndSwitchesOnlyVerifiedMetadata(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	privateSource := migrationFile(t, root, "private/unusual-photo-name.jpg", []byte("%PDF-1.7\nprivate document"))
	photoSource := migrationFile(t, root, "public/licence-looking-name.pdf", migrationPNG)
	migrationFile(t, root, "public/unknown.png", migrationPNG)
	doc := migrationDocument(t, s, privateSource)
	profile := models.PassengerProfile{UserID: uuid.NewString(), ProfilePictureURL: "/api/v1/uploads/licence-looking-name.pdf"}
	if err := s.DB().Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	destination := &migrationStorage{}
	counts, err := s.MigrateFilesToR2(context.Background(), root, destination, false)
	if err != nil || counts.Documents != 1 || counts.ProfilePhotos != 1 || counts.Migrated != 2 || len(destination.puts) != 2 || destination.headCalls != 2 {
		t.Fatalf("migration: %+v %v", counts, err)
	}
	if err := s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.FilePath != "" || doc.FileURL != "" || !strings.HasPrefix(doc.ObjectKey, "documents/"+doc.DriverID+"/"+doc.ID+"/") || !strings.HasSuffix(doc.ObjectKey, ".pdf") || doc.ContentType != "application/pdf" || doc.ExpectedSize == 0 {
		t.Fatalf("document metadata: %+v", doc)
	}
	if _, ok := destination.objects["private:"+doc.ObjectKey]; !ok {
		t.Fatal("document was not private")
	}
	if err := s.DB().First(&profile, "user_id = ?", profile.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if profile.ProfilePictureURL != "" || !strings.HasPrefix(profile.ProfilePictureKey, "profile/"+profile.UserID+"/") || !strings.HasSuffix(profile.ProfilePictureKey, ".png") {
		t.Fatalf("profile metadata: %+v", profile)
	}
	if _, ok := destination.objects["public:"+profile.ProfilePictureKey]; !ok {
		t.Fatal("profile was not public")
	}
	for _, source := range []string{privateSource, photoSource} {
		if _, err := os.Stat(source); err != nil {
			t.Fatal("source was deleted")
		}
	}
	counts, err = s.MigrateFilesToR2(context.Background(), root, destination, false)
	if err != nil || counts.AlreadyMigrated != 2 || counts.Migrated != 0 || len(destination.puts) != 2 {
		t.Fatalf("restart: %+v %v", counts, err)
	}
}

func TestFilesToR2DryRunDoesNotCallProviderOrChangeMetadata(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	source := migrationFile(t, root, "private/document.pdf", []byte("%PDF-1.7\nprivate document"))
	doc := migrationDocument(t, s, source)
	provider := &migrationStorage{providerError: true}
	counts, err := s.MigrateFilesToR2(context.Background(), root, provider, true)
	if err != nil || counts.Documents != 1 || counts.Migrated != 0 || len(provider.puts) != 0 || provider.headCalls != 0 {
		t.Fatalf("dry run: %+v %v", counts, err)
	}
	if err := s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.FilePath != source || doc.ObjectKey != "" || doc.FileURL == "" {
		t.Fatal("dry run changed document")
	}
	if _, err := s.MigrateFilesToR2(context.Background(), root, nil, true); err != nil {
		t.Fatal("dry run required a provider")
	}
}

func TestFilesToR2RejectsWrongHeadAndRetriesDeterministicKey(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	source := migrationFile(t, root, "private/document.pdf", []byte("%PDF-1.7\nprivate document"))
	doc := migrationDocument(t, s, source)
	provider := &migrationStorage{wrongHead: true}
	if _, err := s.MigrateFilesToR2(context.Background(), root, provider, false); err == nil {
		t.Fatal("unverified object adopted")
	}
	if err := s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.FilePath != source || doc.ObjectKey != "" {
		t.Fatal("failed verification changed metadata")
	}
	provider.wrongHead = false
	counts, err := s.MigrateFilesToR2(context.Background(), root, provider, false)
	if err != nil || counts.Migrated != 1 || len(provider.puts) != 2 || provider.puts[0] != provider.puts[1] {
		t.Fatalf("retry: %+v %v", counts, err)
	}
}

func TestFilesToR2MigrationFailsClosedOnPrivatePublicOverlap(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	source := migrationFile(t, root, "public/shared.png", migrationPNG)
	migrationDocument(t, s, source)
	profile := models.PassengerProfile{UserID: uuid.NewString(), ProfilePictureURL: "/api/v1/uploads/shared.png"}
	if err := s.DB().Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	provider := &migrationStorage{}
	counts, err := s.MigrateFilesToR2(context.Background(), root, provider, false)
	if err != nil || counts.Migrated != 1 || counts.Rejected != 1 || len(provider.puts) != 1 || !strings.HasPrefix(provider.puts[0], "private:") {
		t.Fatalf("private overlap: %+v %v", counts, err)
	}
	if err := s.DB().First(&profile, "user_id = ?", profile.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if profile.ProfilePictureKey != "" {
		t.Fatal("private source copied publicly")
	}
}

func TestFilesToR2RejectsOutsideRootAndUnsupportedContent(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	outside := migrationFile(t, t.TempDir(), "secret.pdf", []byte("%PDF-1.7\nsecret"))
	migrationDocument(t, s, outside)
	bad := migrationFile(t, root, "private/script.jpg", []byte("<html>script</html>"))
	migrationDocument(t, s, bad)
	migrationDocument(t, s, filepath.Join(root, "private", "missing.pdf"))
	profile := models.PassengerProfile{UserID: uuid.NewString(), ProfilePictureURL: "https://outside.example.test/api/v1/uploads/photo.jpg"}
	if err := s.DB().Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	provider := &migrationStorage{}
	counts, err := s.MigrateFilesToR2(context.Background(), root, provider, false)
	if err != nil || counts.Rejected != 2 || counts.SkippedMissing != 1 || counts.SkippedExternal != 1 || len(provider.puts) != 0 {
		t.Fatalf("unsafe sources: %+v %v", counts, err)
	}
}

func TestFilesToR2MigrationDoesNotOverwriteConcurrentProfileChange(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	migrationFile(t, root, "public/photo.png", migrationPNG)
	profile := models.PassengerProfile{UserID: uuid.NewString(), ProfilePictureURL: "/api/v1/uploads/photo.png"}
	if err := s.DB().Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	provider := &migrationStorage{onPut: func() {
		if err := s.DB().Model(&models.PassengerProfile{}).Where("user_id = ?", profile.UserID).Update("profile_picture_url", "/api/v1/uploads/replacement.png").Error; err != nil {
			t.Error(err)
		}
	}}
	counts, err := s.MigrateFilesToR2(context.Background(), root, provider, false)
	if err != nil || counts.Conflicts != 1 || counts.Migrated != 0 {
		t.Fatalf("concurrent switch: %+v %v", counts, err)
	}
	if err := s.DB().First(&profile, "user_id = ?", profile.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if profile.ProfilePictureURL != "/api/v1/uploads/replacement.png" || profile.ProfilePictureKey != "" {
		t.Fatal("concurrent replacement overwritten")
	}
}

func TestFilesToR2MigrationRedactsProviderErrors(t *testing.T) {
	s := migrationStore(t)
	root := t.TempDir()
	source := migrationFile(t, root, "private/private-source-name.pdf", []byte("%PDF-1.7\nprivate"))
	migrationDocument(t, s, source)
	_, err := s.MigrateFilesToR2(context.Background(), root, &migrationStorage{providerError: true}, false)
	if err == nil || strings.Contains(err.Error(), "private-source-name") || strings.Contains(err.Error(), "credentials") {
		t.Fatalf("unsafe migration error: %v", err)
	}
}
