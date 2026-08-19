package opensearch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// positionFile is the sidecar file name holding the exporter's high-water mark.
const positionFile = "opensearch.pos"

// PositionStore persists the exporter's high-water mark — the highest record
// [Position] indexed into OpenSearch — so a restart resumes without re-exporting
// (ADR-0114). It is a single small file written atomically (temp + rename),
// matching the durable-sidecar discipline (ADR-0019): a crash mid-write leaves
// either the old value or the new one, never a torn number.
type PositionStore struct {
	dir  string
	path string
}

// NewPositionStore returns a store backed by dir/opensearch.pos. The directory is
// created on the first Save.
func NewPositionStore(dir string) *PositionStore {
	return &PositionStore{dir: dir, path: filepath.Join(dir, positionFile)}
}

// Load returns the persisted high-water mark, or 0 if none has been written yet
// (a fresh exporter starts from genesis). A present-but-corrupt file is an error
// rather than a silent reset, so a misconfiguration is not mistaken for "start
// over and re-export everything".
func (p *PositionStore) Load() (uint64, error) {
	raw, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("opensearch: read position: %w", err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, nil
	}
	pos, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("opensearch: parse position %q: %w", s, err)
	}
	return pos, nil
}

// Save writes pos to a temp file and renames it over the target, so a concurrent
// or crashing write never leaves a partial value. It deliberately does not fsync:
// the mark is only a resume hint, and losing the very latest write to a crash just
// re-exports a few already-indexed records, which OpenSearch de-duplicates by
// document id (ADR-0114) — so the extra durability is not worth the cost.
func (p *PositionStore) Save(pos uint64) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("opensearch: create position dir: %w", err)
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(pos, 10)), 0o644); err != nil {
		return fmt.Errorf("opensearch: write temp position: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("opensearch: rename position: %w", err)
	}
	return nil
}
