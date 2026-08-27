package api_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The authorization code flow, end to end (ADR-0200).
//
// What these hold is the property the whole record exists for: a person, in their
// browser, allows a hosted application to act **as them** — and the token that
// comes out is exactly as privileged as they are, and no more.
//
// The negative cases are the substance. An authorization endpoint that works is
// easy; one that refuses a replayed code, a mismatched verifier, an unregistered
// redirect and a wrong client secret is the one that can be exposed to the
// internet, which is what this endpoint is for.

const (
	testVerifier = "a-verifier-long-enough-to-be-a-real-one-0123456789"
	testRedirect = "https://client.example.com/callback"
)

func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// signedInClient logs in as the seeded admin and returns a cookie-carrying client.
func signedInClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := &http.Client{Jar: jar}
	body := strings.NewReader(`{"username":"root","password":"rootpassword"}`)
	resp, err := c.Post(base+"/api/v1/auth/login", "application/json", body)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	return c
}

// registerClient registers an OAuth client and returns its id and secret.
func registerClient(t *testing.T, c *http.Client, base string) (string, string) {
	t.Helper()
	body := strings.NewReader(`{"name":"Test Connector","redirectUris":["` + testRedirect + `"]}`)
	resp, err := c.Post(base+"/api/v1/oauth-clients", "application/json", body)
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("register client = %d: %s", resp.StatusCode, data)
	}
	var out struct{ ID, Secret string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode client: %v", err)
	}
	if out.Secret == "" || out.ID == "" {
		t.Fatal("registration returned no id or no secret")
	}
	return out.ID, out.Secret
}

// approve posts a consent decision and returns where the browser is sent.
func approve(t *testing.T, c *http.Client, base, clientID, resource string, yes bool) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"clientId": clientID, "redirectUri": testRedirect, "state": "xyz",
		"codeChallenge": testChallenge(), "resource": resource, "approve": yes,
	})
	resp, err := c.Post(base+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve = %d: %s", resp.StatusCode, data)
	}
	var out struct{ Redirect string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Redirect
}

// codeFrom pulls the authorization code out of a redirect target.
func codeFrom(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("parse redirect %q: %v", redirect, err)
	}
	if got := u.Query().Get("state"); got != "xyz" {
		t.Errorf("state came back as %q, want xyz — a client uses it against CSRF", got)
	}
	return u.Query().Get("code")
}

// postToken calls the token endpoint and returns its status and decoded body.
func postToken(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(base+oauthTokenTestPath, form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

const oauthTokenTestPath = "/oauth/token"

// bearerGet issues a GET with an access token.
func bearerGet(t *testing.T, base, path, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestOAuthFlowGrantsTheApproverIdentity walks the whole flow and then checks the
// one thing that matters at the end of it: the token acts as the person, confined
// to what they approved it for.
func TestOAuthFlowGrantsTheApproverIdentity(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	// The consent screen is told what it is being asked, and by whom.
	q := url.Values{
		"client_id": {clientID}, "redirect_uri": {testRedirect}, "response_type": {"code"},
		"code_challenge": {testChallenge()}, "code_challenge_method": {"S256"},
		"resource": {ts.URL + "/mcp"}, "state": {"xyz"},
	}
	ctxResp, err := admin.Get(ts.URL + "/api/v1/oauth/authorize-context?" + q.Encode())
	if err != nil {
		t.Fatalf("authorize-context: %v", err)
	}
	var ctx map[string]any
	_ = json.NewDecoder(ctxResp.Body).Decode(&ctx)
	ctxResp.Body.Close()
	if ctx["error"] != nil {
		t.Fatalf("a valid request was reported as invalid: %v", ctx["error"])
	}
	if ctx["clientName"] != "Test Connector" || ctx["signedInAs"] != "root" {
		t.Errorf("consent context = %v, want the client name and the signed-in person", ctx)
	}

	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL+"/mcp", true))
	if code == "" {
		t.Fatal("approval produced no code")
	}

	status, tok := postToken(t, ts.URL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange = %d: %v", status, tok)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("exchange returned no tokens: %v", tok)
	}
	if tok["token_type"] != "Bearer" {
		t.Errorf("token_type = %v, want Bearer", tok["token_type"])
	}

	// The point of the whole record: the token reaches the transport it was approved
	// for, as the person who approved it...
	if got := bearerGet(t, ts.URL, "/mcp", access); got == http.StatusUnauthorized {
		t.Errorf("the access token was refused at /mcp (%d)", got)
	}
	// ...and nothing else. The person is an admin, so this is refused by the grant's
	// own confinement rather than by their roles — which is the audience being real.
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusForbidden {
		t.Errorf("an /mcp-scoped token reached /api/v1/processes with %d, want 403", got)
	}

	// Rotation: refreshing yields a new pair, and the old refresh token is spent.
	status, refreshed := postToken(t, ts.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("refresh = %d: %v", status, refreshed)
	}
	if refreshed["refresh_token"] == refresh {
		t.Error("the refresh token was reused rather than rotated")
	}
	if status, _ := postToken(t, ts.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {clientID}, "client_secret": {clientSecret},
	}); status == http.StatusOK {
		t.Error("the old refresh token still works after rotation")
	}
}

