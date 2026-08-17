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

// applicationRelease is one publish of a process application: the manifest of what
// shipped together, at which artifact versions (ADR-0127).
//
// It is design-time metadata, not an engine fact — publishing does not put a record
// in the event log, and a release never participates in replay. The per-processId
// version on each member is the authoritative ADR-0019 deployment version; Version
// here is a *bundle-level* counter layered above it, answering "which release of
// this application is that?" rather than replacing the per-process number.
//
// Membership is snapshotted by value: a release records the refs and versions that
// were live at publish time, so later edits to the application's artifacts cannot
// rewrite the history of what was already shipped.
type applicationRelease struct {
	ID string `json:"id"`
	// ApplicationID is the owning application. On disk this is the ADR-0034
	// projectId — ADR-0127 renames the API/UI boundary, not the stored shape.
	ApplicationID string          `json:"applicationId"`
	Version       int32           `json:"version"` // per-application counter, 1-based
	PublishedAt   int64           `json:"publishedAt"`
	PublishedBy   string          `json:"publishedBy,omitempty"`
	Note          string          `json:"note,omitempty"`
	Members       []releaseMember `json:"members"`
}

// releaseMember is one artifact as it shipped in a release.
type releaseMember struct {
	Kind        string `json:"kind"` // "process" today; "decision"/"form" as later slices add them
	Ref         string `json:"ref"`  // processId for a process
	ArtifactVer int32  `json:"artifactVer"`
	Key         uint64 `json:"key,omitempty"` // definition key for a process
}

// releaseStore is a durable store for application releases, one JSON file per
// release id under a single directory. It reuses the sidecar approach of the
// deployment, draft, and project stores (ADR-0019/0021/0034) and, like them, is
// owned solely by the server's run-loop goroutine, so it needs no locking.
type releaseStore struct {
	dir string
}

// newReleaseStore opens (creating if needed) the releases directory.
func newReleaseStore(dir string) (*releaseStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("releasestore: create dir: %w", err)
	}
	return &releaseStore{dir: dir}, nil
}

// fileFor maps a release id to its record path. The id is hex-encoded so any id
// yields a safe, unique, deterministic filename; the real id is also stored inside
// the record.
func (s *releaseStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a release durably (atomic write + directory fsync), so the caller may
// treat a nil return as "on disk" (I2).
func (s *releaseStore) save(rec applicationRelease) error {
	return atomicWriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

// loadAll reads every release, newest first (version descending within an
// application, then publish time), which is the order the history view wants.
// Files that are not release records are ignored.
func (s *releaseStore) loadAll() ([]applicationRelease, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("releasestore: read dir: %w", err)
	}
	var out []applicationRelease
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("releasestore: read %s: %w", e.Name(), err)
		}
		var rec applicationRelease
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("releasestore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	// Newest first: application id groups together, highest version first, ties
	// broken by id so the order is deterministic.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ApplicationID != out[j].ApplicationID {
			return out[i].ApplicationID < out[j].ApplicationID
		}
		if out[i].Version != out[j].Version {
			return out[i].Version > out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// forApplication returns one application's releases, newest first.
func (s *releaseStore) forApplication(appID string) ([]applicationRelease, error) {
	all, err := s.loadAll()
	if err != nil {
		return nil, err
	}
	out := []applicationRelease{}
	for _, rec := range all {
		if rec.ApplicationID == appID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// deleteForApplication removes every release of an application. Used when the
// application itself is deleted, so its history does not outlive it as orphaned
// records. A missing file is not an error (idempotent cleanup).
func (s *releaseStore) deleteForApplication(appID string) error {
	all, err := s.loadAll()
	if err != nil {
		return err
	}
	removed := false
	for _, rec := range all {
		if rec.ApplicationID != appID {
			continue
		}
		if err := os.Remove(s.fileFor(rec.ID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("releasestore: remove: %w", err)
		}
		removed = true
	}
	if !removed {
		return nil
	}
	return fsyncDir(s.dir)
}
