package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// deploymentTarget is another Atlas server this one can promote a release to
// (ADR-0129). It is operator configuration in the same category as managed
// connectors (ADR-0041) and per-server call-activity overrides (ADR-0105): it
// belongs to this server, not to any one application, and applications merely
// reference it.
//
// Like a connector, it stores only a *reference* to its credential, never the
// secret: CredentialRef names a vault entry (ADR-0069/0070) that holds the peer's
// deploy token. The token itself is resolved at promotion time, used for one
// request, and never written anywhere.
type deploymentTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BaseURL       string `json:"baseUrl"`
	Kind          string `json:"kind,omitempty"` // free operator label, e.g. "prod"
	CredentialRef string `json:"credentialRef,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	// Bindings maps a *local* application id to the id the same application has on
	// this target, learned from the remote's reply on the first successful
	// promotion (ADR-0129 option C1). The two servers keep their own ids; this is
	// how the publisher addresses the application over there afterwards.
	Bindings map[string]string `json:"bindings,omitempty"`
}

// validateTargetURL checks a target's base URL and returns it normalized.
//
// A target is a trust relationship, so plaintext is refused: a deploy token
// presented over http:// is a credential handed to anyone on the path. Loopback is
// the one exception — it is what a single-host setup and the tests need, and it
// does not cross a network.
//
// There is deliberately no "skip TLS verification" option anywhere in this file:
// it would be the first thing reached for when a certificate is wrong, which is
// exactly when it must not be available.
func validateTargetURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("base URL is not a valid URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("base URL must include a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if !isLoopbackHost(host) {
			return "", fmt.Errorf("base URL must use https (plaintext is allowed only for loopback)")
		}
	default:
		return "", fmt.Errorf("base URL must use https")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// isLoopbackHost reports whether a hostname denotes this machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// targetStore is a durable store for deployment targets, one JSON file per id under
// a single directory — the same sidecar approach as the deployment, project, and
// release stores. Owned solely by the run-loop goroutine, so it needs no locking.
type targetStore struct {
	dir string
}

func newTargetStore(dir string) (*targetStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("targetstore: create dir: %w", err)
	}
	return &targetStore{dir: dir}, nil
}

func (s *targetStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

func (s *targetStore) save(rec deploymentTarget) error {
	return sidecar.WriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

func (s *targetStore) get(id string) (deploymentTarget, bool, error) {
	data, err := os.ReadFile(s.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return deploymentTarget{}, false, nil
		}
		return deploymentTarget{}, false, fmt.Errorf("targetstore: read: %w", err)
	}
	var rec deploymentTarget
	if err := json.Unmarshal(data, &rec); err != nil {
		return deploymentTarget{}, false, fmt.Errorf("targetstore: decode: %w", err)
	}
	return rec, true, nil
}

func (s *targetStore) delete(id string) error {
	if err := os.Remove(s.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("targetstore: remove: %w", err)
	}
	return sidecar.FsyncDir(s.dir)
}

// loadAll reads every target, oldest first, for a stable listing.
func (s *targetStore) loadAll() ([]deploymentTarget, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("targetstore: read dir: %w", err)
	}
	var out []deploymentTarget
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("targetstore: read %s: %w", e.Name(), err)
		}
		var rec deploymentTarget
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("targetstore: decode %s: %w", e.Name(), err)
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