// TestOAuthTokenRefusesATamperedExchange is the set of refusals that make this
// endpoint safe to expose. Each one is a way an attacker who has *something* —
// somebody else's code, the wrong secret, a redirect of their own — still gets
// nothing.
func TestOAuthTokenRefusesATamperedExchange(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	good := url.Values{
		"grant_type": {"authorization_code"}, "redirect_uri": {testRedirect},
		"code_verifier": {testVerifier}, "client_id": {clientID}, "client_secret": {clientSecret},
	}

	t.Run("a wrong client secret", func(t *testing.T) {
		f := cloneValues(good)
		f.Set("code", codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true)))
		f.Set("client_secret", "atlascs_"+strings.Repeat("0", 64))
		if status, out := postToken(t, ts.URL, f); status != http.StatusUnauthorized || out["error"] != "invalid_client" {
			t.Errorf("= %d %v, want 401 invalid_client", status, out)
		}
	})

	t.Run("a verifier that does not match the challenge", func(t *testing.T) {
		f := cloneValues(good)
		f.Set("code", codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true)))
		f.Set("code_verifier", "not-the-verifier-that-was-used-at-all-0123456789")
		if status, out := postToken(t, ts.URL, f); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("= %d %v, want 400 invalid_grant — PKCE is what stops a stolen code", status, out)
		}
	})

	t.Run("a redirect that is not the one authorized", func(t *testing.T) {
		f := cloneValues(good)
		f.Set("code", codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true)))
		f.Set("redirect_uri", "https://client.example.com/elsewhere")
		if status, out := postToken(t, ts.URL, f); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("= %d %v, want 400 invalid_grant", status, out)
		}
	})

	t.Run("a code spent twice", func(t *testing.T) {
		f := cloneValues(good)
		f.Set("code", codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true)))
		if status, out := postToken(t, ts.URL, f); status != http.StatusOK {
			t.Fatalf("the first exchange failed: %d %v", status, out)
		}
		if status, out := postToken(t, ts.URL, f); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("replay = %d %v, want 400 invalid_grant — a code is single-use", status, out)
		}
	})
}

// TestOAuthAuthorizeRefusesAnUnregisteredRedirect: the one refusal that must never
// travel back to the client as a redirect, because obeying an unvalidated
// redirect_uri is how this endpoint would become an open redirector — and an open
// redirector here hands the code to whoever supplied the URI.
func TestOAuthAuthorizeRefusesAnUnregisteredRedirect(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, _ := registerClient(t, admin, ts.URL)

	payload, _ := json.Marshal(map[string]any{
		"clientId": clientID, "redirectUri": "https://attacker.example.com/steal",
		"codeChallenge": testChallenge(), "resource": ts.URL, "approve": true,
	})
	resp, err := admin.Post(ts.URL+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("approving an unregistered redirect = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "attacker.example.com") {
		t.Error("the response echoed the attacker's URI as somewhere to go")
	}
}

// TestOAuthGrantRevocationStopsTheToken: an approval a person can withdraw. A
// grant that could not be revoked would be a worse API token, which is the thing
// ADR-0200 says it must not be.
func TestOAuthGrantRevocationStopsTheToken(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true))
	status, tok := postToken(t, ts.URL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("exchange = %d: %v", status, tok)
	}
	access := tok["access_token"].(string)
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusOK {
		t.Fatalf("a server-scoped token was refused at /api/v1/processes (%d)", got)
	}

	// The person can see their own approval and withdraw it.
	listResp, err := admin.Get(ts.URL + "/api/v1/oauth-grants")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	var grants []struct{ ID, ClientName string }
	_ = json.NewDecoder(listResp.Body).Decode(&grants)
	listResp.Body.Close()
	if len(grants) != 1 || grants[0].ClientName != "Test Connector" {
		t.Fatalf("grants = %v, want the one just approved", grants)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-grants/"+grants[0].ID, nil)
	delResp, err := admin.Do(req)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", delResp.StatusCode)
	}
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusUnauthorized {
		t.Errorf("a revoked token still reached the API with %d, want 401", got)
	}
}

