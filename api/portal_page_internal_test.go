package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The portal offers its interface in the visitor's own language, and it may only
// do that on one condition: every string it renders exists in every locale it
// offers (ADR-0313).
//
// ADR-0267 refuses to consult the browser precisely because guessing can land
// somebody on a half-translated screen. That is a statement about the state of a
// catalogue, not about browsers — so the condition is what makes the exception
// sound, and a test is what makes the condition a property rather than a promise.
// Without it this record is an intention.

var (
	portalLocaleBlock = regexp.MustCompile(`(?s)\n  (\w+): \{(.*?)\n  \},`)
	portalStringKey   = regexp.MustCompile(`'([^']+)':`)
)

// stringsCatalogue reads one page's message catalogue: locale to sorted keys. It
// is shared by the portal and the approval page because both offer the browser's
// language under the same condition, and a second copy of the parser is a second
// thing to keep pointed at the right place.
func stringsCatalogue(t *testing.T, file string) map[string][]string {
	t.Helper()
	src := readWeb(t, file)

	start := strings.Index(src, "const STRINGS = {")
	if start < 0 {
		t.Fatalf("%s declares no STRINGS catalogue — if it moved, this test now "+
			"passes vacuously and must be pointed at the new place", file)
	}
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatalf("%s: the STRINGS catalogue is not closed where this test expects", file)
	}

	out := map[string][]string{}
	for _, m := range portalLocaleBlock.FindAllStringSubmatch(src[start:start+end+3], -1) {
		var keys []string
		for _, k := range portalStringKey.FindAllStringSubmatch(m[2], -1) {
			keys = append(keys, k[1])
		}
		sort.Strings(keys)
		out[m[1]] = keys
	}
	return out
}

// assertCatalogueIsComplete holds the condition the language record names: every
// string exists in every locale the page offers.
func assertCatalogueIsComplete(t *testing.T, page string, got map[string][]string) {
	t.Helper()
	if len(got) < 2 {
		t.Fatalf("%s declares %d locale(s); with one there is nothing to guess "+
			"between and the browser need not be consulted at all", page, len(got))
	}

	// Every locale against every other: "complete" is not a property of one of
	// them, and comparing each to a chosen first would hide a key the first also
	// lacks.
	all := map[string]bool{}
	for _, keys := range got {
		for _, k := range keys {
			all[k] = true
		}
	}
	for locale, keys := range got {
		have := map[string]bool{}
		for _, k := range keys {
			have[k] = true
		}
		var missing []string
		for k := range all {
			if !have[k] {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: locale %q is missing %v.\n"+
				"The page reads the browser's language, and it may only do that while "+
				"every string exists in every locale it offers. Translate them, or drop "+
				"the locale.", page, locale, missing)
		}

		// A key written twice in one locale is the failure this whole file is least
		// able to see otherwise: JavaScript keeps the *last* property silently, so
		// the catalogue is complete, every locale agrees, nothing is missing — and
		// one of the two strings simply never renders. It happened to `appr.back`,
		// which carried both "back to Atlas" on the header link and "all approvals"
		// on an in-page one; the header link kept working and started reading "all
		// approvals", so the way back out of the page was there and unrecognisable.
		seen := map[string]bool{}
		var twice []string
		for _, k := range keys {
			if seen[k] {
				twice = append(twice, k)
			}
			seen[k] = true
		}
		if len(twice) > 0 {
			sort.Strings(twice)
			t.Errorf("%s: locale %q declares %v more than once.\n"+
				"The later one silently wins and the earlier string is unreachable — "+
				"nothing fails, the wrong words simply appear. Give the two uses two "+
				"keys.", page, locale, twice)
		}
	}
}

func TestPortalCatalogueIsComplete(t *testing.T) {
	assertCatalogueIsComplete(t, "portal.js", stringsCatalogue(t, "portal.js"))
}

