package api

import (
	"net/http"
	"sort"
)

// This file answers one question about every request before a handler sees it:
// may it be served without an authenticated principal?
//
// It replaces a path-prefix rule. That rule — gated if and only if the path
// starts with /api/v1 — made a route public by *omission*: anything mounted
// outside the prefix was reachable by anyone, not because somebody decided it
// should be but because of where it happened to be registered. That is how the
// MCP transport came to be open, and the Prometheus exposition with it. The rule
// could not tell a GET from a PUT either, so a path that had to be readable
// before login was writable as far as the boundary was concerned, and the admin
// check inside the handler was the only thing behind it.
//
// The shape here is the one deployAgentAllowed already uses a layer down, for
// the reason its comment gives: what a credential — or, here, an anonymous
// caller — can reach has to be provable by reading one short list, not by
// auditing every mount site. Two properties make that true.
//
//   - Every route states its class where it is mounted. mount takes one, so a
//     route cannot reach the mux without it (see mountRoutes).
//   - classify keys on the pattern that will actually serve the request, looked
//     up in what was declared. A pattern registered without a declaration is not
//     in the map, and an unknown pattern is gated — so a route mounted off to
//     the side fails closed instead of inheriting the "/" catch-all's class.
//
// TestPublicRoutesAreExactlyTheAllowlist then holds the resulting public set
// against a written-out list, so opening a route is a reviewable diff rather
// than a side effect.

// accessClass says who may reach a route.
type accessClass uint8

const (
	// accessAuthenticated is the zero value on purpose. A route whose class
	// nobody stated is gated; "public" is the thing that has to be said out loud.
	accessAuthenticated accessClass = iota

	// accessPublic is served without a principal even when enforcement is on.
	// Every route carrying it has a reason it must work before login, written at
	// its mount site.
	accessPublic
)

// String renders a class for a test failure or a log line.
func (c accessClass) String() string {
	switch c {
	case accessAuthenticated:
		return "authenticated"
	case accessPublic:
		return "public"
	default:
		return "unknown"
	}
}

// publicAPIRoutes names the entries of the /api/v1 route table that are served
// without authentication. Routes mounted outside that table state their class at
// their own mount site; this list exists because the table is registered in a
// loop, and a loop cannot say "except these five".
//
// Each entry is a net/http pattern, so the method is part of it. That is the
// point for the settings routes: the login screen reads the brand accent, the
// brand logo and the registration link before anyone has a session, but writing
// any of them is an admin act. They used to share one path-shaped exemption that
// covered the write as well, leaving requireAdmin inside the handler as the only
// refusal; now the read is public and the write is gated at the boundary too.
var publicAPIRoutes = []string{
	"POST /api/v1/auth/login",           // the call that creates a session
	"GET /api/v1/info",                  // product name and version, shown on the login screen
	"GET /api/v1/settings/theme",        // brand accent, applied before login (ADR-0113)
	"GET /api/v1/settings/logo",         // brand logo, shown before login (ADR-0148)
	"GET /api/v1/settings/registration", // whether the login screen offers self-registration (ADR-0126)
}

// publicAPIRouteSet indexes publicAPIRoutes for lookup while mounting. Built
// once; TestEveryPublicAPIRouteEntryIsRegistered checks that no entry is a typo
// that quietly matches nothing.
var publicAPIRouteSet = func() map[string]bool {
	set := make(map[string]bool, len(publicAPIRoutes))
	for _, pattern := range publicAPIRoutes {
		set[pattern] = true
	}
	return set
}()

// apiRouteAccess is the class of one entry of the /api/v1 route table: public if
// the allowlist names it, gated otherwise.
func apiRouteAccess(pattern string) accessClass {
	if publicAPIRouteSet[pattern] {
		return accessPublic
	}
	return accessAuthenticated
}

// accessPolicy holds the class of every mounted route and resolves a request to
// the class of the route that will serve it.
type accessPolicy struct {
	// served is the mux the request will actually be routed by. classify asks it
	// which pattern wins rather than matching the path itself, so precedence is
	// decided by net/http once — a second, hand-written copy of those rules is
	// exactly where an allowlist springs a leak.
	served *http.ServeMux
	class  map[string]accessClass
}

func newAccessPolicy(served *http.ServeMux) *accessPolicy {
	return &accessPolicy{served: served, class: map[string]accessClass{}}
}

// declare records the class of a mounted pattern.
func (p *accessPolicy) declare(pattern string, class accessClass) {
	p.class[pattern] = class
}

// classify returns the class of the route that will serve r.
//
// An unmatched pattern yields the zero value, accessAuthenticated. That covers
// both a route registered without a declaration and a request that matched
// nothing at all, and it is the fail-closed half of this file: not knowing means
// gated.
func (p *accessPolicy) classify(r *http.Request) accessClass {
	_, pattern := p.served.Handler(r)
	return p.class[pattern]
}

// declaredPatterns returns every pattern that was mounted, sorted. A scope's
// allowlist is checked against this rather than against the /api/v1 route table,
// because a scope may name a route mounted outside it — /metrics is one.
func (p *accessPolicy) declaredPatterns() []string {
	out := make([]string, 0, len(p.class))
	for pattern := range p.class {
		out = append(out, pattern)
	}
	sort.Strings(out)
	return out
}

// publicPatterns returns the declared public patterns, sorted. It exists for the
// inventory test, which holds this set against a written-out list.
func (p *accessPolicy) publicPatterns() []string {
	out := make([]string, 0, len(p.class))
	for pattern, class := range p.class {
		if class == accessPublic {
			out = append(out, pattern)
		}
	}
	sort.Strings(out)
	return out
}