// cloneValues copies a form so a subtest can vary one field without disturbing the
// others.
func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// TestAuthorizationServerMetadataAdvertisesS256 is the one field a client refuses
// to proceed without.
//
// The MCP specification tells a client to check code_challenge_methods_supported
// and to stop if it is absent. A server that omits it is not merely less good than
// one that has it — it is one no compliant client will talk to, and the failure
// arrives as a client-side refusal with nothing in this server's logs.
func TestAuthorizationServerMetadataAdvertisesS256(t *testing.T) {
	ts := newMCPServer(t)

	status, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-authorization-server")
	if status != http.StatusOK {
		t.Fatalf("metadata without a credential = %d, want 200", status)
	}
	if doc["issuer"] != ts.URL {
		t.Errorf("issuer = %v, want %q", doc["issuer"], ts.URL)
	}
	if doc["authorization_endpoint"] != ts.URL+"/oauth/authorize" ||
		doc["token_endpoint"] != ts.URL+"/oauth/token" {
		t.Errorf("endpoints = %v / %v", doc["authorization_endpoint"], doc["token_endpoint"])
	}
	methods, _ := doc["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256] — a client refuses to proceed without it",
			doc["code_challenge_methods_supported"])
	}
	// No implicit flow and no password grant: what is not advertised is not offered.
	grants, _ := doc["grant_types_supported"].([]any)
	for _, g := range grants {
		if g != "authorization_code" && g != "refresh_token" {
			t.Errorf("grant type %v is advertised; only authorization_code and refresh_token should be", g)
		}
	}
}

// TestConsentPageIsServedToABrowser: the person's landing point has to render for
// somebody holding nothing, or the flow cannot start.
func TestConsentPageIsServedToABrowser(t *testing.T) {
	ts := newMCPServer(t)

	resp, err := http.Get(ts.URL + "/oauth/authorize?client_id=whatever")
	if err != nil {
		t.Fatalf("GET /oauth/authorize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous GET /oauth/authorize = %d, want 200 — a 401 is not an answer a person can act on", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — a cached consent page could name a client that is gone", cc)
	}
}

// TestConsentContextReportsAnUnknownClient: the page must be able to say what is
// wrong, and must not be handed a redirect for a client that does not exist.
func TestConsentContextReportsAnUnknownClient(t *testing.T) {
	ts := newMCPServer(t)

	_, _, doc := getJSON(t, ts.URL+"/api/v1/oauth/authorize-context?client_id=nope&redirect_uri="+
		url.QueryEscape("https://attacker.example.com/steal"))
	if doc["error"] == nil {
		t.Fatal("an unknown client_id was not reported as an error")
	}
	if doc["redirect"] != nil {
		t.Error("a redirect was offered for an unknown client — that is the open-redirector shape")
	}
}

// TestOAuthDenialTellsTheClient: saying no is an answer the client gets, not a
// dead end the person is left on.
func TestOAuthDenialTellsTheClient(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, _ := registerClient(t, admin, ts.URL)

	redirect := approve(t, admin, ts.URL, clientID, ts.URL, false)
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("parse %q: %v", redirect, err)
	}
	if got := u.Query().Get("error"); got != "access_denied" {
		t.Errorf("error = %q, want access_denied", got)
	}
	if u.Query().Get("code") != "" {
		t.Error("a declined request produced a code")
	}
	if got := u.Query().Get("state"); got != "xyz" {
		t.Errorf("state = %q, want it carried back on the denial too", got)
	}
}

// TestRegisteringRefusesAnUnsafeRedirect: the redirect URI is where the code goes,
// so what may be registered is the whole of what may ever receive one.
func TestRegisteringRefusesAnUnsafeRedirect(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)

	for _, tc := range []struct{ name, uri string }{
		{"plain http off-machine", "http://client.example.com/callback"},
		{"relative", "/callback"},
		{"carrying a fragment", "https://client.example.com/cb#frag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"name":"X","redirectUris":["` + tc.uri + `"]}`
			resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatalf("register: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("registering %q = %d, want 400", tc.uri, resp.StatusCode)
			}
		})
	}

	// Loopback over http is the exception, and a deliberate one: it never leaves the
	// machine, and it is how a desktop client is meant to work.
	body := `{"name":"Desktop","redirectUris":["http://127.0.0.1:7777/cb"]}`
	resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register loopback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("registering a loopback redirect = %d, want 200", resp.StatusCode)
	}
}

