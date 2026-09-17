package api

import (
	"strings"
	"testing"
)

// Every position carries its own way into the process working on it.
//
// The link hung on the order, and the order's process is the fulfilment
// orchestration: it says the order is running and nothing about which of four
// positions is waiting on an approval and which is being provisioned. "Where am I"
// is a question about a position, and it is the position that has a process of its
// own — the fulfilment model starts one per ready line.
//
// Found by the position rather than by the order, because the search answers with
// only the variables that matched the query: a search for the order returns every
// instance it started, each carrying `orderId` and nothing else, so there is
// nothing left to tell them apart by on the page. `positionId` is the one the
// fulfilment model passes for exactly this, and it names one instance.

// TestAPositionIsFollowedByItsOwnId.
func TestAPositionIsFollowedByItsOwnId(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function followProcess(", "\n}")
	if !strings.Contains(body, "positionId=") {
		t.Error("a position is looked up by something other than its own id, and the " +
			"search answers with only the variables that matched — so two positions of " +
			"one order cannot be told apart in the answer")
	}
	// The order's own process is still reachable, and it is a different question:
	// the orchestration, not one line's provisioning.
	if !strings.Contains(body, "atlas-auftrag-erfuellung") {
		t.Error("the order's own fulfilment process is no longer reachable")
	}
}

// TestThePositionRowOffersTheLink.
func TestThePositionRowOffersTheLink(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function orderRowBodies(", "\n}")
	if !strings.Contains(body, "followProcess(o, l)") {
		t.Error("the position rows offer no way into the process working on them, so " +
			"the reader is told the order is running and not where it stands")
	}
}

// TestAnInstanceThatCannotBeNamedIsNotGuessedAt.
//
// A position whose instance the search does not find is said, never approximated.
// The tempting fallback — search the product id instead — finds instances from
// every order that ever carried that product, and the answer carries only the
// variable that matched, so there is nothing left to check the order by. Opening
// one of those would be the same defect the position key exists to prevent, one
// screen further out.
func TestAnInstanceThatCannotBeNamedIsNotGuessedAt(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function followProcess(", "\n}")
	if strings.Contains(body, "itemId=") {
		t.Error("the lookup falls back to the product id, which matches instances from " +
			"other orders and cannot be narrowed by the answer")
	}
	if !strings.Contains(body, "proc.none") {
		t.Error("a position whose instance is not found follows a link to nowhere")
	}
}
