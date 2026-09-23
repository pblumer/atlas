package api

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
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
		{`langFields("t", langs, v.texts)`, "the name per language"},
		{`langFields("cat", langs,`, "the heading it sits under"},
		{`name="keywords"`, "what it can be found by"},
		{`name="price"`, "what it costs"},
		{`name="variants"`, "the shapes it is ordered in"},
		{`section("How an order is handled"`, "the heading for what an order does"},
		{`name="state"`, "whether it is orderable"},
		{`name="akind"`, "whether it needs approval"},
		{`eligibleField(dir, v.eligible)`, "who may receive it"},
		{`name="configForm"`, "what the orderer is asked"},
		{`procSelect("provisionProcess"`, "what runs to grant it"},
		{`name="maxDays"`, "how long the right may last"},
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

// The two headings, and where they are read.
//
// The portal's cascade reads Kategorie › Produktgruppe › Produkt › Services. The
// two upper columns are collected from the products nothing contains and the two
// lower ones from the containment graph (ADR-0383), so a product that is a part is
// reached through the product carrying it and its own heading is never read.
//
// The form offered both fields on every product and its hint said the category was
// "the heading this product sits under in the portal" — which is false for a part.
// A maintainer could fill in a column that had already stopped reading the field,
// and nothing anywhere said so: publishing does not refuse it, because a heading on
// a part is not wrong, it is unread.

// TestTheFormSaysWhereTheTwoHeadingsAreRead.
//
// Both halves: the hint that stops overclaiming, and the note that answers it for
// the product actually open.
func TestTheFormSaysWhereTheTwoHeadingsAreRead(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	form := webRegion(t, src, "function productForm(", "\n}")

	// Collapsed, because the hint is prose wrapped to the source's width: a guard
	// matching the line breaks too would fail on a reflow that changed nothing.
	if !strings.Contains(strings.Join(strings.Fields(form), " "),
		"Read off the products nothing contains") {
		t.Error("the category hint does not say which products the heading is read " +
			"off, so it still reads as an attribute of every product")
	}
	if !strings.Contains(form, "partOfNote(it, cat, items, langs)") {
		t.Error("the form renders no note about the product being a part, so a " +
			"maintainer filling in a heading that is never read is told nothing")
	}

	note := webRegion(t, src, "function partOfNote(", "\n}")
	if !strings.Contains(note, "cat.edges") {
		t.Fatal("the note does not read the catalogue's edges, so it cannot know " +
			"whether this product is a part; this guard has lost its subject")
	}
	// Both structural kinds. An integral part and an optional one are both reached
	// through the product carrying them, so a note reading one kind would stay
	// silent for half the products it is about — and silence is the defect.
	for _, kind := range []string{"composition", "aggregation"} {
		if !strings.Contains(note, kind) {
			t.Errorf("the note ignores %q edges, so a product carried by one is told "+
				"its heading is read when it is not", kind)
		}
	}
	// A note and not a gate. Containment belongs to a catalogue and not to the
	// product: the same item is legitimately a part here and offered on its own in
	// the catalogue next door, where the heading IS read — so hiding the controls
	// would hide a field another catalogue depends on.
	if strings.Contains(note, "name=") {
		t.Error("the note renders a control of its own; it exists to say where the " +
			"fields are read, and the fields stay the ones above")
	}
	for _, field := range []string{`langFields("cat", langs,`, `langFields("grp", langs,`} {
		if !strings.Contains(form, field) {
			t.Errorf("the form no longer offers %s at all; a product that is a part "+
				"here may be offered on its own elsewhere, and the heading is read there", field)
		}
	}
}

// Every field a product carries, against the form that maintains it.
//
// The gap this closes: the form rendered eight of a product's fields and the portal
// read twelve. There was no control for the orderable shapes, the search terms, the
// eligible groups or the ceiling on how long a right may last — so a portal that
// blocks an order until a shape is chosen, a search that reads the keywords, an
// order refused for an ineligible recipient and a right that expires were driven by
// data no maintainer could see. Settable only over REST or MCP, which is to say:
// not by the person whose job it is.
//
// Derived from the record and not from a list kept beside it, for the reason the
// MCP schema's guard is: the list is the thing that was already wrong. A field
// added to the record from now on either gets a control or gets named below with a
// reason, and neither can happen silently.

