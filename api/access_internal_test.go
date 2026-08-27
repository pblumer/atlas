package api

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// The route inventory. These tests are the reason access.go exists: they turn
// "every interface is behind a login" from a claim into something a reviewer can
// check by reading one list and re-running one command.

// wantPublicRoutes is the complete set of patterns this server serves without an
// authenticated principal. It is written out here, rather than derived, on
// purpose: opening a route is then a diff to this list that a reviewer sees, and
// a route that becomes public by accident fails the build instead of shipping.
//
// Every entry needs a reason it must work before login. The reasons are in the
// comments at each mount site in mountRoutes.
var wantPublicRoutes = []string{
	// Probes. A readiness probe that needs a credential is a probe that does not
	// work in the incident it exists for.
	"GET /healthz",
	"GET /readyz",

	// What the login screen itself reads, before anyone has a session.
	"POST /api/v1/auth/login",
	"GET /api/v1/info",
	"GET /api/v1/settings/theme",
	"GET /api/v1/settings/logo",
	"GET /api/v1/settings/registration",

	// Share links, where the token in the URL is the whole authorization
	// (ADR-0029/0143). Rate-limited in their handlers.
	"GET /public/process-docs/{token}",
	"GET /public/forms/{token}",
	"GET /public/forms/{token}/schema",
	"POST /public/forms/{token}/start",
	"OPTIONS /public/forms/{token}/schema",
	"OPTIONS /public/forms/{token}/start",

	// What this server is, as an OAuth protected resource (RFC 9728, ADR-0200).
	// These are read *after* a refusal, to find out what did the refusing — behind
	// the credential they help you obtain, nobody who needs them could read them.
	// They carry the origin, a product name, and that a bearer goes in a header.
	"GET /.well-known/oauth-protected-resource",
	"GET /.well-known/oauth-protected-resource/mcp",

	// The embedded web UI. Static assets; the login screen has to load.
	"/",
}

// accessTestServer is a Server with the flags that decide which routes exist set
// the way a default `atlas serve` sets them: metrics on, docs on, and an MCP
// transport supplied. mountRoutes reads only those flags and takes method values
// off the handlers without calling them, so a zero engine is enough — the same
// trick specServer uses for the route table.
func accessTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		metricsEnabled: true,
		docsEnabled:    true,
		mcpHandler:     http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
}

// classifyPath is the class the real route table gives one request. Tests
// elsewhere use it to state a route's posture without rebuilding the mux
// themselves.
func classifyPath(t *testing.T, method, path string) accessClass {
	t.Helper()
	_, policy := accessTestServer(t).mountRoutes()
	return policy.classify(httptest.NewRequest(method, path, nil))
}

// TestPublicRoutesAreExactlyTheAllowlist is the inventory guard. A new route
// mounted as public, or an existing one flipped, shows up here as a failing test
// naming the pattern — which is the review this file exists to force.
func TestPublicRoutesAreExactlyTheAllowlist(t *testing.T) {
	_, policy := accessTestServer(t).mountRoutes()

	got := policy.publicPatterns()
	want := append([]string(nil), wantPublicRoutes...)
	sort.Strings(want)

	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	inWant := map[string]bool{}
	for _, p := range want {
		inWant[p] = true
	}
	inGot := map[string]bool{}
	for _, p := range got {
		inGot[p] = true
	}
	for _, p := range got {
		if !inWant[p] {
			t.Errorf("route %q is public and not on the allowlist — if that is intended, add it to wantPublicRoutes with its reason", p)
		}
	}
	for _, p := range want {
		if !inGot[p] {
			t.Errorf("allowlisted public route %q is not served as public — a stale entry, or the mount site changed", p)
		}
	}
}

// TestEveryPublicAPIRouteEntryIsRegistered catches the failure mode an allowlist
// of strings has: an entry that matches nothing. A typo there is silent — the
// route it meant to open stays gated and the list still reads as if it were
// open — so the entries are checked against the route table itself.
func TestEveryPublicAPIRouteEntryIsRegistered(t *testing.T) {
	s := accessTestServer(t)
	registered := map[string]bool{}
	for _, r := range s.apiRoutes() {
		registered[r.method+" "+r.pattern] = true
	}
	for _, pattern := range publicAPIRoutes {
		if !registered[pattern] {
			t.Errorf("publicAPIRoutes names %q, which the route table does not register", pattern)
		}
	}
}

