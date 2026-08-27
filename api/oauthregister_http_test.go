package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Dynamic client registration, RFC 7591 (ADR-0200, step 2).
//
// The record calls this "an unauthenticated write endpoint to think hard about",
// and these tests are where that thinking is pinned. Two properties carry most of
// it: the endpoint does not exist unless an operator turned it on, and a client
// that registered itself is *marked* as such, so the consent screen can say so —
// without that mark, opening registration would silently degrade every consent
// decision a person makes, because they could no longer assume an operator had
// vetted the application in front of them.

// registerDynamic posts a registration request and returns status and body.
func registerDynamic(t *testing.T, base, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(base+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

// TestDynamicRegistrationIsOffByDefault: the endpoint is not merely closed, it is
// absent — and the metadata does not advertise it, so a client discovers the truth
// rather than being told to try and then refused.
func TestDynamicRegistrationIsOffByDefault(t *testing.T) {
	ts := newMCPServer(t)

	status, _ := registerDynamic(t, ts.URL, `{"redirect_uris":["https://c.example.com/cb"]}`)
	if status != http.StatusNotFound {
		t.Errorf("POST /oauth/register on a default server = %d, want 404", status)
	}

	_, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-authorization-server")
	if _, ok := doc["registration_endpoint"]; ok {
		t.Error("the metadata advertises a registration endpoint that is not served")
	}
}

// TestDynamicRegistrationIssuesAUsableClient: with it on, a client registers itself
// and can complete the whole flow — which is the point of the step.
func TestDynamicRegistrationIssuesAUsableClient(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	_, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-authorization-server")
	if got := doc["registration_endpoint"]; got != ts.URL+"/oauth/register" {
		t.Errorf("registration_endpoint = %v, want it advertised", got)
	}

	status, out := registerDynamic(t, ts.URL, `{
		"client_name": "Self-Registered Connector",
		"redirect_uris": ["`+testRedirect+`"]
	}`)
	if status != http.StatusCreated {
		t.Fatalf("registration = %d: %v — RFC 7591 says 201", status, out)
	}
	clientID, _ := out["client_id"].(string)
	clientSecret, _ := out["client_secret"].(string)
	if clientID == "" || clientSecret == "" {
		t.Fatalf("registration returned no usable credentials: %v", out)
	}
	// RFC 7591: client_secret_expires_at is required when a secret is issued, and 0
	// means it does not expire.
	if _, ok := out["client_secret_expires_at"]; !ok {
		t.Error("no client_secret_expires_at, which is required when a secret is issued")
	}
	// The submitted metadata comes back, so a client can confirm what was recorded.
	if out["client_name"] != "Self-Registered Connector" {
		t.Errorf("client_name = %v, want it echoed", out["client_name"])
	}

	// And it works: the whole flow, with a client nobody registered by hand.
	admin := signedInClient(t, ts.URL)
	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true))
	st, tok := postToken(t, ts.URL, map[string][]string{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if st != http.StatusOK {
		t.Fatalf("exchange with a self-registered client = %d: %v", st, tok)
	}
	if got := bearerGet(t, ts.URL, "/api/v1/processes", tok["access_token"].(string)); got != http.StatusOK {
		t.Errorf("the token from a self-registered client was refused (%d)", got)
	}
}

// TestConsentScreenSeesWhoVouchedForTheClient is the mitigation that makes opening
// registration defensible at all.
//
// With registration open, "an application is asking for access" no longer implies
// anybody checked it. The consent context has to carry that distinction, or a
// person deciding cannot tell an operator-vetted connector from one that named
// itself thirty seconds ago.
func TestConsentScreenSeesWhoVouchedForTheClient(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	vettedID, _ := registerClient(t, admin, ts.URL)
	_, out := registerDynamic(t, ts.URL, `{"client_name":"Stranger","redirect_uris":["`+testRedirect+`"]}`)
	selfID := out["client_id"].(string)

	ctxFor := func(clientID string) map[string]any {
		t.Helper()
		q := "client_id=" + clientID + "&redirect_uri=" + testRedirect +
			"&response_type=code&code_challenge=" + testChallenge() +
			"&code_challenge_method=S256&resource=" + ts.URL
		resp, err := admin.Get(ts.URL + "/api/v1/oauth/authorize-context?" + q)
		if err != nil {
			t.Fatalf("authorize-context: %v", err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m
	}

	if got := ctxFor(vettedID)["selfRegistered"]; got != false {
		t.Errorf("an operator-registered client reported selfRegistered=%v, want false", got)
	}
	if got := ctxFor(selfID)["selfRegistered"]; got != true {
		t.Errorf("a self-registered client reported selfRegistered=%v, want true — the person must be able to tell", got)
	}
}

// TestDynamicRegistrationRefusesWhatItCannotHonour: a client is told at
// registration that its request does not fit, rather than at the first flow that
// fails. The error codes are RFC 7591's, because a client library branches on them.
func TestDynamicRegistrationRefusesWhatItCannotHonour(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	for _, tc := range []struct{ name, body, wantErr string }{
		{"no redirect URIs", `{"client_name":"X"}`, "invalid_redirect_uri"},
		{"a plain-http redirect off the machine", `{"redirect_uris":["http://c.example.com/cb"]}`, "invalid_redirect_uri"},
		{"a redirect with a fragment", `{"redirect_uris":["https://c.example.com/cb#f"]}`, "invalid_redirect_uri"},
		{"a grant type this server does not offer", `{"redirect_uris":["https://c.example.com/cb"],"grant_types":["password"]}`, "invalid_client_metadata"},
		{"an implicit response type", `{"redirect_uris":["https://c.example.com/cb"],"response_types":["token"]}`, "invalid_client_metadata"},
		{"an auth method this server does not offer", `{"redirect_uris":["https://c.example.com/cb"],"token_endpoint_auth_method":"private_key_jwt"}`, "invalid_client_metadata"},
		{"a body that is not JSON", `{{{`, "invalid_client_metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, out := registerDynamic(t, ts.URL, tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("= %d, want 400", status)
			}
			if out["error"] != tc.wantErr {
				t.Errorf("error = %v, want %q", out["error"], tc.wantErr)
			}
		})
	}
}

// TestDynamicRegistrationIsBounded: an open write endpoint that grows without limit
// is a way to fill somebody's disk.
//
// The cap evicts the oldest self-registered client that nobody ever approved,
// rather than refusing once full. A cap that only refuses is its own denial of
// service: whoever fills the table first locks everybody else out of registering,
// permanently and from outside.
func TestDynamicRegistrationIsBounded(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	var first string
	for i := 0; i < api.MaxDynamicClients+3; i++ {
		status, out := registerDynamic(t, ts.URL,
			`{"client_name":"c","redirect_uris":["`+testRedirect+`"]}`)
		if status != http.StatusCreated {
			t.Fatalf("registration %d = %d: %v — the cap must evict, not refuse", i, status, out)
		}
		if first == "" {
			first = out["client_id"].(string)
		}
	}

	listResp, err := admin.Get(ts.URL + "/api/v1/oauth-clients")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var clients []struct{ ID string }
	_ = json.NewDecoder(listResp.Body).Decode(&clients)
	listResp.Body.Close()
	if len(clients) > api.MaxDynamicClients {
		t.Errorf("%d clients registered, want the cap of %d to have held",
			len(clients), api.MaxDynamicClients)
	}
	for _, c := range clients {
		if c.ID == first {
			t.Error("the oldest unused self-registration survived the cap")
		}
	}
}

// TestAnApprovedSelfRegistrationIsNotEvicted: the eviction takes the unused, never
// something a person has said yes to. Dropping an approved client would revoke
// their access because a stranger registered enough new ones.
func TestAnApprovedSelfRegistrationIsNotEvicted(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	_, out := registerDynamic(t, ts.URL, `{"client_name":"Wanted","redirect_uris":["`+testRedirect+`"]}`)
	keptID := out["client_id"].(string)
	keptSecret := out["client_secret"].(string)

	code := codeFrom(t, approve(t, admin, ts.URL, keptID, ts.URL, true))
	st, tok := postToken(t, ts.URL, map[string][]string{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {keptID}, "client_secret": {keptSecret},
	})
	if st != http.StatusOK {
		t.Fatalf("exchange = %d: %v", st, tok)
	}
	access := tok["access_token"].(string)

	// Now flood past the cap.
	for i := 0; i < api.MaxDynamicClients+3; i++ {
		if status, out := registerDynamic(t, ts.URL,
			`{"client_name":"flood","redirect_uris":["`+testRedirect+`"]}`); status != http.StatusCreated {
			t.Fatalf("flood registration %d = %d: %v", i, status, out)
		}
	}
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusOK {
		t.Errorf("an approved self-registration was evicted by a flood (%d) — a stranger revoked somebody's access", got)
	}
}

// newOpenRegistrationServer starts a server with dynamic registration turned on.
func newOpenRegistrationServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newOpenRegistrationServerOn(t, t.TempDir())
}

// newOpenRegistrationServerOn is the same over a data directory the caller keeps,
// so a test can go and break what the stores are sitting on.
func newOpenRegistrationServerOn(t *testing.T, dir string) *httptest.Server {
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
	loopback := "http://" + ts.Listener.Addr().String()
	srv, err := api.New(proc, store, dir,
		api.WithAuth(),
		api.WithDynamicClientRegistration(),
		api.WithMCP(mcp.NewServer(mcp.NewClient(loopback))),
	)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})
	return ts
}

