// Package httpapi holds the primitives every Atlas HTTP handler is written
// against: how a response is written, who the caller is, and where the request
// came from.
//
// It exists because a handler cannot move out of the `api` package while the
// helpers it depends on live there — a per-area service (ADR-0147) would either
// import `api` (a cycle) or grow its own copy of the response envelope, and a
// second copy of that envelope is how one endpoint quietly starts answering in a
// different shape. Keeping them here means the wire contract has exactly one
// definition no matter which package serves the route.
//
// Nothing here touches engine or design-time state; reaching that still goes
// through [github.com/pblumer/atlas/api/runloop], which is a separate boundary
// on purpose.
package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
)

// JSON writes v as the response body with the given status. Encoding errors are
// deliberately swallowed: the status line and headers are already on the wire by
// then, so there is no way left to report a failure to the client, and the
// alternative — panicking mid-response — turns a malformed body into a dropped
// connection.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes the API's error envelope: a JSON object carrying a single
// human-readable "error". Every non-2xx response in Atlas has this shape, so
// clients can parse one thing.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// ClientIP is the host part of a request's remote address, without the source
// port. Rate limiting buckets on it, so it must be stable across the many
// connections one caller opens. An address that carries no port is used as-is.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Principal is an authenticated caller: a person with a session, the service
// identity of a process the server started itself, or a peer authenticated by a
// deploy token (ADR-0044/0129). A request with no principal is unauthenticated —
// either auth is off, or nothing valid was presented.
type Principal struct {
	UserID   string
	Username string
	Roles    []string
	// GroupIDs are the ids of the groups the user belongs to, snapshotted at login
	// like Roles, so a scope's group grant resolves as a pure check against this
	// slice without a store read (ADR-0180). A membership change
	// takes effect on the user's next login.
	GroupIDs []string
	// Scope confines a principal authenticated by a machine credential to a named
	// set of operations, whatever its roles would otherwise permit. Empty for a
	// person: a session is not scoped, it is who you are. The boundary enforces it
	// in one place; handlers never read it.
	Scope string
}

// InGroup reports whether the principal belongs to the group with the given id.
func (p *Principal) InGroup(groupID string) bool {
	for _, g := range p.GroupIDs {
		if g == groupID {
			return true
		}
	}
	return false
}

// HasRole reports whether the principal carries a role. A principal with no
// roles carries none, so the authorization gates fail closed.
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// principalCtxKey is unexported so only this package can put a principal into a
// request context. Identity is then something the auth middleware establishes,
// not something any package holding a context can assert.
type principalCtxKey struct{}

// WithPrincipal returns ctx carrying p as the request's authenticated caller.
// The auth middleware is its only caller.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the request's authenticated principal, or nil when the
// request is unauthenticated (auth disabled, or no valid session). Handlers
// branch on nil; a zero-valued Principal would read as a real caller with an
// empty name, so a miss is never one.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalCtxKey{}).(*Principal)
	return p
}
