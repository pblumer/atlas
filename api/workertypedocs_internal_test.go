package api

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Every Worker Type ships the answer to "what do I have to do before this works".
//
// The catalog in editor.js describes what a task *states*; api/web/workertypedocs.js
// describes what has to exist outside Atlas first — the app registration, the service
// account key, the shared spreadsheet — and links to the handbook card that says it at
// length. Three surfaces render it: the service task's properties panel, the Console's
// create form, and the worker dialog.
//
// The failure this guards is silent in every one of them. A Worker Type with no entry
// renders no block at all, which looks exactly like a type that needs no setup; and an
// anchor into a handbook card that no longer exists lands the reader at the top of an
// 800 KB page with no error. Neither is visible from the Go tests unless something
// holds the three files to each other, which is what this file does.

var (
	// A doc entry opens at two-space indent inside the exported map: `  jira: {`.
	setupDocEntryRe   = regexp.MustCompile(`(?m)^  ([a-zA-Z0-9]+): \{$`)
	setupDocFieldRe   = regexp.MustCompile(`(?m)^    anchor: "([a-z0-9-]+)", title: `)
	setupDocCheckedRe = regexp.MustCompile(`(?m)^    checked: "(\d{4})-(\d{2})",$`)
	// One step per line, opened by a template literal or a plain string, at the
	// indent the array's entries sit on. Counting the lines rather than parsing the
	// array keeps a step that itself contains a bracket from being miscounted.
	setupDocStepRe  = regexp.MustCompile("(?m)^      [`\"]")
	setupDocKindMap = regexp.MustCompile(`(?s)const WORKER_KIND_TO_DOC = \{(.*?)\};`)
	setupDocKindRe  = regexp.MustCompile(`([a-zA-Z0-9]+):\s*"([a-zA-Z0-9]+)"`)
	handbookIDRe    = regexp.MustCompile(`id="([a-zA-Z0-9-]+)"`)
)

// workerTypeDoc is one parsed entry: what it links to, and enough of its body to tell a
// written entry from a placeholder.
type workerTypeDoc struct {
	anchor  string
	checked string
	body    string
	steps   int
}

// parseWorkerTypeDocs reads api/web/workertypedocs.js into entries keyed by catalog id.
// It stays a regex rather than a JavaScript parse for the same reason the other Modeler
// guards do: the file is data written in a fixed shape, and a shape that changed is a
// failure this test should report rather than silently accommodate.
func parseWorkerTypeDocs(t *testing.T) map[string]workerTypeDoc {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/workertypedocs.js")
	if err != nil {
		t.Fatalf("read workertypedocs.js: %v", err)
	}
	src := string(body)
	locs := setupDocEntryRe.FindAllStringSubmatchIndex(src, -1)
	if len(locs) == 0 {
		t.Fatal("no Worker Type setup entries found; the shape of workertypedocs.js must have changed")
	}
	out := make(map[string]workerTypeDoc, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		id := src[loc[2]:loc[3]]
		entry := src[loc[1]:end]
		doc := workerTypeDoc{body: entry, steps: len(setupDocStepRe.FindAllString(entry, -1))}
		if m := setupDocFieldRe.FindStringSubmatch(entry); m != nil {
			doc.anchor = m[1]
		}
		if m := setupDocCheckedRe.FindStringSubmatch(entry); m != nil {
			doc.checked = m[1] + "-" + m[2]
		}
		out[id] = doc
	}
	return out
}

// parseWorkerKindAliases reads WORKER_KIND_TO_DOC — the few Worker Types whose Console
// kind and catalog id differ, because the catalog names the task and the record names
// the capability.
func parseWorkerKindAliases(t *testing.T) map[string]string {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/workertypedocs.js")
	if err != nil {
		t.Fatalf("read workertypedocs.js: %v", err)
	}
	m := setupDocKindMap.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatal("WORKER_KIND_TO_DOC not found in workertypedocs.js; the Console lookup must have been renamed")
	}
	out := map[string]string{}
	for _, kv := range setupDocKindRe.FindAllStringSubmatch(m[1], -1) {
		out[kv[1]] = kv[2]
	}
	return out
}

