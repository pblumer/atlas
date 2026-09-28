package api

import (
	"strings"
	"testing"
)

// The portal page when there is nobody to be (ADR-0377).
//
// The server half is held in api/catalog. What this holds is the half that makes
// the mode usable rather than merely reachable: the catalogue resolves, and every
// control that needs an account is absent with a reason instead of present and
// refused. A page that offers a button which fails at the end teaches somebody it
// is broken; one that says why teaches them what the mode is.

// TestOrderingIsNotOfferedWithoutAnIdentity.
//
// The server refuses an order with no orderer — one has nobody to notify and
// nobody to hold responsible — and that refusal stays. What changes is that the
// page stops walking somebody up to it.
func TestOrderingIsNotOfferedWithoutAnIdentity(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function renderActions(", "\n}")
	if !strings.Contains(body, "state.canOrder") {
		t.Error("the action row offers an order without asking whether there is " +
			"anybody to place it, so the last button reaches the server and comes back refused")
	}
	if !strings.Contains(body, "t('noid.title')") {
		t.Error("where the order button is not offered, nothing says why")
	}
}

// TestTheBasketIsNotFillableWhenItCannotBeSubmitted.
//
// The same rule one step earlier. A basket somebody can fill and never empty is
// the control-that-fails moved rather than removed — and the catalogue is still
// worth reading in this mode, so the control is shown disabled rather than hidden,
// exactly as an integral part is.
func TestTheBasketIsNotFillableWhenItCannotBeSubmitted(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function toggle(", "\n}")
	if !strings.Contains(body, "state.canOrder") {
		t.Error("the basket control is offered with nobody to order, so a basket can " +
			"be filled that has no way out")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("the control is hidden rather than shown disabled, so the column stops " +
			"reading as a decomposition")
	}
}

// TestWhetherAnOrderIsPossibleIsTheServersRuleMirrored.
//
// Not guessed from authEnabled: the server's rule is that an order needs a
// principal carrying a user id, and a page that inferred it from the mode would
// disagree the day the two stop meaning the same thing.
func TestWhetherAnOrderIsPossibleIsTheServersRuleMirrored(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function loadWhoIAm(", "\n}")
	if !strings.Contains(body, "state.canOrder = state.meID !== ''") {
		t.Error("whether an order can be placed is not read from the identity the " +
			"session carries")
	}
	// And ordering in somebody else's name only arises where an order is possible
	// at all, or the field is one whose every use ends in a refusal.
	if !strings.Contains(body, "state.mayOrderForOthers = state.canOrder &&") {
		t.Error("the recipient field is offered where no order can be placed")
	}
}

// TestAMissingPerAccountListDoesNotTakeThePageDown.
//
// The inventory and the favourites are facts about an account, and the routes say
// so rather than inventing an empty answer — which is right of them, and must not
// cost the catalogue. Before this, one 400 from a per-person list threw out of
// load() and the page showed an error instead of the shop.
func TestAMissingPerAccountListDoesNotTakeThePageDown(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "async function load(", "\n}")
	for _, route := range []string{"/api/v1/inventory", "/api/v1/shop/favourites"} {
		i := strings.Index(body, route)
		if i < 0 {
			t.Errorf("load() no longer reads %s", route)
			continue
		}
		// The try that guards it has to open before the call, not somewhere after.
		before := body[:i]
		if !strings.Contains(before[max(0, len(before)-220):], "try {") {
			t.Errorf("%s is read outside a try, so a per-account list that is not "+
				"there takes the catalogue with it", route)
		}
	}
}
