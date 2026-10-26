package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/taskfolder"
)

// TestFolderCatalogueHasMessagesForEveryFieldAndOperator holds the seam the
// folder editor is built across: the server publishes field and operator *ids*
// and the console owns the words (ADR-0267).
//
// That split has exactly one failure mode. Adding a field or an operator to the
// catalogue in Go is a one-line change, and the editor then draws a listbox entry
// reading `tasks.folders.field.instanceAge` — visibly broken by design, but only
// to whoever opens that dialog. This is the drift test that makes it fail in CI
// instead, in the same spirit as the route/OpenAPI inventory: what the two sides
// agree on is checked, not remembered.
func TestFolderCatalogueHasMessagesForEveryFieldAndOperator(t *testing.T) {
	// The catalogue carries one object per locale. German is the one the console
	// falls back to, so it is the one that must be complete; a second locale is
	// checked for the same keys so a half-translated one is caught too.
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
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

// TestFolderCatalogueCoversTheEditorsOtherLists is the same guard for the two
// value lists the console fills itself: the priority presets and the "due within"
// offers name their own messages, and a locale missing one shows a raw key in a
// listbox somebody is picking from.
func TestFolderCatalogueCoversTheEditorsOtherLists(t *testing.T) {
	keys := []string{
		"tasks.folders.priority.high", "tasks.folders.priority.normal", "tasks.folders.priority.low",
		"tasks.folders.within.PT8H", "tasks.folders.within.P1D",
		"tasks.folders.within.P3D", "tasks.folders.within.P7D",
		"tasks.folders.unit.h", "tasks.folders.unit.d",
		"tasks.folders.joiner.all", "tasks.folders.joiner.any",
		"tasks.folders.matchCount.one", "tasks.folders.matchCount.other",
	}
	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		for _, k := range keys {
			if !strings.Contains(block, `"`+k+`":`) {
				t.Errorf("locale %q has no message for %q", locale, k)
			}
		}
	}
}