// TestPortalRendersNoUntranslatedText: the boundary ADR-0267 draws holds here
// too. Interface words live in the catalogue, not in the markup, or a translated
// page would still have German headings in it.
func TestPortalRendersNoUntranslatedText(t *testing.T) {
	page := readWeb(t, "portal.html")
	body := page[strings.Index(page, "<body>"):]

	// The page's only text node is the one the script replaces.
	for _, word := range []string{"Katalog", "Bestellung", "Catalogue", "Order"} {
		if strings.Contains(body, ">"+word) {
			t.Errorf("portal.html renders %q directly; interface words belong in the "+
				"message catalogue, where every locale has them", word)
		}
	}
}

// TestPortalDeclaresTheBrandTokens: the page loads no app.css, so it declares the
// token set itself — the same one the public form declares, and for the same
// reason: theme.js writes the accent onto the root and the page has to have
// somewhere to write.
func TestPortalDeclaresTheBrandTokens(t *testing.T) {
	page := readWeb(t, "portal.html")
	for _, token := range []string{"--accent:", "--accent-hover:", "--accent-soft:", "--accent-ink:"} {
		if !strings.Contains(page, token) {
			t.Errorf("portal.html does not declare %s, so theme.js has nothing to override", token)
		}
	}
	if !strings.Contains(page, `type="module" src="/portal.js"`) {
		t.Error("portal.html does not load portal.js as a module, so its import of " +
			"theme.js's palette derivation cannot resolve and the page would show the " +
			"stock blue whatever brand was set")
	}
}

// TestPortalDerivesNoPaletteOfItsOwn: the accent's hover, soft and ink shades are
// computed in one place. The ink in particular decides whether a label stays
// readable on a brand colour, and a second implementation of that is a second
// place for it to be wrong (ADR-0263).
func TestPortalDerivesNoPaletteOfItsOwn(t *testing.T) {
	src := readWeb(t, "portal.js")
	if !strings.Contains(src, `from './theme.js'`) {
		t.Fatal("portal.js does not import theme.js — if it now derives the palette " +
			"itself, that derivation exists twice")
	}
	// Naming a derived token in a comment is how the rule is explained; assigning
	// one is how it gets broken. Only the assignment is the defect.
	for _, token := range []string{"--accent-hover", "--accent-soft", "--accent-ink"} {
		for _, form := range []string{
			`setProperty("` + token, `setProperty('` + token, token + `:`,
		} {
			if strings.Contains(src, form) {
				t.Errorf("portal.js assigns %s, which theme.js derives. Setting it here "+
					"means computing it here.", token)
			}
		}
	}
}

// TestPortalTypefacesMatchTheServer: the page paints with a stack, the server
// refuses a name that is not one, and a catalogue naming a face the page has no
// stack for would silently render in the default one.
func TestPortalTypefacesMatchTheServer(t *testing.T) {
	src := readWeb(t, "portal.js")
	for name := range catalog.Typefaces {
		if !strings.Contains(src, name+":") {
			t.Errorf("the server offers the typeface %q and portal.js has no stack for it", name)
		}
	}
}

// TestPortalMarkCascadeStopsAtTheOperator holds the one decision in the brand
// mark that is not obvious from reading the code: what the portal shows when
// nobody has set one.
//
// The console falls back to the built-in Atlas glyph, which is right there — it is
// the operator's own tool. The portal is a customer-facing page, and branding
// somebody's service catalogue with the name of the engine underneath it is a
// disclosure nobody asked for. So the cascade is catalogue, then operator, then
// nothing, and a future edit that "fixes the empty box" by importing the glyph
// fails here instead of shipping.
func TestPortalMarkCascadeStopsAtTheOperator(t *testing.T) {
	src := readWeb(t, "portal.js")

	if !strings.Contains(src, "/logo`") {
		t.Error("portal.js asks for no catalogue mark, so a catalogue's own logo is never shown")
	}
	if !strings.Contains(src, "/api/v1/settings/logo") {
		t.Error("portal.js does not fall back to the operator's mark")
	}
	// Matched as code and not as a mention: the comment above renderMark names
	// logo.js as the thing it is deliberately *not* importing, and a check that
	// could not tell the two apart would forbid explaining the decision.
	for _, forbidden := range []string{"BUILTIN_MARK", "'./logo.js'", `"./logo.js"`} {
		if strings.Contains(src, forbidden) {
			t.Errorf("portal.js reaches for %s: the portal is a customer-facing page and "+
				"the engine's own glyph does not belong on it", forbidden)
		}
	}
	// An uploaded SVG is scriptable. It is safe in an <img> and in nothing else, so
	// the page must never put those bytes into the document.
	for _, forbidden := range []string{"innerHTML", "insertAdjacentHTML"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("portal.js uses %s; a mark is rendered through an <img> and never "+
				"inlined, or an uploaded SVG's script runs in the page", forbidden)
		}
	}
}

