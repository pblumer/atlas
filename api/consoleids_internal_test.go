package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goRegion is webRegion for a Go source file read from disk rather than from the
// embedded web tree. The guards below hold a comment as much as they hold code:
// a sentence that describes a delay the code no longer has is read by the next
// person as the contract.
func goRegion(t *testing.T, rel, from, to string) string {
	t.Helper()
	body, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	src := string(body)
	start := strings.Index(src, from)
	if start < 0 {
		t.Fatalf("%s no longer contains %q; this guard has lost its subject", rel, from)
	}
	end := strings.Index(src[start:], to)
	if end < 0 {
		t.Fatalf("%s: %q does not follow %q; this guard has lost its subject", rel, to, from)
	}
	return src[start : start+end]
}

// An id the console asks for and never shows.
//
// Catalogue sharing used to ask an administrator to type `usr_…` or a group id,
// with a hint saying to read it from Console → Organization. That screen showed
// names and nothing else: the id lived in a `data-id` attribute, which is markup
// for the click handler and invisible to a reader. The hint pointed at a place
// that did not have the answer.
//
// The pickers removed most of the need. This is the rest of it: an id is what every
// API call, every scope grant and every audit line is written in, so the screen that
// administers accounts and groups has to be able to tell you one.
func TestTheOrganizationScreenShowsTheIdsItIsAskedFor(t *testing.T) {
	src := readWeb(t, "app.js")

	for _, c := range []struct{ name, from, to, want string }{
		// To the card, not to `const eligible` — that one is declared *inside*
		// groupRow, before the markup, so the region would have ended above the row
		// this guard is about and passed on an empty subject.
		{"a group", "const groupRow = (g) =>", "const groupsCard = ", "${esc(g.id)}"},
		{"an account", "const userRow = (u) =>", "<td>${roleChips(", "${esc(u.id)}"},
	} {
		body := webRegion(t, src, c.from, c.to)
		// The row already carries the id as `data-id` for its own click handler, so a
		// search for the id would find that and prove nothing about what a reader can
		// see. Asked as a rendered <code>, which is what a reader copies.
		if !strings.Contains(body, "<code>"+c.want+"</code>") {
			t.Errorf("the row for %s does not show its id where a reader can copy it; "+
				"every scope grant and audit line names that id", c.name)
		}
	}
}

// TestTheGroupsCardNoLongerPromisesADelayThatIsNotThere.
//
// Adding somebody to a group pushes the change into their live sessions
// (handleAddGroupMember → setUserGroupMembership, ADR-0185), so it applies from
// their next request. Two places went on saying it takes effect at the next
// sign-in long after that stopped being true.
//
// A stale claim about a delay is worse than no claim: an administrator waits for
// it, and tells a colleague to sign out and back in for nothing.
func TestTheGroupsCardNoLongerPromisesADelayThatIsNotThere(t *testing.T) {
	if src := readWeb(t, "app.js"); strings.Contains(src, "takes effect on the member's next sign-in") {
		t.Error("the groups card still says a membership change waits for the next sign-in")
	}
	// The same sentence, in the comment on the field the sessions carry.
	body := goRegion(t, filepath.Join("httpapi", "httpapi.go"), "GroupIDs are the ids", "GroupIDs []string")
	if strings.Contains(body, "takes effect on the user's next login") {
		t.Error("Principal.GroupIDs still documents a delay that ADR-0185 removed")
	}
	if !strings.Contains(body, "ADR-0185") {
		t.Error("the field does not say where the live push comes from, so the next reader " +
			"has only the older ADR and will write the old sentence again")
	}
}
