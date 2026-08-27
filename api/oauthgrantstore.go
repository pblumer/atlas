package api

import (
	"crypto/subtle"
	"strings"
	"sync"

	"github.com/pblumer/atlas/api/sidecar"
)

// What a person's approval produced: a grant (ADR-0200).
//
// A grant is one person having allowed one client to act as them against one
// resource. It is the thing that actually reaches anything — the client secret
// only lets an application *ask*. It is durable because a connector must survive a
// restart of this server: losing grants on a restart would sign every connector
// out, and an operator would learn that from a support ticket.
//
// The credential shape is ADR-0194's, deliberately: the token is CSPRNG output,
// only its SHA-256 is stored, and revocation is deletion. What differs is what the
// record carries, and each difference is the point of this record existing at all:
//
//   - **A person.** An API token identifies a machine; a grant identifies the human
//     who approved it. That is what keeps ADR-0196's property true through a hosted
//     client: a tool call is exactly as privileged as whoever made it.
//   - **An expiry in hours, and a refresh token.** An API token lives for months
//     because a human pasted it somewhere; an access token is short because the
//     client can silently get another, and a short-lived one limits what a copy of
//     it is worth.
//   - **A resource.** Which of this server's two canonical URIs the person approved
//     it for, which decides what it reaches.

const (
	// oauthAccessPrefix and oauthRefreshPrefix mark the two token kinds. Distinct so
	// that presenting one where the other belongs is refused for a legible reason
	// rather than by hash mismatch, and so a leaked one is greppable as what it is.
	oauthAccessPrefix  = "atlasoa_"
	oauthRefreshPrefix = "atlasor_"
)

// oauthGrant is one person's standing approval for one client.
type oauthGrant struct {
	ID string `json:"id"`

	ClientID   string `json:"clientId"`
	ClientName string `json:"clientName"` // copied so a listing reads without a join

	// The person, and what they could do when they approved.
	//
	// Snapshotted rather than read fresh, because resolving a token happens on a
	// handler goroutine and the user store belongs to the run loop — the same reason
	// the token indexes exist at all. A snapshot that nobody maintained would be the
	// wrong trade for a credential that can stand for months, so it is maintained:
	// disabling or deleting the account revokes the grants, and a role or group
	// change rewrites them, at the same call sites that already do this for live
	// sessions (revokeUserGrants, refreshUserGrants).
	UserID   string   `json:"userId"`
	Username string   `json:"username"`
	Roles    []string `json:"roles,omitempty"`
	GroupIDs []string `json:"groupIds,omitempty"`

	// Resource is the canonical URI the person approved this for, as the client
	// named it in the RFC 8707 `resource` parameter. It is validated at issuance —
	// against the two URIs this server publishes metadata for — rather than
	// re-derived per request, because the origin a request appears to arrive at can
	// move with a proxy header while a grant must not stop working when it does.
	Resource string `json:"resource"`

	AccessHash    string `json:"accessHash"`
	AccessExpires int64  `json:"accessExpires"`
	RefreshHash   string `json:"refreshHash"`

	CreatedAt int64 `json:"createdAt"`
}

// accessExpired reports whether the access token has run out at the given Unix
// time. The refresh token has no expiry of its own: a grant ends when it is
// revoked, which is the event an operator can see and cause.
func (g oauthGrant) accessExpired(now int64) bool {
	return g.AccessExpires != 0 && now >= g.AccessExpires
}

// scope is the reach a token from this grant has.
//
// A grant for the MCP transport reaches the transport and nothing else. That is
// not the OAuth `scope` parameter — it follows from *which resource* the person
// approved, which is the honest reading of an audience: a token minted to talk to
// /mcp has no business driving /api/v1 directly. A grant for the server as a whole
// carries no confinement and reaches what its person reaches.
func (g oauthGrant) scope() string {
	if strings.HasSuffix(g.Resource, "/mcp") {
		return apiScopeMCP
	}
	return ""
}

// oauthGrantView is what an operator sees: who approved what, for which client,
// and when. Neither token is recoverable from it.
type oauthGrantView struct {
	ID            string `json:"id"`
	ClientID      string `json:"clientId"`
	ClientName    string `json:"clientName"`
	UserID        string `json:"userId"`
	Username      string `json:"username"`
	Resource      string `json:"resource"`
	AccessExpires int64  `json:"accessExpires"`
	CreatedAt     int64  `json:"createdAt"`
}

