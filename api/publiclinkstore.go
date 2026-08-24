package api

import (
	"github.com/pblumer/atlas/api/sidecar"

	"github.com/pblumer/atlas/api/token"
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

// publicLinkStore is a durable sidecar for public start links, one JSON file per
// token, mirroring the form/draft stores (ADR-0019/0021). Owned solely by the
// server's run-loop goroutine, so it needs no locking of its own.

// publicLinkStore is a durable store for publicLink records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type publicLinkStore = sidecar.Store[publicLink]

// newPublicLinkStore opens (creating if needed) the public-links directory. A
// token is already hex, so it names its own file directly; token.IsHex is what
// keeps a foreign name from being read as a link (and a token from traversing a
// path). Links list newest first.
func newPublicLinkStore(dir string) (*publicLinkStore, error) {
	return sidecar.NewStore(dir, "publiclinkstore",
		func(rec publicLink) string { return rec.Token },
		sidecar.Names[publicLink](func(token string) string { return token }, token.IsHex),
		sidecar.Order(func(a, b publicLink) bool { return a.CreatedAt > b.CreatedAt }),
	)
}