// TestPageBuildersDropAnUnsetAttribute.
//
// `disabled: busy ? 'disabled' : null` is the natural way to write a conditional
// attribute, and setAttribute has no falsy handling: it renders disabled="null",
// which a browser reads as disabled. On the approval page that meant both buttons
// dead from the first paint, found by reading rather than by running — there is no
// browser in this test suite, so the guard is asserted in the source instead.
func TestPageBuildersDropAnUnsetAttribute(t *testing.T) {
	// One page now. The approval page had the same builder and the same defect; it
	// is gone, and its half of this guard with it — what it protected is protected
	// where the decision moved, by the end-to-end cases that press the buttons
	// (e2e/tasks-approval.spec.mjs).
	for _, page := range []string{"portal.js"} {
		src := readWeb(t, page)
		i := strings.Index(src, "function el(tag, attrs")
		if i < 0 {
			t.Errorf("%s has no el() builder where this test expects one", page)
			continue
		}
		body := src[i:]
		if end := strings.Index(body, "\n}\n"); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "v == null") {
			t.Errorf("%s's el() sets every attribute it is given, including a nullish one. "+
				"A conditional attribute then renders as the string \"null\", which for "+
				"disabled means permanently disabled.", page)
		}
	}
}

// TestPortalNamesEveryStatusTheServerCanProduce.
//
// The portal renders a line's status by looking up "status.<value>" and an
// order's by "order.<value>", so a status the server can produce and the page has
// no word for reaches a customer as a bare key. Adding one means touching two
// files in two languages, which is exactly the kind of thing that gets done once
// and remembered twice.
//
// The set is read out of the order package's source rather than written here, so
// this cannot go stale the way a hand-kept list would.
func TestPortalNamesEveryStatusTheServerCanProduce(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("order", "order.go"))
	if err != nil {
		t.Fatalf("read the order model: %v", err)
	}
	catalogue := stringsCatalogue(t, "portal.js")
	if len(catalogue) == 0 {
		t.Fatal("the portal declares no locales")
	}

	for _, tc := range []struct {
		kind   string
		prefix string
		re     *regexp.Regexp
	}{
		{"line status", "status.", regexp.MustCompile(`Status\w+ LineStatus = "(\w+)"`)},
		{"order status", "order.", regexp.MustCompile(`Order\w+ Status = "(\w+)"`)},
	} {
		found := tc.re.FindAllSubmatch(src, -1)
		if len(found) < 4 {
			t.Fatalf("only %d %s constants found; the pattern has gone stale and a green "+
				"result here would mean nothing", len(found), tc.kind)
		}
		for _, m := range found {
			want := tc.prefix + string(m[1])
			for locale, keys := range catalogue {
				has := false
				for _, k := range keys {
					if k == want {
						has = true
						break
					}
				}
				if !has {
					t.Errorf("locale %q has no word for the %s %q.\n"+
						"The portal renders it by key, so a customer would read %q.",
						locale, tc.kind, string(m[1]), want)
				}
			}
		}
	}
}