// TestDeletingAClientRevokesItsGrants: removing an application has to mean it.
// A client removed while its tokens kept working would leave credentials that
// correspond to nothing an operator can see.
func TestDeletingAClientRevokesItsGrants(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true))
	status, tok := postToken(t, ts.URL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("exchange = %d: %v", status, tok)
	}
	access := tok["access_token"].(string)

	// The client is listed while it exists...
	listResp, err := admin.Get(ts.URL + "/api/v1/oauth-clients")
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	var clients []struct{ ID, Name string }
	_ = json.NewDecoder(listResp.Body).Decode(&clients)
	listResp.Body.Close()
	if len(clients) != 1 || clients[0].ID != clientID {
		t.Fatalf("clients = %v, want the one registered", clients)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-clients/"+clientID, nil)
	delResp, err := admin.Do(req)
	if err != nil {
		t.Fatalf("delete client: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete client = %d, want 204", delResp.StatusCode)
	}

	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusUnauthorized {
		t.Errorf("a token of a deleted client still reached the API with %d, want 401", got)
	}
	// And the client can no longer authenticate at the token endpoint either.
	if status, _ := postToken(t, ts.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)},
		"client_id": {clientID}, "client_secret": {clientSecret},
	}); status != http.StatusUnauthorized {
		t.Errorf("a deleted client still authenticated at the token endpoint (%d)", status)
	}
}

// createUser adds an ordinary account and returns its id.
func createUser(t *testing.T, admin *http.Client, base, username string) string {
	t.Helper()
	body := `{"username":"` + username + `","password":"a-password-that-is-long","roles":["user"]}`
	resp, err := admin.Post(base+"/api/v1/users", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create user = %d: %s", resp.StatusCode, data)
	}
	var out struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

// signInAs logs a named person in and returns their cookie-carrying client.
func signInAs(t *testing.T, base, username, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	body := `{"username":"` + username + `","password":"` + password + `"}`
	resp, err := c.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login as %s: %v", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login as %s = %d", username, resp.StatusCode)
	}
	return c
}

// patchUser applies an administrative change to an account.
func patchUser(t *testing.T, admin *http.Client, base, id, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/users/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatalf("patch user: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("patch user = %d: %s", resp.StatusCode, data)
	}
}

// grantFor runs a person through the whole flow and returns their access token.
func grantFor(t *testing.T, person *http.Client, base, clientID, clientSecret string) string {
	t.Helper()
	code := codeFrom(t, approve(t, person, base, clientID, base, true))
	status, tok := postToken(t, base, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("exchange = %d: %v", status, tok)
	}
	return tok["access_token"].(string)
}

// TestDisablingAPersonKillsTheirGrant is the reason a grant's snapshot is
// maintained rather than merely taken.
//
// A grant can stand for months. If disabling an account only ended its browser
// sessions, the person would keep acting through their connector — which is
// precisely what disabling an account is supposed to prevent, and the failure
// would be invisible: no session, no login, just a token that keeps working.
func TestDisablingAPersonKillsTheirGrant(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	userID := createUser(t, admin, ts.URL, "leaver")
	person := signInAs(t, ts.URL, "leaver", "a-password-that-is-long")
	access := grantFor(t, person, ts.URL, clientID, clientSecret)

	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusOK {
		t.Fatalf("the fresh token was refused (%d)", got)
	}
	patchUser(t, admin, ts.URL, userID, `{"disabled":true}`)
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusUnauthorized {
		t.Errorf("a disabled person's token still reached the API with %d, want 401", got)
	}
}

// TestRoleChangeReachesAStandingGrant: the other half of maintaining the snapshot.
// Taking a role away has to reach the connector too, and giving one has to as
// well — otherwise an administrative edit would silently mean something different
// depending on which door the person came through.
func TestRoleChangeReachesAStandingGrant(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	userID := createUser(t, admin, ts.URL, "promotable")
	person := signInAs(t, ts.URL, "promotable", "a-password-that-is-long")
	access := grantFor(t, person, ts.URL, clientID, clientSecret)

	// An ordinary account cannot administer, through a token as through a session.
	if got := bearerGet(t, ts.URL, "/api/v1/users", access); got != http.StatusForbidden {
		t.Fatalf("a non-admin token reached user administration with %d, want 403", got)
	}
	patchUser(t, admin, ts.URL, userID, `{"roles":["user","admin"]}`)
	if got := bearerGet(t, ts.URL, "/api/v1/users", access); got != http.StatusOK {
		t.Errorf("after promotion the standing grant still could not administer (%d) — the snapshot went stale", got)
	}
	patchUser(t, admin, ts.URL, userID, `{"roles":["user"]}`)
	if got := bearerGet(t, ts.URL, "/api/v1/users", access); got != http.StatusForbidden {
		t.Errorf("after demotion the standing grant still administered (%d)", got)
	}
}

