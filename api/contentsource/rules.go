package contentsource

import (
	"fmt"
	"strings"
)

// The security rules of § 8, as pure functions of the facts an adapter returned.
//
// Atlas decides on a page's *protection*, never on whether its account could read
// it: the account reading a page proves only that the account may, and an account
// named on a restriction reads exactly the pages the shop must never show. So a
// rule here asks "is this protected", and every fact an adapter could not
// establish answers yes.
//
// Each function returns the reasons it refuses, in a sentence a product manager
// can act on, and nothing when it does not. An empty list is the only pass.

// RoleViewer is the one role the account may hold in a space (§ 4). Any other —
// editor, space administrator — reads protected passages or bypasses
// restrictions, which is exactly what the shop must not be built from.
const RoleViewer = "viewer"

// ScopeRead is the one token scope that passes (its ADR-0027): a token that can
// change no content.
const ScopeRead = "read"

// CheckStanding refuses an account that is anything but a read-only viewer
// (§ 4): a token not of scope read, any system role, any space role other than
// viewer. A wrong standing stops every fetch and quarantines every bound item of
// the source.
func CheckStanding(st Standing) []string {
	var out []string
	switch st.Scope {
	case ScopeRead:
	case "":
		out = append(out, "the source did not say which scope the account's token has")
	default:
		out = append(out, fmt.Sprintf("the account's token has scope %q; only a read token may supply the shop", st.Scope))
	}
	if !st.SystemRolesKnown {
		out = append(out, "the source did not say which system roles the account holds")
	} else if len(st.SystemRoles) > 0 {
		out = append(out, "the account holds the system role "+strings.Join(st.SystemRoles, ", ")+
			"; it must hold none, because an administrator reads every protected passage")
	}
	if !st.SpacesKnown {
		out = append(out, "the source did not say which space roles the account holds")
	}
	for _, g := range st.Spaces {
		if g.Role != RoleViewer {
			via := ""
			if g.Via != "" {
				via = " through " + g.Via
			}
			out = append(out, fmt.Sprintf("the account holds %q in space %s%s; it may hold nothing but %q",
				g.Role, g.Space, via, RoleViewer))
		}
	}
	return out
}

// ReleasedFor reports whether a space is released for the shop: the account
// holds viewer there, and nothing else (§ 4). A space it holds no grant in is not
// released, whatever the account happens to read through an inherited default.
func ReleasedFor(st Standing, space string) bool {
	if !st.SpacesKnown || space == "" {
		return false
	}
	found := false
	for _, g := range st.Spaces {
		if g.Space != space {
			continue
		}
		if g.Role != RoleViewer {
			return false
		}
		found = true
	}
	return found
}

// CheckPage refuses a page for any protection of § 8: a read restriction on it or
// on any ancestor, an open hold, a page not live, a space not released for the
// shop. ancestors is the whole parent chain; an ancestor that could not be read
// never reaches here, because reading the chain fails first.
//
// An edit restriction alone is no reason: reading stays open, and the facts do
// not carry it at all.
func CheckPage(p Page, ancestors []Page, st Standing) []string {
	var out []string
	switch p.ReadRestricted {
	case Present:
		out = append(out, "the page carries a read restriction")
	case Unknown:
		out = append(out, "the source did not say whether the page is read-restricted")
	}
	switch p.Held {
	case Present:
		out = append(out, "the page is held for a data protection review")
	case Unknown:
		out = append(out, "the source did not say whether the page is held for a data protection review")
	}
	switch p.Withdrawn {
	case Present:
		out = append(out, fmt.Sprintf("the page is not live (%s)", nonEmpty(p.Presence, "withdrawn")))
	case Unknown:
		out = append(out, "the source did not say whether the page is live")
	}
	switch {
	case p.Space == "":
		out = append(out, "the source did not say which space the page is in")
	case !ReleasedFor(st, p.Space):
		out = append(out, "space "+p.Space+" is not released for the shop: the account does not hold exactly viewer there")
	}
	for _, a := range ancestors {
		switch a.ReadRestricted {
		case Present:
			out = append(out, "the page's ancestor "+name(a)+" carries a read restriction, which the page inherits")
		case Unknown:
			out = append(out, "the source did not say whether the page's ancestor "+name(a)+" is read-restricted")
		}
	}
	return out
}

// name is how a refusal names a page: its key where it has one.
func name(p Page) string {
	return nonEmpty(p.Key, p.ID)
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
