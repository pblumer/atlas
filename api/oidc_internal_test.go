package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The parts of talking to a provider that are not the login itself: the
// configuration, the documents, and what happens when the provider is not what it
// said it was (ADR-0210).

// TestAProviderIsInertUntilItIsConfigured. The promise an installation is owed is
// that nothing changes until somebody asks for it, and it holds at the level of
// whether the server has a provider at all.
func TestAProviderIsInertUntilItIsConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  OIDCConfig
		want bool
	}{
		{"nothing at all", OIDCConfig{}, false},
		{"an issuer with no client id", OIDCConfig{Issuer: "https://idp.example"}, false},
		{"a client id with no issuer", OIDCConfig{ClientID: "atlas"}, false},
		{"blank strings", OIDCConfig{Issuer: "  ", ClientID: " "}, false},
		{"both", OIDCConfig{Issuer: "https://idp.example", ClientID: "atlas"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			WithOIDC(tc.cfg)(s)
			if got := s.oidc != nil; got != tc.want {
				t.Errorf("configured = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTheIssuerIsNormalizedAndLabelled: what an operator types and what the login
// screen says are different things, and neither should need them to be careful.
func TestTheIssuerIsNormalizedAndLabelled(t *testing.T) {
	s := &Server{}
	WithOIDC(OIDCConfig{Issuer: "https://login.example.com/realms/atlas/ ", ClientID: "atlas"})(s)
	if s.oidc == nil {
		t.Fatal("a trailing slash and spaces left the provider unconfigured")
	}
	if got := s.oidc.cfg.Issuer; got != "https://login.example.com/realms/atlas" {
		t.Errorf("issuer = %q, want it trimmed", got)
	}
	if got := s.oidc.cfg.label(); got != "login.example.com" {
		t.Errorf("label = %q, want the issuer's host when nobody named it", got)
	}
	if got := s.oidc.cfg.scopes(); got != oidcDefaultScopes {
		t.Errorf("scopes = %q, want the default", got)
	}

	named := OIDCConfig{Issuer: "https://idp.example", ClientID: "a", Name: "Contoso ID", Scopes: "openid"}
	if got := named.label(); got != "Contoso ID" {
		t.Errorf("label = %q, want what the operator wrote", got)
	}
	if got := named.scopes(); got != "openid" {
		t.Errorf("scopes = %q, want what the operator wrote", got)
	}
	// A configuration this malformed cannot happen through WithOIDC, but label is
	// display code and must not answer with an empty button.
	if got := (OIDCConfig{Issuer: "https://"}).label(); got != "single sign-on" {
		t.Errorf("label of an issuer with no host = %q", got)
	}
}

// discoveryServer answers a discovery document, counting how often it is asked.
func discoveryServer(t *testing.T, doc func(base string) string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != oidcDiscoveryPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(doc(ts.URL)))
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

// TestDiscoveryIsRefusedWhenItIsNotTheProviderItClaimedToBe. Everything after the
// document comes out of it, so believing the wrong one is believing the wrong
// provider entirely — and the failure has to say which, because an operator
// reading it is looking at a URL they typed.
func TestDiscoveryIsRefusedWhenItIsNotTheProviderItClaimedToBe(t *testing.T) {
	cases := []struct {
		name string
		doc  func(base string) string
		says string
	}{
		{"another issuer", func(string) string {
			return `{"issuer":"https://somebody.else","authorization_endpoint":"a","token_endpoint":"t","jwks_uri":"j"}`
		}, "issuer"},
		{"no token endpoint", func(base string) string {
			return `{"issuer":"` + base + `","authorization_endpoint":"a","jwks_uri":"j"}`
		}, "missing an endpoint"},
		{"not JSON at all", func(string) string { return "<html>login page</html>" }, "not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, _ := discoveryServer(t, tc.doc)
			p := newOIDCProvider(OIDCConfig{Issuer: ts.URL, ClientID: "atlas"})
			_, err := p.endpoints(context.Background(), time.Now())
			if err == nil {
				t.Fatal("accepted a discovery document that must be refused")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to name %q", err, tc.says)
			}
		})
	}

	t.Run("a provider that is not there", func(t *testing.T) {
		p := newOIDCProvider(OIDCConfig{Issuer: "http://127.0.0.1:1", ClientID: "atlas"})
		if _, err := p.endpoints(context.Background(), time.Now()); err == nil {
			t.Error("an unreachable provider produced no error")
		}
	})

	t.Run("a provider that answers an error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer ts.Close()
		p := newOIDCProvider(OIDCConfig{Issuer: ts.URL, ClientID: "atlas"})
		_, err := p.endpoints(context.Background(), time.Now())
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("error = %v, want it to carry the status the provider gave", err)
		}
	})
}

// TestTheDiscoveryDocumentIsReused: a login costs one round trip to the provider,
// not three, and the document is the same until it is not.
func TestTheDiscoveryDocumentIsReused(t *testing.T) {
	ts, hits := discoveryServer(t, func(base string) string {
		return `{"issuer":"` + base + `","authorization_endpoint":"` + base +
			`/a","token_endpoint":"` + base + `/t","jwks_uri":"` + base + `/j"}`
	})
	p := newOIDCProvider(OIDCConfig{Issuer: ts.URL, ClientID: "atlas"})
	now := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := p.endpoints(context.Background(), now); err != nil {
			t.Fatalf("endpoints: %v", err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("fetched the document %d times, want 1", got)
	}
	// And it is refetched once it is old enough to be worth doubting.
	if _, err := p.endpoints(context.Background(), now.Add(2*oidcDocumentTTL)); err != nil {
		t.Fatalf("endpoints after the TTL: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("fetched the document %d times, want it refetched after the TTL", got)
	}
}

// TestAFailedStartSendsThePersonBackToTheLoginScreen. A provider that is
// unreachable is the ordinary bad day, and what a person must not meet is a stack
// trace or a blank page.
func TestAFailedStartSendsThePersonBackToTheLoginScreen(t *testing.T) {
	s := &Server{oidcStates: newOIDCStateStore()}
	WithOIDC(OIDCConfig{Issuer: "http://127.0.0.1:1", ClientID: "atlas"})(s)

	rec := httptest.NewRecorder()
	s.handleOIDCStart(rec, httptest.NewRequest(http.MethodGet, "http://atlas.example"+oidcStartPath, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/"+oidcFailedQuery {
		t.Errorf("Location = %q, want the login screen with a failure flag", got)
	}
}

// TestTokenErrorNamesWhatTheProviderSaid: "invalid_grant" and "invalid_client"
// send an operator to entirely different places, so the audit line carries the
// provider's own words.
func TestTokenErrorNamesWhatTheProviderSaid(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a code and a description", `{"error":"invalid_grant","error_description":"expired"}`, "invalid_grant (expired)"},
		{"a bare code", `{"error":"invalid_client"}`, "invalid_client"},
		{"an empty body", ``, "no error code"},
		{"an HTML error page", `<html>502</html>`, "no error code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenError([]byte(tc.body)); got != tc.want {
				t.Errorf("tokenError = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestALoginInFlightExpires. The window is generous enough for a second factor and
// finite, and a state that has run out is not a state.
func TestALoginInFlightExpires(t *testing.T) {
	now := time.Now()
	store := newOIDCStateStore()
	store.begin("s1", oidcPending{nonce: "n", expires: now.Add(oidcLoginWindow)}, now)

	if _, ok := store.spend("s1", now.Add(oidcLoginWindow+time.Second)); ok {
		t.Error("spent a state after its window closed")
	}
	if _, ok := store.spend("s1", now); ok {
		t.Error("a state survived being spent")
	}
	if _, ok := store.spend("", now); ok {
		t.Error("an empty state resolved to something")
	}

	// A new login sweeps what nobody came back for, so an abandoned flow is not a
	// leak on a server people keep starting logins against.
	store.begin("old", oidcPending{expires: now.Add(time.Second)}, now)
	store.begin("new", oidcPending{expires: now.Add(oidcLoginWindow)}, now.Add(time.Minute))
	store.mu.Lock()
	held := len(store.pending)
	store.mu.Unlock()
	if held != 1 {
		t.Errorf("the store holds %d logins, want the abandoned one swept", held)
	}
}

// TestAUsernameIsProposedFromTheClaims. The username is display and identity, not
// the link — but it is what a person sees in a task list, so it comes from what
// the provider calls them rather than from an opaque subject.
func TestAUsernameIsProposedFromTheClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims oidcClaims
		want   string
	}{
		{"the preferred username", oidcClaims{PreferredUsername: "Ada.Lovelace", Email: "a@x.org", Subject: "s"}, "ada.lovelace"},
		{"the address when there is no username", oidcClaims{Email: "Ada.Lovelace@example.org", Subject: "s"}, "ada.lovelace"},
		{"the subject when there is neither", oidcClaims{Subject: "8f2b-11"}, "8f2b-11"},
		{"anything at all when the claims are unusable", oidcClaims{Subject: "!!!"}, "sso-user"},
		{"a username that is an address", oidcClaims{PreferredUsername: "ada@example.org"}, "ada"},
		{"punctuation nobody wants in a username", oidcClaims{PreferredUsername: "Ada L(1)"}, "adal1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := oidcUsername(tc.claims); got != tc.want {
				t.Errorf("oidcUsername = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnExternalIdentityLookupTakesBothHalves. A subject means nothing without the
// provider that issued it, so a lookup missing either half resolves to nothing
// rather than to the first account that happens to match.
func TestAnExternalIdentityLookupTakesBothHalves(t *testing.T) {
	dir := t.TempDir()
	users, err := newUserStore(dir)
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	if err := users.Save(User{ID: "u1", Username: "ada", Source: SourceOIDC, ExternalID: "sub-1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, tc := range []struct {
		name, source, id string
		want             bool
	}{
		{"both", SourceOIDC, "sub-1", true},
		{"another provider's subject", "saml", "sub-1", false},
		{"no source", "", "sub-1", false},
		{"no subject", SourceOIDC, "", false},
		{"a subject nobody has", SourceOIDC, "sub-2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, err := users.byExternalID(tc.source, tc.id)
			if err != nil {
				t.Fatalf("byExternalID: %v", err)
			}
			if ok != tc.want {
				t.Errorf("found = %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestAKeySetIsOnlyAsGoodAsTheDocumentThatNamesIt: the key set is found through
// discovery, so a provider whose document cannot be read has no keys either — and
// that has to be an error rather than an empty set nothing verifies against.
func TestAKeySetIsOnlyAsGoodAsTheDocumentThatNamesIt(t *testing.T) {
	p := newOIDCProvider(OIDCConfig{Issuer: "http://127.0.0.1:1", ClientID: "atlas"})
	if _, err := p.keySet(context.Background(), time.Now(), false); err == nil {
		t.Error("an unreachable provider produced a key set")
	}
	// And the same when a refresh is forced, which is the path a rotation takes.
	if _, err := p.keySet(context.Background(), time.Now(), true); err == nil {
		t.Error("a forced refresh against an unreachable provider produced a key set")
	}

	// A document that resolves but publishes a key set that is not one.
	ts, _ := discoveryServer(t, func(base string) string {
		return `{"issuer":"` + base + `","authorization_endpoint":"` + base +
			`/a","token_endpoint":"` + base + `/t","jwks_uri":"` + base + `/nothing"}`
	})
	p = newOIDCProvider(OIDCConfig{Issuer: ts.URL, ClientID: "atlas"})
	if _, err := p.keySet(context.Background(), time.Now(), false); err == nil {
		t.Error("a jwks_uri that answers 404 produced a key set")
	}
}

// TestAnExchangeAgainstAProviderThatIsNotThere. The token endpoint is the one call
// that happens while somebody is waiting on a redirect, so its failure has to be
// an error the caller can turn into a refusal.
func TestAnExchangeAgainstAProviderThatIsNotThere(t *testing.T) {
	ts, _ := discoveryServer(t, func(base string) string {
		return `{"issuer":"` + base + `","authorization_endpoint":"` + base +
			`/a","token_endpoint":"http://127.0.0.1:1/t","jwks_uri":"` + base + `/j"}`
	})
	p := newOIDCProvider(OIDCConfig{Issuer: ts.URL, ClientID: "atlas"})
	_, err := p.exchange(context.Background(), "code", "verifier", "https://atlas.example/cb", time.Now())
	if err == nil || !strings.Contains(err.Error(), "token exchange") {
		t.Errorf("error = %v, want the exchange to name itself", err)
	}

	// And when discovery itself is what fails, the exchange never happens.
	broken := newOIDCProvider(OIDCConfig{Issuer: "http://127.0.0.1:1", ClientID: "atlas"})
	if _, err := broken.exchange(context.Background(), "c", "v", "r", time.Now()); err == nil {
		t.Error("an exchange proceeded without a token endpoint to send it to")
	}
}

// TestLoginsInFlightAreBounded. The endpoint that starts one is public by
// necessity, so the store behind it must not grow with whatever anybody sends —
// and a person starting a login now must still get a slot.
func TestLoginsInFlightAreBounded(t *testing.T) {
	now := time.Now()
	store := newOIDCStateStore()
	for i := 0; i < oidcMaxPending+50; i++ {
		store.begin("state-"+strconv.Itoa(i), oidcPending{
			nonce: "n", expires: now.Add(oidcLoginWindow + time.Duration(i)*time.Second),
		}, now)
	}
	store.mu.Lock()
	held := len(store.pending)
	store.mu.Unlock()
	if held > oidcMaxPending {
		t.Errorf("the store holds %d logins, want at most %d", held, oidcMaxPending)
	}
	// The most recent login is the one that must still work.
	if _, ok := store.spend("state-"+strconv.Itoa(oidcMaxPending+49), now); !ok {
		t.Error("the login started last was evicted")
	}
}
