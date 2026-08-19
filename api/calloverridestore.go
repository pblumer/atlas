package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// Call-activity target override actions (ADR-0105). Exactly one applies per record:
//   - redirect: resolve the latest of TargetProcessID instead of the called id;
//   - pin:      resolve exactly TargetVersion of the called id;
//   - disable:  resolve nothing — the call parks (as an undeployed callee does).
const (
	overrideRedirect = "redirect"
	overridePin      = "pin"
	overrideDisable  = "disable"
)

// callOverride is one operator-configured, per-server call-activity target override
// (ADR-0105), keyed by the called bpmn process id. It stores the operator's *intent*
// — never a raw definition key — so it survives redeploys sensibly: a pin re-resolves
// its version to a key at load time. It is admin config, the same category as a
// connector record (ADR-0041): durable on a sidecar, owned by the run-loop goroutine,
// never in the event log.
type callOverride struct {
	CalledProcessID string `json:"calledProcessId"`
	Action          string `json:"action"`
	TargetProcessID string `json:"targetProcessId,omitempty"` // redirect
	TargetVersion   int32  `json:"targetVersion,omitempty"`   // pin
	UpdatedAt       int64  `json:"updatedAt"`
}

// callOverrideStore is a durable store for per-server call-activity overrides, one
// JSON file per called process id under a single directory — the same on-disk sidecar
// approach as the connector/deployment stores (ADR-0019/0041). Owned solely by the
// server's run-loop goroutine, so it needs no locking, and it holds no secrets.
type callOverrideStore struct {
	dir string
}

// newCallOverrideStore opens (creating if needed) the overrides directory.
func newCallOverrideStore(dir string) (*callOverrideStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("calloverridestore: create dir: %w", err)
	}
	return &callOverrideStore{dir: dir}, nil
}

func (s *callOverrideStore) fileFor(processID string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(processID))+".json")
}

// save writes an override durably (atomic write + directory fsync), overwriting any
// existing record for the same called process id.
func (s *callOverrideStore) save(rec callOverride) error {
	return sidecar.WriteJSON(s.dir, s.fileFor(rec.CalledProcessID), rec)
}

// delete removes an override. A missing override is not an error (idempotent).
func (s *callOverrideStore) delete(processID string) error {
	if err := os.Remove(s.fileFor(processID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("calloverridestore: remove: %w", err)
	}
	return sidecar.FsyncDir(s.dir)
}

// loadAll reads every override, oldest first (by last update), so listings are
// stable. Non-record files are ignored.
func (s *callOverrideStore) loadAll() ([]callOverride, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("calloverridestore: read dir: %w", err)
	}
	var out []callOverride
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("calloverridestore: read %s: %w", e.Name(), err)
		}
		var rec callOverride
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("calloverridestore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt < out[j].UpdatedAt
		}
		return out[i].CalledProcessID < out[j].CalledProcessID
	})
	return out, nil
}