// productFieldControls says what proves each of a product's fields is maintainable.
//
// Most fields are their own control name. The ones that are not are the ones where
// a field and a control are legitimately different things: a name per language is a
// box per language, an approval is a kind plus the ref that kind asks for, and
// eligibility is a picker that degrades to an id field.
var productFieldControls = map[string]string{
	"id":    `name="id"`,
	"state": `name="state"`,
	// One box per language, because the field is a map per language tag: one
	// control could only ever maintain one of its entries. The row is drawn by a
	// helper, so what proves the field is maintainable is that the helper is given
	// it — the control names themselves are built inside and there is no literal
	// to search for.
	"texts":              `langFields("t", langs, v.texts)`,
	"descriptions":       `langFields("d", langs, v.descriptions`,
	"variants":           `name="variants"`,
	"approval":           `name="akind"`,
	"provisionProcess":   `procSelect("provisionProcess"`,
	"deprovisionProcess": `procSelect("deprovisionProcess"`,
	"multipleAllowed":    `name="multipleAllowed"`,
	"targets":            `name="targets"`,
	"maxDays":            `name="maxDays"`,
	"eligible":           `eligibleField(dir, v.eligible)`,
	"lifecycle":          `name="orderableFrom"`,
	"keywords":           `name="keywords"`,
	"configForm":         `name="configForm"`,
	"price":              `name="price"`,
	// The two headings are one row of per-language boxes each, and that row
	// maintains the key and the wordings together — the first box that has
	// anything in it is the key. So both the key and its wordings are proved by
	// the same fragment: the helper that fills the row from them. There is
	// deliberately no control called categoryTexts (ADR-0412, as amended).
	"category":          `headingBoxes(v.category, v.categoryTexts, langs)`,
	"categoryTexts":     `headingBoxes(v.category, v.categoryTexts, langs)`,
	"productGroup":      `headingBoxes(v.productGroup, v.productGroupTexts, langs)`,
	"productGroupTexts": `headingBoxes(v.productGroup, v.productGroupTexts, langs)`,
}

// productFieldsWithNoControl are the fields the form deliberately does not ask for,
// each with the reason it is not an omission.
//
// The orderable window used to be the fourth entry, excused because nothing read
// it: a control would have promised an effect that did not exist. It is enforced
// at placement now (ADR-0397), so the excuse is gone
// and so is the entry — which is what the list is for. Everything left is a field
// no form can ask for, rather than one waiting on a decision.
var productFieldsWithNoControl = map[string]string{
	"homeCatalog": "the save states the catalogue being edited; asking would invite " +
		"somebody to move a product by typing",
	"revision":  "the precondition is carried from what was read, never typed",
	"createdAt": "carried from what was read; a replace would otherwise reset it",
	"updatedAt": "the server stamps it on every write",
}

// TestTheFormMaintainsEveryFieldAProductCarries.
func TestTheFormMaintainsEveryFieldAProductCarries(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	form := webRegion(t, src, "function productForm(", "\n}")
	body := webRegion(t, src, "export function productBody(", "\n}")

	for _, field := range itemJSONFieldNames(t) {
		if why, ok := productFieldsWithNoControl[field]; ok {
			if _, both := productFieldControls[field]; both {
				t.Errorf("%q is listed as having no control and as having one; the two "+
					"lists disagree and one of them is stale", field)
			}
			// And nothing asks for it. A field with no control that the save reads from
			// the form anyway is a field cleared on every save — the defect, in the one
			// shape that leaves no trace. What the save legitimately does write for
			// these is what it was handed (the catalogue being edited) or what it read
			// (the revision, the creation date).
			if strings.Contains(form, `name="`+field+`"`) {
				t.Errorf("the form renders a control for %q, which is listed as having "+
					"none because %s; the list is stale", field, why)
			}
			if strings.Contains(body, `f.get("`+field+`")`) {
				t.Errorf("the save reads a control named %q although the form renders "+
					"none (%s), so every save overwrites the field with what an absent "+
					"control reads as", field, why)
			}
			continue
		}
		control, known := productFieldControls[field]
		if !known {
			t.Errorf("a product carries %q and this guard does not say how it is "+
				"maintained: either the form grew a control for it and the list is "+
				"stale, or the field is unmaintainable and belongs in the list of "+
				"fields with none, with the reason written down", field)
			continue
		}
		if !strings.Contains(form, control) {
			t.Errorf("a product carries %q and the form renders no %s for it, so it can "+
				"be set over REST and MCP and not by the person whose job it is",
				field, control)
		}
		// Rendered is not maintained: a control the save never reads leaves the field
		// at whatever was stored, which looks like a form that silently refuses to
		// change one field.
		if !writesField(body, field) {
			t.Errorf("the save does not read the control for %q, so editing it changes "+
				"nothing", field)
		}
	}
}

// writesField reports whether the body assembles this field, either as a key or as
// the shorthand a value computed a few lines above is written with.
func writesField(body, field string) bool {
	return strings.Contains(body, field+":") || strings.Contains(body, " "+field+",")
}

// itemJSONFieldNames is how a product arrives on the wire — the tag names, which
// are what a control and a body key have to match.
func itemJSONFieldNames(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(catalog.Item{})
	var out []string
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("a product carries no marshalled field; this guard has lost its subject")
	}
	return out
}
