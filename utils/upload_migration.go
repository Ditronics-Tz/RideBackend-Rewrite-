package utils

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type UploadClass string

const (
	UploadPublic     UploadClass = "public"
	UploadPrivate    UploadClass = "private"
	UploadQuarantine UploadClass = "quarantine"
)

type UploadMove struct {
	Source, Destination string
	Class               UploadClass
}
type UploadMigrationCounts struct {
	Scanned     int `json:"scanned"`
	Public      int `json:"public"`
	Private     int `json:"private"`
	Quarantined int `json:"quarantined"`
	Moved       int `json:"moved"`
}

func withinUploadRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// safeUploadDirectory checks every existing directory before a move, including
// symlinks, so a public/private directory cannot redirect a move outside root.
func safeUploadDirectory(root, directory string, create bool) error {
	if !withinUploadRoot(root, directory) {
		return errors.New("upload path outside root")
	}
	rel, err := filepath.Rel(root, directory)
	if err != nil {
		return errors.New("invalid upload path")
	}
	paths := []string{root}
	current := root
	if rel != "." {
		for _, part := range strings.Split(rel, string(os.PathSeparator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && create {
			if err := os.Mkdir(path, 0o750); err != nil {
				return errors.New("unable to create private migration directory")
			}
			continue
		}
		if os.IsNotExist(err) && !create {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe upload directory")
		}
	}
	return nil
}

// MigrateUploads examines legacy flat files and files previously made public.
// Classification is supplied from database metadata. Unknowns are private by
// default. The private journal lets a restart finish a database update after a
// successful filesystem move; dry-run creates no directories or journal files.
func MigrateUploads(root string, classify func(string) UploadClass, moved func(UploadMove) error, dry bool) (UploadMigrationCounts, error) {
	counts := UploadMigrationCounts{}
	root, err := filepath.Abs(root)
	if err != nil {
		return counts, errors.New("invalid upload root")
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return counts, nil
	}
	if err := safeUploadDirectory(root, root, false); err != nil {
		return counts, err
	}
	journal := filepath.Join(root, "private", "upload-migration")
	migratedDestinations := map[string]bool{}
	finish := func(plan UploadMove, path string) error {
		if !withinUploadRoot(root, plan.Source) || !withinUploadRoot(root, plan.Destination) {
			return errors.New("invalid migration journal")
		}
		allowed := filepath.Join(root, "private", "quarantine")
		if plan.Class == UploadPrivate {
			allowed = filepath.Join(root, "licences")
		}
		if plan.Class == UploadPublic {
			allowed = filepath.Join(root, "public")
		}
		if !withinUploadRoot(allowed, plan.Destination) {
			return errors.New("invalid migration destination")
		}
		if err := safeUploadDirectory(root, filepath.Dir(plan.Source), false); err != nil {
			return err
		}
		if err := safeUploadDirectory(root, filepath.Dir(plan.Destination), true); err != nil {
			return err
		}
		_, sourceErr := os.Lstat(plan.Source)
		_, destErr := os.Lstat(plan.Destination)
		if sourceErr == nil {
			if !os.IsNotExist(destErr) {
				return errors.New("migration destination already exists")
			}
			if err := os.Rename(plan.Source, plan.Destination); err != nil {
				return errors.New("upload migration move failed")
			}
		} else if !os.IsNotExist(sourceErr) || destErr != nil {
			return errors.New("migration file unavailable")
		}
		if moved != nil {
			if err := moved(plan); err != nil {
				return errors.New("migration metadata update failed; retry the command")
			}
		}
		if err := os.Remove(path); err != nil {
			return errors.New("migration journal cleanup failed; retry the command")
		}
		counts.Moved++
		migratedDestinations[plan.Destination] = true
		return nil
	}
	if !dry {
		if err := safeUploadDirectory(root, journal, false); err != nil {
			return counts, err
		}
		pending, err := os.ReadDir(journal)
		if err != nil && !os.IsNotExist(err) {
			return counts, errors.New("cannot read migration journal")
		}
		for _, entry := range pending {
			if !entry.Type().IsRegular() {
				return counts, errors.New("unsafe migration journal entry")
			}
			path := filepath.Join(journal, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return counts, errors.New("cannot read migration journal")
			}
			var plan UploadMove
			if err := json.Unmarshal(raw, &plan); err != nil {
				return counts, errors.New("invalid migration journal")
			}
			if err := finish(plan, path); err != nil {
				return counts, err
			}
		}
	}
	for _, directory := range []string{root, filepath.Join(root, "public")} {
		if err := safeUploadDirectory(root, directory, false); err != nil {
			return counts, err
		}
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return counts, errors.New("cannot inventory uploads")
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			source := filepath.Join(directory, entry.Name())
			if migratedDestinations[source] {
				continue
			}
			counts.Scanned++
			class := classify(source)
			if !entry.Type().IsRegular() || (class != UploadPublic && class != UploadPrivate) {
				class = UploadQuarantine
			}
			destination := filepath.Join(root, "private", "quarantine", uuid.NewString()+filepath.Ext(entry.Name()))
			if class == UploadPrivate {
				destination = filepath.Join(root, "licences", uuid.NewString()+filepath.Ext(entry.Name()))
			}
			if class == UploadPublic {
				destination = filepath.Join(root, "public", entry.Name())
				if destination != source {
					if _, err := os.Lstat(destination); err == nil {
						class = UploadQuarantine
						destination = filepath.Join(root, "private", "quarantine", uuid.NewString()+filepath.Ext(entry.Name()))
					}
				}
			}
			switch class {
			case UploadPublic:
				counts.Public++
			case UploadPrivate:
				counts.Private++
			default:
				counts.Quarantined++
			}
			if dry || source == destination {
				continue
			}
			if err := safeUploadDirectory(root, journal, true); err != nil {
				return counts, err
			}
			plan := UploadMove{Source: source, Destination: destination, Class: class}
			raw, err := json.Marshal(plan)
			if err != nil {
				return counts, errors.New("cannot encode migration journal")
			}
			path := filepath.Join(journal, uuid.NewString()+".json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				return counts, errors.New("cannot write migration journal")
			}
			if err := finish(plan, path); err != nil {
				return counts, err
			}
		}
	}
	return counts, nil
}
