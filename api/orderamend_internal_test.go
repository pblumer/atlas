package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/order"
)

// What the page owes the two acts (ADR-0359).
//
// The server decides what may be withdrawn and what may be corrected. The page
// decides what to *offer*, and the two can drift apart in either direction: a
// button that always ends in 409, or an action the server would allow and nobody
// can reach. Both are held here.

// TestThePortalOffersNoWithdrawalTheServerWouldRefuse.
//
// The integral half is the one that matters. The basket will not let anybody
// deselect a part its whole always carries, and a page that offered it here would
// have the same rule holding in one screen and not the other — with the refusal
// arriving only after somebody pressed the button.
func TestThePortalOffersNoWithdrawalTheServerWouldRefuse(t *testing.T) {
	src := readWeb(t, "shop.js")
	start := strings.Index(src, "function withdrawable(")
	if start < 0 {
		t.Fatal("shop.js has no withdrawable(); if the per-position withdrawal moved, " +
			"this test now passes vacuously and says so instead")
	}
	body := src[start : start+strings.Index(src[start:], "\n}")]
	if !strings.Contains(body, "!line.integral") {
		t.Error("the page offers to withdraw a position its whole always carries. The " +
			"server refuses it, so the button exists only to produce a refusal")
	}
	// And exactly the statuses the server calls cancellable. Reading them out of the
	// Go constants means renaming one fails here rather than leaving a button that
	// never appears.
	for _, st := range []order.LineStatus{order.StatusPending, order.StatusBlocked} {
		if !strings.Contains(body, "'"+string(st)+"'") {
			t.Errorf("the server can still withdraw a %q line and the page does not "+
				"offer it", st)
		}
	}
	for _, st := range []order.LineStatus{order.StatusRunning, order.StatusDone} {
		if strings.Contains(body, "'"+string(st)+"'") {
			t.Errorf("the page offers to withdraw a %q line, which the server refuses", st)
		}
	}
}

// TestThePortalOffersCorrectionWhereTheServerAllowsIt.
//
// The bands are the status machine's, not a second rule: not yet attempted, or
// held. A page that offered it for a running line would produce the one refusal
// that cannot be explained to somebody standing in front of it.
func TestThePortalOffersCorrectionWhereTheServerAllowsIt(t *testing.T) {
	src := readWeb(t, "shop.js")
	start := strings.Index(src, "function correctable(")
	if start < 0 {
		t.Fatal("shop.js has no correctable(); if it moved, this test now checks " +
			"nothing and says so instead")
	}
	body := src[start : start+strings.Index(src[start:], "\n}")]
	if !strings.Contains(body, "line.configForm") {
		t.Error("the page offers to correct the details of a product that asks for none")
	}
	for _, st := range []order.LineStatus{
		order.StatusPending, order.StatusBlocked,
		order.StatusDone, order.StatusReturning, order.StatusReturnFailed,
	} {
		if !strings.Contains(body, "'"+string(st)+"'") {
			t.Errorf("the server allows a %q line's details to be corrected and the page "+
				"does not offer it", st)
		}
	}
	for _, st := range []order.LineStatus{
		order.StatusRunning, order.StatusRejected, order.StatusCancelled, order.StatusAbandoned,
	} {
		if strings.Contains(body, "'"+string(st)+"'") {
			t.Errorf("the page offers to correct a %q line, which the server refuses", st)
		}
	}
}

// TestACorrectionInFlightSurvivesTheOrdersFilter.
//
// The correction's form lives inside the order rows, and those rows are replaced
// on every keystroke in a column filter. Without the same two steps render() takes,
// typing "Muster" into the person filter would empty a cost centre somebody is in
// the middle of fixing, with nothing on screen saying anything was lost.
func TestACorrectionInFlightSurvivesTheOrdersFilter(t *testing.T) {
	src := readWeb(t, "shop.js")
	start := strings.Index(src, "function repaintOrderRows(")
	if start < 0 {
		t.Fatal("shop.js has no repaintOrderRows(); if it moved, this test now checks " +
			"nothing and says so instead")
	}
	body := src[start : start+strings.Index(src[start:], "\n}")]
	if !strings.Contains(body, "harvest()") {
		t.Error("repaintOrderRows does not capture what was typed before it replaces the " +
			"rows, so a filter keystroke empties a correction in progress")
	}
	if !strings.Contains(body, "mountConfigForms()") {
		t.Error("repaintOrderRows does not rebuild the forms it replaced, so a filter " +
			"keystroke leaves an open correction showing nothing but its loading note")
	}
}

// TestACorrectionAndABasketDoNotShareOneSetOfAnswers.
//
// The same product can be in the basket and in an order at once. Keying both by
// item id would put what somebody is correcting into what they are about to buy —
// silently, and in the direction that places an order with the wrong figures.
func TestACorrectionAndABasketDoNotShareOneSetOfAnswers(t *testing.T) {
	src := readWeb(t, "shop.js")
	if !strings.Contains(src, "function amendKey(") {
		t.Fatal("shop.js has no amendKey(); a correction and a basket would share one " +
			"bucket of answers keyed by item id")
	}
	// Inside the panel, not merely somewhere in the file: saveDetails names the same
	// key, so looking for it anywhere stays green after the panel has gone back to
	// keying by item — which is what breaking it showed.
	start := strings.Index(src, "function detailsPanel(")
	if start < 0 {
		t.Fatal("shop.js has no detailsPanel(); if it moved, this test now checks " +
			"nothing and says so instead")
	}
	panel := src[start : start+strings.Index(src[start:], "\n}")]
	// By the position and not by the product: two positions of one product are two
	// corrections (ADR-0384),
	// and a key naming the product would merge them as surely as it once merged a
	// correction with a basket.
	if !strings.Contains(panel, "amendKey(order.id, lineKey(line))") {
		t.Error("the correction panel does not key its answers by order and position, so " +
			"a correction and a basket for the same product would share one set")
	}
}
