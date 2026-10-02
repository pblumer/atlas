package api

import (
	"regexp"
	"strings"
	"testing"
)

// The Console's half of the catalogue opt-out
// (ADR-draft-the-catalogue-can-be-switched-off). The server stops serving the area;
// these hold the Console to not leading anybody into it — a menu entry for a
// switched-off shop is a link to a page that cannot load, offered to every employee.

// catalogueMenuEntries are the menu entries that open a view of the area: the two
// apps in the drawer and the two views that live in other apps' bars.
var catalogueMenuEntries = []string{
	`id: "portal"`,                         // the shop
	`id: "catalog"`,                        // catalogue maintenance
	`route: "#/operations/reconciliation"`, // where Atlas and the target systems disagree
	`route: "#/tasks/recertification"`,     // access review of held rights
}

// menuLine returns the one line of app.js that declares the entry carrying marker.
func menuLine(t *testing.T, src, marker string) string {
	t.Helper()
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, marker) {
			return trimmed
		}
	}
	t.Fatalf("app.js declares no menu entry with %s; the list these checks read has changed", marker)
	return ""
}

// TestTheCatalogueMenuEntriesSayTheyAreTheCatalogues: each entry of the area is
// marked, so the one filter that reads the mark can leave it out.
func TestTheCatalogueMenuEntriesSayTheyAreTheCatalogues(t *testing.T) {
	src := readWeb(t, "app.js")
	for _, marker := range catalogueMenuEntries {
		if line := menuLine(t, src, marker); !strings.Contains(line, `feature: "catalogue"`) {
			t.Errorf("the menu entry %s is not marked feature: \"catalogue\", so it stays in the menu with the catalogue off:\n\t%s", marker, line)
		}
	}
}

// TestTheMenusLeaveOutWhatTheServerSwitchedOff: both menus — the drawer and the bar
// of the open app — go through the one predicate that reads the mark, and that
// predicate is fed from what /api/v1/info says. A second filter written beside the
// role check would be the place the next menu forgets.
func TestTheMenusLeaveOutWhatTheServerSwitchedOff(t *testing.T) {
	src := readWeb(t, "app.js")
	for _, want := range []string{
		`APPS.filter((a) => offered(a))`,
		`(TOPNAV[appId] || []).filter((t) => offered(t))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js does not filter a menu with offered(): want %s", want)
		}
	}
	if !regexp.MustCompile(`FEATURES\.catalogue\s*=\s*!\(i && i\.catalogue === false\)`).MatchString(src) {
		t.Error("app.js does not take the catalogue's state from /api/v1/info; " +
			"an absent field must read as on, so a Console served by an older binary keeps its menu")
	}
	// A bookmark into a view of the area must say why there is nothing there, not
	// open a view whose every call answers 404.
	if !strings.Contains(src, "if (isCatalogueRoute(path) && !FEATURES.catalogue) return viewSwitchedOff();") {
		t.Error("the router does not stop at a view of the switched-off catalogue")
	}
}
