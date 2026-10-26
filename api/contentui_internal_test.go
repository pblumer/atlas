package api

import (
	"regexp"
	"strings"
	"testing"
)

// The screens of a product's content from the wiki
// (ADR-0491): the administrator's content
// sources in the Console, the product manager's panel on the catalogue screen, and
// "more in the wiki" in the shop. The backend decides every rule; these hold the
// pages to the few things they can still get wrong — being reachable, asking for
// words that exist, and never turning a wiki page's text into markup.

// TestTheContentSourcesScreenIsTheAdministratorsAndTheCatalogues: the menu entry is
// admin-only and goes with the catalogue switch, the router opens the module, and the
// routes it calls are admin-only on the server too — a menu gate with an open route
// behind it would be a screen gate and no more.
func TestTheContentSourcesScreenIsTheAdministratorsAndTheCatalogues(t *testing.T) {
	src := readWeb(t, "app.js")
	line := menuLine(t, src, `route: "#/console/content-sources"`)
	for _, want := range []string{`role: "admin"`, `feature: "catalogue"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the content sources menu entry lacks %s:\n\t%s", want, line)
		}
	}
	router := webRegion(t, src, `if (path === "#/console/content-sources") {`, "\n    }")
	if !strings.Contains(router, `import("./content-sources.js")`) || !strings.Contains(router, "viewContentSources(") {
		t.Error("the router does not open the content sources module")
	}
	if !strings.Contains(webRegion(t, src, "const isCatalogueRoute = (path) =>", ";\n"), `"#/console/content-sources"`) {
		t.Error("the content sources screen is not one of the catalogue's routes, so it opens with the catalogue switched off")
	}
	seen := 0
	for _, r := range accessTestServer(t).apiRoutes() {
		if !strings.HasPrefix(r.pattern, "/api/v1/content-sources") {
			continue
		}
		seen++
		if r.op.role != RoleAdmin {
			t.Errorf("%s %s needs %q; a content source names a vault secret and is the administrator's", r.method, r.pattern, r.op.role)
		}
	}
	if seen != 6 {
		t.Errorf("found %d content-source routes, want 6 — the screen calls list, create, read, change, remove and check", seen)
	}
	module := readWeb(t, "content-sources.js")
	for _, want := range []string{
		`api("GET", "/api/v1/content-sources")`,
		`api("POST", "/api/v1/content-sources", body)`,
		"api(\"PUT\", `/api/v1/content-sources/${enc(editing)}`, body)",
		"api(\"DELETE\", `/api/v1/content-sources/${enc(src.id)}`)",
		"api(\"POST\", `/api/v1/content-sources/${enc(src.id)}/check`)",
	} {
		if !strings.Contains(module, want) {
			t.Errorf("content-sources.js does not call %s", want)
		}
	}
}

// TestTheCheckShowsEveryProblem: the check's answer is shown whole — reachable, the
// scope, the spaces and every problem — because each problem stops every fetch.
func TestTheCheckShowsEveryProblem(t *testing.T) {
	card := webRegion(t, readWeb(t, "content-sources.js"), "export function checkCard(", "\n}")
	for _, want := range []string{"res.reachable", "res.scope", "res.spaces", "problems.map(", "res.error"} {
		if !strings.Contains(card, want) {
			t.Errorf("the check card does not show %s", want)
		}
	}
	// A refused delete names the products still bound: the 409's message is shown.
	remove := webRegion(t, readWeb(t, "content-sources.js"), "const remove = async (src) => {", "\n  };")
	if !strings.Contains(remove, "err.message") || !strings.Contains(remove, "err.status === 409") {
		t.Error("a refused delete does not show which products are still bound")
	}
}

// wikiModules are the files that draw a wiki page's words.
var wikiModules = []string{"catalog-content.js", "content-sources.js"}

// TestAWikiPagesWordsAreNeverMarkup is the record's "foreign content is never markup"
// held on the screen's side. The two modules build every node through el(), which
// only creates text nodes; none of them may reach for an HTML sink at all, so a later
// change cannot route a title or a description through one by accident.
func TestAWikiPagesWordsAreNeverMarkup(t *testing.T) {
	sinks := regexp.MustCompile(`\.(innerHTML|outerHTML)\b|insertAdjacentHTML|document\.write|createContextualFragment|DOMParser`)
	for _, name := range wikiModules {
		body := stripComments(readWeb(t, name))
		if m := sinks.FindString(body); m != "" {
			t.Errorf("%s uses %s; everything it draws may be a wiki page's text and must be a text node", name, m)
		}
	}
	el := webRegion(t, readWeb(t, "catalog-content.js"), "export function el(", "\n}")
	if !strings.Contains(el, "document.createTextNode(String(c))") {
		t.Error("el() no longer appends text as text nodes")
	}
	// The catalogue screen builds its form as markup, so a bound product's
	// description and keywords are left out of it and filled in as text afterwards.
	admin := readWeb(t, "catalog-admin.js")
	fill := webRegion(t, admin, "function fillBoundFields(", "\n}")
	if strings.Count(fill, ".textContent =") != 2 {
		t.Error("fillBoundFields does not set the bound description and keywords as text")
	}
	form := webRegion(t, admin, "function productForm(", "\n}")
	bound := webRegion(t, form, "${bound ? `<div class=\"bound-fields\">", "langFields(\"d\"")
	if strings.Contains(bound, "descriptions") {
		t.Error("the bound description is interpolated into the form's markup instead of filled in as text")
	}
	if !strings.Contains(webRegion(t, admin, `if (act === "edit") {`, "return;"), "fillBoundFields(editor, it)") {
		t.Error("the editor never fills in a bound product's description")
	}
}

// TestAWikiLinkOpensANewTabWithoutOpenerAndOnlyOverHTTPS: on the catalogue screen and
// in the shop, a page address becomes a link only when it is https, in a new tab, and
// without handing the wiki a reference to the page that opened it.
func TestAWikiLinkOpensANewTabWithoutOpenerAndOnlyOverHTTPS(t *testing.T) {
	console := readWeb(t, "catalog-content.js")
	link := webRegion(t, console, "function wikiLink(", "\n}")
	href := webRegion(t, console, "export function wikiHref(", "\n}")
	shop := webRegion(t, readWeb(t, "shop.js"), "function wikiLinkOf(", "\n}")
	for name, body := range map[string]string{"catalog-content.js": link, "shop.js": shop} {
		for _, want := range []string{`target`, `_blank`, `noopener`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: a wiki link lacks %s", name, want)
			}
		}
	}
	if !strings.Contains(href, "^https:") || !strings.Contains(shop, "^https:") {
		t.Error("a page address that is not https can become a link")
	}
	// The shop asks the server and nothing else: the binding arrives only while the
	// content may be shown, and the link is drawn only beside a description.
	if !strings.Contains(shop, "contentSource") || !strings.Contains(shop, "descriptionOf(item)") {
		t.Error("the shop's wiki link is not read from the release's binding beside the description")
	}
	if !strings.Contains(webRegion(t, readWeb(t, "shop.js"), "function infoPanel(", "\n}"), "wikiLinkOf(item)") {
		t.Error("the product panel never draws the wiki link")
	}
	if !strings.Contains(readWeb(t, "shop.js"), "'info.wiki':") {
		t.Error("the shop has no words for the wiki link")
	}
}

