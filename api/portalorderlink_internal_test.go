package api

import (
	"strings"
	"testing"
)

// The one link into the process, and where its answer lands.
//
// The orders table offered three ways into a process: the order's own
// orchestration, one position's instance, and a per-position "where does this
// stand". The two per-position ones are gone — the people this page is for read the
// positions and their statuses, which answer the same question out of the order's
// own record and without an operations surface.
//
// What is left is the order's link, and it was reported as not working. It does
// work while the instance is there: pressing it searches for the fulfilment
// orchestration by orderId and opens it. What it did not survive is the two states
// where there is nothing to open — and in both of those it said so at the top of
// the page, above the table, where somebody who pressed a button in a row further
// down never sees it. A button whose answer is off-screen has not answered.

// TestTheAnswerAppearsWhereTheButtonWasPressed.
//
// Beside the row, not at the top of the page. state.error is painted above the
// table and is right for a load that failed, which is about the whole page; a
// lookup started from one row is about that row.
func TestTheAnswerAppearsWhereTheButtonWasPressed(t *testing.T) {
	src := readWeb(t, "portal.js")
	body := webRegion(t, src, "async function followProcess(", "\n}")
	if strings.Contains(body, "state.error =") {
		t.Error("the lookup reports into state.error, which is painted above the " +
			"table — somebody who pressed the button in a row further down sees the " +
			"page do nothing at all")
	}
	if !strings.Contains(body, "state.following") {
		t.Error("nothing keeps what the lookup found for the row it was started " +
			"from, so there is nothing a row could render")
	}
	rows := webRegion(t, src, "function orderRowBodies(", "\n}")
	if !strings.Contains(rows, "followNote(o)") {
		t.Error("the order row draws no answer beside its own button, so pressing it " +
			"changes nothing the reader can see")
	}
}

// TestAnInstanceThisServerNoLongerHoldsIsNotFollowed.
//
// The search falls back to the exported event log when this server's own index has
// nothing, and marks those rows archived: the instance was hard-deleted by history
// retention and exists only in the export. Following one leads to a replay view
// that answers "Could not load this instance's replay." — which is what a reader
// calls a link that does not work.
func TestAnInstanceThisServerNoLongerHoldsIsNotFollowed(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function followProcess(", "\n}")
	// The hit that is navigated to has to be drawn from rows the flag excluded, not
	// merely from a function that mentions it: naming the flag in a comment is what
	// this guard first accepted, and the link stayed broken.
	if !strings.Contains(body, "!i.archived") {
		t.Error("the rows this link chooses from are not filtered on the archive " +
			"flag, so an instance this server no longer holds is followed like a " +
			"live one — and the replay view fails there rather than here, where it " +
			"could be explained")
	}
	if !strings.Contains(body, "i.archived &&") {
		t.Error("nothing tells the archived absence apart from the ordinary one, so " +
			"an instance the export still records is reported as one that may never " +
			"have been started")
	}
}

// TestThePositionRowsOfferNoProcessLinks.
//
// Asked for: the two per-position links are not useful to whoever reads this page.
// Withdrawing a position and correcting its details are acts and stay; the two that
// went were both ways of looking at a process.
//
// This reverses "Every position carries its own way into the process working on it"
// and "Tell the orderer where their own position stands", whose guards this file
// replaces. The routes behind them are untouched — GET
// /api/v1/portal/orders/{id}/lines/{position}/progress and the instance search are
// API surface and have callers that are not this page.
func TestThePositionRowsOfferNoProcessLinks(t *testing.T) {
	src := readWeb(t, "portal.js")
	rows := webRegion(t, src, "function orderRowBodies(", "\n}")
	for _, gone := range []struct{ frag, what string }{
		{"proc.where", `the per-position "where does this stand" link`},
		{"proc.openLine", "the per-position link into the position's own instance"},
		{"askProgress(", "the per-position progress lookup"},
		{"progressNote(", "the per-position progress answer"},
	} {
		if strings.Contains(rows, gone.frag) {
			t.Errorf("the position row still draws %s", gone.what)
		}
	}
	// And the position lookup is gone with the button, rather than left as a
	// parameter nothing passes.
	body := webRegion(t, src, "async function followProcess(", "\n}")
	if strings.Contains(body, "positionId=") {
		t.Error("followProcess still looks a position up, which nothing asks it for")
	}
	// The order's own link is what remains, and it is still found by the order and
	// narrowed to the orchestration.
	if !strings.Contains(rows, "followProcess(o)") {
		t.Error("the order row no longer offers the one link this page kept")
	}
}

// TestTheStringsOfTheDeletedLinksAreGoneToo.
//
// A message catalogue that keeps words nothing renders is a catalogue somebody
// translates twice and reads as still in use.
func TestTheStringsOfTheDeletedLinksAreGoneToo(t *testing.T) {
	src := readWeb(t, "portal.js")
	for _, key := range []string{"'proc.where'", "'proc.openLine'", "'proc.standing'", "'proc.nothingRunning'"} {
		if strings.Contains(src, key) {
			t.Errorf("%s is still in the message catalogue, and nothing renders it", key)
		}
	}
}
