package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// form is a stored form definition: a form-js JSON schema plus the metadata the
// Tasks app and the Modeler list it by (ADR-0028). A user task binds a form by
// id (compiler: zeebe:formDefinition formId="..."); the Tasks app fetches the
// schema by that id and renders it. Schema is the raw form-js JSON, kept as a
// string so the store never has to understand the form model — rendering is a UI
// concern, storage is not.
type form struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	SavedAt   int64  `json:"savedAt"`
	Schema    string `json:"schema"`
}

// formStore is a durable store for form definitions, one JSON file per form id
// under a single directory. Like the draft store (ADR-0021) it reuses the on-disk
// sidecar approach of the deployment store (ADR-0019) and is owned solely by the
// server's run-loop goroutine, so it needs no locking of its own.
type formStore struct {
	dir string
}

// newFormStore opens (creating if needed) the forms directory.
func newFormStore(dir string) (*formStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("formstore: create dir: %w", err)
	}
	return &formStore{dir: dir}, nil
}

// fileFor maps a form id to its record path. The id is hex-encoded so any id
// yields a safe, unique, deterministic filename (overwrite targets the same
// file); the real id is also stored inside the record.
func (f *formStore) fileFor(id string) string {
	return filepath.Join(f.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a form durably (atomic write + directory fsync), overwriting any
// existing form with the same id (I2).
func (f *formStore) save(rec form) error {
	return atomicWriteJSON(f.dir, f.fileFor(rec.ID), rec)
}

// get returns the form for an id, or ok=false if none is stored.
func (f *formStore) get(id string) (form, bool, error) {
	data, err := os.ReadFile(f.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return form{}, false, nil
		}
		return form{}, false, fmt.Errorf("formstore: read: %w", err)
	}
	var rec form
	if err := json.Unmarshal(data, &rec); err != nil {
		return form{}, false, fmt.Errorf("formstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a form. A missing form is not an error (idempotent cleanup).
func (f *formStore) delete(id string) error {
	if err := os.Remove(f.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("formstore: remove: %w", err)
	}
	return fsyncDir(f.dir)
}

// loadAll reads every form, most recently saved first. Files that are not form
// records are ignored.
func (f *formStore) loadAll() ([]form, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, fmt.Errorf("formstore: read dir: %w", err)
	}
	var out []form
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("formstore: read %s: %w", e.Name(), err)
		}
		var rec form
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("formstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt > out[j].SavedAt })
	return out, nil
}
