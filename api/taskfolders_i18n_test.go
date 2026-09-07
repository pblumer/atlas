package api

import (
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/taskfolder"
)

// TestFolderCatalogueHasMessagesForEveryFieldAndOperator holds the seam the
// folder editor is built across: the server publishes field and operator *ids*
// and the console owns the words (ADR-draft-console-speaks-german-first).
//
// That split has exactly one failure mode. Adding a field or an operator to the
// catalogue in Go is a one-line change, and the editor then draws a listbox entry
// reading `tasks.folders.field.instanceAge` — visibly broken by design, but only
// to whoever opens that dialog. This is the drift test that makes it fail in CI
// instead, in the same spirit as the route/OpenAPI inventory: what the two sides
// agree on is checked, not remembered.
func TestFolderCatalogueHasMessagesForEveryFieldAndOperator(t *testing.T) {
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	// The catalogue carries one object per locale. German is the one the console
	// falls back to, so it is the one that must be complete; a second locale is
	// checked for the same keys so a half-translated one is caught too.
	src := string(catalogue)
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(src, locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for _, f := range taskfolder.Catalog() {
			want := `"tasks.folders.field.` + f.ID + `":`
			if !strings.Contains(block, want) {
				t.Errorf("locale %q has no message for field %q (add %s)", locale, f.ID, want)
			}
			for _, op := range f.Ops {
				want := `"tasks.folders.op.` + op.ID + `":`
				if !strings.Contains(block, want) {
					t.Errorf("locale %q has no message for operator %q (add %s)", locale, op.ID, want)
				}
			}
		}
	}
}

// localeBlock returns the text of one locale's object in the catalogue: from its
// key to the line that closes it. It is a text scan rather than a parse because
// the catalogue is JavaScript, and a Go test that had to parse JavaScript to
// check a convention would be the more fragile of the two.
func localeBlock(src, locale string) (string, bool) {
	start := strings.Index(src, "\n  "+locale+": {")
	if start < 0 {
		return "", false
	}
	rest := src[start:]
	end := strings.Index(rest, "\n  },")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// TestFolderCatalogueCoversTheEditorsOtherLists is the same guard for the two
// value lists the console fills itself: the priority presets and the "due within"
// offers name their own messages, and a locale missing one shows a raw key in a
// listbox somebody is picking from.
func TestFolderCatalogueCoversTheEditorsOtherLists(t *testing.T) {
	catalogue, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatalf("read the message catalogue: %v", err)
	}
	src := string(catalogue)
	keys := []string{
		"tasks.folders.priority.high", "tasks.folders.priority.normal", "tasks.folders.priority.low",
		"tasks.folders.within.PT8H", "tasks.folders.within.P1D",
		"tasks.folders.within.P3D", "tasks.folders.within.P7D",
		"tasks.folders.unit.h", "tasks.folders.unit.d",
		"tasks.folders.joiner.all", "tasks.folders.joiner.any",
		"tasks.folders.matchCount.one", "tasks.folders.matchCount.other",
	}
	for _, locale := range []string{"de", "en"} {
		block, ok := localeBlock(src, locale)
		if !ok {
			t.Fatalf("no %q block in web/i18n.js", locale)
		}
		for _, k := range keys {
			if !strings.Contains(block, `"`+k+`":`) {
				t.Errorf("locale %q has no message for %q", locale, k)
			}
		}
	}
}
