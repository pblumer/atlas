package api

import (
	"strings"
	"testing"
)

// The give-back button on "Meine Leistungen" hangs off the order line that granted
// the right. The orders the page reads include ones placed for somebody else and
// older lines for the same product that were cancelled or refused; keyed by product
// alone, the last order read — the oldest — won, and a right the viewer held from
// a newer order lost its button. These hold the choice to one place and to the
// two conditions that make it right.
func TestServicesViewPicksTheViewersHeldLine(t *testing.T) {
	src := readWeb(t, "shop.js")

	view := webRegion(t, src, "function renderServices(", "\n}")
	if !strings.Contains(view, "grantingLines(state.orders, state.meID)") {
		t.Error("the services view does not take its order lines from grantingLines, " +
			"so which order a held right is given back through is decided somewhere " +
			"nobody guards")
	}
	if strings.Contains(view, "lineOf.set(") {
		t.Error("the services view builds its own product-to-line map again; keyed by " +
			"product alone the oldest order wins")
	}

	pick := webRegion(t, src, "function grantingLines(", "\n}")
	if !strings.Contains(pick, "o.recipient !== meID") {
		t.Error("grantingLines does not skip orders placed for somebody else, so a " +
			"colleague's line can stand in for the viewer's own")
	}
	if !strings.Contains(pick, "'done', 'returning', 'returnFailed'") {
		t.Error("grantingLines does not require the line to be held, so a cancelled " +
			"or refused line can stand in for the one that granted the right")
	}
	if !strings.Contains(pick, "if (out.has(l.itemId)) continue;") {
		t.Error("grantingLines lets a later order overwrite an earlier one; the orders " +
			"arrive newest first, so the newest held line has to be kept")
	}
}