// TestEveryCatalogKindHasSetupDocs: a Worker Type an author can pick says how it is
// configured, or it ships a panel that quietly implies there is nothing to configure.
func TestEveryCatalogKindHasSetupDocs(t *testing.T) {
	docs := parseWorkerTypeDocs(t)
	var missing, thin []string
	for _, id := range modelerCatalogKindIDs(t) {
		doc, ok := docs[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		// A written entry says what it needs, links somewhere, and walks through it.
		// Two steps is the floor, not the target: below it the block says less than
		// the field hints it sits above.
		if doc.anchor == "" || !strings.Contains(doc.body, "needs:") || doc.steps < 2 {
			thin = append(thin, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("api/web/workertypedocs.js documents no setup for %d Worker Type(s): %s\n\n"+
			"The properties panel then shows nothing beside the type, which reads as "+
			"\"nothing to configure\" — including for a type that needs an account, a "+
			"permission and a secret. Add an entry keyed by the catalog id.",
			len(missing), strings.Join(missing, ", "))
	}
	if len(thin) > 0 {
		sort.Strings(thin)
		t.Errorf("%d Worker Type setup entries are incomplete: %s\n\n"+
			"Each needs an `anchor` into the handbook, a `needs` line, and at least two `steps`.",
			len(thin), strings.Join(thin, ", "))
	}
}

// TestEverySetupDocAnchorIsInTheHandbook keeps the deep links honest. A link into a card
// the handbook no longer has does not fail: the browser drops the reader at the top of
// the page, and the one thing they came for is the one thing they will not find.
func TestEverySetupDocAnchorIsInTheHandbook(t *testing.T) {
	handbook, err := fs.ReadFile(webFS, "web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range handbookIDRe.FindAllStringSubmatch(string(handbook), -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("no ids found in handbuch.html; this guard would pass vacuously")
	}
	var dangling []string
	for id, doc := range parseWorkerTypeDocs(t) {
		if doc.anchor != "" && !ids[doc.anchor] {
			dangling = append(dangling, id+" → #"+doc.anchor)
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		t.Errorf("%d Worker Type setup link(s) point at a handbook section that does not exist: %s\n\n"+
			"The browser lands at the top of the page instead of failing, so nothing else "+
			"reports this. Add the card to api/web/handbuch.html or point the anchor at the one that replaced it.",
			len(dangling), strings.Join(dangling, ", "))
	}
}

// TestNoStaleSetupDoc is the other direction: an entry for a Worker Type the catalog
// dropped is documentation nobody can reach, and it is the entry a reader of the file
// will copy the next time they add a type.
func TestNoStaleSetupDoc(t *testing.T) {
	known := map[string]bool{}
	for _, id := range modelerCatalogKindIDs(t) {
		known[id] = true
	}
	// A Worker Type an operator configures but no service task offers is documented
	// here too — temis is the business rule task's other decision binding — so a
	// managed kind is as good a reason for an entry as a catalog id.
	for _, k := range managedConnectorKinds {
		known[k.name] = true
	}
	var stale []string
	for id := range parseWorkerTypeDocs(t) {
		if !known[id] {
			stale = append(stale, id)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("api/web/workertypedocs.js documents %d Worker Type(s) the catalog no longer offers: %s",
			len(stale), strings.Join(stale, ", "))
	}
}

// TestEveryManagedWorkerKindHasSetupDocs covers the Console's side. The create form and
// the worker dialog look an entry up by the *record's* kind, which is the catalog id for
// all but a few — so a kind that resolves to nothing leaves an operator on the one screen
// where the setup actually happens with no setup instructions.
func TestEveryManagedWorkerKindHasSetupDocs(t *testing.T) {
	docs := parseWorkerTypeDocs(t)
	alias := parseWorkerKindAliases(t)
	var missing []string
	for _, k := range managedConnectorKinds {
		id := k.name
		if mapped, ok := alias[id]; ok {
			id = mapped
		}
		if _, ok := docs[id]; !ok {
			missing = append(missing, k.name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d managed Worker Type(s) have no setup entry the Console can show: %s\n\n"+
			"Add one keyed by the catalog id, and — where the Console's kind differs from that id — "+
			"an entry in WORKER_KIND_TO_DOC.", len(missing), strings.Join(missing, ", "))
	}
	for kind, id := range alias {
		if _, ok := docs[id]; !ok {
			t.Errorf("WORKER_KIND_TO_DOC maps %q onto %q, which has no setup entry; the alias is stale", kind, id)
		}
	}
}

// TestTheSetupBlockIsRenderedWhereTheChoiceIsMade: the entries are only worth writing if
// something puts them on screen. Three surfaces do, and an import that gets dropped in a
// refactor takes the block with it silently — the panel just renders one element less.
func TestTheSetupBlockIsRenderedWhereTheChoiceIsMade(t *testing.T) {
	for _, tc := range []struct{ file, call string }{
		// In the Modeler the block is wrapped in its collapsible Setup group, so the
		// call the panel makes is the group's rather than the block's.
		{"web/editor.js", "workerTypeInfoHTML(cur.id, kindNamesAWorker(cur))"},
		{"web/app.js", "workerKindDocHTML(kindSel.value)"},
		{"web/workerdialog.js", "workerKindDocHTML(c.kind)"},
		// The business rule task's temis binding is the fourth place a Worker Type is
		// chosen, and the one whose setup nothing else in the Modeler mentions.
		{"web/editor.js", `workerTypeInfoHTML("temis"`},
		// And the group is only a group if something gives it its collapse behaviour:
		// without this the section renders permanently open, which is the state the
		// group exists to end.
		{"web/editor.js", "wireWorkerTypeInfo(body, groupCtl)"},
	} {
		body, err := fs.ReadFile(webFS, tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(string(body), tc.call) {
			t.Errorf("%s no longer renders the Worker Type setup block (%s); the entries in "+
				"workertypedocs.js are then written for a surface that shows none of them", tc.file, tc.call)
		}
	}
}

// The one thing about these entries that no other test can see.
//
// They name menu paths in somebody else's product — "IAM & Admin → Service accounts",
// "Certificates & secrets", "Reset Token" — which is exactly what makes them worth
// writing and exactly what stops being true when that product is rearranged. Nothing in
// this repository observes Google's console. The text goes on looking authoritative, and
// the person it sends in a circle is the one least able to tell whether they or the
// instructions are wrong.
//
// So each entry states when it was last read against the real thing, the panel prints
// that date, and this test puts a limit on how long it may stand unread.
//
// **This test depends on the wall clock, which the testing conventions otherwise
// forbid, and that is the point.** A freshness check that can only fail when somebody
// edits the file would never fire — the file not being edited is the condition it exists
// to catch. The cost is real and belongs on the record: it will one day turn CI red on a
// change that has nothing to do with these entries. The fix is never to bump the date.
// It is to open the provider, walk the steps, correct what has moved, and then date what
// you actually read.
//
// They were all written in one sitting, so they will all come due in one sitting. That
// is an afternoon for twenty-four short entries — and re-dating them as they are checked
// rather than all at once is what spreads the round after.
const setupDocMaxAge = 12 * 30 * 24 * time.Hour // twelve months, in the units time understands

func TestSetupDocsAreRecentlyChecked(t *testing.T) {
	now := time.Now()
	var missing, stale, ahead []string
	for id, doc := range parseWorkerTypeDocs(t) {
		if doc.checked == "" {
			missing = append(missing, id)
			continue
		}
		checked, err := time.Parse("2006-01", doc.checked)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s (unreadable date %q)", id, doc.checked))
			continue
		}
		// A date in the future is a typo, and it is the one typo that makes this test
		// quieter rather than louder — so it fails on its own terms rather than waiting
		// out the year somebody added by accident.
		if checked.After(now) {
			ahead = append(ahead, fmt.Sprintf("%s (%s)", id, doc.checked))
			continue
		}
		if now.Sub(checked) > setupDocMaxAge {
			stale = append(stale, fmt.Sprintf("%s (last checked %s)", id, doc.checked))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d Worker Type setup entries carry no `checked` date: %s\n\n"+
			"Add `checked: \"YYYY-MM\"` naming the month you last walked these steps at the provider.",
			len(missing), strings.Join(missing, ", "))
	}
	if len(ahead) > 0 {
		sort.Strings(ahead)
		t.Errorf("%d Worker Type setup entries are dated in the future: %s\n\n"+
			"That silences this check for as long as the typo lasts.", len(ahead), strings.Join(ahead, ", "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d Worker Type setup entries have not been checked in over a year: %s\n\n"+
			"Open each provider, walk the steps as written, and correct what has moved — then date "+
			"what you read. Bumping the date without re-reading is worse than the stale date, because "+
			"it tells the next reader the steps were verified when they were not.",
			len(stale), strings.Join(stale, ", "))
	}
}

// The handbook's runbook cards are the long form of the same instructions, read by the
// same person on the same evening, and they name the same menus in the same products. So
// they carry the same date and the same limit: a card that has stood unread for a year
// is as misleading as a panel entry that has, and it is the more detailed of the two.
//
// Only the cards. The three per-product anchors inside the SQL card (runbook-mssql and
// its siblings) are deep-link targets within one card, not runbooks of their own, and
// dating them separately would claim three checks where one was made.
var (
	handbookCardRe    = regexp.MustCompile(`<div class="card" id="(runbook-[a-z]+)">`)
	handbookCheckedRe = regexp.MustCompile(`data-checked="(\d{4})-(\d{2})"`)
)

// handbookRunbookCards cuts the handbook into its runbook cards, keyed by anchor. A card
// runs to the start of the next one, which is enough to see what it carries without
// teaching the test to parse HTML.
func handbookRunbookCards(t *testing.T) map[string]string {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	src := string(body)
	locs := handbookCardRe.FindAllStringSubmatchIndex(src, -1)
	if len(locs) < 20 {
		t.Fatalf("found only %d runbook cards in handbuch.html; the markup must have changed", len(locs))
	}
	out := make(map[string]string, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[src[loc[2]:loc[3]]] = src[loc[0]:end]
	}
	return out
}

func TestHandbookRunbooksAreRecentlyChecked(t *testing.T) {
	now := time.Now()
	var missing, stale, ahead []string
	for id, card := range handbookRunbookCards(t) {
		m := handbookCheckedRe.FindStringSubmatch(card)
		if m == nil {
			missing = append(missing, id)
			continue
		}
		checked, err := time.Parse("2006-01", m[1]+"-"+m[2])
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s (unreadable date %q)", id, m[1]+"-"+m[2]))
			continue
		}
		if checked.After(now) {
			ahead = append(ahead, fmt.Sprintf("%s (%s-%s)", id, m[1], m[2]))
			continue
		}
		if now.Sub(checked) > setupDocMaxAge {
			stale = append(stale, fmt.Sprintf("%s (last checked %s-%s)", id, m[1], m[2]))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d handbook runbook card(s) carry no check date: %s\n\n"+
			"Close the card with `<p class=\"checked\" data-checked=\"YYYY-MM\">` naming the month "+
			"you last walked it, in both languages like every other line in that file.",
			len(missing), strings.Join(missing, ", "))
	}
	if len(ahead) > 0 {
		sort.Strings(ahead)
		t.Errorf("%d handbook runbook card(s) are dated in the future: %s", len(ahead), strings.Join(ahead, ", "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d handbook runbook card(s) have not been checked in over a year: %s\n\n"+
			"Walk each one at the provider and correct what has moved, then date what you read. "+
			"The panel entry for the same Worker Type is the short form of the same instructions — "+
			"check them together, they go stale together.",
			len(stale), strings.Join(stale, ", "))
	}
}

// A card and its panel entry are one instruction in two lengths. Dating them apart is
// how one gets re-read and the other quietly does not — so the anchor that already ties
// them together ties their dates together too.
func TestAPanelEntryAndItsHandbookCardAgreeOnTheDate(t *testing.T) {
	cards := handbookRunbookCards(t)
	var drifted []string
	for id, doc := range parseWorkerTypeDocs(t) {
		card, ok := cards[doc.anchor]
		if !ok || doc.checked == "" {
			continue // the anchor guard and the date guard report those
		}
		m := handbookCheckedRe.FindStringSubmatch(card)
		if m == nil {
			continue // TestHandbookRunbooksAreRecentlyChecked reports it
		}
		if got := m[1] + "-" + m[2]; got != doc.checked {
			drifted = append(drifted, fmt.Sprintf("%s: panel %s, handbook #%s %s", id, doc.checked, doc.anchor, got))
		}
	}
	if len(drifted) > 0 {
		sort.Strings(drifted)
		t.Errorf("%d Worker Type(s) whose panel entry and handbook card claim different check dates: %s\n\n"+
			"They are the same instructions at two lengths. Whichever was actually re-read, date both from it.",
			len(drifted), strings.Join(drifted, ", "))
	}
}
