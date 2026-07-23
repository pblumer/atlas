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

// project is a named container that groups artifacts so related work lives
// together in the Modeler (ADR-0033). It holds only organizational metadata:
// membership is a projectId tag carried on each artifact (a draft, in Phase 1),
// not a list stored here, so a project and its artifacts stay loosely coupled and
// deleting a project leaves its artifacts intact (they fall back to "Ungrouped").
type project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// projectStore is a durable store for projects, one JSON file per project id
// under a single directory. It reuses the on-disk sidecar approach of the
// deployment and draft stores (ADR-0019/0021) and, like them, is owned solely by
// the server's run-loop goroutine, so it needs no locking of its own.
type projectStore struct {
	dir string
}

// newProjectStore opens (creating if needed) the projects directory.
func newProjectStore(dir string) (*projectStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("projectstore: create dir: %w", err)
	}
	return &projectStore{dir: dir}, nil
}

// fileFor maps a project id to its record path. The id is hex-encoded so any id
// yields a safe, unique, deterministic filename; the real id is also stored
// inside the record.
func (p *projectStore) fileFor(id string) string {
	return filepath.Join(p.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a project durably (atomic write + directory fsync), overwriting any
// existing project with the same id — the path a rename takes (I2 / ADR-0033).
func (p *projectStore) save(rec project) error {
	return atomicWriteJSON(p.dir, p.fileFor(rec.ID), rec)
}

// get returns the project for an id, or ok=false if none exists.
func (p *projectStore) get(id string) (project, bool, error) {
	data, err := os.ReadFile(p.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return project{}, false, nil
		}
		return project{}, false, fmt.Errorf("projectstore: read: %w", err)
	}
	var rec project
	if err := json.Unmarshal(data, &rec); err != nil {
		return project{}, false, fmt.Errorf("projectstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a project. A missing project is not an error (idempotent
// cleanup). Artifacts tagged with the id are left untouched; they simply cease to
// name an existing project and read as Ungrouped (ADR-0033).
func (p *projectStore) delete(id string) error {
	if err := os.Remove(p.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("projectstore: remove: %w", err)
	}
	return fsyncDir(p.dir)
}

// loadAll reads every project, oldest first (creation order), so the Modeler
// lists projects in a stable order that does not reshuffle on rename. Files that
// are not project records are ignored.
func (p *projectStore) loadAll() ([]project, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, fmt.Errorf("projectstore: read dir: %w", err)
	}
	var out []project
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(p.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("projectstore: read %s: %w", e.Name(), err)
		}
		var rec project
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("projectstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	// CreatedAt ascending, tie-broken by id, so the order is deterministic even
	// for projects created within the same second.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