func (g oauthGrant) view() oauthGrantView {
	return oauthGrantView{
		ID: g.ID, ClientID: g.ClientID, ClientName: g.ClientName,
		UserID: g.UserID, Username: g.Username, Resource: g.Resource,
		AccessExpires: g.AccessExpires, CreatedAt: g.CreatedAt,
	}
}

type oauthGrantStore = sidecar.Store[oauthGrant]

func newOAuthGrantStore(dir string) (*oauthGrantStore, error) {
	return sidecar.NewStore(dir, "oauthgrantstore",
		func(rec oauthGrant) string { return rec.ID },
		sidecar.Order(func(a, b oauthGrant) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// oauthGrantIndex mirrors the durable grants for the handler goroutines, keyed by
// both hashes: an access token is resolved on every request, a refresh token on
// every renewal.
type oauthGrantIndex struct {
	mu        sync.RWMutex
	byAccess  map[string]oauthGrant
	byRefresh map[string]oauthGrant
}

func newOAuthGrantIndex() *oauthGrantIndex {
	return &oauthGrantIndex{
		byAccess:  map[string]oauthGrant{},
		byRefresh: map[string]oauthGrant{},
	}
}

func (i *oauthGrantIndex) replaceAll(recs []oauthGrant) {
	access := make(map[string]oauthGrant, len(recs))
	refresh := make(map[string]oauthGrant, len(recs))
	for _, rec := range recs {
		access[rec.AccessHash] = rec
		refresh[rec.RefreshHash] = rec
	}
	i.mu.Lock()
	i.byAccess, i.byRefresh = access, refresh
	i.mu.Unlock()
}

// put registers or replaces a grant. Rotation goes through here: the old hashes
// are dropped in the same critical section the new ones are added in, so a rotated
// token is never briefly valid alongside its replacement.
func (i *oauthGrantIndex) put(rec oauthGrant) {
	i.mu.Lock()
	for h, g := range i.byAccess {
		if g.ID == rec.ID {
			delete(i.byAccess, h)
		}
	}
	for h, g := range i.byRefresh {
		if g.ID == rec.ID {
			delete(i.byRefresh, h)
		}
	}
	i.byAccess[rec.AccessHash] = rec
	i.byRefresh[rec.RefreshHash] = rec
	i.mu.Unlock()
}

// forUser returns the ids of every grant belonging to a user.
func (i *oauthGrantIndex) forUser(userID string) []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, g := range i.byAccess {
		if g.UserID == userID && !seen[g.ID] {
			seen[g.ID] = true
			out = append(out, g.ID)
		}
	}
	return out
}

func (i *oauthGrantIndex) remove(id string) {
	i.mu.Lock()
	for h, g := range i.byAccess {
		if g.ID == id {
			delete(i.byAccess, h)
		}
	}
	for h, g := range i.byRefresh {
		if g.ID == id {
			delete(i.byRefresh, h)
		}
	}
	i.mu.Unlock()
}

// matchAccess resolves a presented access token, refusing an expired one.
//
// The walk is constant-time with respect to the secret, for apiTokenIndex.match's
// reason: neither the value presented nor how many grants exist should be readable
// from how long this takes.
func (i *oauthGrantIndex) matchAccess(secret string, now int64) (oauthGrant, bool) {
	if !strings.HasPrefix(secret, oauthAccessPrefix) {
		return oauthGrant{}, false // not an access token; let other schemes try
	}
	g, ok := i.find(true, hashAPIToken(secret))
	if !ok || g.accessExpired(now) {
		return oauthGrant{}, false
	}
	return g, true
}

// matchRefresh resolves a presented refresh token. It has no expiry to check: a
// grant ends by revocation, which is an act an operator performs and can see.
func (i *oauthGrantIndex) matchRefresh(secret string) (oauthGrant, bool) {
	if !strings.HasPrefix(secret, oauthRefreshPrefix) {
		return oauthGrant{}, false
	}
	return i.find(false, hashAPIToken(secret))
}

// find walks one of the two hash maps in constant time with respect to want.
//
// Which map is a parameter rather than the map itself, so the read lock is taken
// exactly once: a helper that returned the map under RLock and then re-locked here
// would nest two RLocks, and Go's RWMutex blocks a new reader once a writer is
// waiting — which makes recursive RLock a deadlock rather than merely untidy.
func (i *oauthGrantIndex) find(access bool, want string) (oauthGrant, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	m := i.byRefresh
	if access {
		m = i.byAccess
	}
	var (
		found oauthGrant
		ok    bool
	)
	for h, g := range m {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			found, ok = g, true
		}
	}
	return found, ok
}