// TestDynamicRegistrationIsThrottled: the endpoint has its own budget, so a flood
// spends that one and not the shared public one.
//
// Two properties in one test, and the second is the point. Registration is
// throttled — but the token exchanges of the clients that already registered are
// not, or abuse of an open endpoint would become an outage for everyone else.
func TestDynamicRegistrationIsThrottled(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	throttled := false
	for i := 0; i < 200 && !throttled; i++ {
		status, out := registerDynamic(t, ts.URL,
			`{"client_name":"c","redirect_uris":["`+testRedirect+`"]}`)
		if status == http.StatusTooManyRequests {
			throttled = true
			if out["error"] != "temporarily_unavailable" {
				t.Errorf("throttled with error %v, want temporarily_unavailable", out["error"])
			}
		}
	}
	if !throttled {
		t.Fatal("registration was never throttled; an unauthenticated write endpoint has to be")
	}

	// And the rest of the public surface still answers. A client that registered
	// before the flood must still be able to exchange and refresh.
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL, true))
	st, tok := postToken(t, ts.URL, map[string][]string{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if st != http.StatusOK {
		t.Errorf("token exchange = %d after a registration flood: %v — the flood took down the token endpoint", st, tok)
	}
}

// TestRegistrationRefusesWhenEveryClientIsApproved is the one case where the cap
// does refuse, and it is the case where refusing is right: there is nothing in the
// table that may be thrown away, because a person said yes to every one of them.
//
// That state cannot be reached from outside — each entry took somebody's approval —
// so refusing here is not the denial of service that refusing in general would be.
func TestRegistrationRefusesWhenEveryClientIsApproved(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	for i := 0; i < api.MaxDynamicClients; i++ {
		status, out := registerDynamic(t, ts.URL,
			`{"client_name":"kept","redirect_uris":["`+testRedirect+`"]}`)
		if status != http.StatusCreated {
			t.Fatalf("registration %d = %d: %v", i, status, out)
		}
		id, _ := out["client_id"].(string)
		secret, _ := out["client_secret"].(string)
		code := codeFrom(t, approve(t, admin, ts.URL, id, ts.URL, true))
		if st, tok := postToken(t, ts.URL, map[string][]string{
			"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
			"client_id": {id}, "client_secret": {secret},
		}); st != http.StatusOK {
			t.Fatalf("exchange %d = %d: %v", i, st, tok)
		}
	}

	status, out := registerDynamic(t, ts.URL,
		`{"client_name":"one too many","redirect_uris":["`+testRedirect+`"]}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("= %d, want 503: with every client approved there is nothing to evict", status)
	}
	if out["error"] != "temporarily_unavailable" {
		t.Errorf("error = %v, want temporarily_unavailable", out["error"])
	}
	// The description has to name the remedy, because the remedy is an operator's:
	// nothing the client does next will help.
	if desc, _ := out["error_description"].(string); !strings.Contains(desc, "administrator") {
		t.Errorf("error_description = %q, want it to say an administrator must act", desc)
	}
}

// TestSelfRegisteredClientsAreFlaggedInTheListing: the distinction the consent
// screen makes is also in the operator's own view, so "what is registered here"
// answers "and who put it there".
func TestSelfRegisteredClientsAreFlaggedInTheListing(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	vettedID, _ := registerClient(t, admin, ts.URL)
	_, out := registerDynamic(t, ts.URL, `{"client_name":"Stranger","redirect_uris":["`+testRedirect+`"]}`)
	selfID, _ := out["client_id"].(string)

	resp, err := admin.Get(ts.URL + "/api/v1/oauth-clients")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var clients []struct {
		ID             string
		SelfRegistered bool `json:"selfRegistered"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range clients {
		seen[c.ID] = c.SelfRegistered
	}
	if seen[vettedID] {
		t.Error("a client an administrator registered is listed as self-registered")
	}
	if !seen[selfID] {
		t.Error("a client that registered itself is not listed as such")
	}
}

// TestARegistrationWithNoNameIsNamedAfterItself: client_name is optional in RFC
// 7591, and the consent screen still has to call the application something. The
// host it registered a redirect for is at least a claim it had to be able to
// receive a redirect on — unlike a name, which is free.
func TestARegistrationWithNoNameIsNamedAfterItself(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	status, out := registerDynamic(t, ts.URL, `{"redirect_uris":["`+testRedirect+`"]}`)
	if status != http.StatusCreated {
		t.Fatalf("= %d: %v", status, out)
	}
	if got := out["client_name"]; got != "client.example.com" {
		t.Errorf("client_name = %v, want the redirect host", got)
	}
}

// TestARegistrationCannotShoutOnTheConsentScreen: the name a self-registered
// client chooses is rendered where a person is deciding. The page escapes it, so
// this is not about markup — it is about a name that restructures the question, or
// pushes it off the screen.
func TestARegistrationCannotShoutOnTheConsentScreen(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	for _, tc := range []struct{ name, clientName string }{
		{"a name with a line break", "Atlas\nApprove this, it is safe"},
		{"a name longer than the screen", strings.Repeat("s", 400)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"client_name": tc.clientName, "redirect_uris": []string{testRedirect},
			})
			status, out := registerDynamic(t, ts.URL, string(body))
			if status != http.StatusBadRequest {
				t.Errorf("= %d, want 400", status)
			}
			if out["error"] != "invalid_client_metadata" {
				t.Errorf("error = %v, want invalid_client_metadata", out["error"])
			}
		})
	}
}