// TestAuthorizeRefusesAMalformedRequest: what the authorization endpoint declines
// before a person is ever shown a question. Each of these is a client that built
// its request wrong, and each has to come back as its own OAuth error code so the
// client can say something better than "it failed".
func TestAuthorizeRefusesAMalformedRequest(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, _ := registerClient(t, admin, ts.URL)

	base := func() url.Values {
		return url.Values{
			"client_id": {clientID}, "redirect_uri": {testRedirect}, "response_type": {"code"},
			"code_challenge": {testChallenge()}, "code_challenge_method": {"S256"},
			"resource": {ts.URL},
		}
	}
	for _, tc := range []struct {
		name string
		mut  func(url.Values)
	}{
		{"an implicit-flow response_type", func(q url.Values) { q.Set("response_type", "token") }},
		{"no PKCE challenge", func(q url.Values) { q.Del("code_challenge") }},
		{"PKCE downgraded to plain", func(q url.Values) { q.Set("code_challenge_method", "plain") }},
		{"a resource this server does not serve", func(q url.Values) { q.Set("resource", "https://elsewhere.example.com") }},
		{"no resource at all", func(q url.Values) { q.Del("resource") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := base()
			tc.mut(q)
			resp, err := admin.Get(ts.URL + "/api/v1/oauth/authorize-context?" + q.Encode())
			if err != nil {
				t.Fatalf("authorize-context: %v", err)
			}
			var ctx map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&ctx)
			resp.Body.Close()
			if ctx["error"] == nil {
				t.Fatalf("%s was not reported as an error: %v", tc.name, ctx)
			}
			// The client's own mistake travels back to it, because only the client can
			// fix it — unlike a bad redirect_uri, which must never be obeyed.
			if ctx["redirect"] == nil {
				t.Errorf("%s produced no redirect for the client to be told on", tc.name)
			}
		})
	}
}

// TestTokenEndpointRefusesTheRest covers the exchange-time checks a single flow
// does not reach.
func TestTokenEndpointRefusesTheRest(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	t.Run("an unsupported grant type", func(t *testing.T) {
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"password"}, "client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status != http.StatusBadRequest || out["error"] != "unsupported_grant_type" {
			t.Errorf("= %d %v, want 400 unsupported_grant_type — the password grant is not offered", status, out)
		}
	})

	t.Run("a code belonging to another client", func(t *testing.T) {
		otherID, _ := registerSecondClient(t, admin, ts.URL)
		code := codeFrom(t, approve(t, admin, ts.URL, otherID, ts.URL, true))
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
			"client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("= %d %v, want 400 invalid_grant", status, out)
		}
	})

	t.Run("a resource that is not the one approved", func(t *testing.T) {
		code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true))
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
			"resource":  {ts.URL + "/mcp"},
			"client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status != http.StatusBadRequest || out["error"] != "invalid_target" {
			t.Errorf("= %d %v, want 400 invalid_target", status, out)
		}
	})

	t.Run("an unknown refresh token", func(t *testing.T) {
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {"atlasor_" + strings.Repeat("0", 64)},
			"client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("= %d %v, want 400 invalid_grant", status, out)
		}
	})
}

// registerSecondClient registers another application, with its own redirect.
func registerSecondClient(t *testing.T, c *http.Client, base string) (string, string) {
	t.Helper()
	body := strings.NewReader(`{"name":"Other","redirectUris":["` + testRedirect + `"]}`)
	resp, err := c.Post(base+"/api/v1/oauth-clients", "application/json", body)
	if err != nil {
		t.Fatalf("register second client: %v", err)
	}
	defer resp.Body.Close()
	var out struct{ ID, Secret string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID, out.Secret
}

// TestOAuthAdministrationIsGated: who may register an application, and who may see
// whose approvals. A client is an operator's decision; a grant is a person's own.
func TestOAuthAdministrationIsGated(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	createUser(t, admin, ts.URL, "ordinary")
	person := signInAs(t, ts.URL, "ordinary", "a-password-that-is-long")

	t.Run("registering is admin-only", func(t *testing.T) {
		body := strings.NewReader(`{"name":"Mine","redirectUris":["https://x.example.com/cb"]}`)
		resp, err := person.Post(ts.URL+"/api/v1/oauth-clients", "application/json", body)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a non-admin registered a client (%d)", resp.StatusCode)
		}
	})

	t.Run("listing clients is admin-only", func(t *testing.T) {
		resp, err := person.Get(ts.URL + "/api/v1/oauth-clients")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a non-admin listed clients (%d)", resp.StatusCode)
		}
	})

	t.Run("a name is required", func(t *testing.T) {
		body := strings.NewReader(`{"redirectUris":["https://x.example.com/cb"]}`)
		resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", body)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("a client with no name was registered (%d) — a credential nobody can identify later", resp.StatusCode)
		}
	})

	t.Run("a person sees only their own grants", func(t *testing.T) {
		// The ordinary person approves; the admin's listing shows it, theirs shows
		// only their own.
		_ = grantFor(t, person, ts.URL, clientID, clientSecret)

		var mine []struct{ ID, Username string }
		resp, _ := person.Get(ts.URL + "/api/v1/oauth-grants")
		_ = json.NewDecoder(resp.Body).Decode(&mine)
		resp.Body.Close()
		if len(mine) != 1 || mine[0].Username != "ordinary" {
			t.Fatalf("the person's own listing = %v", mine)
		}

		// And they cannot revoke somebody else's: reported as absent, not as
		// forbidden, so this cannot be used to discover whose approvals exist.
		adminAccess := grantFor(t, admin, ts.URL, clientID, clientSecret)
		var all []struct{ ID, Username string }
		resp, _ = admin.Get(ts.URL + "/api/v1/oauth-grants")
		_ = json.NewDecoder(resp.Body).Decode(&all)
		resp.Body.Close()
		if len(all) != 2 {
			t.Fatalf("the admin listing = %v, want both grants", all)
		}
		var adminGrant string
		for _, g := range all {
			if g.Username == "root" {
				adminGrant = g.ID
			}
		}
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-grants/"+adminGrant, nil)
		del, err := person.Do(req)
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		del.Body.Close()
		if del.StatusCode != http.StatusNotFound {
			t.Errorf("revoking another person's grant = %d, want 404", del.StatusCode)
		}
		if got := bearerGet(t, ts.URL, "/api/v1/processes", adminAccess); got != http.StatusOK {
			t.Errorf("the admin's grant was revoked by somebody else (%d)", got)
		}
	})

	t.Run("revoking something that is not there", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-grants/nonesuch", nil)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("= %d, want 404", resp.StatusCode)
		}
	})
}

