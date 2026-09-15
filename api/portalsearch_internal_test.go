package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// Finding a service when nobody knows what it is called
// (ADR-draft-catalogue-search).
//
// The search runs in the browser over the release the page already has, so there
// is no route to test and nothing on the server decides what matches. What there
// is, is a seam: the portal reads fields out of JSON the server spells from Go
// struct tags, and neither side knows the other exists. A rename on either side
// turns the search into one that quietly matches less — no error, no failing
// request, just a product that stops being findable. So the seam is what these
// hold.

// jsonKeyOf returns the wire name the server spells a field under, taken from the
// value the server actually marshals rather than from a tag somebody read once.
func jsonKeyOf(t *testing.T, v any, find func(any) bool) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k, got := range out {
		if find(got) {
			return k
		}
	}
	t.Fatalf("no field in %s carries the marker value; the fixture has gone stale", raw)
	return ""
}

// TestThePortalSearchesTheFieldTheReleaseCarries.
//
// The keyword list exists for one reason — a person who does not know the product
// name — and it earns that only if the portal reads the field the server writes.
// Deriving the name from the marshalled item means renaming the Go field, the JSON
// tag or the portal's reader fails here instead of silently halving the search.
func TestThePortalSearchesTheFieldTheReleaseCarries(t *testing.T) {
	marker := "atlas-keyword-marker"
	key := jsonKeyOf(t, catalog.Item{Keywords: []string{marker}}, func(v any) bool {
		list, ok := v.([]any)
		return ok && len(list) == 1 && list[0] == marker
	})

	src := readWeb(t, "portal.js")
	if !strings.Contains(src, "item."+key) {
		t.Errorf("the release spells a product's search words %q and portal.js never "+
			"reads item.%s, so the search sees names only — which answers for exactly "+
			"the person who did not need to search", key, key)
	}
}

// TestThePortalSearchesEveryLanguageTheCatalogueCarries.
//
// A German catalogue's English name is in the release whether or not the page is
// being read in English, and somebody typing the word the catalogue itself holds
// must find the thing. Searching only the rendered locale would hide a product
// behind a language setting — the failure the language record calls landing
// somebody on a half-translated screen, with the half being the answer.
func TestThePortalSearchesEveryLanguageTheCatalogueCarries(t *testing.T) {
	src := readWeb(t, "portal.js")
	start := strings.Index(src, "function searchable(")
	if start < 0 {
		t.Fatal("portal.js has no searchable(); if the search moved, this test now " +
			"passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatal("searchable() is not closed where this test expects")
	}
	body := src[start : start+end]
	if !strings.Contains(body, "Object.values(item.texts") {
		t.Error("searchable() does not read every locale's text. A search over the " +
			"rendered language alone hides a product from somebody who typed the name " +
			"the catalogue itself carries in another one")
	}
	if strings.Contains(body, "textOf(") {
		t.Error("searchable() resolves the item's text for the current locale. That is " +
			"the display path, and using it here narrows the search to one language")
	}
}

// TestTypingDoesNotRebuildTheFieldBeingTypedInto.
//
// The page re-renders from state, and a full render replaces the search input —
// which moves the caret to the end of the word after every character, so "Notebook"
// arrives as "Nkoobeto". It is invisible to anything that sets the value
// programmatically and immediate to anybody who types, which is why it is pinned
// here rather than left to be noticed.
func TestTypingDoesNotRebuildTheFieldBeingTypedInto(t *testing.T) {
	src := readWeb(t, "portal.js")
	start := strings.Index(src, "type: 'search', id: 'find'")
	if start < 0 {
		t.Fatal("portal.js has no search field with id 'find'; if it moved, this test " +
			"now checks nothing and says so instead")
	}
	end := strings.Index(src[start:], "}),")
	handler := src[start : start+end]
	if !strings.Contains(handler, "repaintCatalogueBody()") {
		t.Errorf("the search field's input handler does not repaint the results alone: "+
			"%s\nCalling render() here replaces the field mid-word and the caret jumps "+
			"to the end after every character", handler)
	}
}
