package api

import (
	"os"
	"strings"
	"testing"
)

// The figure, from the catalogue to the person deciding
// (ADR-0361).
//
// What it is frozen for is proved where freezing happens, in api/catalog and
// api/order. What is left is the two surfaces that show it, and one decision that
// is only visible as an absence.

// TestTheApproverSeesTheFigureTheyAreDecidingOn.
//
// Half the question the story asks an approver is the cost, and the page showed
// none. It is on the detail *and* on the row: a list of forty is scanned, not
// opened one at a time, and an approver who has to open each to see a figure is
// being asked to do the thing the list exists to prevent.
func TestTheApproverSeesTheFigureTheyAreDecidingOn(t *testing.T) {
	// Read where the decision now is. The page this guarded is gone and the approval
	// is read and decided in the inbox
	// (ADR-0394); what it
	// protected did not change, and neither did the reason.
	src := readWeb(t, "app.js")
	panel := webRegion(t, src, "function approvalBlock(", "\n  }")
	if !strings.Contains(panel, "a.price") {
		t.Error("the approval block does not show the price, so an approver decides " +
			"without the figure")
	}
	rows := webRegion(t, src, "const apprLine = ap", ";")
	if !strings.Contains(rows, "ap.price") {
		t.Error("the approval rows do not show the price, so forty approvals have to " +
			"be opened one at a time to compare what they cost")
	}
}

// TestTheApprovalTakesTheFigureFromTheOrderAndNotTheCatalogue.
//
// The two sources answer identically until somebody edits a price — which is
// exactly when the difference matters and nobody is looking. There is no cheap way
// to place an order, raise an approval and edit a catalogue in one test, so the
// seam is pinned where it is decided: the one line that chooses the source.
func TestTheApprovalTakesTheFigureFromTheOrderAndNotTheCatalogue(t *testing.T) {
	raw, err := os.ReadFile("approvals.go")
	if err != nil {
		t.Fatalf("read approvals.go: %v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "Price: line.Price") {
		t.Error("the approval response does not take the price from the order line. " +
			"Taking it from the release would show an approver a figure the order was " +
			"not placed at, the first time somebody edits the catalogue")
	}
	// And not from the release beside it, where the texts come from.
	if strings.Contains(src, "a.Price = it.Price") {
		t.Error("the approval response takes the price from the release, which changes " +
			"when the catalogue is republished")
	}
}

// TestThePortalSaysWhenTheCatalogueNamesNoCost.
//
// An empty field where a figure belongs is unreadable: "we do not say" and
// "somebody left it blank" look identical. Publishing refuses the second, and this
// is the first said out loud.
func TestThePortalSaysWhenTheCatalogueNamesNoCost(t *testing.T) {
	src := readWeb(t, "portal.js")
	if !strings.Contains(src, "t('price.none')") {
		t.Error("the portal renders a missing price as nothing, which reads as a field " +
			"somebody forgot rather than a catalogue that says nothing about cost")
	}
}

// TestNoPageTurnsAPriceIntoANumber.
//
// The decision this measure rests on, and the only one visible as an absence:
// displayed, never computed. A total would need a currency, a rate and a date —
// an installation's finance rules, not the catalogue's — and the first page to
// parse a price would have invented all three without deciding anything. If a
// total is ever wanted it arrives with a money model, not by accident.
func TestNoPageTurnsAPriceIntoANumber(t *testing.T) {
	for _, page := range []string{"portal.js", "app.js", "catalog-admin.js"} {
		src := readWeb(t, page)
		for _, arithmetic := range []string{
			"parseFloat(", "parseInt(", "Number(",
		} {
			for i := 0; ; {
				j := strings.Index(src[i:], arithmetic)
				if j < 0 {
					break
				}
				at := i + j
				window := src[maxZero(at-100) : at+100]
				if strings.Contains(window, "price") {
					t.Errorf("%s does arithmetic near a price, which is the money model "+
						"the catalogue deliberately does not have:\n%s", page, window)
				}
				i = at + len(arithmetic)
			}
		}
	}
}

func maxZero(i int) int {
	if i < 0 {
		return 0
	}
	return i
}