// TestThePortalMarksWhatIsAlreadyHeldFromTheInventory pins where the marking gets
// its facts.
//
// Deriving "already held" from the orders the page has just fetched would look
// identical and work for ninety days. Then retention deletes the order, the right
// is still held, and the catalogue quietly stops marking it — the exact failure
// the three-model split exists to prevent
// (ADR-0312). So the source of the marking is
// the inventory route, and this says so in the one form that cannot be satisfied
// by a comment: the fetch itself.
func TestThePortalMarksWhatIsAlreadyHeldFromTheInventory(t *testing.T) {
	src := readWeb(t, "portal.js")
	if !strings.Contains(src, `api('/api/v1/inventory')`) {
		t.Fatal("portal.js does not read /api/v1/inventory. If the catalogue's " +
			"already-held marking is derived from the orders on the page instead, it " +
			"stops marking the day retention deletes the order that granted the right")
	}
	// The catalogue's own answer decides whether a mark is even meaningful: an item
	// that may be held twice is orderable again, and marking it would train people
	// to ignore the mark.
	if !strings.Contains(src, "multipleAllowed") {
		t.Error("portal.js marks held items without consulting multipleAllowed, so a " +
			"second licence looks like a mistake")
	}
}

// A page nothing leads to is a page nobody uses.
//
// The portal was built, served under the embedded web root, and reachable only by
// somebody who already knew to type its URL. Nothing broke, no test failed, and
// the whole self-service surface was invisible to every person it was written for
// — the same class of omission as a value type that never reaches the fold: silent,
// and only found by looking.
//
// So the menu is checked rather than remembered. The entry's route is a *path* and
// not a hash, because the portal is a page of its own and not a view of the console
// app, and that distinction is what the second half of this test holds: a "#/portal"
// would render a nav link that navigates the console to a route it does not have.
func TestBothPortalSurfacesAreReachableFromTheMenu(t *testing.T) {
	src := readWeb(t, "app.js")

	start := strings.Index(src, "const APPS = [")
	if start < 0 {
		t.Fatal("app.js has no APPS list; this test now checks nothing and says so instead")
	}
	end := strings.Index(src[start:], "\n];")
	if end < 0 {
		t.Fatal("the APPS list is not terminated as expected; the pattern has gone stale")
	}
	apps := src[start : start+end]

	if !strings.Contains(apps, `route: "portal.html"`) {
		t.Error("no menu entry leads to the service portal. The page is served and " +
			"works; without an entry it is reachable only by somebody who already " +
			"knows the URL, which is every employee except the one who built it")
	}
	// Gated like the Tasks inbox: everybody signed in orders things, and a portal
	// only modellers can see is a portal for nobody.
	//
	// Matched field by field rather than as one literal line. The entry grows a
	// field whenever the drawer learns something new about it — `separate` was the
	// first — and pinning the whole line makes every such addition fail here with a
	// message about the role gate, which is not what changed.
	if !strings.Contains(apps, `{ id: "portal", name: "Portal", route: "portal.html", on: true, role: "user"`) {
		t.Error("the portal entry is not in the expected shape; check its role gate — " +
			"an ordinary employee must see it")
	}
	if strings.Contains(apps, `route: "#/portal`) {
		t.Error("the portal entry uses a hash route. The portal is a separate page, " +
			"not a view of this app: a hash would ask the console to route to " +
			"something it does not have, and the visitor would land on a blank screen")
	}

	// There is no approver's half any more. The page it named is gone — an approval
	// is read and decided in the inbox that already held it
	// (ADR-0394) — so what this
	// guard asked of the navigation is asked of it the other way round: no entry
	// may lead back to a page that is a forwarding stub.
	if strings.Contains(src, `route: "genehmigung.html"`) {
		t.Error("a menu entry still leads to the approvals page, which now only " +
			"forwards into the inbox: the menu would send somebody through a redirect " +
			"to reach a screen it could name directly")
	}
}

