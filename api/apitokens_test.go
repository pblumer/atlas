package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// API tokens, over the real HTTP path.
//
// The gap they close: until now the only non-session credential a general caller
// could hold was the internal service token, which is minted at startup and served
// over no endpoint — obtainable only by the process that minted it. A worker on
// another host, a stdio MCP adapter against a remote server, a CI job: none of them
// had anything to present, and `--token` on either command had no value an operator
// could put in it. That was survivable while a login was opt-in and became the
// default path the moment it was not.

// apiTokenServer starts an --auth server and returns it with a signed-in admin.
func apiTokenServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login: got %d", code)
	}
	return ts, admin
}

// mint asks for a token and returns its one-time secret and id.
func mint(t *testing.T, admin *http.Client, ts *httptest.Server, body string) (secret, id string) {
	t.Helper()
	code, resp := cReq(t, admin, ts, "POST", "/api/v1/api-tokens", body)
	if code != http.StatusOK {
		t.Fatalf("mint %s: got %d (%s)", body, code, resp)
	}
	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(resp, &minted); err != nil {
		t.Fatalf("decode mint response %s: %v", resp, err)
	}
	if minted.Token == "" || minted.ID == "" {
		t.Fatalf("mint response missing token or id: %s", resp)
	}
	return minted.Token, minted.ID
}

// TestAPITokenAuthenticatesAMachine is the whole point: an administrator issues a
// credential, and a process that is not this server's child can use it.
func TestAPITokenAuthenticatesAMachine(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"ci","scope":"full","expiresInDays":30}`)

	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/processes", "", secret); code != http.StatusOK {
		t.Errorf("a full-scope token on the ordinary API = %d, want 200", code)
	}
	// Never an admin. A machine that administers accounts is not a case Atlas has,
	// and a leaked token that could would be a far worse leak.
	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/users", "", secret); code != http.StatusForbidden {
		t.Errorf("a token on an admin route = %d, want 403", code)
	}
	// A wrong secret of the right shape is refused like any other.
	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/processes", "", "atlasat_deadbeef"); code != http.StatusUnauthorized {
		t.Errorf("an unknown token = %d, want 401", code)
	}
}

// TestWorkerScopeReachesOnlyAWorkersOperations is the scope earning its place: a
// worker is a long-lived credential on another host, often in another network zone,
// and its whole job is four calls.
func TestWorkerScopeReachesOnlyAWorkersOperations(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"a worker","scope":"worker","expiresInDays":30}`)

	// Inside the scope: the call reaches its handler, so whatever it answers, it is
	// not the boundary's 401 or 403.
	code, body := bearerReq(t, ts, http.MethodPost, "/api/v1/jobs/activate",
		`{"jobType":"nothing","worker":"w1","maxJobs":1}`, secret)
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("a worker token leasing jobs = %d (%s), want the handler's own answer", code, body)
	}

	// Outside it: refused, and told why.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/processes", ""},
		{http.MethodPost, "/api/v1/deployments", "{}"},
		{http.MethodGet, "/api/v1/instances", ""},
		{http.MethodGet, "/api/v1/mail/outbox", ""}, // the same path, the wrong method
	} {
		code, body := bearerReq(t, ts, tc.method, tc.path, tc.body, secret)
		if code != http.StatusForbidden {
			t.Errorf("worker token on %s %s = %d, want 403", tc.method, tc.path, code)
			continue
		}
		if !strings.Contains(string(body), "worker") {
			t.Errorf("refusal for %s %s does not name the scope: %s", tc.method, tc.path, body)
		}
	}
}

// TestMetricsScopeReachesOnlyTheExposition: the Prometheus exposition was the last
// route whose protection depended on a proxy rule. A scraper is a machine, so it
// presents a credential — and the narrowest one there is, because a scraper needs
// exactly one GET forever.
func TestMetricsScopeReachesOnlyTheExposition(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"prometheus","scope":"metrics","expiresInDays":365}`)

	code, body := bearerReq(t, ts, http.MethodGet, "/metrics", "", secret)
	if code != http.StatusOK {
		t.Fatalf("a metrics token scraping = %d, want 200", code)
	}
	if !strings.Contains(string(body), "atlas_") {
		t.Errorf("the exposition does not look like one: %.120s", body)
	}
	for _, path := range []string{"/api/v1/processes", "/api/v1/instances", "/api/v1/jobs/activate"} {
		if code, _ := bearerReq(t, ts, http.MethodGet, path, "", secret); code != http.StatusForbidden {
			t.Errorf("metrics token on %s = %d, want 403", path, code)
		}
	}
}

// TestMetricsRequiresACredential: anonymous scraping is over, and a token minted
// for something else does not get in either.
func TestMetricsRequiresACredential(t *testing.T) {
	ts, admin := apiTokenServer(t)

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous scrape = %d, want 401", resp.StatusCode)
	}

	worker, _ := mint(t, admin, ts, `{"name":"a worker","scope":"worker"}`)
	if code, _ := bearerReq(t, ts, http.MethodGet, "/metrics", "", worker); code != http.StatusForbidden {
		t.Errorf("a worker token scraping = %d, want 403", code)
	}

	// A signed-in person still reaches it: they are authenticated, and the numbers
	// are operational data, not a secret kept from users.
	if code, _ := cReq(t, admin, ts, http.MethodGet, "/metrics", ""); code != http.StatusOK {
		t.Errorf("a signed-in admin scraping = %d, want 200", code)
	}

	// The probes stay open. A readiness probe that needs a credential is a probe
	// that does not work in the incident it exists for.
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("anonymous %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

// TestAPITokenWithoutALifetimeDoesNotExpire pins the contract of the field: an
// omitted or zero lifetime means the token keeps working. A worker that runs for a
// year is a real case; what must not happen is that omitting the field silently
// means something else. (Expiry itself is asserted where the clock can be driven —
// see TestAPITokenIndexRefusesAnExpiredToken.)
func TestAPITokenWithoutALifetimeDoesNotExpire(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"forever","scope":"full"}`)

	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/processes", "", secret); code != http.StatusOK {
		t.Errorf("a token minted without a lifetime = %d, want 200", code)
	}
	code, list := cReq(t, admin, ts, http.MethodGet, "/api/v1/api-tokens", "")
	if code != http.StatusOK {
		t.Fatalf("list: got %d", code)
	}
	if strings.Contains(string(list), `"expiresAt"`) {
		t.Errorf("a token that never expires should carry no expiry in the listing: %s", list)
	}
}

