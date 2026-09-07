package api

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The Worker catalog card on Console › Workers is a hand-written list in app.js, and
// the card above the form that configures a worker. It says "Worker Types available to
// this Atlas instance" — so a Worker Type this Atlas serves and the card does not name
// is the card being wrong about the thing it exists to answer.
//
// It went wrong exactly the way a hand-written list beside the real implementation
// always does. Entra (ADR-0172) and the three SQL products (ADR-0173) were added as
// managed kinds, the "New worker" form offered them because its dropdown is built from
// the server, and the catalog above it did not — so an operator looking for SQL Server
// found it missing from the list of what this Atlas can do, and reasonably concluded
// they could not configure one. That is the same defect `KnownConnectorKinds` had for
// jira, and it is fixed the same way: a test holding the list to the switch.
//
// The list is read out of the source rather than exercised, which is what the moddle
// drift tests and TestTheConsoleNavPointsAtRoutesTheRouterServes already do over the
// same file.
var catalogEntryRe = regexp.MustCompile(`\n\s*id: "([a-z0-9-]+)", name: "`)

func consoleWorkerCatalogIDs(t *testing.T) map[string]bool {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	matches := catalogEntryRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no Worker catalog entries found in app.js; the pattern must have changed, and this guard would pass vacuously")
	}
	ids := make(map[string]bool, len(matches))
	for _, m := range matches {
		ids[m[1]] = true
	}
	return ids
}

// Every kind an operator can configure below must be described above. The managed
// kinds are exactly what the "New worker" dropdown offers, so this is the card and the
// form held to each other.
func TestEveryConfigurableWorkerTypeIsInTheConsoleCatalog(t *testing.T) {
	ids := consoleWorkerCatalogIDs(t)
	for _, k := range managedConnectorKinds {
		if !ids[k.name] {
			t.Errorf("the Worker catalog card does not describe %q, which the New worker form offers — "+
				"an operator reads the card to learn what this Atlas can do, and concludes it cannot do this", k.name)
		}
	}
}

// And the other direction: a catalog entry naming a kind Atlas does not serve promises
// a capability that is not there. It is allowed to describe a Worker Type that needs no
// Console record (REST authors its endpoint in the model), which is why this checks the
// placement catalog — every authorable kind — rather than only the managed ones.
func TestTheConsoleCatalogDescribesNothingAtlasDoesNotServe(t *testing.T) {
	for id := range consoleWorkerCatalogIDs(t) {
		if _, ok := authoredKindJobTypes[id]; !ok {
			t.Errorf("the Worker catalog card describes %q, which is not a Worker Type this Atlas serves "+
				"(no entry in authoredKindJobTypes) — the card would offer a capability nothing answers", id)
		}
	}
}

// The other hand-written list on the same page: the kind picker in the "New worker"
// form. It happened to be complete when the catalog above it was not, which is luck
// rather than a mechanism — and it is the list that decides what an operator can
// configure at all, so a kind missing here is a capability Atlas has and nobody can
// reach from the Console.
var kindOptionRe = regexp.MustCompile(`<option value="([a-z0-9-]+)">[^<]*</option>`)

func TestTheNewWorkerFormOffersEveryConfigurableKind(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	// The picker is the one select whose options are the Worker Types; find it by
	// the field it sits in rather than by position.
	src := string(body)
	i := strings.Index(src, `<select name="kind">`)
	if i < 0 {
		t.Fatal(`no <select name="kind"> in app.js; the New worker form must have changed`)
	}
	end := strings.Index(src[i:], "</select>")
	if end < 0 {
		t.Fatal("the kind select is not closed; the pattern must have changed")
	}
	offered := map[string]bool{}
	for _, m := range kindOptionRe.FindAllStringSubmatch(src[i:i+end], -1) {
		offered[m[1]] = true
	}
	if len(offered) == 0 {
		t.Fatal("the kind select offers nothing; this guard would pass vacuously")
	}
	for _, k := range managedConnectorKinds {
		if !offered[k.name] {
			t.Errorf("the New worker form does not offer %q, so a capability this Atlas has cannot be configured from the Console", k.name)
		}
	}
	for id := range offered {
		if _, ok := lookupManagedConnectorKind(id); !ok {
			t.Errorf("the New worker form offers %q, which the server refuses to create — the form would fail on submit", id)
		}
	}
}

// catalogDescRe matches one catalog entry's description text.
var catalogDescRe = regexp.MustCompile(`\n\s*desc:\s*"((?:[^"\\]|\\.)*)"`)

// The card renders its description with esc(), so markup in it is shown to the operator
// as markup. That is not a hypothetical: the AI Worker Type's card carried
// <b>…</b> around three phrases and read them out literally on the page —
// "The model an <b>agent-driven ad-hoc subprocess</b> asks…" — for as long as the card
// existed, because nothing looks at these strings and nobody re-reads a paragraph they
// wrote themselves.
//
// The escaping is right: these are operator-facing sentences, not a template. So the
// rule is that they are plain prose, and this is what holds them to it.
func TestNoWorkerTypeCardShowsItsMarkup(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	matches := catalogDescRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no Worker catalog descriptions found in app.js; the pattern must have changed, and this guard would pass vacuously")
	}
	// Three spellings, because the bracket can reach this file as any of them: plain;
	// the HTML entity; and the JavaScript unicode escape, which is what an edit made
	// through JSON produces and is exactly how the AI card's markup got in without
	// looking like markup to anyone reading the diff.
	brackets := []string{"<", "&lt;", `\u003c`, `\u003C`}
	for _, m := range matches {
		desc := m[1]
		for _, bracket := range brackets {
			if strings.Contains(desc, bracket) {
				t.Errorf("a Worker Type card's description contains %s, which esc() shows to the operator "+
					"as literal markup rather than rendering it:\n\t%s", bracket, desc)
				break
			}
		}
	}
}
