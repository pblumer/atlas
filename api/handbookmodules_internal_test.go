package api

import (
	"regexp"
	"strings"
	"testing"
)

// The handbook's "The apps at a glance" names every app on a card of its own. Each
// card leads with the app's symbol — the one the drawer and the top bar show — so a
// reader who meets the symbol in the Console finds it again here. The handbook does
// not carry copies: a module script fills the cards from web/module-icons.js, the one
// drawing of the symbols, and this holds the page to doing so.
func TestTheHandbookShowsEveryAppsSymbol(t *testing.T) {
	src, err := webFS.ReadFile("web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	page := string(src)
	start := strings.Index(page, `<section id="willkommen">`)
	end := strings.Index(page, `<section id="nuggets">`)
	if start < 0 || end < start {
		t.Fatal("handbuch.html has no welcome section before the training nuggets")
	}
	welcome := page[start:end]
	for _, id := range appIDs(t) {
		if !strings.Contains(welcome, `data-module="`+id+`"`) {
			t.Errorf("the handbook's app overview has no symbol for %q (data-module=%q)", id, id)
		}
	}
	if !regexp.MustCompile(`<script type="module">[^<]*import\s*\{\s*moduleSymbol\s*\}\s*from\s*"\./module-icons\.js"`).MatchString(page) {
		t.Error("handbuch.html has no module script importing moduleSymbol from ./module-icons.js, so its symbols stay empty")
	}
}
