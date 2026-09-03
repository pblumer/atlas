package api

import (
	"crypto/subtle"
	"net/http"
	"sort"
)

// What an API token may reach (ADR-0194).
//
// A machine credential that can do everything a signed-in person can is the thing
// this whole line of work exists to stop handing out, so a token carries a scope.
// The mechanism is the one deployAgentAllowed already uses and access.go
// generalized a layer up: a fail-closed allowlist of net/http patterns, resolved
// through an http.ServeMux so precedence is decided by the same matcher that
// routes the real request. Hand-written path comparison is exactly where an
// allowlist springs a leak.
//
// A scope is not a permission system. It answers "what does this kind of machine
// need", not "what may this identity do" — that second question is answered by the
// role each route names (routeroles.go, ADR-0209), and a
// request has to pass both: the scope says which routes this credential may reach
// at all, the roles say which kinds of operation its holder may perform. Adding a
// scope should mean a new kind of machine turned up, not that somebody wanted a
// finer slice of the same one.

const (
	// apiScopeFull reaches everything a signed-in non-admin user reaches. It is the
	// honest scope for a CI job or an MCP adapter, which drive the product surface
	// and cannot be enumerated in advance — and it is broad, which is why it is
	// named rather than being what you get by leaving the field out of a request.
	apiScopeFull = "full"

	// apiScopeDeploy is what a deploy token reaches: publish a bundle here, and read
	// back what this server now runs for that application (ADR-0129). It is not
	// mintable as an API token — no request can ask for it — because the credential
	// that carries it has its own store, its own prefix and its own record. It lives
	// here so that every confined credential is confined by one mechanism, which is
	// the property that makes the reach of all of them provable by reading one file.
	apiScopeDeploy = "deploy"

	// apiScopeWorker reaches the four operations an out-of-process worker performs
	// and nothing else (ADR-0007/0168). This is the scope that earns the mechanism:
	// a worker is a long-lived credential on another host, often in another network
	// zone, and its whole job is four calls.
	apiScopeWorker = "worker"

	// apiScopeMCP reaches the Model Context Protocol transport and nothing else. It
	// is what a person's OAuth grant carries when they approved a client for the
	// transport rather than for the whole server (ADR-0200) — an audience made real:
	// a token minted to talk to /mcp has no business driving /api/v1 directly. Like
	// apiScopeDeploy it is not mintable, because nothing asks for it: it follows from
	// which resource the person approved.
	//
	// "Directly" is the whole of the word: the adapter serving /mcp turns every tool
	// call into an API request of its own, carrying this same token, so the scope
	// confines the *tools* too unless those requests can be told apart. See
	// mcpTransportHeader for how they say what they are.
	apiScopeMCP = "mcp"

	// apiScopeMetrics reaches the Prometheus exposition and nothing else. It is what
	// let /metrics move behind the boundary at all: a scraper is a machine that needs
	// exactly one GET forever, which is the narrowest scope there is and the easiest
	// one to hand out (ADR-0198).
	apiScopeMetrics = "metrics"

	// apiScopeStatus reaches this server's node descriptor and nothing else. It is
	// what ADR-0189 §6 requires of remote correlation: another Atlas asking "who are
	// you, and what can you be asked for" must not be handed a deploy credential to
	// get the answer, and a credential handed to a peer should be the narrowest one
	// that answers the question — here, one GET.
	apiScopeStatus = "status"
)

// apiScopeAllowed is the complete reach of each confined scope. A scope absent
// from this map is unconfined — apiScopeFull is the only one, and it says so.
//
// The worker set is derived from what `atlas worker` actually calls: leasing a
// batch by type, settling each job either way, and — for a mail worker running
// in preview — posting the framed message back to this server's outbox (ADR-0150),
// and, for an AD worker in mockup mode, reporting the forest it holds so the
// Console can show it (ADR-0213).
// Nothing else. Notably not `POST /api/v1/jobs/{key}/activate`, which is the
// single-job form no worker uses.
var apiScopeAllowed = map[string][]string{
	// A publisher has to see the result of what it shipped, or its own per-target
	// view is blind — hence the second entry (ADR-0129).
	apiScopeDeploy: {
		"POST /api/v1/applications/import",
		"GET /api/v1/applications/{id}/deployments",
	},
	apiScopeWorker: {
		"POST /api/v1/jobs/activate",
		"POST /api/v1/jobs/{key}/complete",
		"POST /api/v1/jobs/{key}/fail",
		"POST /api/v1/mail/outbox",
		"POST /api/v1/ad/mock-directory",
	},
	// One route, and not one of /api/v1's: the exposition is mounted beside the
	// probes. A scope is a set of mounted patterns, not of API operations, which is
	// what makes it able to cover this at all.
	apiScopeMetrics: {
		"GET /metrics",
	},
	// Read-only, and deliberately not the PUT on the same path: a peer reads an
	// identity, it never sets one.
	apiScopeStatus: {
		"GET /api/v1/node",
	},
	// The transport, both the exact path and everything under it, because that is
	// how it is mounted. No method: the transport answers POST for JSON-RPC and GET
	// for the event stream, and confining a scope to one of them would break the
	// other half of the same protocol.
	apiScopeMCP: {
		"/mcp",
		"/mcp/",
	},
}

