package utils_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ride-backend/utils"
)

func TestUploadMigrationPrivateDefaultAndDryRun(t *testing.T) {
	for _, dry := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry", false: "move"}[dry], func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "public"), 0o750); err != nil {
				t.Fatal(err)
			}
			files := map[string]utils.UploadClass{"licence_named_public.jpg": utils.UploadPublic, "unclassified.txt": utils.UploadQuarantine, "document.jpg": utils.UploadPrivate}
			for name := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "public", "unknown.jpg"), []byte("private fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			classify := func(path string) utils.UploadClass {
				return files[filepath.Base(path)]
			}
			counts, err := utils.MigrateUploads(root, classify, nil, dry)
			if err != nil {
				t.Fatal(err)
			}
			if counts.Scanned != 4 || counts.Public != 1 || counts.Private != 1 || counts.Quarantined != 2 {
				t.Fatalf("unexpected counts: %+v", counts)
			}
			if dry {
				if counts.Moved != 0 {
					t.Fatal("dry run moved files")
				}
				if _, err := os.Stat(filepath.Join(root, "private")); !os.IsNotExist(err) {
					t.Fatal("dry run created private directory")
				}
				for name := range files {
					if _, err := os.Stat(filepath.Join(root, name)); err != nil {
						t.Fatal("dry run modified file")
					}
				}
				return
			}
			public, err := os.ReadDir(filepath.Join(root, "public"))
			if err != nil {
				t.Fatal(err)
			}
			if len(public) != 1 || public[0].Name() != "licence_named_public.jpg" {
				t.Fatal("nonpublic file became publicly reachable")
			}
			again, err := utils.MigrateUploads(root, classify, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if again.Moved != 0 {
				t.Fatal("restart repeated migration")
			}
		})
	}
}

func TestUploadMigrationResumesMetadataUpdate(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "private-proof.jpg")
	if err := os.WriteFile(source, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	classifier := func(string) utils.UploadClass { return utils.UploadPrivate }
	var destination string
	if _, err := utils.MigrateUploads(root, classifier, func(move utils.UploadMove) error {
		destination = move.Destination
		return errors.New("temporary database failure")
	}, false); err == nil {
		t.Fatal("metadata failure swallowed")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("private file left in old location")
	}
	repairs := 0
	if _, err := utils.MigrateUploads(root, classifier, func(move utils.UploadMove) error {
		repairs++
		if move.Destination != destination {
			t.Fatal("resume changed destination")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
	if repairs != 1 {
		t.Fatalf("metadata updates = %d", repairs)
	}
}

func TestUploadMigrationRefusesSymlinkDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "public")); err != nil {
		t.Skip("symlink permission unavailable")
	}
	if _, err := utils.MigrateUploads(root, func(string) utils.UploadClass { return utils.UploadPublic }, nil, false); err == nil {
		t.Fatal("symlink escape allowed")
	}
}
