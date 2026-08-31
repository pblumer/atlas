package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A federated login, end to end against a provider that behaves like a real one
// (ADR-0210).
//
// The tests are internal because the provider has to sign tokens with the same
// helpers that test the validation itself, and because what they assert about is
// the account the flow produces — a record, not a response body.

// fakeIdP is an OpenID provider: discovery, a key set, and a token endpoint that
// checks what a relying party is required to send.
type fakeIdP struct {
	*httptest.Server
	key signingKey

	mu       sync.Mutex
	nonce    string            // what the next id token will carry
	issued   map[string]string // code → PKCE challenge it was issued against
	gotForm  url.Values        // the last token-endpoint request
	tokenErr int               // when non-zero, the token endpoint answers this status

	// mutate bends the next id token in exactly one way, for the tests that need a
	// provider to say something Atlas must refuse.
	mutate func(header, claims map[string]any)

	// tokenBody and jwksBody replace what those endpoints answer, for the tests
	// about a provider that is compliant enough to talk to and wrong in its content.
	tokenBody string
	jwksBody  string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	idp := &fakeIdP{key: newSigningKey(t, "k1"), issued: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 idp.URL,
			"authorization_endpoint": idp.URL + "/authorize",
			"token_endpoint":         idp.URL + "/token",
			"jwks_uri":               idp.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		body, override := idp.key.jwks(), idp.jwksBody
		idp.mu.Unlock()
		if override != "" {
			body = []byte(override)
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.mu.Lock()
		idp.gotForm = r.PostForm
		status, nonce := idp.tokenErr, idp.nonce
		challenge := idp.issued[r.PostForm.Get("code")]
		idp.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		// A provider that did not check PKCE would hide exactly the bug this flow
		// exists to prevent, so the fake one checks it.
		if !verifyPKCE(challenge, r.PostForm.Get("code_verifier")) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"pkce"}`))
			return
		}
		want := oidcExpect{issuer: idp.URL, clientID: "atlas-test", nonce: nonce, now: time.Now()}
		idp.mu.Lock()
		token, body := idp.key.tokenFor(t, want, idp.mutate), idp.tokenBody
		idp.mu.Unlock()
		if body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ignored", "token_type": "Bearer",
			"id_token": token,
		})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

// rotate replaces the provider's signing key, as a provider is entitled to do
// whenever it likes and without telling anybody.
func (f *fakeIdP) rotate(t *testing.T, kid string) {
	t.Helper()
	key := newSigningKey(t, kid)
	f.mu.Lock()
	f.key = key
	f.mu.Unlock()
}

// authorize plays the part the person's browser plays at the provider: it reads
// the authorization request, remembers the PKCE challenge and the nonce, and
// returns the code and state to hand back to Atlas.
func (f *fakeIdP) authorize(t *testing.T, location string) (code, state string) {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	q := u.Query()
	for _, k := range []string{"client_id", "state", "nonce", "code_challenge", "redirect_uri"} {
		if q.Get(k) == "" {
			t.Fatalf("authorization request carries no %s: %s", k, location)
		}
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q, want code", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	code = "code-" + q.Get("state")[:8]
	f.issued[code] = q.Get("code_challenge")
	f.nonce = q.Get("nonce")
	return code, q.Get("state")
}

// oidcServer is an auth-enforcing Atlas with this provider configured.
func oidcServer(t *testing.T, idp *fakeIdP) (*httptest.Server, *Server) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	srv := newServerWithOptions(t, WithAuth(), WithOIDC(OIDCConfig{
		Issuer: idp.URL, ClientID: "atlas-test", ClientSecret: "shh", Name: "Test IdP",
	}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// noRedirects is a client that stops at every redirect, because every redirect in
// this flow is a thing under test.
func noRedirects(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// federate runs the whole dance and returns the callback's response.
func federate(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response {
	t.Helper()
	start, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer start.Body.Close()
	if start.StatusCode != http.StatusFound {
		t.Fatalf("start = %d, want 302", start.StatusCode)
	}
	code, state := idp.authorize(t, start.Header.Get("Location"))
	resp, err := c.Get(ts.URL + oidcCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	return resp
}

// accountBySubject finds the account a federated login produced.
func accountBySubject(t *testing.T, srv *Server, sub string) (User, bool) {
	t.Helper()
	var (
		all []User
		err error
	)
	srv.do(func() { all, err = srv.users.LoadAll() })
	if err != nil {
		t.Fatalf("load users: %v", err)
	}
	for _, u := range all {
		if u.Source == SourceOIDC && u.ExternalID == sub {
			return u, true
		}
	}
	return User{}, false
}

// TestWithoutAProviderNothingChanges is the promise an operator is owed: an
// installation that configures no provider is the installation it was before, down
// to the routes it serves.
func TestWithoutAProviderNothingChanges(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	srv := newServerWithOptions(t, WithAuth())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := noRedirects(t)
	resp, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("start without a provider = %d, want 404 — the route must not exist", resp.StatusCode)
	}

	// And the login screen is told there is nothing to offer.
	list, err := c.Get(ts.URL + "/api/v1/auth/providers")
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	defer list.Body.Close()
	var providers []map[string]any
	if err := json.NewDecoder(list.Body).Decode(&providers); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("providers = %v, want none", providers)
	}
}

// TestAFederatedLoginCreatesAnAccountAndASession is the measure itself.
func TestAFederatedLoginCreatesAnAccountAndASession(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	c := noRedirects(t)

	resp := federate(t, ts, idp, c)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want 302 to the Console", resp.StatusCode)
	}
	var session string
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			session = ck.Value
		}
	}
	if session == "" {
		t.Fatal("callback set no session cookie")
	}

	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("no account was created for the provider's subject")
	}
	if u.Source != SourceOIDC || u.ExternalID != "subject-1" {
		t.Errorf("account = %+v, want it linked by source and external id", u)
	}
	if strings.Join(u.Roles, " ") != RoleUser {
		t.Errorf("roles = %v, want exactly [user] — a first login grants nothing else", u.Roles)
	}
	if u.PasswordHash != "" {
		t.Error("a federated account carries a password hash")
	}
	if u.Email != "ada@example.org" || u.DisplayName != "Ada Lovelace" {
		t.Errorf("profile = %q/%q, want it taken from the claims", u.Email, u.DisplayName)
	}

	// The token exchange sent what it had to: this client, the PKCE verifier, and
	// the secret it was configured with.
	idp.mu.Lock()
	form := idp.gotForm
	idp.mu.Unlock()
	if form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") == "" ||
		form.Get("client_id") != "atlas-test" || form.Get("client_secret") != "shh" {
		t.Errorf("token request = %v, want an authorization_code exchange with PKCE and client authentication", form)
	}
}

// TestASecondFederatedLoginReusesTheAccount. The subject is the identity, so a
// person whose name or address changed at the provider is still the same person
// here — and a provider that reissues a display name must not fork the account.
func TestASecondFederatedLoginReusesTheAccount(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	before, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("first login created no account")
	}

	idp.mutate = func(_, c map[string]any) {
		c["name"] = "Ada King"
		c["email"] = "ada.king@example.org"
	}
	second := federate(t, ts, idp, noRedirects(t))
	second.Body.Close()

	after, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("second login lost the account")
	}
	if after.ID != before.ID {
		t.Errorf("second login created a second account (%s then %s)", before.ID, after.ID)
	}
	if after.Username != before.Username {
		t.Errorf("username changed from %q to %q; it identifies sessions and assignments", before.Username, after.Username)
	}
	if after.DisplayName != "Ada King" || after.Email != "ada.king@example.org" {
		t.Errorf("profile = %q/%q, want the provider's current values", after.DisplayName, after.Email)
	}
}

// TestAFederatedLoginIsRefused walks the ways the callback must not produce a
// session. None of them may set the cookie, and none may create an account.
func TestAFederatedLoginIsRefused(t *testing.T) {
	cases := []struct {
		name string
		call func(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response
	}{
		{"no state at all", func(t *testing.T, ts *httptest.Server, _ *fakeIdP, c *http.Client) *http.Response {
			resp, err := c.Get(ts.URL + oidcCallbackPath + "?code=whatever")
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			return resp
		}},
		{"a state nobody started", func(t *testing.T, ts *httptest.Server, _ *fakeIdP, c *http.Client) *http.Response {
			resp, err := c.Get(ts.URL + oidcCallbackPath + "?code=whatever&state=invented")
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			return resp
		}},
		{"the provider reports an error", func(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response {
			start, err := c.Get(ts.URL + oidcStartPath)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			start.Body.Close()
			_, state := idp.authorize(t, start.Header.Get("Location"))
			resp, err := c.Get(ts.URL + oidcCallbackPath + "?error=access_denied&state=" + state)
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			return resp
		}},
		{"the token endpoint refuses", func(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response {
			idp.mu.Lock()
			idp.tokenErr = http.StatusBadRequest
			idp.mu.Unlock()
			return federate(t, ts, idp, c)
		}},
		{"the id token is for another audience", func(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response {
			idp.mutate = func(_, cl map[string]any) { cl["aud"] = "another-client" }
			return federate(t, ts, idp, c)
		}},
		{"the id token carries another login's nonce", func(t *testing.T, ts *httptest.Server, idp *fakeIdP, c *http.Client) *http.Response {
			idp.mutate = func(_, cl map[string]any) { cl["nonce"] = "somebody-elses" }
			return federate(t, ts, idp, c)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			ts, srv := oidcServer(t, idp)
			c := noRedirects(t)

			resp := tc.call(t, ts, idp, c)
			defer resp.Body.Close()
			for _, ck := range resp.Cookies() {
				if ck.Name == sessionCookie && ck.Value != "" {
					t.Error("a refused federated login set a session cookie")
				}
			}
			if _, ok := accountBySubject(t, srv, "subject-1"); ok {
				t.Error("a refused federated login created an account")
			}
		})
	}
}

// TestAStateIsSpentOnce: the callback is the one request that turns a code into a
// session, so replaying it must not produce a second one.
func TestAStateIsSpentOnce(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start.Body.Close()
	code, state := idp.authorize(t, start.Header.Get("Location"))

	first, err := c.Get(ts.URL + oidcCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusFound {
		t.Fatalf("first callback = %d, want 302", first.StatusCode)
	}

	second, err := c.Get(ts.URL + oidcCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	defer second.Body.Close()
	for _, ck := range second.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Error("replaying the callback produced a second session")
		}
	}
}

// TestADisabledAccountCannotFederate. Disabling is how an account is taken away
// before the provider knows about it, and a second door that ignored it would make
// the first one decoration.
func TestADisabledAccountCannotFederate(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("first login created no account")
	}
	u.Disabled = true
	var saveErr error
	srv.do(func() { saveErr = srv.users.Save(u) })
	if saveErr != nil {
		t.Fatalf("disable: %v", saveErr)
	}

	second := federate(t, ts, idp, noRedirects(t))
	defer second.Body.Close()
	for _, ck := range second.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Error("a disabled account got a session through the provider")
		}
	}
}

// TestAConfiguredProviderIsOfferedOnTheLoginScreen: the flow is unreachable if
// nothing tells the browser it exists, and the endpoint that says so is read
// before anybody is signed in.
func TestAConfiguredProviderIsOfferedOnTheLoginScreen(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)

	resp, err := http.Get(ts.URL + "/api/v1/auth/providers")
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("providers = %d, want 200 without a session", resp.StatusCode)
	}
	var providers []struct{ ID, Name, Start string }
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "Test IdP" || providers[0].Start != oidcStartPath {
		t.Errorf("providers = %+v, want the one configured provider with where to start", providers)
	}
}

// TestTheStartRequestCarriesAChallengeItKeeps pins PKCE at the one place it can be
// got wrong invisibly: the challenge in the authorization request must be the
// S256 hash of the verifier the token exchange later sends.
func TestTheStartRequestCarriesAChallengeItKeeps(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start.Body.Close()
	loc, _ := url.Parse(start.Header.Get("Location"))
	challenge := loc.Query().Get("code_challenge")

	code, state := idp.authorize(t, start.Header.Get("Location"))
	resp, err := c.Get(ts.URL + oidcCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()

	idp.mu.Lock()
	verifier := idp.gotForm.Get("code_verifier")
	idp.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
		t.Errorf("the verifier sent at the exchange does not hash to the challenge sent at the start")
	}
}

// TestAProviderThatRotatesItsKeyStillWorks. Key rotation is a thing providers do
// on their own schedule, and a relying party that cached a key set until it
// expired would answer every login with "signature invalid" in the meantime — an
// outage with no cause visible from either end.
func TestAProviderThatRotatesItsKeyStillWorks(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	if _, ok := accountBySubject(t, srv, "subject-1"); !ok {
		t.Fatal("the first login did not work at all")
	}

	// The provider replaces its key, under a key id this server has never seen.
	idp.rotate(t, "k2")

	second := federate(t, ts, idp, noRedirects(t))
	defer second.Body.Close()
	if second.StatusCode != http.StatusFound {
		t.Fatalf("callback after a rotation = %d, want 302", second.StatusCode)
	}
	signedIn := false
	for _, ck := range second.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			signedIn = true
		}
	}
	if !signedIn {
		t.Error("a login after the provider rotated its key produced no session")
	}
}

// TestAUsernameTakenLocallyDoesNotBlockAFederatedLogin. The provider's subject is
// the identity, so a name collision with a local account is an inconvenience and
// must not be a refusal — and it must not silently hand the federated person the
// local account either.
func TestAUsernameTakenLocallyDoesNotBlockAFederatedLogin(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	// A local account already called "ada", which is what the claims will propose.
	local := User{
		ID: "usr_local", Username: "ada", Source: SourceLocal,
		PasswordHash: "x", CreatedAt: 1, UpdatedAt: 1, Roles: []string{RoleUser},
	}
	var saveErr error
	srv.do(func() { saveErr = srv.users.Save(local) })
	if saveErr != nil {
		t.Fatalf("seed local account: %v", saveErr)
	}

	resp := federate(t, ts, idp, noRedirects(t))
	defer resp.Body.Close()

	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("a name collision refused the federated login")
	}
	if u.ID == local.ID {
		t.Fatal("the federated login took over the local account with the same name")
	}
	if u.Username == "ada" {
		t.Errorf("username = %q, want a free one derived from it", u.Username)
	}
}

// TestATokenResponseThatIsNotOne. A provider can answer 200 and still not have
// said anything usable — a proxy's HTML error page, a body with no id_token. Each
// is a failed login and none of them is a session.
func TestATokenResponseThatIsNotOne(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"an HTML page", "<html>maintenance</html>"},
		{"JSON with no id token", `{"access_token":"a","token_type":"Bearer"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			ts, srv := oidcServer(t, idp)
			idp.mu.Lock()
			idp.tokenBody = tc.body
			idp.mu.Unlock()

			resp := federate(t, ts, idp, noRedirects(t))
			defer resp.Body.Close()
			for _, ck := range resp.Cookies() {
				if ck.Name == sessionCookie && ck.Value != "" {
					t.Error("a token response that said nothing produced a session")
				}
			}
			if _, ok := accountBySubject(t, srv, "subject-1"); ok {
				t.Error("a token response that said nothing created an account")
			}
		})
	}
}

