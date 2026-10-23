package identity

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/vault"
)

// memSecrets is the vault as the service sees it.
type memSecrets struct {
	mu   sync.Mutex
	data map[string]string
	fail error
}

func (m *memSecrets) Get(name string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[name]
	return v, ok, nil
}

func (m *memSecrets) Set(name, value string) (vault.Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return vault.Meta{}, m.fail
	}
	m.data[name] = value
	return vault.Meta{Name: name}, nil
}

func (m *memSecrets) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, name)
	return nil
}

type rig struct {
	svc     *Service
	secrets *memSecrets
	now     time.Time
	refresh func(ctx context.Context, c Connection, rt string) (Tokens, error)
}

func newRig(t *testing.T) *rig {
	t.Helper()
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	r := &rig{secrets: &memSecrets{data: map[string]string{}}, now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	stores, err := OpenStores(filepath.Join(t.TempDir(), "identity"))
	if err != nil {
		t.Fatal(err)
	}
	r.svc = New(loop, stores, r.secrets,
		func(ctx context.Context, c Connection, rt string) (Tokens, error) { return r.refresh(ctx, c, rt) },
		func(name string) bool { return name == "sp-prod" || name == "sp-test" },
		func() time.Time { return r.now })
	return r
}

func aliceLink() LinkRequest {
	return LinkRequest{
		UserID: "usr_alice", Provider: ProviderMicrosoft,
		Issuer: "https://login.microsoftonline.com/tid-1/v2.0", TenantID: "tid-1", Subject: "oid-alice",
		Username: "alice@contoso.example", DisplayName: "Alice", Scopes: []string{"Sites.ReadWrite.All", "offline_access"},
		RefreshToken: "rt-1",
	}
}

func TestLinkingCreatesAnIdentityAndAConnectionThatAllowsNothingYet(t *testing.T) {
	r := newRig(t)
	c, err := r.svc.Link(aliceLink())
	if err != nil {
		t.Fatal(err)
	}
	if c.OwnerID != "usr_alice" || c.Status != StatusActive || len(c.AllowedWorkers) != 0 ||
		c.AuthMethod != AuthCodePKCE || c.LastInteractiveAuthAt != r.now.UnixNano() {
		t.Fatalf("connection = %+v", c)
	}
	ids, conns := r.svc.ForOwner("usr_alice")
	if len(ids) != 1 || len(conns) != 1 {
		t.Fatalf("for owner: %d identities, %d connections", len(ids), len(conns))
	}
	if ids[0].Key() != "microsoft:tid-1:oid-alice" {
		t.Errorf("identity key = %s", ids[0].Key())
	}
	if got := r.secrets.data[vault.ConnectionCredentialName(c.ID)]; got == "" {
		t.Fatal("no credential was stored")
	}
}

// An external account is one person's: binding it to a second Atlas user would let
// that user act as the first.
func TestAnIdentityLinkedToSomebodyElseIsRefused(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Link(aliceLink()); err != nil {
		t.Fatal(err)
	}
	bob := aliceLink()
	bob.UserID = "usr_bob"
	if _, err := r.svc.Link(bob); !errors.Is(err, ErrLinkedElsewhere) {
		t.Fatalf("err = %v", err)
	}
	// The same object id in another tenant is another identity.
	other := aliceLink()
	other.UserID, other.TenantID, other.Issuer = "usr_bob", "tid-2", "https://login.microsoftonline.com/tid-2/v2.0"
	if _, err := r.svc.Link(other); err != nil {
		t.Fatalf("a different tenant's identity: %v", err)
	}
}

func TestLinkingRefusesWhatCannotBeUsedOrIdentified(t *testing.T) {
	r := newRig(t)
	for name, mutate := range map[string]func(*LinkRequest){
		"no refresh token": func(l *LinkRequest) { l.RefreshToken = "" },
		"no subject":       func(l *LinkRequest) { l.Subject = "" },
		"no issuer":        func(l *LinkRequest) { l.Issuer = "" },
		"no tenant":        func(l *LinkRequest) { l.TenantID = "" },
		"no owner":         func(l *LinkRequest) { l.UserID = "" },
		"unknown provider": func(l *LinkRequest) { l.Provider = "apple" },
	} {
		l := aliceLink()
		mutate(&l)
		if _, err := r.svc.Link(l); err == nil {
			t.Errorf("%s: linked", name)
		}
	}
}

// Linking again is re-authentication: the same connection, active, with a fresh
// credential and a fresh interactive-authentication time — and its allowed Workers
// kept, because consent to a Worker is not consent to a sign-in.
func TestLinkingAgainReauthenticatesTheSameConnection(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	if _, err := r.svc.SetAllowedWorkers(c.ID, "usr_alice", []string{"sp-prod"}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.markReauth(c.ID); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Hour)
	again := aliceLink()
	again.RefreshToken = "rt-2"
	c2, err := r.svc.Link(again)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID != c.ID || c2.Status != StatusActive || c2.LastInteractiveAuthAt != r.now.UnixNano() || len(c2.AllowedWorkers) != 1 {
		t.Fatalf("relinked = %+v", c2)
	}
}

func TestOnlyTheOwnerAllowsWorkersAndOnlyRealOnes(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	if _, err := r.svc.SetAllowedWorkers(c.ID, "usr_bob", []string{"sp-prod"}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("somebody else allowed a Worker to act as alice: %v", err)
	}
	if _, err := r.svc.SetAllowedWorkers(c.ID, "usr_alice", []string{"nope"}); !errors.Is(err, ErrUnknownWorker) {
		t.Fatalf("an unknown Worker was allowed: %v", err)
	}
	got, err := r.svc.SetAllowedWorkers(c.ID, "usr_alice", []string{"sp-test", "sp-prod", "sp-prod"})
	if err != nil || len(got.AllowedWorkers) != 2 || got.AllowedWorkers[0] != "sp-prod" {
		t.Fatalf("allowed = %+v (%v)", got.AllowedWorkers, err)
	}
	if _, err := r.svc.SetAllowedWorkers("con_missing", "usr_alice", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestRevocationDeletesTheCredentialAndIsFinal(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	if _, err := r.svc.Revoke(c.ID, "usr_bob", false, "x"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("a stranger revoked: %v", err)
	}
	got, err := r.svc.Revoke(c.ID, "usr_admin", true, "left the company")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRevoked || got.RevokedBy != "usr_admin" || got.RevokeReason != "left the company" {
		t.Fatalf("revoked = %+v", got)
	}
	if _, ok := r.secrets.data[vault.ConnectionCredentialName(c.ID)]; ok {
		t.Fatal("the credential outlived its connection")
	}
	if _, err := r.svc.SetAllowedWorkers(c.ID, "usr_alice", []string{"sp-prod"}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked connection was changed: %v", err)
	}
	// Linking the same account again makes a new connection: a revocation is not undone.
	c2, err := r.svc.Link(aliceLink())
	if err != nil || c2.ID == c.ID {
		t.Fatalf("relink after revocation: %+v %v", c2, err)
	}
}

func TestTheExecutingConnectionIsTheOwnersOneForThatWorker(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	if _, ok, _ := r.svc.ConnectionFor("usr_alice", ProviderMicrosoft, "sp-prod"); ok {
		t.Fatal("a connection that allows no Worker was chosen")
	}
	_, _ = r.svc.SetAllowedWorkers(c.ID, "usr_alice", []string{"sp-prod"})
	got, ok, err := r.svc.ConnectionFor("usr_alice", ProviderMicrosoft, "sp-prod")
	if err != nil || !ok || got.ID != c.ID {
		t.Fatalf("chosen = %+v %v %v", got, ok, err)
	}
	if _, ok, _ := r.svc.ConnectionFor("usr_bob", ProviderMicrosoft, "sp-prod"); ok {
		t.Fatal("somebody else's connection was chosen")
	}
	// Two accounts allowed for one Worker is ambiguous, and ambiguity chooses nothing.
	second := aliceLink()
	second.Subject = "oid-alice-admin"
	c2, _ := r.svc.Link(second)
	_, _ = r.svc.SetAllowedWorkers(c2.ID, "usr_alice", []string{"sp-prod"})
	if _, _, err := r.svc.ConnectionFor("usr_alice", ProviderMicrosoft, "sp-prod"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheBrokerRefreshesRotatesAndCaches(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	calls := 0
	r.refresh = func(_ context.Context, got Connection, rt string) (Tokens, error) {
		calls++
		if got.ID != c.ID {
			t.Errorf("refreshed %s", got.ID)
		}
		return Tokens{AccessToken: "at-" + rt, RefreshToken: rt + "'", ExpiresIn: 3600}, nil
	}
	tok, exp, err := r.svc.AccessToken(context.Background(), c.ID)
	if err != nil || tok != "at-rt-1" || !exp.Equal(r.now.Add(time.Hour)) {
		t.Fatalf("token = %q %v %v", tok, exp, err)
	}
	// The rotated token replaced the old one before the access token was released.
	if cred := r.secrets.data[vault.ConnectionCredentialName(c.ID)]; cred == "" || !contains(cred, `rt-1'`) {
		t.Fatalf("stored credential = %s", cred)
	}
	if tok, _, _ := r.svc.AccessToken(context.Background(), c.ID); tok != "at-rt-1" || calls != 1 {
		t.Fatalf("not cached: %q after %d calls", tok, calls)
	}
	r.now = r.now.Add(time.Hour)
	if tok, _, _ := r.svc.AccessToken(context.Background(), c.ID); tok != "at-rt-1'" || calls != 2 {
		t.Fatalf("after expiry: %q after %d calls", tok, calls)
	}
}

func TestTheBrokerReleasesNothingItCouldNotPersist(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		return Tokens{AccessToken: "at", RefreshToken: "rotated", ExpiresIn: 3600}, nil
	}
	r.secrets.fail = errors.New("disk full")
	if tok, _, err := r.svc.AccessToken(context.Background(), c.ID); err == nil || tok != "" {
		t.Fatalf("released %q although the rotation was not durable (%v)", tok, err)
	}
}

func TestAProviderRefusalMarksTheConnectionForReauthentication(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		return Tokens{}, ErrInvalidGrant
	}
	if _, _, err := r.svc.AccessToken(context.Background(), c.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
	got, _, _ := r.svc.Get(c.ID)
	if got.Status != StatusReauthRequired {
		t.Fatalf("status = %s", got.Status)
	}
	if _, _, err := r.svc.AccessToken(context.Background(), c.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("an inactive connection was refreshed: %v", err)
	}
}

// A refresh in flight when the connection is revoked must not write the rotated token
// back: that would resurrect a credential its owner withdrew.
func TestARevocationDuringARefreshWins(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		if _, err := r.svc.Revoke(c.ID, "usr_alice", false, "now"); err != nil {
			t.Error(err)
		}
		return Tokens{AccessToken: "at", RefreshToken: "rotated", ExpiresIn: 3600}, nil
	}
	if tok, _, err := r.svc.AccessToken(context.Background(), c.ID); !errors.Is(err, ErrInactive) || tok != "" {
		t.Fatalf("token %q, err %v", tok, err)
	}
	if _, ok := r.secrets.data[vault.ConnectionCredentialName(c.ID)]; ok {
		t.Fatal("the revoked credential was written back")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
