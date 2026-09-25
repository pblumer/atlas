package api

import (
	"strings"
	"testing"
)

// The basket asks which shape of a product was ordered.
//
// A variant is one orderable shape — a colour, a licence tier — and the catalogue
// has carried them from the start. The portal never asked for one, so
// `position.variantId` reached provisioning empty for every order ever placed.
//
// The server now refuses an order that leaves a variant open, which makes the
// question not optional for the page: a basket that did not ask would send an
// order the server rejects, and the reader would be told something is wrong
// without being told what to do about it.

// TestTheBasketAsksWhichVariant.
func TestTheBasketAsksWhichVariant(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function renderBasket(", "\n// --- What a product needs")
	if !strings.Contains(body, "variants") {
		t.Error("the basket never reads a product's variants, so an order leaves the " +
			"one question only the orderer can answer unanswered")
	}
	if !strings.Contains(body, "state.variants[") {
		t.Error("the basket asks but does not record the answer")
	}
}

// TestNothingPicksAVariantForTheOrderer.
//
// Variants are unordered on purpose: "higher" is meaningful for a licence tier
// and meaningless for black against silver, so there is no first one to fall back
// on. A pre-selected colour would be shipped to everybody who did not look.
func TestNothingPicksAVariantForTheOrderer(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function renderBasket(", "\n// --- What a product needs")
	for _, pick := range []string{".variants[0]", "variants[0].id"} {
		if strings.Contains(body, pick) {
			t.Errorf("the basket reaches for %q, which is the ordering the catalogue "+
				"deliberately does not have", pick)
		}
	}
}

// TestOrderingWaitsUntilEveryVariantIsChosen.
//
// Held on the page rather than discovered at the server: the answers are on
// screen, and a round trip that comes back 400 loses the reader's place to tell
// them something they could have been told without leaving it.
func TestOrderingWaitsUntilEveryVariantIsChosen(t *testing.T) {
	src := readWeb(t, "shop.js")
	actions := webRegion(t, src, "function renderActions(", "\nfunction currentView(")
	if !strings.Contains(actions, "variantsMissing(") {
		t.Error("the order button does not ask whether a variant is still open, so it " +
			"offers an order the server refuses")
	}
	body := webRegion(t, src, "async function order(", "\nfunction el(")
	if !strings.Contains(body, "variants") {
		t.Error("the order is placed without the variants that were chosen, so the " +
			"answer is collected on screen and dropped on the way out")
	}
}
