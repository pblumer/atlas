package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/api/vault"
)

// Linking a Microsoft account (ADR-0475 §4) against a
// fake tenant that signs real tokens and checks PKCE, as Microsoft's does.

type fakeTenant struct {
	*httptest.Server
	key signingKey
	// tenant and client are the directory and application ids the tenant answers
	// for; newFakeTenant's are short words, newFakeTenantFor takes GUIDs as Entra
	// writes them.
	tenant, client string

	mu      sync.Mutex
	nonce   string
	issued  map[string]string
	mutate  func(claims map[string]any)
	noRT    bool
	refuse  bool
	refresh []url.Values
}

func newFakeTenant(t *testing.T) *fakeTenant { return newFakeTenantFor(t, "tid-1", "atlas-ms") }

func newFakeTenantFor(t *testing.T, tenant, client string) *fakeTenant {
	t.Helper()
	f := &fakeTenant{key: newSigningKey(t, "ms1"), issued: map[string]string{}, tenant: tenant, client: client}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+tenant+"/v2.0/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 f.URL + "/" + tenant + "/v2.0",
			"authorization_endpoint": f.URL + "/" + tenant + "/oauth2/v2.0/authorize",
			"token_endpoint":         f.URL + "/" + tenant + "/oauth2/v2.0/token",
			"jwks_uri":               f.URL + "/" + tenant + "/discovery/v2.0/keys",
		})
	})
	mux.HandleFunc("GET /"+tenant+"/discovery/v2.0/keys", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(f.key.jwks())
	})
	mux.HandleFunc("POST /"+tenant+"/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PostForm.Get("client_secret") != "shh" || r.PostForm.Get("client_id") != client {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if r.PostForm.Get("grant_type") == "refresh_token" {
			f.refresh = append(f.refresh, r.PostForm)
			if f.refuse {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS70008"}`))
				return
			}
			rt := r.PostForm.Get("refresh_token")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + rt, "refresh_token": rt + "'",
				"expires_in": 3600, "scope": "Sites.ReadWrite.All"})
			return
		}
		if !verifyPKCE(f.issued[r.PostForm.Get("code")], r.PostForm.Get("code_verifier")) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		want := oidcExpect{issuer: f.URL + "/" + tenant + "/v2.0", clientID: client, nonce: f.nonce, now: time.Now()}
		token := f.key.tokenFor(t, want, func(_, claims map[string]any) {
			claims["tid"], claims["oid"], claims["sub"] = tenant, "oid-ada", "pairwise-sub"
			if f.mutate != nil {
				f.mutate(claims)
			}
		})
		body := map[string]any{"id_token": token, "access_token": "at-0", "expires_in": 3600,
			"scope": "openid profile offline_access https://graph.microsoft.com/Sites.ReadWrite.All", "refresh_token": "rt-1"}
		if f.noRT {
			delete(body, "refresh_token")
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTenant) authorize(t *testing.T, location string) (string, string) {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("prompt") != "select_account" || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(q.Get("scope"), "offline_access") || !strings.Contains(q.Get("scope"), "Sites.ReadWrite.All") {
		t.Fatalf("authorization request: %s", location)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	code := "code-" + q.Get("state")[:8]
	f.issued[code] = q.Get("code_challenge")
	f.nonce = q.Get("nonce")
	return code, q.Get("state")
}

func linkServer(t *testing.T, f *fakeTenant) (*httptest.Server, *Server) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	srv := newServerWithOptions(t, WithAuth(), WithMicrosoftLinking(MicrosoftLinkConfig{
		TenantID: "tid-1", ClientID: "atlas-ms", ClientSecret: "shh",
		Scopes: "https://graph.microsoft.com/Sites.ReadWrite.All", Authority: f.URL,
	}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

func signInClient(t *testing.T, ts *httptest.Server, user, pass string) *http.Client {
	t.Helper()
	c := noRedirects(t)
	resp, err := c.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %v %v", user, err, resp)
	}
	resp.Body.Close()
	return c
}

// link runs the browser's part and returns where the callback sent it.
func link(t *testing.T, ts *httptest.Server, f *fakeTenant, starter, finisher *http.Client) string {
	t.Helper()
	start, err := starter.Get(ts.URL + linkMicrosoftStartPath)
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if start.StatusCode != http.StatusFound {
		t.Fatalf("start = %d", start.StatusCode)
	}
	code, state := f.authorize(t, start.Header.Get("Location"))
	// The state cookie belongs to the browser that started; a different finisher gets
	// it copied, which is the attack a bound session has to stop.
	u, _ := url.Parse(ts.URL + linkMicrosoftCallbackPath)
	if finisher != starter {
		// Only the state cookie: the finisher keeps its own session, which is the point.
		for _, c := range starter.Jar.Cookies(u) {
			if c.Name == linkStateCookie {
				finisher.Jar.SetCookies(u, []*http.Cookie{{Name: c.Name, Value: c.Value, Path: linkMicrosoftCallbackPath}})
			}
		}
	}
	resp, err := finisher.Get(ts.URL + linkMicrosoftCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

func TestLinkingAMicrosoftAccountAuthorizesADelegatedConnection(t *testing.T) {
	f := newFakeTenant(t)
	ts, srv := linkServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	where := link(t, ts, f, admin, admin)
	if !strings.HasPrefix(where, linkDonePage+"?linked=con_") {
		t.Fatalf("callback sent the browser to %q", where)
	}
	resp, err := admin.Get(ts.URL + "/api/v1/me/connections")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "rt-1") {
		t.Fatal("the refresh token reached a response")
	}
	var v struct {
		Identities  []identity.ExternalIdentity `json:"identities"`
		Connections []identity.Connection       `json:"connections"`
	}
	if err := json.Unmarshal(body, &v); err != nil || len(v.Identities) != 1 || len(v.Connections) != 1 {
		t.Fatalf("me/connections = %s", body)
	}
	// Bound by tenant and object id, not by the pairwise sub and not by an address.
	if v.Identities[0].Subject != "oid-ada" || v.Identities[0].TenantID != "tid-1" || v.Identities[0].Issuer != f.URL+"/tid-1/v2.0" {
		t.Fatalf("identity = %+v", v.Identities[0])
	}
	c := v.Connections[0]
	if c.Status != identity.StatusActive || len(c.AllowedWorkers) != 0 {
		t.Fatalf("connection = %+v", c)
	}
	srv.do(func() {
		raw, ok, _ := srv.vault.Get(vault.ConnectionCredentialName(c.ID))
		if !ok || !strings.Contains(raw, "rt-1") {
			t.Errorf("credential = %q", raw)
		}
	})

	// The broker refreshes through the tenant, persists the rotation, and a refusal
	// marks the connection for re-authentication.
	tok, _, err := srv.identity.AccessToken(context.Background(), c.ID)
	if err != nil || tok != "at-rt-1" {
		t.Fatalf("access token = %q %v", tok, err)
	}
	if got := f.refresh[0].Get("scope"); !strings.Contains(got, "Sites.ReadWrite.All") {
		t.Fatalf("refresh asked for scope %q", got)
	}
	f.mu.Lock()
	f.refuse = true
	f.mu.Unlock()
	srv.identity = identity.New(srv.runLoop, mustReopenIdentity(t, srv), srv.vault, srv.refreshDelegatedToken, srv.configuredWorkerExists, func() time.Time { return time.Now().Add(2 * time.Hour) })
	if _, _, err := srv.identity.AccessToken(context.Background(), c.ID); !errors.Is(err, identity.ErrInvalidGrant) {
		t.Fatalf("refused refresh: %v", err)
	}
	got, _, _ := srv.identity.Get(c.ID)
	if got.Status != identity.StatusReauthRequired {
		t.Fatalf("status after refusal = %s", got.Status)
	}
}

// mustReopenIdentity reopens the identity stores the server already uses, so a test
// can rebuild the service with another clock over the same records.
func mustReopenIdentity(t *testing.T, srv *Server) *identity.Stores {
	t.Helper()
	stores, err := identity.OpenStores(srv.dataDir + "/identity")
	if err != nil {
		t.Fatal(err)
	}
	return stores
}

func TestALinkIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(claims map[string]any)
		noRT   bool
	}{
		{"another tenant", func(c map[string]any) { c["tid"] = "tid-2" }, false},
		{"no object id", func(c map[string]any) { delete(c, "oid") }, false},
		{"another issuer", func(c map[string]any) { c["iss"] = "https://evil.example/tid-1/v2.0" }, false},
		{"another audience", func(c map[string]any) { c["aud"] = "someone-else" }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, false},
		{"no refresh token", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTenant(t)
			f.mutate, f.noRT = tc.mutate, tc.noRT
			ts, _ := linkServer(t, f)
			admin := signInClient(t, ts, "root", "rootpassword12")
			if where := link(t, ts, f, admin, admin); where != linkDonePage+"?link=failed" {
				t.Fatalf("callback sent the browser to %q", where)
			}
		})
	}
}

