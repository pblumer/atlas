package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The MCP transport, end to end, on the two properties that used to be missing.
//
// It was mounted on a mux beside the API server, so withAuth never resolved a
// principal for it and --auth did not gate it — while the adapter attached the
// server's internal service token to every loopback call it made. Reaching the
// port was therefore enough to drive 71 tools, deploy included, as a principal
// nobody had to present. Both halves are fixed here: the transport is a route
// like any other (api.WithMCP), and a tool call carries the caller's own
// credential rather than one the adapter supplies.

// newMCPServer starts an Atlas server with authentication on and its MCP
// transport mounted, and returns it with the admin credentials.
//
// The listener is created before the handler so the adapter can be pointed at the
// address this same server is about to serve on — the loopback arrangement
// cmd/atlas builds, which is what makes the adapter a pure proxy over the public
// API rather than a second way into the engine.
func newMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")

	dir := t.TempDir()
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

// rpc posts one JSON-RPC message to /mcp with c's cookies and returns the status
// and decoded body.
func rpc(t *testing.T, c *http.Client, ts *httptest.Server, payload string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(strings.TrimSpace(string(data))) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	return resp.StatusCode, m
}

// toolText returns a tools/call result's text content and whether the tool
// reported an error.
func toolText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	isErr, _ := res["isError"].(bool)
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in %v", res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	return text, isErr
}

const listProcessesCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
	`"params":{"name":"atlas_list_processes","arguments":{}}}`

// TestMCPTransportRequiresAuthentication: under --auth, /mcp is gated like the
// rest of the surface. A request with no credential is refused before the adapter
// runs, and told which scheme to present.
func TestMCPTransportRequiresAuthentication(t *testing.T) {
	ts := newMCPServer(t)

	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(listProcessesCall))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp: status=%d, want 401", resp.StatusCode)
	}
	if auth := resp.Header.Get("WWW-Authenticate"); !strings.Contains(auth, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want it to name Bearer so a client knows what to send", auth)
	}

	// The subpath the transport also answers on is gated the same way; it is one
	// route as far as the boundary is concerned.
	resp2, err := http.Post(ts.URL+"/mcp/", "application/json", strings.NewReader(listProcessesCall))
	if err != nil {
		t.Fatalf("POST /mcp/: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp/: status=%d, want 401", resp2.StatusCode)
	}
}

// TestMCPTransportAdmitsASignedInCaller: the gate is a gate, not a wall. A
// browser session reaches the tools, which is what lets the signed-in UI drive
// one itself.
func TestMCPTransportAdmitsASignedInCaller(t *testing.T) {
	ts := newMCPServer(t)
	c := mcpClient(t)
	if code := login(t, c, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}

	code, resp := rpc(t, c, ts, listProcessesCall)
	if code != http.StatusOK {
		t.Fatalf("signed-in /mcp: status=%d, want 200", code)
	}
	if text, isErr := toolText(t, resp); isErr {
		t.Fatalf("tool reported an error: %s", text)
	}
}

// TestMCPToolActsAsTheCallingPrincipal is the half that a gate alone does not
// give. The adapter no longer supplies an identity, so the API sees the caller's
// own: an admin gets past requireAdmin on an admin-only tool, and a signed-in
// non-admin does not.
//
// The old arrangement failed this from both directions. Every call arrived as the
// service principal, so the admin was refused work they were entitled to, and the
// non-admin was refused for a reason that had nothing to do with who they were.
func TestMCPToolActsAsTheCallingPrincipal(t *testing.T) {
	ts := newMCPServer(t)

	admin := mcpClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login: got %d", code)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	alice := mcpClient(t)
	if code := login(t, alice, ts, "alice", "password1"); code != http.StatusOK {
		t.Fatalf("alice login: got %d", code)
	}

	// atlas_migration_plan maps to an admin-gated endpoint, and requireAdmin runs
	// before the handler looks at the keys — so bogus keys still tell the two
	// principals apart.
	const planCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_migration_plan",` +
		`"arguments":{"key":1,"targetProcessDefKey":2}}}`
	const adminRefusal = "admin role required"

	code, resp := rpc(t, alice, ts, planCall)
	if code != http.StatusOK {
		t.Fatalf("alice /mcp: status=%d, want 200 with a tool error", code)
	}
	text, isErr := toolText(t, resp)
	if !isErr || !strings.Contains(text, adminRefusal) {
		t.Errorf("non-admin over MCP: got isError=%v %q, want the admin refusal — the call did not arrive as alice", isErr, text)
	}

	code, resp = rpc(t, admin, ts, planCall)
	if code != http.StatusOK {
		t.Fatalf("admin /mcp: status=%d, want 200", code)
	}
	text, _ = toolText(t, resp)
	if strings.Contains(text, adminRefusal) {
		t.Errorf("admin over MCP was refused for lacking the admin role (%q) — the call did not arrive as root", text)
	}
}

// TestMCPRefusesADeployToken: a deploy token is a machine credential confined to
// two operations (ADR-0129), and /mcp is neither of them. Because the transport is
// now an ordinary route, that confinement covers it without anything in the MCP
// path knowing about deploy tokens — the credential is refused at the boundary,
// before the adapter runs at all.
func TestMCPRefusesADeployToken(t *testing.T) {
	ts := newMCPServer(t)
	admin := mcpClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login: got %d", code)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deploy-tokens", `{"name":"a peer"}`)
	if code != http.StatusOK {
		t.Fatalf("mint deploy token: got %d (%s)", code, body)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Token == "" {
		t.Fatalf("no token in %s (%v)", body, err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(listProcessesCall))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+minted.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("deploy token at /mcp: status=%d, want 403", resp.StatusCode)
	}
}

// TestDocsSurfaceIsBehindTheLogin: the API description and the explorer are a
// developer surface, not something the login screen reads, and the explorer drives
// the same mutating API a session is required for. Both are refused without one and
// served with one (ADR-0195).
//
// They are gated together on purpose: an explorer that renders and then cannot load
// its own document would be worse than one that says plainly it needs a login.
func TestDocsSurfaceIsBehindTheLogin(t *testing.T) {
	ts := newMCPServer(t) // an --auth server; --docs is on by default

	for _, path := range []string{"/api/v1/openapi.json", "/api/docs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s: status=%d, want 401", path, resp.StatusCode)
		}
	}

	c := mcpClient(t)
	if code := login(t, c, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	for _, path := range []string{"/api/v1/openapi.json", "/api/docs"} {
		code, _ := cReq(t, c, ts, http.MethodGet, path, "")
		if code != http.StatusOK {
			t.Errorf("signed-in GET %s: status=%d, want 200", path, code)
		}
	}
}

// mcpClient is a cookie-keeping client, so a login sticks for the /mcp calls that
// follow it.
func mcpClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}