// TestEveryMessageTheWikiScreensAskForExists: every key the modules look up is in
// both locales of the Console's catalogue, and the German is Swiss — ss, never ß.
func TestEveryMessageTheWikiScreensAskForExists(t *testing.T) {
	keys := map[string]bool{}
	lookup := regexp.MustCompile(`\b(?:t|boundNote)\("((?:content|contentSources|common)\.[A-Za-z0-9.]+)"`)
	for _, name := range append([]string{"catalog-admin.js"}, wikiModules...) {
		for _, m := range lookup.FindAllStringSubmatch(readWeb(t, name), -1) {
			keys[m[1]] = true
		}
	}
	if len(keys) < 100 {
		t.Fatalf("found only %d message keys; the scan no longer matches how the modules look them up", len(keys))
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for key := range keys {
			if !strings.Contains(block, `"`+key+`":`) {
				t.Errorf("the wiki screens ask for %q, which locale %q does not have", key, locale)
			}
		}
	}
	if strings.Contains(localeCatalogue(t, "de"), "ß") {
		t.Error("the German catalogue writes ß; Swiss spelling is ss")
	}
}

// TestTheWikiPanelChangesNothingWithoutASource: the panel draws nothing where no
// content source is configured, and the catalogue's refresh only where a product is
// bound — an installation without a source keeps the screen it had (§ 3).
func TestTheWikiPanelChangesNothingWithoutASource(t *testing.T) {
	content := readWeb(t, "catalog-content.js")
	mount := webRegion(t, content, "export async function mountContentPanel(", "\n}")
	if !strings.Contains(mount, "if (!sources.length && !(status && status.binding)) { host.replaceChildren(); return; }") {
		t.Error("the panel draws something on an installation without a content source")
	}
	if !strings.Contains(webRegion(t, content, "export function mountCatalogRefresh(", "\n}"), "if (!host || !boundCount) return;") {
		t.Error("the catalogue's refresh is offered where nothing is bound")
	}
	admin := readWeb(t, "catalog-admin.js")
	if !strings.Contains(admin, "offered.filter((iid) => byID[iid] && byID[iid].contentSource).length") {
		t.Error("the catalogue's refresh is not counted from the bound products the catalogue offers")
	}
}

