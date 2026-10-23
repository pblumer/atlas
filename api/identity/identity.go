// Package identity holds the external identities Atlas users link and the delegated
// connections they authorize, and brokers the short-lived credentials those
// connections yield (ADR-0475 §3, §4).
//
// Four concepts stay apart here. An Atlas principal is the user store's. An
// [ExternalIdentity] is one account at a provider, bound by issuer, tenant and stable
// subject — never by e-mail. A [Connection] is a principal's authorization for Atlas
// to act as that identity, through the Workers its owner allowed. Its credential, a
// refresh token, is a vault secret under a reserved name and never part of the
// connection record, a response, a log line or an event.
//
// Design-time state: two sidecar stores owned by the run loop. Nothing here reaches
// the event log; the decisions made with a connection do, through the enforcement
// point.
package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/limits"
)

// Providers this build can link.
const ProviderMicrosoft = "microsoft"

// AuthCodePKCE is how a delegated connection was authorized: the browser's
// authorization-code flow with PKCE, at the provider.
const AuthCodePKCE = "authorization_code_pkce"

// Connection states. They are authz's words, so the decision reads them as stored.
const (
	StatusActive         = "active"
	StatusReauthRequired = "reauth_required"
	StatusRevoked        = "revoked"
)

// Errors a caller distinguishes.
var (
	ErrNotFound        = errors.New("identity: no such connection")
	ErrNotOwner        = errors.New("identity: only the connection's owner may do that")
	ErrRevoked         = errors.New("identity: the connection is revoked")
	ErrInactive        = errors.New("identity: the connection is not active")
	ErrLinkedElsewhere = errors.New("identity: that external account is linked to another Atlas user")
	ErrUnknownWorker   = errors.New("identity: no configured Worker has that name")
	ErrAmbiguous       = errors.New("identity: more than one connection of the owner allows this Worker")
	// ErrInvalidGrant is a provider refusing a refresh token: revoked, expired, or
	// consent withdrawn. Only a person signing in again can mend it.
	ErrInvalidGrant = errors.New("identity: the provider refused the refresh token")
)

// ExternalIdentity is one account at a provider that an Atlas user linked.
type ExternalIdentity struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	Provider string `json:"provider"`
	Issuer   string `json:"issuer"`
	TenantID string `json:"tenantId,omitempty"`
	// Subject is the provider's stable identifier for the person: Microsoft's object
	// id (oid), which unlike the pairwise sub identifies the person across
	// applications.
	Subject string `json:"subject"`
	// Username and DisplayName are what the provider called the account at the last
	// sign-in. Shown, never matched: an e-mail address can be reassigned.
	Username            string `json:"username,omitempty"`
	DisplayName         string `json:"displayName,omitempty"`
	LinkedAt            int64  `json:"linkedAt"`
	LastAuthenticatedAt int64  `json:"lastAuthenticatedAt"`
}

// Key is the identity's name in a decision's ExecutedAs.
func (x ExternalIdentity) Key() string { return x.Provider + ":" + x.TenantID + ":" + x.Subject }

// Connection is the public half of a delegated connection.
type Connection struct {
	ID         string `json:"id"`
	OwnerID    string `json:"ownerId"`
	Provider   string `json:"provider"`
	IdentityID string `json:"identityId"`
	// IdentityKey is the identity's Key, kept here so a decision can name whom it ran
	// as without reading the identity store.
	IdentityKey string   `json:"identityKey"`
	AuthMethod  string   `json:"authMethod"`
	TenantID    string   `json:"tenantId,omitempty"`
	Scopes      []string `json:"scopes"`
	// AllowedWorkers are the configured Workers that may act through this
	// connection. Empty allows none: consenting to a sign-in is not consenting to
	// every Worker.
	AllowedWorkers        []string `json:"allowedWorkers"`
	Status                string   `json:"status"`
	CreatedAt             int64    `json:"createdAt"`
	UpdatedAt             int64    `json:"updatedAt"`
	LastInteractiveAuthAt int64    `json:"lastInteractiveAuthAt"`
	LastRefreshAt         int64    `json:"lastRefreshAt,omitempty"`
	RevokedAt             int64    `json:"revokedAt,omitempty"`
	RevokedBy             string   `json:"revokedBy,omitempty"`
	RevokeReason          string   `json:"revokeReason,omitempty"`
}