// TestRegistrationSurvivesABrokenDataDirectory: what an open endpoint answers when
// the disk beneath it is not what it was.
//
// A 503 and no client, rather than a client id the caller could use and the server
// has no record of — which would be a credential nobody can see or remove.
func TestRegistrationSurvivesABrokenDataDirectory(t *testing.T) {
	breakDir := func(t *testing.T, dir, name string) {
		t.Helper()
		p := filepath.Join(dir, name)
		// A plain file where a directory belongs fails every read and every write the
		// store makes, and fails for root too — which chmod would not achieve here.
		if err := os.RemoveAll(p); err != nil {
			t.Fatalf("remove %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	for _, broken := range []string{"oauth-clients", "oauth-grants"} {
		t.Run(broken, func(t *testing.T) {
			dir := t.TempDir()
			ts := newOpenRegistrationServerOn(t, dir)
			breakDir(t, dir, broken)

			status, out := registerDynamic(t, ts.URL,
				`{"client_name":"c","redirect_uris":["`+testRedirect+`"]}`)
			if status != http.StatusServiceUnavailable {
				t.Errorf("= %d, want 503 — an unwritable store must not read as a successful registration", status)
			}
			if out["error"] != "temporarily_unavailable" {
				t.Errorf("error = %v, want temporarily_unavailable", out["error"])
			}
			if _, ok := out["client_id"]; ok {
				t.Error("a client id was handed out that nothing recorded")
			}
		})
	}
}

// TestARegistrationThatStopsHalfwayIsNotAClient: a body that announces more than
// it sends. Registering has to fail rather than proceed on a partial one — a JSON
// object that happens to parse from the first half of a request is still not what
// the caller asked for.
func TestARegistrationThatStopsHalfwayIsNotAClient(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	addr := strings.TrimPrefix(ts.URL, "http://")

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	body := `{"client_name":"half","redirect_uris":["` + testRedirect + `"]}`
	// Content-Length promises more than what follows; the half-close then ends the
	// body early, which is what the reader sees as a broken request.
	fmt.Fprintf(conn, "POST /oauth/register HTTP/1.1\r\nHost: %s\r\n"+
		"Content-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		addr, len(body)+64, body)
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.CloseWrite(); err != nil {
			t.Fatalf("close write: %v", err)
		}
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("= %d, want 400", resp.StatusCode)
	}
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	if out["error"] != "invalid_client_metadata" {
		t.Errorf("error = %v, want invalid_client_metadata", out["error"])
	}
	if _, ok := out["client_id"]; ok {
		t.Error("a truncated request produced a client")
	}
}

// TestAnOversizedRegistrationIsNamedAsSuch: a body past the limit is refused for
// being too large, not reported as malformed JSON — which would send a client
// hunting a syntax error it does not have.
func TestAnOversizedRegistrationIsNamedAsSuch(t *testing.T) {
	ts := newOpenRegistrationServer(t)

	uris := make([]string, 0, 512)
	for i := 0; i < 512; i++ {
		uris = append(uris, "https://c.example.com/"+strings.Repeat("p", 64))
	}
	body, _ := json.Marshal(map[string]any{"client_name": "big", "redirect_uris": uris})
	status, out := registerDynamic(t, ts.URL, string(body))
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("= %d, want 413", status)
	}
	if desc, _ := out["error_description"].(string); !strings.Contains(desc, "too large") {
		t.Errorf("error_description = %q, want it to say the request is too large", desc)
	}
}
