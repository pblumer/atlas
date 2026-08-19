package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// publicLink is a revocable, unauthenticated entry point that starts one process
// via one start form (ADR-0029). The token is an opaque, unguessable handle; it
// binds to a process *id* (not a version), so the link always starts the current
// deployment. FormID pins which form the public page renders and validates
// against, captured when the link is minted.
type publicLink struct {
	Token     string `json:"token"`
	ProcessID string `json:"processId"`
	FormID    string `json:"formId"`
	CreatedAt int64  `json:"createdAt"`
}

// newPublicToken mints an opaque, URL-safe, unguessable link token. 32 bytes of
// crypto randomness hex-encoded is filename-safe (so it is its own store key) and
// well beyond guessing.
func newPublicToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("publiclink: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// publicLinkStore is a durable sidecar for public start links, one JSON file per
// token, mirroring the form/draft stores (ADR-0019/0021). Owned solely by the
// server's run-loop goroutine, so it needs no locking of its own.
type publicLinkStore struct {
	dir string
}

func newPublicLinkStore(dir string) (*publicLinkStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("publiclinkstore: create dir: %w", err)
	}
	return &publicLinkStore{dir: dir}, nil
}

// fileFor maps a token to its record path. A token is hex, so it is already a
// safe filename; the ".json" suffix marks it as a record.
func (p *publicLinkStore) fileFor(token string) string {
	return filepath.Join(p.dir, token+".json")
}

func (p *publicLinkStore) save(rec publicLink) error {
	return sidecar.WriteJSON(p.dir, p.fileFor(rec.Token), rec)
}

func (p *publicLinkStore) get(token string) (publicLink, bool, error) {
	// Reject anything that is not a bare hex token so a crafted token cannot walk
	// out of the directory or name a non-record file.
	if token == "" || !isHexToken(token) {
		return publicLink{}, false, nil
	}
	data, err := os.ReadFile(p.fileFor(token))
	if err != nil {
		if os.IsNotExist(err) {
			return publicLink{}, false, nil
		}
		return publicLink{}, false, fmt.Errorf("publiclinkstore: read: %w", err)
	}
	var rec publicLink
	if err := json.Unmarshal(data, &rec); err != nil {
		return publicLink{}, false, fmt.Errorf("publiclinkstore: decode: %w", err)
	}
	return rec, true, nil
}

func (p *publicLinkStore) delete(token string) error {
	if !isHexToken(token) {
		return nil
	}
	if err := os.Remove(p.fileFor(token)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("publiclinkstore: remove: %w", err)
	}
	return sidecar.FsyncDir(p.dir)
}

func (p *publicLinkStore) loadAll() ([]publicLink, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, fmt.Errorf("publiclinkstore: read dir: %w", err)
	}
	var out []publicLink
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if !isHexToken(strings.TrimSuffix(name, ".json")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.dir, name))
		if err != nil {
			return nil, fmt.Errorf("publiclinkstore: read %s: %w", name, err)
		}
		var rec publicLink
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("publiclinkstore: decode %s: %w", name, err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// isHexToken reports whether s is a non-empty, even-length hex string — the only
// shape newPublicToken produces. It is the guard that keeps a token from being
// used as a path traversal or naming a foreign file.
func isHexToken(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