// credential is the vault value behind a connection.
type credential struct {
	RefreshToken string `json:"refreshToken"`
	// Generation counts rotations, so a credential's age can be told from its value.
	Generation int `json:"generation"`
}

// Tokens is a provider's answer to a refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string // empty when the provider does not rotate
	ExpiresIn    int64  // seconds
	Scope        string
}

// Refresher asks the provider for an access token with a refresh token. It returns
// ErrInvalidGrant (wrapped or not) when the provider refused the token itself.
type Refresher func(ctx context.Context, c Connection, refreshToken string) (Tokens, error)

// Secrets is the vault, as this service uses it. Every call is made on the run loop.
type Secrets interface {
	Get(name string) (string, bool, error)
	Set(name, value string) (vault.Meta, error)
	Delete(name string) error
}

// Stores are the two sidecar stores.
type Stores struct {
	identities  *sidecar.Store[ExternalIdentity]
	connections *sidecar.Store[Connection]
}

// OpenStores opens the stores under dir.
func OpenStores(dir string) (*Stores, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	ids, err := sidecar.NewStore(filepath.Join(dir, "external-identities"), "external identity",
		func(x ExternalIdentity) string { return x.ID })
	if err != nil {
		return nil, err
	}
	conns, err := sidecar.NewStore(filepath.Join(dir, "connections"), "connection",
		func(c Connection) string { return c.ID })
	if err != nil {
		return nil, err
	}
	return &Stores{identities: ids, connections: conns}, nil
}

// Service is the identity area. Build it with [New].
type Service struct {
	loop        *runloop.Loop
	stores      *Stores
	secrets     Secrets
	refresh     Refresher
	workerKnown func(name string) bool
	now         func() time.Time

	// tokens caches access tokens per connection; each entry's mutex serializes that
	// connection's refreshes, so one connection is refreshed by one caller at a time
	// and a rotated refresh token is never used twice.
	mu     sync.Mutex
	tokens map[string]*cachedToken
	// linkable is what HandleMine offers to link with.
	linkable []LinkOption

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own.
	Limits limits.Limits
}

// budgets is how this service reads a ceiling, defaulting the zero Limits (which
// admits nothing) to [limits.Default].
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}

type cachedToken struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// New builds the service. refresh asks the provider for tokens; workerKnown says
// whether a configured Worker of that name exists (it is called on the loop).
func New(loop *runloop.Loop, stores *Stores, secrets Secrets, refresh Refresher,
	workerKnown func(name string) bool, now func() time.Time) *Service {
	return &Service{loop: loop, stores: stores, secrets: secrets, refresh: refresh,
		workerKnown: workerKnown, now: now, tokens: map[string]*cachedToken{}, Limits: limits.Default()}
}

func newID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// LinkRequest is a verified sign-in at a provider: every field comes out of an ID
// token whose signature, issuer, audience, nonce and lifetime were checked, or out of
// the token response that accompanied it.
type LinkRequest struct {
	UserID       string
	Provider     string
	Issuer       string
	TenantID     string
	Subject      string
	Username     string
	DisplayName  string
	Scopes       []string
	RefreshToken string
}

