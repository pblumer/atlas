package api

import (
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// This file answers the question the layer below it does not: not *whether* a
// caller is somebody (access.go, ADR-0199), but *what that somebody may do*
// (ADR-draft-roles-per-endpoint-group).
//
// Until this, Atlas enforced exactly one role, from inside 52 handlers: 51 routes
// demanded admin and one demanded it conditionally, while the other 146 were
// reachable by anyone with a login — deploying a model among them, which is code
// execution. The sharing scopes of ADR-0071 and ADR-0205 are a different axis and
// do not cover it: they say *which object* a person may touch, never *which kind of
// operation* they may perform at all.
//
// The shape is the one access.go already proved, for the reason its comment gives:
// what a credential can reach has to be provable by reading one list. So the role
// is declared where the route is declared — in the /api/v1 route table for the API
// surface, at the mount site for everything else — and read here, once, at the
// boundary. Two properties make that hold:
//
//   - A route cannot be mounted without naming a role, because mount takes one.
//   - A route that names none reaches nobody: the empty string is not a role
//     anybody holds, so an unannotated route fails closed rather than open.
//
// TestEveryRouteDeclaresAKnownRole and TestAdminRoutesAreExactlyTheAllowlist then
// hold the result against the table and against a written-out list, so narrowing or
// widening a route is a diff a reviewer sees. The 51 in-handler checks are gone with
// them: a check the boundary makes unreachable is not a check, it is decoration that
// reads like one.

// roleAny is the role of a route every signed-in identity may reach: it demands a
// principal (access.go decided that) but no particular authority. It is spelled out
// rather than left blank, because blank is what an unannotated route has and that
// one must reach nobody.
//
// It is not a role anybody is granted — no account, token or grant ever carries it
// — so a user record naming it grants nothing.
const roleAny = "any"

// routeRoles is every role a route may name. The inventory test holds the table
// against it, so a typo ("moduler") is a failing build rather than an endpoint
// nobody can reach.
var routeRoles = []string{roleAny, RoleAdmin, RoleModeler, RoleOperator, RoleUser}

// grantableRoles is every role an account may be given. roleAny is deliberately
// absent: it describes a route, not a person.
var grantableRoles = []string{RoleAdmin, RoleModeler, RoleOperator, RoleUser}

// isRouteRole reports whether a string is one of the roles a route may name.
func isRouteRole(role string) bool {
	for _, r := range routeRoles {
		if r == role {
			return true
		}
	}
	return false
}

// isGrantableRole reports whether a string is a role an account may be given.
func isGrantableRole(role string) bool {
	for _, r := range grantableRoles {
		if r == role {
			return true
		}
	}
	return false
}

// unenforcedRoles returns the roles in a list that no route asks for.
//
// Granting one is not an error: ADR-0044 keeps the field free-form, and an
// installation may carry a role of its own for its own reporting. But a typo —
// "modeller" for "modeler" — looks exactly like that at write time and is silent
// afterwards, and the account it produces reaches nothing while its listing shows a
// role. So the write says so in its audit line, once, where somebody is already
// looking.
func unenforcedRoles(roles []string) []string {
	var out []string
	for _, role := range roles {
		if !isGrantableRole(role) {
			out = append(out, role)
		}
	}
	return out
}

// unenforcedAttr is unenforcedRoles as an audit attribute, or nothing at all when
// every role is one Atlas enforces — which is the ordinary case, and an attribute
// that is empty on every line is one nobody reads on the line where it is not.
func unenforcedAttr(roles []string) []slog.Attr {
	extra := unenforcedRoles(roles)
	if len(extra) == 0 {
		return nil
	}
	return []slog.Attr{slog.Any("unenforced_roles", extra)}
}

// mayReach reports whether this principal holds what a route requires.
//
// Admin passes everything: it is the role that administers the instance, and an
// instance where the administrator cannot reach an endpoint to fix it on the day
// its usual holder is unreachable is not administered. Everything else is the
// literal question — does this principal carry this role — because the roles are a
// list and not a rank order.
//
// An empty requirement is refused. That is the fail-closed half of this file: a
// route nobody annotated, or a pattern that was never declared, reaches no one.
func mayReach(p *httpapi.Principal, required string) bool {
	switch {
	case required == "":
		return false
	case p == nil:
		// Nobody to ask. The access class already decided whether this request may be
		// served without a principal, and this layer does not second-guess it.
		return true
	case required == roleAny:
		return true
	case p.HasRole(RoleAdmin):
		return true
	default:
		return p.HasRole(required)
	}
}

// refuseRole writes the 403 and the audit line for a request whose principal does
// not hold the role its route names.
//
// The message names the role, because the alternative — "forbidden" — sends the
// person to an administrator with nothing to ask for. It says nothing about what
// the route does or whether it exists, which the caller already knew: they reached
// a route they are signed in for.
func refuseRole(w http.ResponseWriter, r *http.Request, required string) {
	if required == "" {
		// A route that named no role. The inventory test exists so this cannot ship,
		// and the refusal says what happened rather than naming a role that is not
		// there — an operator reading "the  role is required" learns nothing.
		auditRefusal(r, logging.AuthDenied, "refused: route declares no role",
			slog.String("method", r.Method), slog.String("path", r.URL.Path))
		httpapi.Error(w, http.StatusForbidden,
			"no role is declared for "+r.Method+" "+r.URL.Path+", so it reaches nobody")
		return
	}
	auditRefusal(r, logging.AuthDenied, "refused: role required",
		slog.String("role", required),
		slog.String("method", r.Method), slog.String("path", r.URL.Path))
	httpapi.Error(w, http.StatusForbidden,
		"the "+required+" role is required for "+r.Method+" "+r.URL.Path)
}

// tokenRoles returns the roles an API token minted by this principal carries.
//
// Two rules, and the second is the one that matters. A token never carries admin,
// whoever mints it: a machine that administers accounts is not a case Atlas has,
// and a leaked credential that could would be a much worse leak. And an
// administrator's token carries the whole non-admin set, because that is exactly
// what an API token could reach the day before roles existed — narrowing it here
// would break every worker and CI job on upgrade, which is the outage this record
// set out not to cause. Minting is admin-only today, so that is the ordinary path;
// the other branch is what keeps the rule true if it ever stops being.
//
// A nil principal is the server running without auth, where there is nobody to be
// narrower than.
func tokenRoles(p *httpapi.Principal) []string {
	if p == nil || p.HasRole(RoleAdmin) {
		return legacyRoles()
	}
	out := []string{}
	for _, role := range grantableRoles {
		if role != RoleAdmin && p.HasRole(role) {
			out = append(out, role)
		}
	}
	return out
}