// The callback must reach the session that started the flow: otherwise a victim lured
// to an attacker's callback URL would get the attacker's account linked to them.
func TestALinkFinishedInAnotherSessionIsRefused(t *testing.T) {
	f := newFakeTenant(t)
	ts, _ := linkServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	resp, err := admin.Post(ts.URL+"/api/v1/users", "application/json",
		strings.NewReader(`{"username":"victim","password":"victimpassword1","roles":["user"]}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %v %v", err, resp)
	}
	resp.Body.Close()
	victim := signInClient(t, ts, "victim", "victimpassword1")
	if where := link(t, ts, f, admin, victim); where != linkDonePage+"?link=failed" {
		t.Fatalf("callback sent the victim to %q", where)
	}
}

func TestLinkingNeedsASignedInPerson(t *testing.T) {
	f := newFakeTenant(t)
	ts, _ := linkServer(t, f)
	resp, err := noRedirects(t).Get(ts.URL + linkMicrosoftStartPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous start = %d", resp.StatusCode)
	}
}

// Every way a callback can be wrong ends on the same failure page, and nothing is
// linked: the provider's own refusal, a state this browser did not start, a state
// nobody started, no code, and a code the provider will not exchange.
func TestEveryBrokenCallbackFailsTheSameWay(t *testing.T) {
	f := newFakeTenant(t)
	ts, srv := linkServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	start := func() string {
		t.Helper()
		resp, err := admin.Get(ts.URL + linkMicrosoftStartPath)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		_, state := f.authorize(t, resp.Header.Get("Location"))
		return state
	}
	callback := func(query string) string {
		t.Helper()
		resp, err := admin.Get(ts.URL + linkMicrosoftCallbackPath + "?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Location")
	}
	failed := linkDonePage + "?link=failed"
	if where := callback("error=access_denied"); where != failed {
		t.Errorf("the provider's refusal: %q", where)
	}
	start()
	if where := callback("code=x&state=not-this-browsers"); where != failed {
		t.Errorf("a state this browser did not start: %q", where)
	}
	// The cookie matches but the flow was already spent by the previous callback's
	// clearing, or never existed on the server.
	u, _ := url.Parse(ts.URL + linkMicrosoftCallbackPath)
	admin.Jar.SetCookies(u, []*http.Cookie{{Name: linkStateCookie, Value: "nobody-started-this", Path: linkMicrosoftCallbackPath}})
	if where := callback("code=x&state=nobody-started-this"); where != failed {
		t.Errorf("a state nobody started: %q", where)
	}
	state := start()
	if where := callback("state=" + state); where != failed {
		t.Errorf("no code: %q", where)
	}
	state = start()
	if where := callback("code=never-issued&state=" + state); where != failed {
		t.Errorf("a code the provider will not exchange: %q", where)
	}
	if ids, conns := srv.identity.ForOwner("usr_admin"); len(ids) != 0 || len(conns) != 0 {
		t.Fatalf("a broken callback linked something: %v %v", ids, conns)
	}
}

func TestLinkingIsOffUntilItIsConfigured(t *testing.T) {
	srv := newServerWithOptions(t, WithMicrosoftLinking(MicrosoftLinkConfig{TenantID: "tid-1"}))
	if srv.msLink.Load() != nil {
		t.Fatal("a configuration without a client id and scopes turned linking on")
	}
	for _, h := range []http.HandlerFunc{srv.handleLinkMicrosoftStart, srv.handleLinkMicrosoftCallback} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("unconfigured: %d", rec.Code)
		}
	}
	if _, err := srv.refreshDelegatedToken(context.Background(), identity.Connection{ID: "con_1", Provider: identity.ProviderMicrosoft}, "rt"); err == nil {
		t.Error("refreshed without a configuration")
	}
	if got := (MicrosoftLinkConfig{TenantID: "tid-1"}).issuer(); got != "https://login.microsoftonline.com/tid-1/v2.0" {
		t.Errorf("default issuer = %q", got)
	}
}

// A connection from another tenant is never refreshed against this one, and a
// Worker name the store does not hold does not exist.
func TestTheRefresherStaysInItsTenant(t *testing.T) {
	f := newFakeTenant(t)
	_, srv := linkServer(t, f)
	if _, err := srv.refreshDelegatedToken(context.Background(),
		identity.Connection{ID: "con_1", Provider: identity.ProviderMicrosoft, TenantID: "tid-2"}, "rt"); err == nil ||
		!strings.Contains(err.Error(), "tenant") {
		t.Fatalf("refreshed in another tenant: %v", err)
	}
	var exists bool
	srv.do(func() { exists = srv.configuredWorkerExists("no-such-worker") })
	if exists {
		t.Fatal("a Worker that is not configured exists")
	}
	srv.connectors = nil
	if srv.configuredWorkerExists("anything") {
		t.Fatal("a server without a Worker store has Workers")
	}
}

// Links in flight are bounded: an expired one is dropped and cannot be finished, and
// when the table is full the oldest gives way.
func TestLinksInFlightExpireAndAreBounded(t *testing.T) {
	l := &microsoftLink{pending: map[string]linkPending{}}
	now := time.Now()
	l.begin("old", linkPending{userID: "usr_a", expires: now.Add(-time.Second)}, now.Add(-2*time.Second))
	if _, ok := l.spend("old", now); ok {
		t.Fatal("an expired link was finished")
	}
	for i := 0; i < linkMaxPending+3; i++ {
		l.begin("s"+strconv.Itoa(i), linkPending{userID: "usr_a", expires: now.Add(time.Duration(i+1) * time.Minute)}, now)
	}
	if len(l.pending) != linkMaxPending {
		t.Fatalf("%d links in flight, bounded at %d", len(l.pending), linkMaxPending)
	}
	if _, ok := l.spend("s0", now); ok {
		t.Fatal("the oldest link was kept when the table was full")
	}
	if _, ok := l.spend("s"+strconv.Itoa(linkMaxPending+2), now); !ok {
		t.Fatal("the newest link was dropped")
	}
	if _, ok := l.spend("", now); ok {
		t.Fatal("an empty state was finished")
	}
}