// Link records a verified sign-in: the identity, and a connection authorized by it.
// Linking an account already linked to this user re-authenticates its connection;
// one linked to somebody else is refused. The credential is stored before the
// connection says it is active, so an active connection always has one.
func (s *Service) Link(req LinkRequest) (Connection, error) {
	switch {
	case req.UserID == "":
		return Connection{}, errors.New("identity: a link needs the signed-in Atlas user")
	case req.Provider != ProviderMicrosoft:
		return Connection{}, fmt.Errorf("identity: provider %q cannot be linked", req.Provider)
	case req.Issuer == "" || req.TenantID == "" || req.Subject == "":
		return Connection{}, errors.New("identity: a link needs a verified issuer, tenant and subject")
	case req.RefreshToken == "":
		return Connection{}, errors.New("identity: the provider issued no refresh token (was offline_access granted?)")
	}
	now := s.now().UnixNano()
	var (
		out Connection
		err error
	)
	s.loop.Do(func() { out, err = s.link(req, now) })
	return out, err
}

func (s *Service) link(req LinkRequest, now int64) (Connection, error) {
	ids, err := s.stores.identities.LoadAll()
	if err != nil {
		return Connection{}, err
	}
	var ident ExternalIdentity
	found := false
	for _, x := range ids {
		if x.Provider == req.Provider && x.Issuer == req.Issuer && x.TenantID == req.TenantID && x.Subject == req.Subject {
			if x.UserID != req.UserID {
				return Connection{}, ErrLinkedElsewhere
			}
			ident, found = x, true
			break
		}
	}
	if !found {
		id, err := newID("xid_")
		if err != nil {
			return Connection{}, err
		}
		ident = ExternalIdentity{ID: id, UserID: req.UserID, Provider: req.Provider, Issuer: req.Issuer,
			TenantID: req.TenantID, Subject: req.Subject, LinkedAt: now}
	}
	ident.Username, ident.DisplayName, ident.LastAuthenticatedAt = req.Username, req.DisplayName, now
	if err := s.stores.identities.Save(ident); err != nil {
		return Connection{}, err
	}
	conns, err := s.stores.connections.LoadAll()
	if err != nil {
		return Connection{}, err
	}
	var conn Connection
	reauth := false
	for _, c := range conns {
		if c.OwnerID == req.UserID && c.IdentityID == ident.ID && c.AuthMethod == AuthCodePKCE && c.Status != StatusRevoked {
			conn, reauth = c, true
			break
		}
	}
	if !reauth {
		id, err := newID("con_")
		if err != nil {
			return Connection{}, err
		}
		conn = Connection{ID: id, OwnerID: req.UserID, Provider: req.Provider, IdentityID: ident.ID,
			AuthMethod: AuthCodePKCE, TenantID: req.TenantID, AllowedWorkers: []string{}, CreatedAt: now}
	}
	gen := 0
	if raw, ok, err := s.secrets.Get(vault.ConnectionCredentialName(conn.ID)); err == nil && ok {
		var old credential
		if json.Unmarshal([]byte(raw), &old) == nil {
			gen = old.Generation + 1
		}
	}
	if err := s.putCredential(conn.ID, credential{RefreshToken: req.RefreshToken, Generation: gen}); err != nil {
		return Connection{}, err
	}
	conn.IdentityKey = ident.Key()
	conn.Scopes = append([]string{}, req.Scopes...)
	conn.Status = StatusActive
	conn.LastInteractiveAuthAt, conn.UpdatedAt = now, now
	if err := s.stores.connections.Save(conn); err != nil {
		return Connection{}, err
	}
	s.dropToken(conn.ID)
	return conn, nil
}

func (s *Service) putCredential(id string, c credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.secrets.Set(vault.ConnectionCredentialName(id), string(b))
	return err
}

// Get reads one connection.
func (s *Service) Get(id string) (Connection, bool, error) {
	var (
		c   Connection
		ok  bool
		err error
	)
	s.loop.Do(func() { c, ok, err = s.stores.connections.Get(id) })
	return c, ok, err
}

