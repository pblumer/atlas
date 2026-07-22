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

// draft is a saved-but-not-deployed diagram: the raw BPMN XML plus the metadata
// the Modeler lists it by. Unlike a deployment it is not compiled, so an
// incomplete or not-yet-executable model (e.g. a lone start event) can still be
// saved and reopened. It is keyed by process id, so re-saving the same diagram
// overwrites the previous draft rather than piling up versions.
type draft struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	SavedAt   int64  `json:"savedAt"`
	XML       string `json:"xml"`
}

// draftStore is a durable store for diagram drafts, one JSON file per process id
// under a single directory. It reuses the on-disk sidecar approach of the
// deployment store (ADR-0019) and, like it, is owned solely by the server's
// run-loop goroutine, so it needs no locking of its own.
type draftStore struct {
	dir string
}

// newDraftStore opens (creating if needed) the drafts directory.
func newDraftStore(dir string) (*draftStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("draftstore: create dir: %w", err)
	}
	return &draftStore{dir: dir}, nil
}

// fileFor maps a process id to its record path. The id is hex-encoded so any
// valid BPMN id yields a safe, unique, deterministic filename (overwrite targets
// the same file); the real id is also stored inside the record.
func (d *draftStore) fileFor(processID string) string {
	return filepath.Join(d.dir, hex.EncodeToString([]byte(processID))+".json")
}

// save writes a draft durably (atomic write + directory fsync), overwriting any
// existing draft for the same process id (I2 / ADR-0021).
func (d *draftStore) save(rec draft) error {
	return atomicWriteJSON(d.dir, d.fileFor(rec.ProcessID), rec)
}

// get returns the draft for a process id, or ok=false if none is saved.
func (d *draftStore) get(processID string) (draft, bool, error) {
	data, err := os.ReadFile(d.fileFor(processID))
	if err != nil {
		if os.IsNotExist(err) {
			return draft{}, false, nil
		}
		return draft{}, false, fmt.Errorf("draftstore: read: %w", err)
	}
	var rec draft
	if err := json.Unmarshal(data, &rec); err != nil {
		return draft{}, false, fmt.Errorf("draftstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a draft. A missing draft is not an error (idempotent cleanup).
func (d *draftStore) delete(processID string) error {
	if err := os.Remove(d.fileFor(processID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("draftstore: remove: %w", err)
	}
	return fsyncDir(d.dir)
}

// loadAll reads every draft, most recently saved first — the order the Modeler
// lists them in. Files that are not draft records are ignored.
func (d *draftStore) loadAll() ([]draft, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, fmt.Errorf("draftstore: read dir: %w", err)
	}
	var out []draft
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(d.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("draftstore: read %s: %w", e.Name(), err)
		}
		var rec draft
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("draftstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt > out[j].SavedAt })
	return out, nil
}
