package api

import (
	"strings"
	"testing"
)

// A catalogue's patch is partial, so it cannot clear a field nobody mentioned.
// What it can still lose is a list: `items`, `edges` and `members` are replaced
// whole when sent, and every place on this page that sends one computes it out of
// `cat` — the snapshot the page was rendered from. Adding a product posts every
// product plus that one.
//
// So two maintainers adding a product a second apart, and the second write is the
// first one's disappearance: no error, no trace, and the person who lost the change
// is the one who did nothing wrong. The server now refuses a write whose stated
// revision has moved on; these tests are that the page states it.

func catalogAdmin(t *testing.T) string {
	t.Helper()
	return readWeb(t, "catalog-admin.js")
}

// TestNoListIsPatchedWithoutStatingTheRevision: the unconditional helper must
// never carry one of the three whole-list fields. Written as an absence on
// purpose — a new call site added later is caught by the same rule, where an
// enumeration of today's call sites would pass while the page grew a seventh.
func TestNoListIsPatchedWithoutStatingTheRevision(t *testing.T) {
	src := catalogAdmin(t)
	for _, field := range []string{"items", "edges", "members"} {
		if strings.Contains(src, "patch({ "+field) {
			t.Errorf("a %s list is sent through the unconditional patch helper, so a "+
				"concurrent change to this catalogue is overwritten without anybody "+
				"being told", field)
		}
	}
}

// TestTheSnapshotPatchStatesTheRevisionItRendered: and the helper that replaces it
// has to send the revision the page actually holds, not a fresh read — a
// precondition re-read at write time is no precondition at all.
func TestTheSnapshotPatchStatesTheRevisionItRendered(t *testing.T) {
	src := catalogAdmin(t)
	if !strings.Contains(src, "revision: cat.revision") {
		t.Error("no patch states the revision the page was rendered from, so the " +
			"server's precondition is never exercised from the Console")
	}
}

// TestACatalogueConflictIsReportedToAPerson: the server's refusal asks for the
// revision that was read, which is a sentence for an API caller. A person has a
// page that is out of date.
func TestACatalogueConflictIsReportedToAPerson(t *testing.T) {
	src := catalogAdmin(t)
	// Asserted on the catalogue's own wording rather than on the status number:
	// the page already handles a 409 from the *product* write, so a test for "409"
	// would pass without a line of this being written.
	if !strings.Contains(src, "changed this catalogue") {
		t.Error("the catalogue page does not report a conflict in its own words, so a " +
			"concurrent change is either unreported or reported by asking a person " +
			"for a revision number")
	}
}
