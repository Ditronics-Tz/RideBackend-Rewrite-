package store

import (
	"errors"
	"sort"
	"sync"

	"gorm.io/gorm"
)

// DataExporter contributes one named, structured section and its owned upload paths.
type DataExporter interface {
	Name() string
	Tables() []string
	Export(userID string) (any, []string, error)
}
type DataExporterFactory func(*Store) DataExporter

// Anonymizer owns cleanup for its module's tables and documents its retention policy.
type Anonymizer interface {
	Name() string
	Tables() []string
	Description() string
	Anonymize(tx *gorm.DB, userID string) error
}

var privacyRegistry = struct {
	sync.RWMutex
	exporters   []DataExporterFactory
	anonymizers []Anonymizer
}{}

func RegisterDataExporter(e DataExporterFactory) {
	privacyRegistry.Lock()
	defer privacyRegistry.Unlock()
	privacyRegistry.exporters = append(privacyRegistry.exporters, e)
}
func RegisterAnonymizer(a Anonymizer) {
	privacyRegistry.Lock()
	defer privacyRegistry.Unlock()
	privacyRegistry.anonymizers = append(privacyRegistry.anonymizers, a)
}
func RegisteredPrivacyCoverage() (exported, anonymized map[string]string) {
	privacyRegistry.RLock()
	defer privacyRegistry.RUnlock()
	exported = map[string]string{}
	anonymized = map[string]string{}
	for _, factory := range privacyRegistry.exporters {
		e := factory(nil)
		for _, t := range e.Tables() {
			exported[t] = e.Name()
		}
	}
	for _, a := range privacyRegistry.anonymizers {
		for _, t := range a.Tables() {
			anonymized[t] = a.Name() + ": " + a.Description()
		}
	}
	return
}
func registeredExporters(s *Store) []DataExporter {
	privacyRegistry.RLock()
	defer privacyRegistry.RUnlock()
	out := make([]DataExporter, 0, len(privacyRegistry.exporters))
	for _, factory := range privacyRegistry.exporters {
		out = append(out, factory(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
func registeredAnonymizers() []Anonymizer {
	privacyRegistry.RLock()
	defer privacyRegistry.RUnlock()
	out := append([]Anonymizer{}, privacyRegistry.anonymizers...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
func (s *Store) exportSections(userID string) (map[string]any, []string, error) {
	sections := map[string]any{}
	paths := []string{}
	for _, e := range registeredExporters(s) {
		data, files, err := e.Export(userID)
		if err != nil {
			return nil, nil, err
		}
		sections[e.Name()] = data
		paths = append(paths, files...)
	}
	return sections, paths, nil
}
func runAnonymizers(tx *gorm.DB, userID string) error {
	for _, a := range registeredAnonymizers() {
		if err := a.Anonymize(tx, userID); err != nil {
			return errors.New("anonymization module failed")
		}
	}
	return nil
}