// ForOwner lists a user's identities and connections, newest first.
func (s *Service) ForOwner(userID string) ([]ExternalIdentity, []Connection) {
	var (
		ids   []ExternalIdentity
		conns []Connection
	)
	s.loop.Do(func() {
		all, _ := s.stores.identities.LoadAll()
		for _, x := range all {
			if x.UserID == userID {
				ids = append(ids, x)
			}
		}
		cs, _ := s.stores.connections.LoadAll()
		for _, c := range cs {
			if c.OwnerID == userID {
				conns = append(conns, c)
			}
		}
	})
	sort.Slice(ids, func(i, j int) bool { return ids[i].LinkedAt > ids[j].LinkedAt })
	sort.Slice(conns, func(i, j int) bool { return conns[i].CreatedAt > conns[j].CreatedAt })
	if ids == nil {
		ids = []ExternalIdentity{}
	}
	if conns == nil {
		conns = []Connection{}
	}
	return ids, conns
}

// SetAllowedWorkers replaces the Workers that may act through a connection. Only its
// owner consents to that: an administrator who could add a Worker here could have any
// Worker act as anybody.
func (s *Service) SetAllowedWorkers(id, by string, workers []string) (Connection, error) {
	seen := map[string]bool{}
	clean := []string{}
	for _, w := range workers {
		if w = strings.TrimSpace(w); w != "" && !seen[w] {
			seen[w] = true
			clean = append(clean, w)
		}
	}
	sort.Strings(clean)
	var (
		out Connection
		err error
	)
	s.loop.Do(func() {
		c, ok, gerr := s.stores.connections.Get(id)
		switch {
		case gerr != nil:
			err = gerr
		case !ok:
			err = ErrNotFound
		case c.OwnerID != by:
			err = ErrNotOwner
		case c.Status == StatusRevoked:
			err = ErrRevoked
		}
		if err != nil {
			return
		}
		for _, w := range clean {
			if !s.workerKnown(w) {
				err = fmt.Errorf("%w: %q", ErrUnknownWorker, w)
				return
			}
		}
		c.AllowedWorkers, c.UpdatedAt = clean, s.now().UnixNano()
		if err = s.stores.connections.Save(c); err == nil {
			out = c
		}
	})
	return out, err
}

// Revoke withdraws a connection for good: the credential is deleted and the record
// says who withdrew it and why. Its owner may revoke it, and so may an administrator
// — taking access away is never a person's alone to block.
func (s *Service) Revoke(id, by string, admin bool, reason string) (Connection, error) {
	var (
		out Connection
		err error
	)
	s.loop.Do(func() {
		c, ok, gerr := s.stores.connections.Get(id)
		switch {
		case gerr != nil:
			err = gerr
		case !ok:
			err = ErrNotFound
		case c.OwnerID != by && !admin:
			err = ErrNotOwner
		}
		if err != nil {
			return
		}
		if c.Status != StatusRevoked {
			now := s.now().UnixNano()
			c.Status, c.RevokedAt, c.RevokedBy, c.RevokeReason, c.UpdatedAt = StatusRevoked, now, by, reason, now
			if err = s.secrets.Delete(vault.ConnectionCredentialName(id)); err != nil {
				return
			}
			if err = s.stores.connections.Save(c); err != nil {
				return
			}
		}
		out = c
	})
	s.dropToken(id)
	return out, err
}

func (s *Service) markReauth(id string) error {
	var err error
	s.loop.Do(func() {
		c, ok, gerr := s.stores.connections.Get(id)
		if gerr != nil || !ok || c.Status != StatusActive {
			err = gerr
			return
		}
		c.Status, c.UpdatedAt = StatusReauthRequired, s.now().UnixNano()
		err = s.stores.connections.Save(c)
	})
	s.dropToken(id)
	return err
}

