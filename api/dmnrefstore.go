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

// dmnRef is a DMN artifact: a *reference* to a decision model authored in temis,
// not a copy of it (ADR-0024 Phase 2, ADR-0014). Atlas organizes and (later)
// deploys the reference; it does not author DMN — so this record holds only a
// display name and the temis model handle to resolve, never DMN XML. Keeping DMN
// authoring in temis is what preserves the "no DMN authoring surface" non-goal.
type dmnRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ModelRef  string `json:"modelRef"` // the temis model handle this points at
	ProjectID string `json:"projectId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// dmnRefStore is a durable store for DMN references, one JSON file per id under a
// single directory. It reuses the on-disk sidecar approach of the deployment,
// draft, and project stores (ADR-0019/0021/0024) and, like them, is owned solely
// by the server's run-loop goroutine, so it needs no locking of its own.
type dmnRefStore struct {
	dir string
}

// newDmnRefStore opens (creating if needed) the dmn-refs directory.
func newDmnRefStore(dir string) (*dmnRefStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("dmnrefstore: create dir: %w", err)
	}
	return &dmnRefStore{dir: dir}, nil
}

// fileFor maps a reference id to its record path. The id is hex-encoded so any id
// yields a safe, unique, deterministic filename; the real id is also stored
// inside the record.
func (d *dmnRefStore) fileFor(id string) string {
	return filepath.Join(d.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a reference durably (atomic write + directory fsync), overwriting
// any existing reference with the same id (I2 / ADR-0024).
func (d *dmnRefStore) save(rec dmnRef) error {
	return atomicWriteJSON(d.dir, d.fileFor(rec.ID), rec)
}

// get returns the reference for an id, or ok=false if none exists.
func (d *dmnRefStore) get(id string) (dmnRef, bool, error) {
	data, err := os.ReadFile(d.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return dmnRef{}, false, nil
		}
		return dmnRef{}, false, fmt.Errorf("dmnrefstore: read: %w", err)
	}
	var rec dmnRef
	if err := json.Unmarshal(data, &rec); err != nil {
		return dmnRef{}, false, fmt.Errorf("dmnrefstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a reference. A missing reference is not an error (idempotent
// cleanup).
func (d *dmnRefStore) delete(id string) error {
	if err := os.Remove(d.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("dmnrefstore: remove: %w", err)
	}
	return fsyncDir(d.dir)
}

// loadAll reads every reference, oldest first (creation order), so the Modeler
// lists them in a stable order. Files that are not reference records are ignored.
func (d *dmnRefStore) loadAll() ([]dmnRef, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, fmt.Errorf("dmnrefstore: read dir: %w", err)
	}
	var out []dmnRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(d.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("dmnrefstore: read %s: %w", e.Name(), err)
		}
		var rec dmnRef
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("dmnrefstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
