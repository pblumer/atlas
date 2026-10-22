package api

import (
	"strings"
	"testing"
)

// The Console's half of the purge (ADR-0469): a card only an
// administrator is drawn, that asks the server what would go before it offers to
// delete anything, and whose delete button stays disabled until the id is typed
// again. The server refuses each of those on its own; the card is what keeps a
// person from meeting the refusal as the first sign of what they were about to do.

func TestThePurgeCardIsDrawnForAnAdministratorOnly(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(src, "${mayTheme(me, enforced) ? purgeCard(cat, items, langs) : \"\"}") {
		t.Error("the purge card is no longer drawn behind the administrator check")
	}
}

func TestThePurgeCardPreviewsBeforeItDeletes(t *testing.T) {
	wire := webRegion(t, readWeb(t, "catalog-admin.js"), "function wirePurge(", "\n}\n")
	preview := strings.Index(wire, "/deletion`")
	del := strings.Index(wire, `api("DELETE"`)
	if preview < 0 || del < 0 {
		t.Fatalf("the card no longer reads the preview or no longer deletes:\n%s", wire)
	}
	if !strings.Contains(wire, "?confirm=${enc(id)}") {
		t.Error("the delete does not repeat the id it deletes, so the server refuses every one")
	}
	// The delete answers a button the preview drew; the click that opens the card
	// only reads.
	if !strings.Contains(wire, `if (act === "purge-confirm")`) {
		t.Error("the delete is no longer tied to the confirmation button the preview draws")
	}
}

func TestThePurgeButtonStartsDisabledAndNeedsTheID(t *testing.T) {
	plan := webRegion(t, readWeb(t, "catalog-admin.js"), "export function purgePlanHTML(", "\n}\n")
	if !strings.Contains(plan, `data-act="purge-confirm"`) || !strings.Contains(plan, "disabled>Delete") {
		t.Error("the confirmation button is not drawn disabled")
	}
	if !strings.Contains(plan, "plan.enabled ?") {
		t.Error("the confirmation is drawn on a server that switched the purge off")
	}
	if !strings.Contains(plan, "plan.warnings") {
		t.Error("the warnings the server sends are not shown before the confirmation")
	}
	wire := webRegion(t, readWeb(t, "catalog-admin.js"), "function wirePurge(", "\n}\n")
	if !strings.Contains(wire, "b.disabled = e.target.value !== b.dataset.id") {
		t.Error("typing the id no longer decides whether the button may be pressed")
	}
}