// A page of its own needs a way out of its own.
//
// Both portal surfaces are reached from the shell's menu and from a link in a
// notification mail, and neither is a view of the shell — so the browser's back
// button is the only exit, and somebody who arrived by following a link has no back
// to press. The header carries a way back on each.
//
// The link is checked in both pages because they were written separately and the
// second inherited the first's shape: a fix applied to one and forgotten on the
// other is exactly how the approvals page came to have no menu entry for months.
func TestBothPortalSurfacesLeadBackToAtlas(t *testing.T) {
	// The approval page is gone, so one page is left with this promise to keep. The
	// reason it was checked in both — a fix applied to one and forgotten on the
	// other — is why the pair is written out here rather than the survivor being
	// silently inlined.
	for _, page := range []struct{ src, key string }{
		{"portal.js", "portal.back"},
	} {
		src := readWeb(t, page.src)
		if !strings.Contains(src, `href: '/index.html'`) {
			t.Errorf("%s renders no way back to Atlas; a page of its own with no exit "+
				"strands whoever followed a link into it", page.src)
		}
		// The label travels through the message catalogue like every other word on
		// these pages, so a locale that offers the page offers the way out too.
		if !strings.Contains(src, `t('`+page.key+`')`) {
			t.Errorf("%s hard-codes the back link's label instead of reading %q from the "+
				"catalogue", page.src, page.key)
		}
	}
}

// TestThePortalSurfacesOpenInTheirOwnWindow: the drawer leaves the console
// standing when it sends somebody to a page that is not part of it.
//
// Both entries are paths rather than hash routes, so following one in the same tab
// unloads the console entirely — the visitor's place in whatever they were doing
// goes with it, and the only way back is the one small link on the page they
// landed on. Opening a window instead makes "back" the thing every browser already
// does, and it is the shape asked for once the separation was confirmed to be
// deliberate rather than an oversight.
func TestThePortalSurfacesOpenInTheirOwnWindow(t *testing.T) {
	src := readWeb(t, "app.js")
	apps := jsListIn(t, src, "const APPS = [", "\n];")

	// The portal alone, since approvals stopped being an application: an approval is
	// a kind of task, and its entry moved into the Tasks sub-navigation. The same
	// promise is made for it there — see
	// TestASeparatePageUnderTasksStillOpensInItsOwnWindow, which checks both the
	// entry's flag and the sub-navigation honouring it, because that renderer had no
	// notion of `separate` until the entry arrived.
	for _, id := range []string{"portal"} {
		var entry string
		for _, line := range strings.Split(apps, "\n") {
			if strings.Contains(line, `id: "`+id+`"`) {
				entry = line
			}
		}
		if entry == "" {
			t.Fatalf("the APPS list has no %q entry; this test now checks nothing", id)
		}
		if !strings.Contains(entry, "separate: true") {
			t.Errorf("the %q entry is not marked separate, so following it replaces the "+
				"console in the same tab. It is a page of its own — its own brand, its "+
				"own message catalogue — and unloading Atlas to reach it costs whoever "+
				"clicked whatever they had open", id)
		}
	}
	// The flag has to reach the markup, or it is a field nothing reads.
	if !strings.Contains(src, `a.separate ? ' target="_blank" rel="noopener"'`) {
		t.Error("paintApps does not turn `separate` into target=_blank. rel=noopener " +
			"belongs with it: a page opened this way can otherwise reach back through " +
			"window.opener")
	}
}

// TestTheCatalogueCanBeFilledFromTheMenu: the authoring surface exists and is
// reachable.
//
// The portal's API landed with no screen at all — catalogues, products, edges and
// releases were reachable only by hand-written JSON. That is a working API and an
// unusable product, and the gap was invisible because every test passed.
func TestTheCatalogueCanBeFilledFromTheMenu(t *testing.T) {
	src := readWeb(t, "app.js")
	start := strings.Index(src, "const APPS = [")
	if start < 0 {
		t.Fatal("app.js has no APPS list; this test now checks nothing and says so instead")
	}
	apps := src[start : start+strings.Index(src[start:], "\n];")]
	if !strings.Contains(apps, `{ id: "catalog", name: "Catalogue", route: "#/catalog", on: true, role: "productmanager" },`) {
		t.Error("no menu entry leads to the catalogue authoring screen, or its gate moved. " +
			"It is a hash route because it *is* a view of this app, unlike the two portal " +
			"pages, and it is gated at productmanager because ADR-0315 exists so that " +
			"filling a catalogue does not need instance administration")
	}
	// The screen is worth nothing if the role it is gated at cannot be handed out.
	if !strings.Contains(src, `{ id: "productmanager", name: "Product manager",`) {
		t.Error("the account dialog cannot grant productmanager, so the catalogue screen " +
			"is reachable by administrators alone — which is the arrangement ADR-0315 refused")
	}
}

