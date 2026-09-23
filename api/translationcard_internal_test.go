package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// A gate removed needs something in its place, and this is the guard on that.
//
// Publishing used to refuse a product named in one declared language and not
// another. The refusal protected no reader — the portal shows the name the
// catalogue has rather than a blank — so it held usable catalogues back until the
// last translation arrived. What it did do is make the gap impossible to ignore.
//
// The relaxation is only safe while the gap is said somewhere a maintainer looks.
// Nothing in the model can hold that: the Console is the surface, so the guard is
// here, and it reads the screen rather than trusting that somebody remembered.

// TestTheCatalogueScreenSaysWhatIsStillUntranslated.
func TestTheCatalogueScreenSaysWhatIsStillUntranslated(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")

	if !strings.Contains(src, "/api/v1/catalog-products/translation-gaps") {
		t.Fatal("the catalogue screen no longer reads the translation report. " +
			"Publishing does not refuse a half-translated product any more, so this " +
			"card is the only thing between a maintainer and a gap nobody mentions")
	}
	body := webRegion(t, src, "function translationCard(", "\n}")

	// Three states, and the third is why it is a card and not a line: a report
	// that could not be read must not render as a clean bill of health.
	if !strings.Contains(body, "report === null") {
		t.Error("an unreadable report is not told apart from one that found nothing, " +
			"so a server that could not answer reads as a catalogue with no gaps")
	}
	// A quiet report says how much it looked at. An empty list with no count reads
	// as "nothing was checked", which is the answer somebody wants to see.
	if !strings.Contains(body, "checked") {
		t.Error("the quiet case does not say how many products were checked, so it " +
			"cannot be told from a report that looked at nothing")
	}
	// And it says the gap does not block a publish. A card that looks like a
	// refusal is a card people try to clear before shipping, which is the
	// behaviour the refusal was removed to end.
	if !strings.Contains(body, "not</b> stop a publish") {
		t.Error("the card does not say that a gap is not a refusal; read as one it " +
			"restores by habit the gate that was removed on purpose")
	}
}

// TestTheReportTheScreenReadsIsTheOneTheModelComputes.
//
// The card renders `gaps`, `checked` and each gap's `catalog`, `item` and
// `message`. Those are field names on a Go struct, and a rename there would leave
// the card drawing an empty table with nothing failing — the JSON would simply
// carry different keys. So the names are read off the struct rather than repeated
// here, and the screen is held to them.
func TestTheReportTheScreenReadsIsTheOneTheModelComputes(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function translationCard(", "\n}")
	for _, want := range append(jsonFieldsOf(t, catalog.TranslationReport{}),
		jsonFieldsOf(t, catalog.Problem{})...) {
		if !strings.Contains(body, want) {
			t.Errorf("the card never reads %q, which the report carries. Either the "+
				"screen is ignoring something the model says, or a rename moved the "+
				"key and left the card reading a field that no longer arrives", want)
		}
	}
}

// jsonFieldsOf is the marshalled field names of a struct, so a guard names what
// the wire carries rather than a list somebody keeps beside it.
func jsonFieldsOf(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	out := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}
