package api

import (
	"crypto/subtle"
	"strings"
	"sync"

	"github.com/pblumer/atlas/api/sidecar"
)

// The OAuth clients an operator has registered (ADR-0200).
//
// A client here is an application allowed to ask a person for access — a hosted
// MCP connector above all. It is not a credential a machine authenticates with on
// its own: holding the client secret lets you *start* an authorization, not reach
// anything. What reaches anything is the token a person's approval produces, and
// that is a grant (oauthgrantstore.go).
//
// Registration is by an administrator by default, and an operator may additionally
// open self-registration (RFC 7591) — see oauthregister.go, which is where the
// consequences of that are worked out. A client that registered itself carries
// Dynamic, so nothing downstream has to treat the two as the same thing.
//
// The storage discipline is the one apiTokenStore uses, for the same reasons: the
// secret is never stored, only its SHA-256; a durable record per client owned by
// the run-loop goroutine, mirrored into a mutex-guarded index that the handler
// goroutines read.

// oauthClientSecretPrefix marks a client secret, so a leaked one is greppable and
// a human can tell at a glance which kind of credential they are holding. Distinct
// from an API token's prefix, because the two are not interchangeable and a
// mistake between them should be visible rather than merely refused.
const oauthClientSecretPrefix = "atlascs_"

// oauthClient is a registered application.
type oauthClient struct {
	// ID is the client_id. It is public — it travels in an authorization URL the
	// person can read in their address bar — so unlike the secret it is stored and
	// listed as-is.
	ID   string `json:"id"`
	Name string `json:"name"` // shown to the person on the consent screen

	// SecretHash is the SHA-256 of the client secret, never the secret.
	SecretHash string `json:"secretHash"`

	// RedirectURIs is the exact set this client may be sent back to. Compared
	// whole, never by prefix: a prefix match is how an open redirector is built,
	// and an open redirector on an authorization endpoint hands the code away.
	RedirectURIs []string `json:"redirectUris"`

	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`

	// Dynamic marks a client that registered *itself* (RFC 7591), rather than one an
	// administrator entered. It is not bookkeeping: it travels to the consent screen,
	// because with self-registration open "an application is asking for access" no
	// longer implies anybody vetted it, and a person deciding has to be able to tell
	// the two apart. It also decides what may be evicted when the registry is full —
	// see oauthregister.go.
	Dynamic bool `json:"dynamic,omitempty"`

	// Seq orders self-registrations against each other. CreatedAt is a whole second
	// and a flood registers many within one, so ordering by it alone would make
	// "evict the oldest" pick an arbitrary member of the burst. Assigned inside the
	// run-loop turn that saves the record, as one more than the highest seen, so it
	// is monotonic without a clock. Zero on an operator-registered client, which is
	// never evicted anyway.
	Seq int64 `json:"seq,omitempty"`
}

// allowsRedirect reports whether uri is one this client registered. Exact string
// equality on purpose — see RedirectURIs.
func (c oauthClient) allowsRedirect(uri string) bool {
	for _, u := range c.RedirectURIs {
		if u == uri {
			return true
		}
	}
	return false
}

// oauthClientView is what an operator sees. The secret is absent because the
// server does not have it.
type oauthClientView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirectUris"`
	CreatedAt    int64    `json:"createdAt"`
	CreatedBy    string   `json:"createdBy,omitempty"`

	// SelfRegistered says nobody entered this client by hand. Always present rather
	// than omitted when false: a listing where the field is simply absent on the
	// vetted ones reads as if the distinction were not being made.
	SelfRegistered bool `json:"selfRegistered"`
}

func (c oauthClient) view() oauthClientView {
	return oauthClientView{
		ID: c.ID, Name: c.Name, RedirectURIs: c.RedirectURIs,
		CreatedAt: c.CreatedAt, CreatedBy: c.CreatedBy,
		SelfRegistered: c.Dynamic,
	}
}

// oauthClientStore is a durable store, one JSON file per client id.
type oauthClientStore = sidecar.Store[oauthClient]

func newOAuthClientStore(dir string) (*oauthClientStore, error) {
	return sidecar.NewStore(dir, "oauthclientstore",
		func(rec oauthClient) string { return rec.ID },
		sidecar.Order(func(a, b oauthClient) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// oauthClientIndex mirrors the durable records for the handler goroutines, keyed
// by client id. Authorization happens on a handler goroutine while the store
// belongs to the run loop, so resolving a client must not touch the store.
type oauthClientIndex struct {
	mu   sync.RWMutex
	byID map[string]oauthClient
}

func newOAuthClientIndex() *oauthClientIndex {
	return &oauthClientIndex{byID: map[string]oauthClient{}}
}

func (i *oauthClientIndex) replaceAll(recs []oauthClient) {
	m := make(map[string]oauthClient, len(recs))
	for _, rec := range recs {
		m[rec.ID] = rec
	}
	i.mu.Lock()
	i.byID = m
	i.mu.Unlock()
}

func (i *oauthClientIndex) add(rec oauthClient) {
	i.mu.Lock()
	i.byID[rec.ID] = rec
	i.mu.Unlock()
}

func (i *oauthClientIndex) remove(id string) {
	i.mu.Lock()
	delete(i.byID, id)
	i.mu.Unlock()
}

// lookup resolves a client_id. The client id is public, so this needs no
// constant-time care — unlike authenticate below.
func (i *oauthClientIndex) lookup(id string) (oauthClient, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	rec, ok := i.byID[id]
	return rec, ok
}

// authenticate resolves a client_id and secret pair, in constant time with respect
// to the secret. A client that presents the wrong secret is indistinguishable from
// one that does not exist.
func (i *oauthClientIndex) authenticate(id, secret string) (oauthClient, bool) {
	rec, ok := i.lookup(id)
	if !ok || !strings.HasPrefix(secret, oauthClientSecretPrefix) {
		return oauthClient{}, false
	}
	want := hashAPIToken(secret) // the same SHA-256 helper; one hashing rule in this package
	if subtle.ConstantTimeCompare([]byte(rec.SecretHash), []byte(want)) != 1 {
		return oauthClient{}, false
	}
	return rec, true
}
