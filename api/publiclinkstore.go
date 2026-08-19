package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

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

// publicLinkStore is a durable store for publicLink records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type publicLinkStore = sidecar.Store[publicLink]

// newPublicLinkStore opens (creating if needed) the public-links directory. A
// token is already hex, so it names its own file directly; isHexToken is what
// keeps a foreign name from being read as a link (and a token from traversing a
// path). Links list newest first.
func newPublicLinkStore(dir string) (*publicLinkStore, error) {
	return sidecar.NewStore(dir, "publiclinkstore",
		func(rec publicLink) string { return rec.Token },
		sidecar.Names[publicLink](func(token string) string { return token }, isHexToken),
		sidecar.Order(func(a, b publicLink) bool { return a.CreatedAt > b.CreatedAt }),
	)
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
