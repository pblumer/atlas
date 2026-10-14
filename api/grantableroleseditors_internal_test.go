package api

import (
	"strings"
	"testing"
)

// Every role an account may be given has to be tickable in both places that grant
// one, and the two are not interchangeable.
//
// There are exactly two editors. The user form grants a role to one account by
// hand. The sign-on mapping grants roles from the provider's claims — and where it
// is on, it *owns* them: `oidcMapping` replaces an account's roles at every login,
// so a role the mapping cannot name is taken away again at the person's next
// sign-in, however deliberately an administrator granted it in the form.
//
// That asymmetry is why this test exists rather than a review note. A role missing
// from the user form is invisible and obviously broken. A role missing from the
// mapping *works* — for one login — and then quietly stops, in exactly the
// installations the mapping exists for, which are the ones whose accounts come from
// a directory. `productmanager` shipped that way: the server accepted it in a rule
// (isGrantableRole did), the form offered it, and the mapping editor did not list
// it, so an Entra-backed installation could not hold the role at all.
//
// The lists are compared against grantableRoles rather than written out, so a sixth
// role is a failing build in both editors until they carry it.

// readWeb is defined beside the other web-asset guards; this file only reads.
func TestBothRoleEditorsOfferEveryGrantableRole(t *testing.T) {
	src := readWeb(t, "app.js")

	form := jsListIn(t, src, "const GRANTABLE_ROLES = [", "\n];")
	sso := jsListIn(t, src, "const SSO_ROLES = [", ";")

	for _, role := range grantableRoles {
		if !strings.Contains(form, `id: "`+role+`"`) {
			t.Errorf("the user form does not offer the role %q. It is grantable on the "+
				"server, so an administrator can hold it only by calling the API by "+
				"hand — which is not a role that exists as far as anybody using Atlas "+
				"is concerned", role)
		}
		// `user` is the floor every signed-in account holds, so the mapping
		// deliberately does not offer it as a grant. Every other one it must.
		if role == RoleUser {
			continue
		}
		if !strings.Contains(sso, `"`+role+`"`) {
			t.Errorf("the sign-on mapping editor cannot grant the role %q. Where the "+
				"mapping is on it decides an account's roles at every login, so this "+
				"role can be granted by hand and is then removed again at the next "+
				"sign-in. It does not look like a missing checkbox; it looks like the "+
				"role not working", role)
		}
	}

	// And the other direction: an editor offering something the server refuses is a
	// checkbox that fails on save, which is worse than one that is absent.
	for _, role := range jsQuoted(sso) {
		if !isGrantableRole(role) {
			t.Errorf("the sign-on mapping editor offers %q, which the server refuses "+
				"as a grant (oidcMapping.validate). Saving a rule with it ticked fails, "+
				"and the message names a role the operator did not type", role)
		}
	}
}

// jsListIn returns the source between a declaration's opening line and its
// terminator, failing loudly rather than checking nothing when either has moved.
func jsListIn(t *testing.T, src, open, close string) string {
	t.Helper()
	start := strings.Index(src, open)
	if start < 0 {
		t.Fatalf("app.js has no %q; this test now checks nothing and says so instead", open)
	}
	rest := src[start:]
	end := strings.Index(rest, close)
	if end < 0 {
		t.Fatalf("the list opened by %q is not terminated by %q; the pattern has gone stale", open, close)
	}
	return rest[:end]
}

// jsQuoted pulls the double-quoted strings out of a fragment of JavaScript. It is
// enough for a flat list of role names and deliberately no more: a parser here
// would be a second implementation of something no test should need.
func jsQuoted(fragment string) []string {
	var out []string
	for i := 0; ; {
		open := strings.Index(fragment[i:], `"`)
		if open < 0 {
			return out
		}
		open += i
		close := strings.Index(fragment[open+1:], `"`)
		if close < 0 {
			return out
		}
		close += open + 1
		out = append(out, fragment[open+1:close])
		i = close + 1
	}
}
