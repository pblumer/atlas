package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// persistedDeployment is the on-disk form of a deployed definition: the metadata
// the UI needs plus the original BPMN XML, enough to recompile the definition and
// render its diagram after a restart. See ADR-0019.
type persistedDeployment struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
	XML        string `json:"xml"`
	// DMNXML is the resolved DMN model this process's business rule tasks evaluate
	// against, snapshotted at deploy time so it is self-contained and re-registers
	// on restart without re-resolving the temis reference (ADR-0034/ADR-0014).
	// Empty when the process has no business rule tasks.
	DMNXML string `json:"dmnXml,omitempty"`
}

// deployStore is a small durable store for deployments, backed by one JSON file
// per deployment under a single directory (ADR-0019). It is an interim mechanism
// for the single-binary server, sidestepping the WAL until deployment becomes a
// first-class event at the Milestone 4 public API. It is owned exclusively by the
// server's run-loop goroutine, so it needs no locking of its own.
type deployStore struct {
	dir string
}

// newDeployStore opens (creating if needed) the deployment directory.
func newDeployStore(dir string) (*deployStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("deploystore: create dir: %w", err)
	}
	return &deployStore{dir: dir}, nil
}

// fileFor returns the record path for a definition key.
func (d *deployStore) fileFor(key uint64) string {
	return filepath.Join(d.dir, strconv.FormatUint(key, 10)+".json")
}

// save writes a deployment durably (atomic write + directory fsync), so the
// caller may treat a nil return as "on disk" (I2 / ADR-0019).
func (d *deployStore) save(rec persistedDeployment) error {
	return atomicWriteJSON(d.dir, d.fileFor(rec.Key), rec)
}

// delete removes a deployment's record. A missing file is not an error, so
// cleanup is idempotent.
func (d *deployStore) delete(key uint64) error {
	if err := os.Remove(d.fileFor(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deploystore: remove: %w", err)
	}
	return fsyncDir(d.dir)
}

// loadAll reads every deployment record, sorted by key ascending so registration
// order (and thus assigned keys) is reconstructed deterministically. Files that
// are not <key>.json records are ignored.
func (d *deployStore) loadAll() ([]persistedDeployment, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, fmt.Errorf("deploystore: read dir: %w", err)
	}
	var out []persistedDeployment
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Skip anything whose stem is not a plain key (e.g. leftover *.tmp is
		// already excluded by the suffix check; a stray README.json is skipped).
		if _, err := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d.dir, name))
		if err != nil {
			return nil, fmt.Errorf("deploystore: read %s: %w", name, err)
		}
		var rec persistedDeployment
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("deploystore: decode %s: %w", name, err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