// TestAKeySetThatCannotBeUsedIsAFailedLogin, not a login verified against
// nothing. The provider is reachable and answers; what it publishes is unusable.
func TestAKeySetThatCannotBeUsedIsAFailedLogin(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	idp.mu.Lock()
	idp.jwksBody = `{"keys":[]}`
	idp.mu.Unlock()

	resp := federate(t, ts, idp, noRedirects(t))
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Error("an empty key set produced a session")
		}
	}
}

// TestACallbackWithoutACode is the shape a bookmarked or hand-edited callback URL
// takes: the state resolves, and there is nothing to exchange.
func TestACallbackWithoutACode(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start.Body.Close()
	_, state := idp.authorize(t, start.Header.Get("Location"))

	resp, err := c.Get(ts.URL + oidcCallbackPath + "?state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/"+oidcFailedQuery {
		t.Errorf("Location = %q, want the login screen", got)
	}
}

// TestAStartWithNoOriginToBuildARedirectFrom. The redirect URI is absolute, so a
// request that names no host leaves nothing to build one from — and a login that
// cannot say where to come back to must not start.
func TestAStartWithNoOriginToBuildARedirectFrom(t *testing.T) {
	idp := newFakeIdP(t)
	_, srv := oidcServer(t, idp)

	req := httptest.NewRequest(http.MethodGet, oidcStartPath, nil)
	req.Host = ""
	req.URL.Host = ""
	rec := httptest.NewRecorder()
	srv.handleOIDCStart(rec, req)
	if got := rec.Header().Get("Location"); got != "/"+oidcFailedQuery {
		t.Errorf("Location = %q, want the login screen rather than a redirect nobody can return from", got)
	}
}

// TestAFederatedLoginSurvivesABrokenDataDirectory. The account store is what a
// federated login writes to, and a disk that is not what it was must produce a
// failed login rather than a panic or a session for a record that was never saved.
func TestAFederatedLoginSurvivesABrokenDataDirectory(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	// Broken under a running server, because a server cannot start on one: New
	// creates the store's directory and fails if something else is in its place. A
	// plain file where the directory belongs fails every read and every write, and
	// fails for root too — which chmod would not achieve here.
	p := filepath.Join(srv.dataDir, "users")
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("remove %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}

	resp := federate(t, ts, idp, noRedirects(t))
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/"+oidcFailedQuery {
		t.Errorf("Location = %q, want the login screen", got)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Error("a login that could not write an account produced a session")
		}
	}
}

// TestAFederatedAccountHasNoPasswordToGuess. The account carries no hash, and the
// password form must refuse it rather than treat "no hash" as "any password" — the
// classic shape of that bug.
func TestAFederatedAccountHasNoPasswordToGuess(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("the federated login created no account")
	}

	for _, password := range []string{"", " ", "password", u.Username} {
		body := `{"username":"` + u.Username + `","password":"` + password + `"}`
		resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("a federated account signed in with password %q", password)
		}
	}
}
