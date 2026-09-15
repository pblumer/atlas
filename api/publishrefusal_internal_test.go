package api

import (
	"strings"
	"testing"
)

// The page's half of the refused publish. The wire half is in
// publishrefusal_http_test.go; the failure was that the two never met.

// TestTheAuthoringScreenRendersTheRefusalAndNotItsStatus.
func TestTheAuthoringScreenRendersTheRefusalAndNotItsStatus(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	// The call site and not the name: the function's own declaration contains the
	// name too, so looking for "publishRefusal(err)" alone still passes after the
	// handler has gone back to err.message — which is how this test was first
	// written, and what deliberately breaking it showed.
	if !strings.Contains(src, "${publishRefusal(err)}") {
		t.Fatal("the publish handler no longer renders the refusal through " +
			"publishRefusal. Reading err.message instead shows an empty box, because " +
			"a 422 carries no \"error\" key and HTTP/2 carries no reason phrase")
	}
	start := strings.Index(src, "function publishRefusal(")
	if start < 0 {
		t.Fatal("catalog-admin.js has no publishRefusal(); if it moved, this test now " +
			"passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatal("publishRefusal() is not closed where this test expects")
	}
	body := src[start : start+end]
	// Every field a problem carries. Dropping one loses part of what the server
	// said, silently — the reader sees a shorter list and no sign anything is
	// missing.
	for _, want := range []string{".problems", "p.message", "p.item", "p.catalog"} {
		if !strings.Contains(body, want) {
			t.Errorf("publishRefusal does not read %s, so part of the refusal never "+
				"reaches the screen", want)
		}
	}
}

// TestAnEmptyStatusTextIsNotAnErrorMessage: the same defect one layer down, and
// on every screen rather than one.
//
// The console reports err.message everywhere. HTTP/2 carries no reason phrase, so
// res.statusText is the empty string, and any error body without an "error" key
// produced an Error whose message was "" — a failure reported as a blank box, in
// every caller, not only this page.
func TestAnEmptyStatusTextIsNotAnErrorMessage(t *testing.T) {
	src := readWeb(t, "app.js")
	if !strings.Contains(src, "res.statusText || `HTTP ${res.status}`") {
		t.Error("apiRaw falls back to res.statusText alone. Over HTTP/2 that is the " +
			"empty string, so an error body carrying no \"error\" key becomes an Error " +
			"with no message, and every caller reporting it shows nothing at all")
	}
}
