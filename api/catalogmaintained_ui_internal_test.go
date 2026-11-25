package api

import (
	"strings"
	"testing"
)

// The Console's half of a catalogue maintained in another system, and what the consent
// page and the AI-access page say a shop grant reaches
// (ADR-draft-a-shop-and-its-catalogues-from-another-system). The server refuses each
// write on its own; the page is what keeps a person from meeting the refusal as the
// first sign that the catalogue lives elsewhere.

func TestTheCatalogueEditorSaysWhereACatalogueIsMaintained(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(src, "const held = cat.maintainedIn || null;") ||
		!strings.Contains(src, `${held ? maintainedBanner(held, mayTheme(me, enforced)) : ""}`) {
		t.Error("the editor no longer draws the banner for a catalogue maintained elsewhere")
	}
	banner := webRegion(t, src, "export function maintainedBanner(", "\n}\n")
	for _, want := range []string{`t("catalog.maintained.title"`, `t("catalog.maintained.body"`, "safeLink(m.url)",
		`rel="noopener noreferrer"`, `${admin ?`, `data-act="take-back"`} {
		if !strings.Contains(banner, want) {
			t.Errorf("the banner lacks %s:\n%s", want, banner)
		}
	}
	link := webRegion(t, src, "function safeLink(", "\n}\n")
	if !strings.Contains(link, `url.protocol === "https:"`) {
		t.Error("the banner links to an address that is not https")
	}
}

func TestTheCatalogueEditorLocksWhatTheServerWouldRefuse(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	sections := webRegion(t, src, "const HELD_SECTIONS = [", "];")
	for _, key := range []string{`"products"`, `"content"`, `"relations"`, `"publish"`, `"settings"`, `"sharing"`, `"reporting"`, `"purge"`} {
		if !strings.Contains(sections, key) {
			t.Errorf("section %s is not locked while the catalogue is maintained elsewhere", key)
		}
	}
	for _, open := range []string{`"appearance"`, `"transfer"`} {
		if strings.Contains(sections, open) {
			t.Errorf("section %s is locked, but the server leaves it open", open)
		}
	}
	hold := webRegion(t, src, "export function holdEditing(", "\n}\n")
	if !strings.Contains(hold, "el.disabled = true") || !strings.Contains(hold, `"export-pick"`) {
		t.Errorf("the lock no longer disables the controls, or takes the export ticks with it:\n%s", hold)
	}
	back := webRegion(t, src, "function wireTakeBack(", "\n}\n")
	if !strings.Contains(back, "window.confirm(") || !strings.Contains(back, `api("DELETE", `+"`/api/v1/catalogs/${encodeURIComponent(cat.id)}/maintainer`") {
		t.Errorf("the take-back no longer asks first, or no longer calls the route:\n%s", back)
	}
}

func TestTheConsentPageAndTheGrantListSayWhatAShopGrantReaches(t *testing.T) {
	consent := readWeb(t, "oauth-consent.html")
	if !strings.Contains(consent, `resource.endsWith("/shop")`) || !strings.Contains(consent, "decide the approvals you hold") ||
		!strings.Contains(consent, "import and publish them") {
		t.Error("the consent page does not say what a grant for …/shop reaches")
	}
	if !strings.Contains(readWeb(t, "aiaccess.js"), "the person's shop and tasks only") {
		t.Error("the AI-access page does not label a grant for …/shop")
	}
}
