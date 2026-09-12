package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The approver's page carries the brand of the order it is deciding, and three
// properties of that are worth holding rather than reading: the brand follows the
// decision and not the visitor, the mark cascade stops at the operator, and the
// palette is derived in one place.

func TestApprovalCatalogueIsComplete(t *testing.T) {
	assertCatalogueIsComplete(t, "genehmigung.js", stringsCatalogue(t, "genehmigung.js"))
}

// TestApprovalRendersNoUntranslatedText: interface words live in the catalogue,
// not in the markup, or a translated page still has German headings in it.
func TestApprovalRendersNoUntranslatedText(t *testing.T) {
	page := readWeb(t, "genehmigung.html")
	body := page[strings.Index(page, "<body>"):]
	for _, word := range []string{"Genehmig", "Ablehn", "Approve", "Refuse"} {
		if strings.Contains(body, ">"+word) {
			t.Errorf("genehmigung.html renders %q directly; interface words belong in the "+
				"message catalogue, where every locale has them", word)
		}
	}
}

func TestApprovalDeclaresTheBrandTokens(t *testing.T) {
	page := readWeb(t, "genehmigung.html")
	for _, token := range []string{"--accent:", "--accent-hover:", "--accent-soft:", "--accent-ink:"} {
		if !strings.Contains(page, token) {
			t.Errorf("genehmigung.html does not declare %s, so the catalogue's brand has "+
				"nothing to override", token)
		}
	}
	if !strings.Contains(page, `type="module" src="/genehmigung.js"`) {
		t.Error("genehmigung.html does not load genehmigung.js as a module, so its import " +
			"of theme.js's palette derivation cannot resolve")
	}
}

// TestApprovalDerivesNoPaletteOfItsOwn: --accent-ink decides whether a button's
// label stays readable on a brand colour (ADR-0263), and the page that offers
// "approve" and "refuse" is the last place to get that wrong. Matched as an
// assignment rather than a mention, so the rule can still be explained in a
// comment.
func TestApprovalDerivesNoPaletteOfItsOwn(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	if !strings.Contains(src, `from './theme.js'`) {
		t.Fatal("genehmigung.js does not import theme.js — if it now derives the palette " +
			"itself, that derivation exists twice")
	}
	for _, derived := range []string{"--accent-hover", "--accent-soft", "--accent-ink"} {
		for _, form := range []string{"setProperty('" + derived, `setProperty("` + derived} {
			if strings.Contains(src, form) {
				t.Errorf("genehmigung.js assigns %s itself; theme.js derives it", derived)
			}
		}
	}
}

// TestApprovalTypefacesMatchTheServer: the server refuses a typeface it does not
// ship, so the page's mirror of that list has to be the same list — otherwise a
// catalogue names a face this page silently has no stack for.
func TestApprovalTypefacesMatchTheServer(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	for name := range catalog.Typefaces {
		if !strings.Contains(src, name+":") {
			t.Errorf("the server ships the %q typeface and genehmigung.js has no stack for it", name)
		}
	}
}

// TestApprovalMarkCascadeStopsAtTheOperator is the same decision the portal makes,
// and it matters more here: this page is shown to somebody deciding on a
// customer's behalf. The engine's own glyph on it would say the request belongs to
// Atlas.
func TestApprovalMarkCascadeStopsAtTheOperator(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	if !strings.Contains(src, "/logo`") {
		t.Error("genehmigung.js asks for no catalogue mark")
	}
	if !strings.Contains(src, "/api/v1/settings/logo") {
		t.Error("genehmigung.js does not fall back to the operator's mark")
	}
	for _, forbidden := range []string{"BUILTIN_MARK", "'./logo.js'", `"./logo.js"`} {
		if strings.Contains(src, forbidden) {
			t.Errorf("genehmigung.js reaches for %s; the engine's own glyph does not belong "+
				"on a page shown to somebody deciding for a customer", forbidden)
		}
	}
	for _, forbidden := range []string{"innerHTML", "insertAdjacentHTML"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("genehmigung.js uses %s; a mark is rendered through an <img> and never "+
				"inlined, or an uploaded SVG's script runs in the page", forbidden)
		}
	}
}

// TestApprovalCachesNoBrand is the difference from the portal, and it is a
// decision rather than an omission.
//
// The portal caches the brand it painted, because which catalogue a visitor
// belongs to is a fact about *them* and painting it before the first frame is
// worth a stale repaint. Which approval is open here is a fact about the moment: a
// cached paint would open the page in the colours of whoever was approved last,
// and an approver who decides for three customer groups would be told, in colour,
// that this request belongs to the wrong one.
func TestApprovalCachesNoBrand(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	for _, store := range []string{"setItem('portal.theme'", "setItem('appr.theme'", "paintFromCache"} {
		if strings.Contains(src, store) {
			t.Errorf("genehmigung.js caches a brand (%s). The brand belongs to the decision, "+
				"not to the page: a cached paint opens it in the last customer's colours.", store)
		}
	}
}

// TestApprovalDecidesThroughTheProcess: the page completes the task and does not
// write the order. What a decision *means* — start provisioning, or record a
// refusal and tell the orderer — is modelled in the approval process, and a page
// that wrote the order itself would be a second implementation of it that the
// process would then contradict.
func TestApprovalDecidesThroughTheProcess(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	if !strings.Contains(src, "/complete`") {
		t.Error("genehmigung.js does not complete the task; the process would never learn " +
			"the decision")
	}
	if strings.Contains(src, "/decision`") || strings.Contains(src, "/lines/") {
		t.Error("genehmigung.js writes the order line directly. The approval process reports " +
			"the decision; a page doing it too is a second answer to one question.")
	}
}