// TestABoundProductsSuppliedFieldsAreReadOnly: the editor draws a bound product's
// description, keywords and picture read-only, sends the stored values back as they
// are, and never uploads or removes a picture the source supplies.
func TestABoundProductsSuppliedFieldsAreReadOnly(t *testing.T) {
	admin := readWeb(t, "catalog-admin.js")
	body := webRegion(t, admin, "export function productBody(", "\n}")
	for _, want := range []string{"bound ? was.descriptions : descriptions", "bound ? was.keywords : list("} {
		if !strings.Contains(body, want) {
			t.Errorf("productBody does not keep a bound product's supplied field: want %s", want)
		}
	}
	form := webRegion(t, admin, "function productForm(", "\n}")
	if strings.Count(form, `${bound ? " disabled" : ""}`) != 2 {
		t.Error("the picture upload and removal are not both disabled for a bound product")
	}
	if !strings.Contains(admin, "if (!was.contentSource) await savePicture(") {
		t.Error("a bound product's save still sends a picture")
	}
}

// TestEveryWikiWriteStatesItsRevisionAndAnUnlinkItsReason: link, refresh and unlink
// carry the revision the page was drawn at, and an unlink goes through the dialog
// that asks for the reason the server requires and says it is audited.
func TestEveryWikiWriteStatesItsRevisionAndAnUnlinkItsReason(t *testing.T) {
	content := readWeb(t, "catalog-content.js")
	for _, want := range []string{
		"{ source: sourceSel.value, primary, pages, revision: item.revision || 0 }",
		"{ ...(lang ? { lang } : {}), reason, revision: item.revision || 0 }",
		"/content/refresh`, { revision: item.revision || 0 }",
		"/content/refresh`)",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("catalog-content.js does not send %s", want)
		}
	}
	unlink := webRegion(t, content, "const unlink = async (lang) => {", "\n  };")
	if !strings.Contains(unlink, "await askReason(") || !strings.Contains(unlink, "if (!reason) return;") {
		t.Error("an unlink is posted without asking for its reason")
	}
	ask := webRegion(t, content, "export function askReason(", "\n}")
	if !strings.Contains(ask, `t("content.unlink.audit")`) || !strings.Contains(ask, "reason = area.value.trim()") {
		t.Error("the unlink dialog does not say the unlink is audited, or accepts a blank reason")
	}
	// A refused link shows every problem the server named.
	if !strings.Contains(webRegion(t, content, "function failure(", "\n}"), "problemList(problems)") {
		t.Error("a refused write shows less than every problem")
	}
}

// TestTheCatalogueRefreshReportsEveryProduct: the catalogue's refresh answers one line
// per product, and each is shown with its outcome and its reasons.
func TestTheCatalogueRefreshReportsEveryProduct(t *testing.T) {
	report := webRegion(t, readWeb(t, "catalog-content.js"), "export function catalogRefreshReport(", "\n}")
	for _, want := range []string{"lines.map(", "statusLabel(l.status)", "l.message", "problemList(l.problems)"} {
		if !strings.Contains(report, want) {
			t.Errorf("the catalogue refresh report does not show %s", want)
		}
	}
}

// TestAPublishProblemReadsAsASentence: the publish gate's content problems are
// predicates about the product ("is quarantined — …"), and the refusal reads them
// after its name rather than after a dash.
func TestAPublishProblemReadsAsASentence(t *testing.T) {
	refusal := webRegion(t, readWeb(t, "catalog-admin.js"), "function publishRefusal(", "\n}")
	if !strings.Contains(refusal, `/^(is|has|holds|names|binds|carries)\b/`) {
		t.Error("a predicate problem is joined to its product with a dash")
	}
}