// TestAPITokenRevocationIsImmediate: revocation is deletion, and it takes effect on
// the next request rather than at some cache's convenience.
func TestAPITokenRevocationIsImmediate(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, id := mint(t, admin, ts, `{"name":"short-lived","scope":"full","expiresInDays":30}`)

	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/processes", "", secret); code != http.StatusOK {
		t.Fatalf("before revocation = %d, want 200", code)
	}
	if code, _ := cReq(t, admin, ts, http.MethodDelete, "/api/v1/api-tokens/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("revoke: got %d", code)
	}
	if code, _ := bearerReq(t, ts, http.MethodGet, "/api/v1/processes", "", secret); code != http.StatusUnauthorized {
		t.Errorf("after revocation = %d, want 401", code)
	}
}

// TestAPITokenSecretIsReturnedOnce: the server keeps only a hash, so a listing
// cannot leak the secret and neither can a stolen data directory.
func TestAPITokenSecretIsReturnedOnce(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"ci","scope":"full","expiresInDays":30}`)

	code, list := cReq(t, admin, ts, http.MethodGet, "/api/v1/api-tokens", "")
	if code != http.StatusOK {
		t.Fatalf("list: got %d", code)
	}
	if strings.Contains(string(list), secret) {
		t.Error("the listing contains the secret")
	}
	if !strings.Contains(string(list), `"ci"`) || !strings.Contains(string(list), `"full"`) {
		t.Errorf("the listing does not show the token's identity and scope: %s", list)
	}
}

// TestAPITokenMintingIsAdminOnly: issuing a credential is the same class of act as
// creating an account.
func TestAPITokenMintingIsAdminOnly(t *testing.T) {
	ts, admin := apiTokenServer(t)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	alice := newClient(t)
	if code := login(t, alice, ts, "alice", "password1"); code != http.StatusOK {
		t.Fatalf("alice login: got %d", code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/api-tokens", `{"name":"mine","scope":"full"}`},
		{"GET", "/api/v1/api-tokens", ""},
		{"DELETE", "/api/v1/api-tokens/whatever", ""},
	} {
		if code, _ := cReq(t, alice, ts, tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Errorf("%s %s as a non-admin = %d, want 403", tc.method, tc.path, code)
		}
	}
}

// TestAPITokenMintValidation: a credential nobody can identify, or whose reach
// nobody chose, is refused at the door.
func TestAPITokenMintValidation(t *testing.T) {
	ts, admin := apiTokenServer(t)
	for _, tc := range []struct{ name, body string }{
		{"no name", `{"scope":"full"}`},
		{"blank name", `{"name":"   ","scope":"full"}`},
		{"no scope", `{"name":"x"}`},
		{"unknown scope", `{"name":"x","scope":"root"}`},
		{"the deploy scope is not mintable here", `{"name":"x","scope":"deploy"}`},
		{"negative lifetime", `{"name":"x","scope":"full","expiresInDays":-1}`},
		{"absurd lifetime", `{"name":"x","scope":"full","expiresInDays":100000}`},
		{"not json", `nonsense`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cReq(t, admin, ts, "POST", "/api/v1/api-tokens", tc.body); code != http.StatusBadRequest {
				t.Errorf("mint %s = %d, want 400", tc.body, code)
			}
		})
	}
}

// TestStatusScopeReachesOnlyTheNodeDescriptor is the least-privilege half of
// ADR-0189 §6. Cross-server correlation needs one peer to ask another "who are
// you, and what can you be asked for" — and the credential handed over for that
// must not be a credential to deploy, to read instance data, or to name this node.
func TestStatusScopeReachesOnlyTheNodeDescriptor(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"peer","scope":"status","expiresInDays":365}`)

	code, body := bearerReq(t, ts, http.MethodGet, "/api/v1/node", "", secret)
	if code != http.StatusOK {
		t.Fatalf("a status token reading the descriptor = %d, want 200; body = %s", code, body)
	}
	if !strings.Contains(string(body), `"features"`) {
		t.Errorf("the descriptor does not look like one: %.160s", body)
	}

	for _, path := range []string{"/api/v1/processes", "/api/v1/instances", "/api/v1/panorama/mesh", "/api/v1/info"} {
		if code, _ := bearerReq(t, ts, http.MethodGet, path, "", secret); code != http.StatusForbidden {
			t.Errorf("status token on GET %s = %d, want 403", path, code)
		}
	}
	// Read-only by construction: a peer reads an identity, it never sets one, and
	// the write half of this very path stays out of the scope.
	if code, _ := bearerReq(t, ts, http.MethodPut, "/api/v1/node", `{"name":"hijacked"}`, secret); code != http.StatusForbidden {
		t.Errorf("status token naming the node = %d, want 403", code)
	}
}
