package api

import (
	"strings"
	"testing"
)

// The portal addresses a position the way the server does.
//
// A line is identified by its product and the shape of it
// (ADR-0384). The portal
// was still naming the product on every route it built — withdraw, correct,
// return — and using it as the key of the row being edited. For an order carrying
// two positions of one product that is ambiguous: the server refuses it, naming
// both positions, and the two rows would have shared one editing panel besides.
//
// The page mirrors the server's rule rather than approximating it, for the reason
// every other mirrored rule here exists: a page that offers what the server
// refuses teaches its reader that the product is broken.

// TestEveryLineRouteNamesThePosition.
func TestEveryLineRouteNamesThePosition(t *testing.T) {
	src := readWeb(t, "portal.js")
	if strings.Contains(src, "encodeURIComponent(line.itemId)") {
		t.Error("a line route still names the product, which an order carrying two " +
			"positions of it answers with a refusal")
	}
	if !strings.Contains(src, "encodeURIComponent(lineKey(line))") {
		t.Error("nothing builds a line route from the position's own key")
	}
}

// TestThePositionBeingEditedIsOneRow.
//
// The editing key is an identity like any other: two rows sharing one would open
// one another's panel and save one another's answers.
func TestThePositionBeingEditedIsOneRow(t *testing.T) {
	src := readWeb(t, "portal.js")
	for _, shape := range []string{"${o.id}|${l.itemId}", "${order.id}|${line.itemId}"} {
		if strings.Contains(src, shape) {
			t.Errorf("the row being edited is keyed by %q, so two positions of one "+
				"product are one row as far as the form is concerned", shape)
		}
	}
}

// TestTheKeyIsTheSameRuleTheServerUses.
//
// Written out rather than delegated, because there is nowhere to delegate it to —
// and written to be recognisable beside the Go it mirrors.
func TestTheKeyIsTheSameRuleTheServerUses(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function lineKey(", "\n}")
	if !strings.Contains(body, "line.itemId") {
		t.Error("the key is not built from the product")
	}
	if !strings.Contains(body, "}#${") {
		t.Error("the key does not join the product and the shape the way the server " +
			"does, so the two disagree about what one position is called")
	}
}

// TestThePositionListNamesTheProduct.
//
// It listed `iphone-18-huelle-clear`. The reader of this list is whoever ordered
// it, and the catalogue holds a name somebody wrote for exactly this purpose. The
// shape belongs there too, now that two positions can differ by nothing else.
func TestThePositionListNamesTheProduct(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function orderRowBodies(", "\n}")
	if strings.Contains(body, "' ', l.itemId, ' \\u2014 '") {
		t.Error("the position list prints the product id where the catalogue has a name")
	}
	if !strings.Contains(body, "lineLabel(") {
		t.Error("the position list does not name the product and the shape it was " +
			"ordered in")
	}
}