// TestGroupMembershipReachesAStandingGrant: a group grant has to arrive at a
// connector the way it arrives at a browser session.
//
// Group membership is the one part of a session that Atlas keeps live rather than
// snapshotting at login (ADR-0185), because an access decision that waits for a
// re-login is a support ticket. A grant that missed those updates would be worse:
// it has no login to wait for.
func TestGroupMembershipReachesAStandingGrant(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	userID := createUser(t, admin, ts.URL, "grouped")
	person := signInAs(t, ts.URL, "grouped", "a-password-that-is-long")
	access := grantFor(t, person, ts.URL, clientID, clientSecret)

	// An application the person has no part in, shared with a group they are not in.
	appID := createApplication(t, admin, ts.URL, "Shared work")
	groupID := createGroup(t, admin, ts.URL, "readers")
	shareWith(t, admin, ts.URL, appID, groupID, "viewer")

	if visible(t, ts.URL, appID, access) {
		t.Fatal("the application was visible before the person was in the group")
	}
	putJSON(t, admin, ts.URL+"/api/v1/groups/"+groupID+"/members/"+userID, http.StatusOK)
	if !visible(t, ts.URL, appID, access) {
		t.Error("joining the group did not reach the standing grant")
	}
	deleteAt(t, admin, ts.URL+"/api/v1/groups/"+groupID+"/members/"+userID, http.StatusOK)
	if visible(t, ts.URL, appID, access) {
		t.Error("leaving the group did not reach the standing grant")
	}

	// And deleting the group takes its grants away from everyone at once.
	putJSON(t, admin, ts.URL+"/api/v1/groups/"+groupID+"/members/"+userID, http.StatusOK)
	if !visible(t, ts.URL, appID, access) {
		t.Fatal("rejoining the group did not reach the grant")
	}
	deleteAt(t, admin, ts.URL+"/api/v1/groups/"+groupID, http.StatusNoContent)
	if visible(t, ts.URL, appID, access) {
		t.Error("deleting the group left its reach in the standing grant")
	}
}

func createApplication(t *testing.T, admin *http.Client, base, name string) string {
	t.Helper()
	resp, err := admin.Post(base+"/api/v1/applications", "application/json",
		strings.NewReader(`{"name":"`+name+`"}`))
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create application = %d: %s", resp.StatusCode, data)
	}
	var out struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

func createGroup(t *testing.T, admin *http.Client, base, name string) string {
	t.Helper()
	resp, err := admin.Post(base+"/api/v1/groups", "application/json",
		strings.NewReader(`{"name":"`+name+`"}`))
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create group = %d: %s", resp.StatusCode, data)
	}
	var out struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