// mcpTransportHeader is how an API request says it is a *tool call* — one the MCP
// adapter made while serving /mcp — rather than a client driving /api/v1 directly
// with a token approved for the transport.
//
// It exists because the two are otherwise the same request. The adapter forwards its
// caller's credential verbatim to a loopback URL (ADR-0196), so a tool call reaches
// the API carrying an /mcp-scoped token and nothing that distinguishes it from the
// direct use apiScopeMCP exists to refuse. Confining the scope without this gave a
// grant for the transport the transport and *nothing it can do*: every tool answered
// "this credential's scope (mcp) does not permit GET /api/v1/…".
//
// The value is this server's own internal token, stamped by [Server.mcpTransport] on
// the way in, so the marker cannot be forged by whoever holds the confined token —
// anyone who could supply the value already holds the credential of a process this
// server started. And it relaxes the scope check only: a tool call is still held to
// its caller's roles, so what this admits is exactly the tool surface and never the
// admin routes an /mcp grant must not reach.
//
// The name is duplicated in the mcp package rather than imported, because the
// dependency runs one way — this package takes an http.Handler and need not know
// that one exists ([WithMCP]). TestMCPTransportHeaderMatchesTheAdapter holds the two
// spellings together.
const mcpTransportHeader = "X-Atlas-Via-MCP"

// apiMintableScopes lists the scopes an API token may be minted with. It is not
// every scope: apiScopeDeploy belongs to a credential with its own store, so
// nothing here can ask for it.
var apiMintableScopes = []string{apiScopeFull, apiScopeWorker, apiScopeMetrics, apiScopeStatus}

// apiScopes returns the mintable scopes, sorted, for the error message that names
// them when a request asks for something else.
func apiScopes() []string {
	out := append([]string(nil), apiMintableScopes...)
	sort.Strings(out)
	return out
}

// validAPIScope reports whether a scope may be minted as an API token.
func validAPIScope(scope string) bool {
	for _, s := range apiMintableScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// apiScopeMux resolves a request against one scope's allowlist. Built once per
// scope; the muxes carry no handlers, only whether a pattern matched is consulted.
var apiScopeMux = func() map[string]*http.ServeMux {
	out := make(map[string]*http.ServeMux, len(apiScopeAllowed))
	nop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for scope, patterns := range apiScopeAllowed {
		m := http.NewServeMux()
		for _, pattern := range patterns {
			m.Handle(pattern, nop)
		}
		out[scope] = m
	}
	return out
}()

// apiScopeMayReach reports whether a token of this scope may perform this request.
//
// An unconfined scope reaches everything the principal's roles allow — which for a
// token is never admin, so "everything" already stops short of user administration
// and of every other route that names the admin role. A confined scope reaches only
// its allowlist, and an unmatched request yields an empty pattern, so anything not
// listed is refused.
//
// An unknown scope reaches nothing. That is the fail-closed direction and it
// matters: a record written by a newer version, naming a scope this binary does
// not know, must be inert rather than unconfined.
func apiScopeMayReach(scope string, r *http.Request) bool {
	if scope == apiScopeFull {
		return true
	}
	mux, ok := apiScopeMux[scope]
	if !ok {
		return false
	}
	_, pattern := mux.Handler(r)
	return pattern != ""
}

// scopeMayReach is [apiScopeMayReach] plus the single exception a confined scope
// has: the MCP scope reaches the API surface when the request is a tool call this
// server's own adapter made, which mcpTransportHeader is how it says so.
//
// It is a method rather than another entry in apiScopeAllowed because the exception
// is not a set of patterns — it is a property of one request — and folding it into
// that table would make the mcp scope read as unconfined to anyone checking what a
// credential reaches.
func (s *Server) scopeMayReach(scope string, r *http.Request) bool {
	if apiScopeMayReach(scope, r) {
		return true
	}
	return scope == apiScopeMCP && s.viaMCPTransport(r)
}

// viaMCPTransport reports whether a request carries the marker [Server.mcpTransport]
// stamps on what enters /mcp.
//
// A server with no internal token — authentication off — never matches, rather than
// matching the empty header every request has. That is the fail-closed direction and
// the only one worth having: this admits a request that proves where it came from,
// and an unset secret proves nothing.
func (s *Server) viaMCPTransport(r *http.Request) bool {
	if s.internalToken == "" {
		return false
	}
	got := r.Header.Get(mcpTransportHeader)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.internalToken)) == 1
}
