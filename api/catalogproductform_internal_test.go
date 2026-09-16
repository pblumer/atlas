package api

import (
	"regexp"
	"strings"
	"testing"
)

// The product form, read in two passes.
//
// It answers two questions to two different readers and used to interleave them. A
// product manager writing a laptop into the catalogue asks "what will people see?"
// — a name, a heading, a price. Only afterwards does anybody ask "and what happens
// when it is ordered?" — who approves, which process runs, what the target systems
// call it. The fields alternated between the two four times down one column, so
// answering either question meant reading past the other.
//
// A section order is the kind of thing that survives exactly as long as nobody
// appends a field to the end of the function, which is why it is held here.

// TestTheProductFormAsksWhatIsShownBeforeWhatIsAdministered.
//
// The order is the feature. Held by position rather than by the headings alone: a
// heading is easy to keep and easy to render above fields that no longer belong
// under it.
func TestTheProductFormAsksWhatIsShownBeforeWhatIsAdministered(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function productForm(", "\n}")

	order := []struct{ frag, what string }{
		{`section("What the catalogue shows"`, "the heading for what a reader meets"},
		{`name="id"`, "the id"},
		{`name="t-${esc(l)}"`, "the name per language"},
		{`name="category"`, "the heading it sits under"},
		{`name="price"`, "what it costs"},
		{`section("How an order is handled"`, "the heading for what an order does"},
		{`name="state"`, "whether it is orderable"},
		{`name="akind"`, "whether it needs approval"},
		{`name="configForm"`, "what the orderer is asked"},
		{`procSelect("provisionProcess"`, "what runs to grant it"},
		{`name="targets"`, "what the target systems call it"},
	}
	at := -1
	for _, step := range order {
		i := strings.Index(body, step.frag)
		if i < 0 {
			t.Fatalf("the form no longer carries %s (%q); this guard has lost its subject",
				step.what, step.frag)
		}
		if i < at {
			t.Errorf("%s (%q) comes before something that should precede it: the form no "+
				"longer reads catalogue-first, administration-second", step.what, step.frag)
		}
		at = i
	}
}

// TestTheProductFormSpellsNoColourOfItsOwn.
//
// The instruction this slice was given, as a check rather than an intention: the
// self-service screens follow the console's theme, which they do by never naming a
// colour. One hard-coded value survives a theme change and is then the one element
// on the page that looks wrong, which is worse than a page that ignores the theme
// entirely — a reader reads it as a defect in their data.
func TestTheProductFormSpellsNoColourOfItsOwn(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function productForm(", "\n}")
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if m := hex.FindString(body); m != "" {
		t.Errorf("the form names the colour %s instead of a token", m)
	}
	for _, bad := range []string{"rgb(", "rgba(", "hsl("} {
		if strings.Contains(body, bad) {
			t.Errorf("the form computes a colour with %s instead of taking a token", bad)
		}
	}

	// And the rules that lay it out: a token for every colour, and the console's own
	// breakpoint rather than a second one chosen for this form. Two form layouts
	// reflowing at different widths is a difference a reader notices and nobody meant.
	css := readWeb(t, "app.css")
	rules := webRegion(t, css, ".product-form { display: grid;", "\n@media")
	if m := hex.FindString(rules); m != "" {
		t.Errorf("the product form's rules name the colour %s instead of a token", m)
	}
	for _, want := range []string{"var(--border)", "var(--muted)"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the product form's rules do not take %s, so a theme change leaves them behind", want)
		}
	}
	grid2 := regexp.MustCompile(`@media \(max-width: (\d+)px\) \{ \.grid2`).FindStringSubmatch(css)
	form := regexp.MustCompile(`@media \(max-width: (\d+)px\) \{ \.product-form`).FindStringSubmatch(css)
	if grid2 == nil || form == nil {
		t.Fatal("one of the two layouts no longer reflows at a stated width; this guard has lost its subject")
	}
	if grid2[1] != form[1] {
		t.Errorf("the product form reflows at %spx and the console's other two-column layout "+
			"at %spx; one number, or a reader meets two different pages", form[1], grid2[1])
	}
}

// TestAProductHasNoDeputyAndTheFormSaysWhereOneIsSet.
//
// A product carries no member list, and that is a decision rather than a gap:
// Item.HomeCatalog records that an item is referenced by catalogues rather than
// owned by one, so access cannot be inherited from "the catalogue it is in", and a
// per-item ACL is the option ADR-0071 weighed and refused.
//
// Somebody looking for a deputy field will not find one. What they need at that
// moment is not a control but an answer: who may already, and where it is changed.
// Without it the honest design reads as a missing feature.
func TestAProductHasNoDeputyAndTheFormSaysWhereOneIsSet(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	form := webRegion(t, src, "function productForm(", "\n}")
	if !strings.Contains(form, "maintainersNote(cat, dir)") {
		t.Fatal("the product form says nothing about who may maintain the product, so the " +
			"absence of a deputy field reads as an oversight")
	}
	note := webRegion(t, src, "function maintainersNote(cat, dir)", "\n}")
	if !strings.Contains(note, `m.role === "editor"`) {
		t.Error("the note does not name the catalogue's editors, who are exactly the people " +
			"a deputy would be")
	}
	if !strings.Contains(note, "cat.ownerId") {
		t.Error("the note leaves out the owner, who may always maintain it")
	}
	if !strings.Contains(note, "no deputy of") {
		t.Error("the note does not say that a product has no deputy of its own, so a reader " +
			"is left looking for the field")
	}
}