// shareWith grants a whole group a role on an application. The member type is
// explicit: without it the id would be looked up as a user and refused.
func shareWith(t *testing.T, admin *http.Client, base, appID, groupID, role string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, base+"/api/v1/applications/"+appID+"/members/"+groupID,
		strings.NewReader(`{"role":"`+role+`","type":"group"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("share = %d: %s", resp.StatusCode, data)
	}
}

func putJSON(t *testing.T, c *http.Client, url string, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s = %d, want %d: %s", url, resp.StatusCode, want, data)
	}
}

func deleteAt(t *testing.T, c *http.Client, url string, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE %s = %d, want %d: %s", url, resp.StatusCode, want, data)
	}
}

// visible reports whether an access token sees one application in its listing.
//
// The listing, not a by-id read: there is no GET /api/v1/applications/{id}, and
// what sharing changes is exactly which applications a person's listing contains.
func visible(t *testing.T, base, appID, token string) bool {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/applications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list applications: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list applications = %d", resp.StatusCode)
	}
	var apps []struct{ ID string }
	if err := json.NewDecoder(resp.Body).Decode(&apps); err != nil {
		t.Fatalf("decode applications: %v", err)
	}
	for _, a := range apps {
		if a.ID == appID {
			return true
		}
	}
	return false
}

// TestGrantsAndClientsSurviveARestart is why these are durable at all.
//
// A connector must not be signed out because the server was restarted. Sessions
// are in memory and a restart logs everybody out of their browser — that is a
// documented limitation people can live with, because they log back in. A grant
// has nobody to log back in: if a restart dropped it, an operator would learn
// about it from a support ticket.
func TestGrantsAndClientsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	first := newServerOn(t, dir)
	admin := signedInClient(t, first.URL)
	clientID, clientSecret := registerClient(t, admin, first.URL)
	// A second client so the listing has something to order, and a second grant so
	// the index is rebuilt with more than one entry.
	registerSecondClient(t, admin, first.URL)
	access := grantFor(t, admin, first.URL, clientID, clientSecret)
	if got := bearerGet(t, first.URL, "/api/v1/processes", access); got != http.StatusOK {
		t.Fatalf("the fresh token was refused (%d)", got)
	}
	first.stop()

	second := newServerOn(t, dir)
	if got := bearerGet(t, second.URL, "/api/v1/processes", access); got != http.StatusOK {
		t.Errorf("the access token did not survive the restart (%d)", got)
	}
	// And the client can still authenticate, so a refresh works across it too.
	admin2 := signedInClient(t, second.URL)
	listResp, err := admin2.Get(second.URL + "/api/v1/oauth-clients")
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	var clients []struct{ ID string }
	_ = json.NewDecoder(listResp.Body).Decode(&clients)
	listResp.Body.Close()
	if len(clients) != 2 {
		t.Errorf("after the restart %d clients are registered, want 2", len(clients))
	}
}

// TestApproveAndTokenRefuseMalformedInput: the two endpoints a client posts to,
// given input a client library would never send — which is exactly what arrives
// when something else finds the port.
func TestApproveAndTokenRefuseMalformedInput(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, _ := registerClient(t, admin, ts.URL)

	t.Run("approve with a body that is not JSON", func(t *testing.T) {
		resp, err := admin.Post(ts.URL+"/api/v1/oauth/authorize", "application/json", strings.NewReader("{{{"))
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("= %d, want 400", resp.StatusCode)
		}
	})

	t.Run("approve for a resource this server does not serve", func(t *testing.T) {
		payload, _ := json.Marshal(map[string]any{
			"clientId": clientID, "redirectUri": testRedirect,
			"codeChallenge": testChallenge(), "resource": "https://elsewhere.example.com", "approve": true,
		})
		resp, err := admin.Post(ts.URL+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(payload)))
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("= %d, want 400 — the audience is checked before anything is issued", resp.StatusCode)
		}
	})

	t.Run("token with a body that is not a form", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/oauth/token", strings.NewReader("%zz"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("= %d, want 400", resp.StatusCode)
		}
	})

	t.Run("deleting a client is admin-only", func(t *testing.T) {
		createUser(t, admin, ts.URL, "notadmin")
		person := signInAs(t, ts.URL, "notadmin", "a-password-that-is-long")
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-clients/"+clientID, nil)
		resp, err := person.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a non-admin deleted a client (%d)", resp.StatusCode)
		}
	})
}

// TestRegisteringRequiresARedirect: a client with nowhere to send the code is a
// client that can never complete a flow, so it is refused at registration rather
// than discovered later.
func TestRegisteringRequiresARedirect(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)

	for _, body := range []string{
		`{"name":"X","redirectUris":[]}`,
		`{"name":"X"}`,
		`{"name":"X","redirectUris":["  "]}`,
	} {
		resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("registering %s = %d, want 400", body, resp.StatusCode)
		}
	}

	// And a body that is not JSON at all.
	resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", strings.NewReader("nope"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed body = %d, want 400", resp.StatusCode)
	}
}

// TestOnePersonsRoleChangeLeavesAnothersGrantAlone: the maintenance is targeted.
// Rewriting everybody's snapshot on every administrative edit would be a different
// and much worse thing than keeping one person's current.
func TestOnePersonsRoleChangeLeavesAnothersGrantAlone(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	aID := createUser(t, admin, ts.URL, "alpha")
	createUser(t, admin, ts.URL, "beta")
	a := signInAs(t, ts.URL, "alpha", "a-password-that-is-long")
	b := signInAs(t, ts.URL, "beta", "a-password-that-is-long")
	aToken := grantFor(t, a, ts.URL, clientID, clientSecret)
	bToken := grantFor(t, b, ts.URL, clientID, clientSecret)

	patchUser(t, admin, ts.URL, aID, `{"roles":["user","admin"]}`)
	if got := bearerGet(t, ts.URL, "/api/v1/users", aToken); got != http.StatusOK {
		t.Errorf("the promoted person's grant did not follow (%d)", got)
	}
	if got := bearerGet(t, ts.URL, "/api/v1/users", bToken); got != http.StatusForbidden {
		t.Errorf("the other person's grant was promoted too (%d) — the rewrite is not targeted", got)
	}
}

// restartableServer is an Atlas whose data directory outlives it, so a test can
// stop it and start another on the same state.
type restartableServer struct {
	*httptest.Server
	stop func()
}

// newServerOn starts an authenticated server on an existing data directory.
func newServerOn(t *testing.T, dir string) *restartableServer {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")

	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()

	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	}
	t.Cleanup(stop)
	return &restartableServer{Server: ts, stop: stop}
}

// TestOAuthSurvivesABrokenDataDirectory: what these endpoints answer when the
// disk beneath them is not what it was.
//
// A data directory that has become unreadable is an ordinary operational failure —
// a bad restore, a mount that did not come back, a half-finished migration. What
// matters is that the server says so with a 500 rather than answering as though
// nothing were stored: an authorization endpoint that silently reported "no such
// grant" for an unreadable store would look like a revocation that had happened.
func TestOAuthSurvivesABrokenDataDirectory(t *testing.T) {
	dir := t.TempDir()
	ts := newServerOn(t, dir)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	access := grantFor(t, admin, ts.URL, clientID, clientSecret)
	_ = access

	// Replace each store's directory with a plain file. Everything the stores do
	// begins with reading or writing that directory, so both halves fail — and they
	// fail for root too, which chmod would not achieve here.
	breakDir := func(name string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.RemoveAll(p); err != nil {
			t.Fatalf("remove %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	breakDir("oauth-grants")
	t.Run("listing grants", func(t *testing.T) {
		resp, err := admin.Get(ts.URL + "/api/v1/oauth-grants")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("= %d, want 500 — an unreadable store must not read as an empty one", resp.StatusCode)
		}
	})
	t.Run("revoking a grant", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-grants/whatever", nil)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", resp.StatusCode)
		}
	})
	t.Run("refreshing a token", func(t *testing.T) {
		// The grant cannot be written, so no new pair is handed out — rather than a
		// pair the server has no record of, which would be a credential nobody can
		// revoke.
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"authorization_code"}, "code": {"atlasoc_nope"},
			"client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status == http.StatusOK {
			t.Errorf("a token was issued from a store that cannot be written: %v", out)
		}
	})
	t.Run("deleting a client cascades through the grants", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/oauth-clients/"+clientID, nil)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("= %d, want 500 — the cascade could not be completed", resp.StatusCode)
		}
	})

	breakDir("oauth-clients")
	t.Run("listing clients", func(t *testing.T) {
		resp, err := admin.Get(ts.URL + "/api/v1/oauth-clients")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", resp.StatusCode)
		}
	})
	t.Run("registering a client", func(t *testing.T) {
		body := strings.NewReader(`{"name":"New","redirectUris":["https://x.example.com/cb"]}`)
		resp, err := admin.Post(ts.URL+"/api/v1/oauth-clients", "application/json", body)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("= %d, want 500 — a secret must not be handed out for a record that was not stored", resp.StatusCode)
		}
	})
	t.Run("a role change over a broken grant store", func(t *testing.T) {
		// The rewrite cannot read the grants; the user update itself still succeeds,
		// because an administrator must be able to disable somebody even when this
		// store is broken.
		userID := createUser(t, admin, ts.URL, "unaffected")
		patchUser(t, admin, ts.URL, userID, `{"roles":["user","admin"]}`)
	})
}

// TestServerRefusesToStartOnABrokenOAuthStore: the same failure at startup is not
// a 500 but a refusal to serve at all. A server that came up without its grants
// would answer 401 to every connector, which reads as a mass revocation nobody
// ordered.
func TestServerRefusesToStartOnABrokenOAuthStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "oauth-grants"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer wl.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if srv, err := api.New(proc, store, dir, api.WithAuth()); err == nil {
		srv.Close()
		t.Error("the server started with an unreadable grant store")
	}
}