// TestAccessClassification pins the decisions the old path-prefix rule could not
// express, and the ones it got wrong.
func TestAccessClassification(t *testing.T) {
	_, policy := accessTestServer(t).mountRoutes()

	cases := []struct {
		name   string
		method string
		path   string
		want   accessClass
	}{
		// The MCP transport is a route like any other now, gated like any other
		// (ADR-0196). It used to be mounted beside this
		// handler, where the boundary never saw it at all.
		{"mcp transport", "POST", "/mcp", accessAuthenticated},
		{"mcp subpath", "POST", "/mcp/anything", accessAuthenticated},

		// Method matters. Reading the brand accent is what the login screen does;
		// writing it is an admin act, and is now refused at the boundary rather than
		// only by requireAdmin inside the handler.
		{"theme read", "GET", "/api/v1/settings/theme", accessPublic},
		{"theme write", "PUT", "/api/v1/settings/theme", accessAuthenticated},
		{"theme delete", "DELETE", "/api/v1/settings/theme", accessAuthenticated},
		{"logo write", "PUT", "/api/v1/settings/logo", accessAuthenticated},
		{"registration write", "PUT", "/api/v1/settings/registration", accessAuthenticated},

		// The ordinary API surface.
		{"processes", "GET", "/api/v1/processes", accessAuthenticated},
		{"deploy", "POST", "/api/v1/deployments", accessAuthenticated},
		{"login", "POST", "/api/v1/auth/login", accessPublic},
		{"who am i", "GET", "/api/v1/auth/me", accessAuthenticated},

		// An /api/v1 path no route claims stays on the API's side of the boundary. It
		// must not fall through to the public UI catch-all, which is how a gap in the
		// route table would otherwise become a gap in the boundary.
		{"unrouted api path", "GET", "/api/v1/nonesuch", accessAuthenticated},
		{"unrouted api subpath", "GET", "/api/v1/processes/", accessAuthenticated},
		{"api root", "GET", "/api/v1", accessAuthenticated},

		// Probes, metrics, share links, UI.
		{"healthz", "GET", "/healthz", accessPublic},
		// The exposition is the one route that left the public set after the
		// boundary existed: a scraper is a machine, and machines present credentials
		// (ADR-0198).
		{"metrics", "GET", "/metrics", accessAuthenticated},
		{"public form", "GET", "/public/forms/abc123", accessPublic},
		{"public form start", "POST", "/public/forms/abc123/start", accessPublic},
		{"ui", "GET", "/", accessPublic},
		{"ui asset", "GET", "/assets/app.js", accessPublic},
		{"ui page", "GET", "/index.html", accessPublic},

		// The API description and the explorer are a developer surface, not something
		// the login screen reads, and the explorer's "Try it out" drives the same
		// mutating API. Both moved behind the boundary when --auth became the default
		// (ADR-0195).
		{"openapi document", "GET", "/api/v1/openapi.json", accessAuthenticated},
		{"api explorer", "GET", "/api/docs", accessAuthenticated},
		{"api explorer asset path", "GET", "/api/docs/", accessAuthenticated},

		{"user administration", "GET", "/api/v1/users", accessAuthenticated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := policy.classify(httptest.NewRequest(c.method, c.path, nil))
			if got != c.want {
				t.Errorf("classify(%s %s) = %v, want %v", c.method, c.path, got, c.want)
			}
		})
	}
}

// TestUndeclaredRouteIsGated is the fail-closed property itself, stated
// directly: a pattern registered on the serving mux without a declaration is
// authenticated, not whatever the "/" catch-all happens to be.
//
// This is the bug the whole file is about. The old rule made a route public
// because of where it was mounted; classify makes an unknown route gated because
// nobody said otherwise, so the next route mounted off to the side fails safe.
func TestUndeclaredRouteIsGated(t *testing.T) {
	mux := http.NewServeMux()
	policy := newAccessPolicy(mux)
	nop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	// The UI catch-all, declared public, exactly as the real server has it.
	mux.Handle("/", nop)
	policy.declare("/", accessPublic)

	// A route mounted straight onto the mux, bypassing mount() — the mistake.
	mux.Handle("/sidecar", nop)

	if got := policy.classify(httptest.NewRequest("GET", "/sidecar", nil)); got != accessAuthenticated {
		t.Errorf("an undeclared route classified as %v, want %v — it inherited the catch-all's class instead of failing closed", got, accessAuthenticated)
	}
	if got := policy.classify(httptest.NewRequest("GET", "/", nil)); got != accessPublic {
		t.Errorf("the declared catch-all classified as %v, want %v", got, accessPublic)
	}
}

// TestAccessClassString keeps the class names readable in a test failure.
func TestAccessClassString(t *testing.T) {
	if got := accessAuthenticated.String(); got != "authenticated" {
		t.Errorf("accessAuthenticated = %q", got)
	}
	if got := accessPublic.String(); got != "public" {
		t.Errorf("accessPublic = %q", got)
	}
	if got := accessClass(9).String(); got == "" {
		t.Error("an unknown class must still render something")
	}
}