// ConnectionFor chooses the connection a Worker acts through for an owner: their one
// connection of that provider that allows the Worker. None is "no connection"; two is
// ambiguous, and ambiguity chooses nothing rather than guessing whose account acts.
// Revoked and inactive connections are returned when they are the only candidate, so
// the decision can say why it refused.
func (s *Service) ConnectionFor(ownerID, provider, worker string) (Connection, bool, error) {
	var (
		matches []Connection
		err     error
	)
	s.loop.Do(func() {
		var all []Connection
		all, err = s.stores.connections.LoadAll()
		for _, c := range all {
			if c.OwnerID == ownerID && c.Provider == provider && c.Status != StatusRevoked && containsString(c.AllowedWorkers, worker) {
				matches = append(matches, c)
			}
		}
	})
	switch {
	case err != nil:
		return Connection{}, false, err
	case len(matches) == 0:
		return Connection{}, false, nil
	case len(matches) > 1:
		return Connection{}, false, ErrAmbiguous
	}
	return matches[0], true, nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// expirySkew is how long before its expiry an access token stops being handed out, so
// a Worker never receives one that dies on its way to the provider.
const expirySkew = 2 * time.Minute

func (s *Service) entry(id string) *cachedToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[id]
	if !ok {
		e = &cachedToken{}
		s.tokens[id] = e
	}
	return e
}

func (s *Service) dropToken(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, id)
}

// AccessToken is the credential broker: a short-lived access token for an active
// connection. Atlas is the only holder of the refresh token, refreshes one connection
// at a time, and writes a rotated refresh token to the vault before releasing the
// access token that came with it — so a crash can lose an access token, never leave
// the vault holding a superseded refresh token. A connection revoked while its
// refresh was in flight keeps its revocation: the rotated token is not written back
// and nothing is released.
//
// The caller is the enforcement point, after a durably recorded Permit; nothing else
// may call this.
func (s *Service) AccessToken(ctx context.Context, id string) (string, time.Time, error) {
	e := s.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	conn, cred, err := s.load(id)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	if e.token != "" && now.Before(e.expires.Add(-expirySkew)) {
		return e.token, e.expires, nil
	}
	tokens, err := s.refresh(ctx, conn, cred.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			_ = s.markReauth(id)
		}
		return "", time.Time{}, err
	}
	if tokens.AccessToken == "" || tokens.ExpiresIn <= 0 {
		return "", time.Time{}, errors.New("identity: the provider answered without a usable access token")
	}
	var persistErr error
	s.loop.Do(func() {
		c, ok, gerr := s.stores.connections.Get(id)
		switch {
		case gerr != nil:
			persistErr = gerr
			return
		case !ok || c.Status != StatusActive:
			persistErr = ErrInactive
			return
		}
		if tokens.RefreshToken != "" && tokens.RefreshToken != cred.RefreshToken {
			if persistErr = s.putCredential(id, credential{RefreshToken: tokens.RefreshToken, Generation: cred.Generation + 1}); persistErr != nil {
				return
			}
		}
		c.LastRefreshAt = s.now().UnixNano()
		persistErr = s.stores.connections.Save(c)
	})
	if persistErr != nil {
		return "", time.Time{}, persistErr
	}
	e.token, e.expires = tokens.AccessToken, now.Add(time.Duration(tokens.ExpiresIn)*time.Second)
	return e.token, e.expires, nil
}

func (s *Service) load(id string) (Connection, credential, error) {
	var (
		conn Connection
		cred credential
		err  error
	)
	s.loop.Do(func() {
		c, ok, gerr := s.stores.connections.Get(id)
		switch {
		case gerr != nil:
			err = gerr
			return
		case !ok:
			err = ErrNotFound
			return
		case c.Status != StatusActive:
			err = ErrInactive
			return
		}
		raw, ok, gerr := s.secrets.Get(vault.ConnectionCredentialName(id))
		if gerr != nil || !ok {
			err = fmt.Errorf("identity: connection %s has no credential: %w", id, ErrInactive)
			return
		}
		if gerr := json.Unmarshal([]byte(raw), &cred); gerr != nil || cred.RefreshToken == "" {
			err = fmt.Errorf("identity: connection %s has an unreadable credential", id)
			return
		}
		conn = c
	})
	return conn, cred, err
}