// The catalogue screen offers sharing to the owner and to nobody else.
//
// The server refuses either way (mayShare), so this is about what the screen
// *offers*: a form that always ends in 403 is its own kind of lie, and a page that
// hid the rule would leave an editor wondering why their grant never took.
//
// It is checked against the source rather than rendered, because the rule is the
// thing worth pinning: enforcement off means everybody, admin passes, the owner
// passes, and an editor does not.
func TestTheCatalogueScreenOffersSharingOnlyToTheOwner(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")

	if !strings.Contains(src, "function mayShare(cat, me, enforced)") {
		t.Fatal("catalog-admin.js has no mayShare; this test now checks nothing and says so instead")
	}
	start := strings.Index(src, "function mayShare(cat, me, enforced)")
	body := src[start : start+strings.Index(src[start:], "\n}")]

	for _, want := range []struct{ frag, why string }{
		{"if (!enforced) return true;", "with authentication off there is nobody to be, so everybody may"},
		{`(me.roles || []).includes("admin")`, "an administrator passes, as everywhere"},
		{"cat.ownerId === me.id", "the owner is who may share"},
	} {
		if !strings.Contains(body, want.frag) {
			t.Errorf("mayShare does not say %q — %s", want.frag, want.why)
		}
	}
	// The absence that matters: an editor must not be offered the form. If this
	// ever starts consulting the member list, the screen has stopped mirroring the
	// server and started inventing a rule.
	if strings.Contains(body, "members") {
		t.Error("mayShare reads the member list, so it offers sharing to editors too — " +
			"which is the grant-amplification the server refuses")
	}
}

// TestTheCatalogueScreenCanRecordWhatAProductIsCalledOutside.
//
// The target references are the join a commissioning load attributes a right by
// (ADR-0333). An API that accepts them and a screen
// that cannot enter them is a working API and an unusable product — which is
// exactly how the catalogue itself shipped, with no screen at all.
func TestTheCatalogueScreenCanRecordWhatAProductIsCalledOutside(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")

	if !strings.Contains(src, `name="targets"`) {
		t.Fatal("the product form has no field for the target references, so the only way " +
			"to fill in the join a load depends on is a hand-written POST")
	}
	if !strings.Contains(src, "targets: parseTargets(") {
		t.Error("the form renders the field and does not send it; what is typed there is lost on save")
	}

	// One reference per line, split on the first colon. Both halves of that are
	// load-bearing and neither is obvious: a distinguished name is full of commas,
	// so a comma-separated list would cut references in half; and splitting on the
	// last colon would move part of an LDAP URL or a scoped SKU into the system
	// name.
	if !strings.Contains(src, `.split("\n")`) {
		t.Error("the references are not split by line. A distinguished name contains " +
			"commas, so anything comma-separated would break CN=X,OU=Y into two references")
	}
	if !strings.Contains(src, "line.indexOf(\":\")") {
		t.Error("the system is not split off at the first colon; splitting anywhere else " +
			"moves part of a reference that contains colons into the system name")
	}
	// A line with no colon must survive as a reference with no system, so publishing
	// can refuse it by name. Swallowing it would leave the author believing they
	// entered something.
	if !strings.Contains(src, `{ system: "", ref: line }`) {
		t.Error("a line with no system is dropped rather than kept and refused at publish, " +
			"so a mistyped reference disappears without anybody being told")
	}
}
