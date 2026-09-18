package api

import (
	"strings"
	"testing"
)

// Offering a product a catalogue does not yet carry was a window.prompt.
//
// The prompt printed every product this catalogue does not offer as a line of
// text — id, a dash, the name — and asked for the id back. That fails the same
// two ways the application picker did before it became a dialog
// (api/web/pickmodal.js):
//
//   - **Silently.** A browser truncates a prompt body past a handful of lines and
//     marks the cut with an ellipsis. A server with a few dozen products shows the
//     ones that sort first, and the product somebody had just created — which is
//     the one they are trying to offer — is cut off with nothing to say so.
//   - **Openly.** The products are printed as text, so nothing in that list can be
//     clicked. Picking means reading an id off a wall of lines and typing it
//     exactly; a typo is answered with "No product with that id" and the whole
//     list has to be re-read. That is what was reported: the entries are not
//     selectable.
//
// So the picker is the console's dialog, whose list is a <select>: no length
// limit, nothing to type, and every product reachable with the pointer.

// offerExistingHandler returns the body of the "add-existing" branch, so these
// tests read the picker and not whatever else the file says about products.
func offerExistingHandler(t *testing.T) string {
	t.Helper()
	src := readWeb(t, "catalog-admin.js")
	start := strings.Index(src, `if (act === "add-existing")`)
	if start < 0 {
		t.Fatal("catalog-admin.js has no add-existing branch; if offering an existing " +
			"product moved, this test now passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n    }\n")
	if end < 0 {
		t.Fatal("the add-existing branch has no recognisable end; the extraction above is stale")
	}
	return src[start : start+end]
}

// TestOfferingAnExistingProductIsPickedAndNotTyped is the defect itself: the list
// has to be a control somebody can operate, not prompt text.
func TestOfferingAnExistingProductIsPickedAndNotTyped(t *testing.T) {
	h := offerExistingHandler(t)

	if strings.Contains(h, "window.prompt(") {
		t.Error("offering an existing product still asks a window.prompt, so the " +
			"products are printed as text: nothing in the list can be clicked, the " +
			"id has to be typed, and a list past a handful of lines is truncated")
	}
	if !strings.Contains(h, "openPickModal(") {
		t.Error("offering an existing product does not open the console's pick dialog, " +
			"so the products are not offered as a list that can be chosen from")
	}

	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(src, `import { openPickModal } from "./pickmodal.js"`) {
		t.Error("catalog-admin.js does not import openPickModal, so the dialog it " +
			"opens is not the one the console ships and tests")
	}
	// The whole screen, not only this branch: a second prompt-shaped picker would
	// reintroduce exactly what this replaced. The call and not the word, so the
	// comment saying what the dialog is there instead of stays sayable.
	if strings.Contains(src, "window.prompt(") {
		t.Error("the catalogue screen asks a window.prompt somewhere, which is the " +
			"pattern that printed a list nobody could click")
	}
}

// TestTheOfferedProductStillArrivesWithItsRevision: the dialog changed, the write
// did not. Adding a product posts the whole item list, so it must carry the
// revision the page was rendered at or a second maintainer's product disappears
// with no error (ADR-0376) — patchList is what does that.
func TestTheOfferedProductStillArrivesWithItsRevision(t *testing.T) {
	h := offerExistingHandler(t)
	if !strings.Contains(h, "patchList(") {
		t.Error("offering a product writes without patchList, so the item list is " +
			"replaced with no precondition and a concurrent addition is lost silently")
	}
	if !strings.Contains(h, "patchFailed(") {
		t.Error("offering a product does not report a refusal, so a conflict on the " +
			"item list is either invisible or a revision number nobody can act on")
	}
}

// TestTheButtonAppearsWheneverThereIsSomethingToOffer.
//
// The button was drawn when the server holds more products than this catalogue
// offers. That count is not the question: an id may be offered and no longer
// defined — the product row renders "offered but not defined" for exactly that —
// and one such entry makes the counts equal while products nobody has offered are
// sitting there. The button then does not appear at all, which reads as "there is
// nothing to add" rather than as an arithmetic accident.
func TestTheButtonAppearsWheneverThereIsSomethingToOffer(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if strings.Contains(src, "items.length > offered.length") {
		t.Error("the offer button is drawn from a count of products against a count " +
			"of offered ids, so an offered id with no product hides the button while " +
			"there is still something to offer")
	}
}
