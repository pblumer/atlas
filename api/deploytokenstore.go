package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/pblumer/atlas/api/sidecar"
)

// deployTokenPrefix marks a deploy token's secret. A recognizable, greppable
// prefix is what lets secret scanners and log filters spot one that leaked, and
// lets a human tell at a glance what kind of credential they are holding.
const deployTokenPrefix = "atlasdt_"

// deployToken is a durable credential a peer Atlas presents to publish a bundle
// here (ADR-0129). It is the first persistent machine credential in Atlas, so two
// properties are deliberate:
//
//   - **The secret is never stored.** Only its SHA-256 is, and the plaintext is
//     returned exactly once, at mint time. A listing therefore cannot leak it and
//     neither can a stolen data directory (the holder still needs the token).
//   - **SHA-256, not bcrypt.** Passwords are bcrypt-hashed because humans choose
//     low-entropy secrets that must be expensive to guess. A deploy token is 32
//     bytes of CSPRNG output, so there is nothing to brute-force, and bcrypt would
//     add its deliberate ~100ms cost to *every* authenticated request.
//
// Revocation is deletion: there is no disabled state to reason about, and a
// deleted record cannot be resurrected by flipping a flag.
type deployToken struct {
	ID        string `json:"id"`
	Name      string `json:"name"` // human label, e.g. the peer it was issued for
	Hash      string `json:"hash"` // sha256 hex of the secret, never the secret
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
}

// deployTokenView is the JSON shape returned to an operator: identity and
// provenance, never anything the secret could be recovered from.
type deployTokenView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
}

func (t deployToken) view() deployTokenView {
	return deployTokenView{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy}
}

// hashDeployToken is the one-way form stored on disk and compared against.
func hashDeployToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// deployTokenStore is a durable store for deploy tokens, one JSON file per token
// id under a single directory (ADR-0129). Like every design-time store it is
// owned solely by the server's run-loop goroutine, so it needs no locking of its
// own — the lookup index below is the concurrent half.
type deployTokenStore = sidecar.Store[deployToken]

// newDeployTokenStore opens (creating if needed) the deploy-tokens directory.
// Tokens list oldest first, tie-broken by id so the order is deterministic.
func newDeployTokenStore(dir string) (*deployTokenStore, error) {
	return sidecar.NewStore(dir, "deploytokenstore",
		func(rec deployToken) string { return rec.ID },
		sidecar.Order(func(a, b deployToken) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// deployTokenIndex is the in-memory view of the durable records, keyed by hash.
//
// It exists because authentication happens on the *handler* goroutine, while the
// store is owned by the run loop: resolving a bearer token must not read the disk
// on every request, and must not touch a run-loop-owned store at all. The index
// mirrors the session store's discipline — a small mutex-guarded map, loaded at
// startup and updated whenever the durable records change — so the two never
// diverge and neither one blocks the other.
type deployTokenIndex struct {
	mu     sync.RWMutex
	byHash map[string]deployToken
}

func newDeployTokenIndex() *deployTokenIndex {
	return &deployTokenIndex{byHash: map[string]deployToken{}}
}

// replaceAll rebuilds the index from the durable records (startup, ADR-0019-style
// recovery of design-time state).
func (i *deployTokenIndex) replaceAll(recs []deployToken) {
	m := make(map[string]deployToken, len(recs))
	for _, rec := range recs {
		m[rec.Hash] = rec
	}
	i.mu.Lock()
	i.byHash = m
	i.mu.Unlock()
}

// add registers a freshly minted token; remove revokes one.
func (i *deployTokenIndex) add(rec deployToken) {
	i.mu.Lock()
	i.byHash[rec.Hash] = rec
	i.mu.Unlock()
}

func (i *deployTokenIndex) remove(id string) {
	i.mu.Lock()
	for h, rec := range i.byHash {
		if rec.ID == id {
			delete(i.byHash, h)
		}
	}
	i.mu.Unlock()
}

// match resolves a presented secret to its token. The comparison walks every entry
// in constant time rather than taking the map hit directly, so neither the secret's
// value nor how many tokens exist is observable through timing.
func (i *deployTokenIndex) match(secret string) (deployToken, bool) {
	if !strings.HasPrefix(secret, deployTokenPrefix) {
		return deployToken{}, false // not a deploy token; let other schemes try
	}
	want := hashDeployToken(secret)
	i.mu.RLock()
	defer i.mu.RUnlock()
	var (
		found deployToken
		ok    bool
	)
	for h, rec := range i.byHash {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			found, ok = rec, true
		}
	}
	return found, ok
}
