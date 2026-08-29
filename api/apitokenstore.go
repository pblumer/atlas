package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/pblumer/atlas/api/sidecar"
)

// API tokens: the credential a machine that is not this server's own child
// authenticates with (ADR-0194).
//
// Until now the only non-session credential a general caller could hold was the
// internal service token of ADR-0049 — minted at startup from CSPRNG output, never
// served over any endpoint, and therefore obtainable only by the process that
// minted it. That was adequate while it had exactly one holder on one host. It
// stopped being adequate the moment a login became the default
// (ADR-0195): a worker on another host, a stdio MCP adapter
// against a remote server, a CI job — none of them had anything to present, and
// `atlas worker --token` / `atlas mcp --token` had no value an operator could put
// in them.
//
// This is the deploy-token pattern of ADR-0129 generalized, deliberately down to
// the details, because those details are the ones that make a machine credential
// safe to hand out:
//
//   - **The secret is never stored.** Only its SHA-256 is, and the plaintext is
//     returned exactly once, at mint time. A listing cannot leak it and neither
//     can a stolen data directory.
//   - **SHA-256, not bcrypt.** 32 bytes of CSPRNG output has nothing to
//     brute-force, and bcrypt would add its deliberate ~100ms to *every*
//     authenticated request rather than to a login.
//   - **Revocation is deletion.** No disabled flag to reason about, and nothing to
//     resurrect by flipping it back.
//
// Two things it adds over a deploy token, both because this credential is general
// where that one is narrow: an expiry, and a scope.

// apiTokenPrefix marks an API token's secret. A recognizable, greppable prefix is
// what lets a secret scanner spot one that leaked into a commit or a log, and lets
// a human tell at a glance which kind of credential they are holding — the same
// reason deploy tokens carry theirs, and distinct from it so the two never get
// mistaken for one another.
const apiTokenPrefix = "atlasat_"

// apiToken is a durable machine credential.
type apiToken struct {
	ID   string `json:"id"`
	Name string `json:"name"` // human label: which machine this was issued for
	Hash string `json:"hash"` // sha256 hex of the secret, never the secret

	// Scope is what this token may reach. Empty means apiScopeFull, so a record
	// written before scopes existed keeps working rather than silently becoming a
	// credential that can do nothing.
	Scope string `json:"scope,omitempty"`

	// Roles is what this token may *do*, snapshotted from the account that minted it
	// (ADR-draft-roles-per-endpoint-group). Never admin: a machine that administers
	// accounts is not a case Atlas has. Both halves are then enforced, and a request
	// has to pass both — the scope says which routes, the roles say which kinds of
	// operation.
	//
	// Empty means the legacy set, by the same reading as Scope above: a token minted
	// before roles existed keeps the reach it was issued with instead of becoming
	// inert on upgrade.
	Roles []string `json:"roles,omitempty"`

	// ExpiresAt is when the token stops being accepted, as a Unix time. Zero means
	// never — allowed, because a worker running for a year is a real case, but the
	// mint API asks for a lifetime so that "never" is a choice somebody made rather
	// than the shape of the request.
	ExpiresAt int64 `json:"expiresAt,omitempty"`

	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
}

// scope returns the token's effective scope, resolving the empty default.
func (t apiToken) scope() string {
	if t.Scope == "" {
		return apiScopeFull
	}
	return t.Scope
}

// roles returns the token's effective roles, resolving the empty default to the
// legacy set — what this credential could reach before roles existed.
func (t apiToken) roles() []string {
	if len(t.Roles) == 0 {
		return legacyRoles()
	}
	return t.Roles
}

// expired reports whether the token's lifetime has run out at the given Unix time.
func (t apiToken) expired(now int64) bool { return t.ExpiresAt != 0 && now >= t.ExpiresAt }

// apiTokenView is the JSON shape an operator sees: identity, reach and lifetime,
// never anything the secret could be recovered from.
type apiTokenView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
}

func (t apiToken) view() apiTokenView {
	return apiTokenView{
		ID: t.ID, Name: t.Name, Scope: t.scope(),
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy,
	}
}

// hashAPIToken is the one-way form stored on disk and compared against.
func hashAPIToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// apiTokenStore is a durable store, one JSON file per token id. Like every
// design-time store it is owned solely by the run-loop goroutine; the index below
// is the concurrent half.
type apiTokenStore = sidecar.Store[apiToken]

// newAPITokenStore opens (creating if needed) the api-tokens directory. Tokens
// list oldest first, tie-broken by id so the order is deterministic.
func newAPITokenStore(dir string) (*apiTokenStore, error) {
	return sidecar.NewStore(dir, "apitokenstore",
		func(rec apiToken) string { return rec.ID },
		sidecar.Order(func(a, b apiToken) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// apiTokenIndex is the in-memory view of the durable records, keyed by hash.
//
// It exists for the reason the deploy-token index does: authentication happens on
// the handler goroutine while the store is owned by the run loop, so resolving a
// bearer must neither read the disk per request nor touch a run-loop-owned store
// at all.
type apiTokenIndex struct {
	mu     sync.RWMutex
	byHash map[string]apiToken
}

func newAPITokenIndex() *apiTokenIndex {
	return &apiTokenIndex{byHash: map[string]apiToken{}}
}

// replaceAll rebuilds the index from the durable records, at startup.
func (i *apiTokenIndex) replaceAll(recs []apiToken) {
	m := make(map[string]apiToken, len(recs))
	for _, rec := range recs {
		m[rec.Hash] = rec
	}
	i.mu.Lock()
	i.byHash = m
	i.mu.Unlock()
}

// add registers a freshly minted token; remove revokes one.
func (i *apiTokenIndex) add(rec apiToken) {
	i.mu.Lock()
	i.byHash[rec.Hash] = rec
	i.mu.Unlock()
}

func (i *apiTokenIndex) remove(id string) {
	i.mu.Lock()
	for h, rec := range i.byHash {
		if rec.ID == id {
			delete(i.byHash, h)
		}
	}
	i.mu.Unlock()
}

// match resolves a presented secret to its token, refusing one whose lifetime has
// run out. The comparison walks every entry in constant time rather than taking
// the map hit directly, so neither the secret's value nor how many tokens exist is
// observable through timing.
//
// An expired token is treated exactly like an unknown one: the record stays on
// disk, so an operator can still see it in a listing and decide whether to reissue,
// but nothing it is presented for succeeds.
func (i *apiTokenIndex) match(secret string, now int64) (apiToken, bool) {
	if !strings.HasPrefix(secret, apiTokenPrefix) {
		return apiToken{}, false // not an API token; let other schemes try
	}
	want := hashAPIToken(secret)
	i.mu.RLock()
	defer i.mu.RUnlock()
	var (
		found apiToken
		ok    bool
	)
	for h, rec := range i.byHash {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			found, ok = rec, true
		}
	}
	if !ok || found.expired(now) {
		return apiToken{}, false
	}
	return found, true
}
