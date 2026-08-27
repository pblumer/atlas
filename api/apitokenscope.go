package api

import (
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
// Two scopes, not a permission system. A scope here answers "what does this kind
// of machine need", not "what may this identity do" — the second question is
// roles per endpoint group, which is a larger piece of work with its own record
// still to write. Adding a third scope should mean a third kind of machine turned
// up, not that somebody wanted a finer slice of the same one.

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

	// apiScopeMetrics reaches the Prometheus exposition and nothing else. It is what
	// let /metrics move behind the boundary at all: a scraper is a machine that needs
	// exactly one GET forever, which is the narrowest scope there is and the easiest
	// one to hand out (ADR-0198).
	apiScopeMetrics = "metrics"
)

// apiScopeAllowed is the complete reach of each confined scope. A scope absent
// from this map is unconfined — apiScopeFull is the only one, and it says so.
//
// The worker set is derived from what `atlas worker` actually calls: leasing a
// batch by type, settling each job either way, and — for a mail connector running
// in preview — posting the framed message back to this server's outbox (ADR-0150).
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
	},
	// One route, and not one of /api/v1's: the exposition is mounted beside the
	// probes. A scope is a set of mounted patterns, not of API operations, which is
	// what makes it able to cover this at all.
	apiScopeMetrics: {
		"GET /metrics",
	},
}

// apiMintableScopes lists the scopes an API token may be minted with. It is not
// every scope: apiScopeDeploy belongs to a credential with its own store, so
// nothing here can ask for it.
var apiMintableScopes = []string{apiScopeFull, apiScopeWorker, apiScopeMetrics}

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
// and of anything else requireAdmin guards. A confined scope reaches only its
// allowlist, and an unmatched request yields an empty pattern, so anything not
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
