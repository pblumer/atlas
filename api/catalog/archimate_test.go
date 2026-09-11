package catalog

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// Importing a catalogue from an architecture model.
//
// The model already says what a product is made of, in the vocabulary an
// architect uses: a Product or Business Service is something offered, a
// composition is a part that comes with the whole, an aggregation is one offered
// beside it. That is the same distinction the catalogue makes, which is why the
// import is worth having at all.
//
// What it is not is a synchronisation. The model is a starting point (decision
// b4): everything arrives as a draft, nothing becomes orderable by importing it,
// and a second import never silently overwrites what somebody has since
// maintained.

func load(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/arbeitsplatz.archimate.xml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

func ids(items []Item) string {
	var out []string
	for _, i := range items {
		out = append(out, i.ID)
	}
	return strings.Join(out, ",")
}

func TestImportTakesProductsAndServices(t *testing.T) {
	got, err := ImportArchiMate(load(t), "cat_1")
	if err != nil {
		t.Fatalf("ImportArchiMate: %v", err)
	}

	if s := ids(got.Items); s != "arbeitsplatz,benutzerkonto,notebook,zweitbildschirm" {
		t.Fatalf("items = %s, want the product and its services, sorted", s)
	}
	for _, it := range got.Items {
		if it.HomeCatalog != "cat_1" {
			t.Errorf("%s has home %q, want cat_1", it.ID, it.HomeCatalog)
		}
		if it.State != StateDraft {
			t.Errorf("%s is %s, want draft — importing must not make anything orderable", it.ID, it.State)
		}
	}
}

// TestImportKeepsEveryLanguageTheModelHas: a catalogue declares its languages,
// and a name the model already carries in one of them must not be retyped.
func TestImportKeepsEveryLanguageTheModelHas(t *testing.T) {
	got, _ := ImportArchiMate(load(t), "cat_1")
	for _, it := range got.Items {
		if it.ID != "arbeitsplatz" {
			continue
		}
		if it.Texts["de"] != "Arbeitsplatz" || it.Texts["en"] != "Workplace" {
			t.Fatalf("texts = %v, want both languages", it.Texts)
		}
	}
}

// TestCompositionAndAggregationKeepTheirMeaning: the whole reason to read the
// model rather than a list of names.
func TestCompositionAndAggregationKeepTheirMeaning(t *testing.T) {
	got, _ := ImportArchiMate(load(t), "cat_1")

	kinds := map[string]EdgeKind{}
	for _, e := range got.Edges {
		kinds[e.From+">"+e.To] = e.Kind
	}
	if kinds["arbeitsplatz>benutzerkonto"] != EdgeComposition {
		t.Errorf("account edge = %s, want composition", kinds["arbeitsplatz>benutzerkonto"])
	}
	if kinds["arbeitsplatz>zweitbildschirm"] != EdgeAggregation {
		t.Errorf("screen edge = %s, want aggregation", kinds["arbeitsplatz>zweitbildschirm"])
	}
}

// TestWhatTheImportCannotTakeIsNamed: a silent drop is the failure mode of every
// importer. An architect who modelled something and does not find it has to be
// told which thing and why.
func TestWhatTheImportCannotTakeIsNamed(t *testing.T) {
	got, _ := ImportArchiMate(load(t), "cat_1")

	joined := strings.Join(got.Skipped, " | ")
	for _, want := range []string{"Personalabteilung", "Association"} {
		if !strings.Contains(joined, want) {
			t.Errorf("skipped list does not mention %q: %s", want, joined)
		}
	}
	// The composition onto an actor is not a catalogue edge: it points at
	// something the catalogue does not carry.
	if !strings.Contains(joined, "r5") {
		t.Errorf("the edge onto an actor was dropped without saying so: %s", joined)
	}
}

// TestNoEdgeSurvivesWithoutBothEnds: an edge into nothing is what publishing
// refuses, so importing must not produce one.
func TestNoEdgeSurvivesWithoutBothEnds(t *testing.T) {
	got, _ := ImportArchiMate(load(t), "cat_1")
	carried := map[string]bool{}
	for _, it := range got.Items {
		carried[it.ID] = true
	}
	for _, e := range got.Edges {
		if !carried[e.From] || !carried[e.To] {
			t.Fatalf("edge %s -> %s names something the import did not take", e.From, e.To)
		}
	}
}

// TestTwoElementsWithTheSameNameAreReported: ids come from names, so a clash is
// a thing the architect has to resolve — silently merging two services would
// merge two orders.
func TestTwoElementsWithTheSameNameAreReported(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="Product"><name xml:lang="de">Konto</name></element>
    <element identifier="b" xsi:type="BusinessService"><name xml:lang="de">Konto</name></element>
  </elements>
</model>`
	got, err := ImportArchiMate([]byte(doc), "cat_1")
	if err != nil {
		t.Fatalf("ImportArchiMate: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %v, want one kept", ids(got.Items))
	}
	if !strings.Contains(strings.Join(got.Skipped, " "), "Konto") {
		t.Fatalf("the clash was not reported: %v", got.Skipped)
	}
}

// TestAnElementWithNoNameIsSkipped: an id is derived from a name, and a product
// nobody can name is a product nobody can order.
func TestAnElementWithNoNameIsSkipped(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements><element identifier="a" xsi:type="Product"/></elements>
</model>`
	got, _ := ImportArchiMate([]byte(doc), "cat_1")
	if len(got.Items) != 0 {
		t.Fatalf("items = %v, want none", ids(got.Items))
	}
	if len(got.Skipped) == 0 {
		t.Fatal("an unnamed element was dropped without saying so")
	}
}

func TestImportRefusesWhatIsNotAModel(t *testing.T) {
	for _, tt := range []struct{ name, doc string }{
		{"not xml", "{\"json\": true}"},
		{"a directive", `<?xml version="1.0"?><!DOCTYPE m [<!ENTITY x "y">]><model/>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ImportArchiMate([]byte(tt.doc), "cat_1"); err == nil {
				t.Fatal("want a refusal")
			}
		})
	}
}

// TestAnEmptyModelImportsNothingAndSaysSo.
func TestAnEmptyModelImportsNothingAndSaysSo(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m"/>`
	got, err := ImportArchiMate([]byte(doc), "cat_1")
	if err != nil {
		t.Fatalf("ImportArchiMate: %v", err)
	}
	if len(got.Items) != 0 || len(got.Edges) != 0 {
		t.Fatalf("got %d items and %d edges, want none", len(got.Items), len(got.Edges))
	}
}

// TestIdsAreReadableAndSafe: an id appears in a catalogue's item list, in an
// order's lines and in a release, so an opaque one would make all three
// unreadable — and umlauts have to survive into something a filename tolerates.
func TestIdsAreReadableAndSafe(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="Product"><name xml:lang="de">Mobilfunk-Abo (Grösse L)</name></element>
    <element identifier="b" xsi:type="BusinessService"><name xml:lang="fr">Boîte aux lettres</name></element>
    <element identifier="c" xsi:type="BusinessService"><name xml:lang="de">!!!</name></element>
  </elements>
</model>`
	got, err := ImportArchiMate([]byte(doc), "cat_1")
	if err != nil {
		t.Fatalf("ImportArchiMate: %v", err)
	}
	if s := ids(got.Items); s != "boite-aux-lettres,mobilfunk-abo-groesse-l" {
		t.Fatalf("ids = %s", s)
	}
	// A name with nothing to make an id out of is reported, not invented.
	if !strings.Contains(strings.Join(got.Skipped, " "), "!!!") {
		t.Errorf("the unusable name was dropped silently: %v", got.Skipped)
	}
}

// TestATooLargeModelIsRefused and TestTooDeepIsRefused: the same bounds panorama
// puts on a document it reads from outside.
func TestATooLargeModelIsRefused(t *testing.T) {
	if _, err := ImportArchiMate(make([]byte, maxImportBytes+1), "cat_1"); err == nil {
		t.Fatal("want a refusal")
	}
}

func TestTooDeepIsRefused(t *testing.T) {
	doc := `<?xml version="1.0"?><model>` + strings.Repeat("<x>", maxImportDepth+2) +
		strings.Repeat("</x>", maxImportDepth+2) + `</model>`
	if _, err := ImportArchiMate([]byte(doc), "cat_1"); err == nil {
		t.Fatal("want a refusal")
	}
}

// TestTheSkipReportIsBounded: a model full of what the catalogue does not carry
// must not answer with a report nobody can read.
func TestTheSkipReportIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m"><elements>`)
	for i := 0; i < maxSkipped+50; i++ {
		b.WriteString(`<element identifier="x" xsi:type="BusinessActor"><name xml:lang="de">A</name></element>`)
	}
	b.WriteString(`</elements></model>`)

	got, err := ImportArchiMate([]byte(b.String()), "cat_1")
	if err != nil {
		t.Fatalf("ImportArchiMate: %v", err)
	}
	if len(got.Skipped) != maxSkipped {
		t.Fatalf("skipped %d entries, want the bound of %d", len(got.Skipped), maxSkipped)
	}
}

// TestAnElementWithNoTypeIsReportedAsSuch: "element with no type" says more than
// repeating an empty string back.
func TestAnElementWithNoTypeIsReportedAsSuch(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m">
  <elements><element identifier="a"><name xml:lang="de">Etwas</name></element></elements>
</model>`
	got, _ := ImportArchiMate([]byte(doc), "cat_1")
	if !strings.Contains(strings.Join(got.Skipped, " "), "no type") {
		t.Fatalf("skipped = %v", got.Skipped)
	}
}

// TestAnUntypedRelationshipIsReported, so a model built with a tool that omits
// the type does not look like a model with no structure.
func TestAnUntypedRelationshipIsReported(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="Product"><name xml:lang="de">A</name></element>
    <element identifier="b" xsi:type="BusinessService"><name xml:lang="de">B</name></element>
  </elements>
  <relationships><relationship identifier="r" source="a" target="b"/></relationships>
</model>`
	got, _ := ImportArchiMate([]byte(doc), "cat_1")
	if len(got.Edges) != 0 {
		t.Fatalf("edges = %v, want none", got.Edges)
	}
	if !strings.Contains(strings.Join(got.Skipped, " "), "no type") {
		t.Fatalf("skipped = %v", got.Skipped)
	}
}

// TestANamespacedTypeReadsTheSame: "archimate:Product" and "Product" are the
// same thing, and different tools write different ones.
func TestANamespacedTypeReadsTheSame(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="archimate:Product"><name xml:lang="de">Konto</name></element>
  </elements>
</model>`
	got, _ := ImportArchiMate([]byte(doc), "cat_1")
	if ids(got.Items) != "konto" {
		t.Fatalf("items = %s, want konto", ids(got.Items))
	}
}

// The endpoint. Importing changes a catalogue, so it needs the same authority as
// changing one — and it writes drafts rather than replacing what is stored,
// because the model is a starting point and not a synchronisation.

func TestImportThroughTheAPI(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	rec := as(t, s.HandleImport, user("usr_owner"), "POST", string(load(t)), "id", cat.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d (%s)", rec.Code, rec.Body)
	}
	got := decode[Import](t, rec)
	if len(got.Items) != 4 || len(got.Edges) != 3 {
		t.Fatalf("imported %d items and %d edges, want 4 and 3", len(got.Items), len(got.Edges))
	}

	// The products are stored as drafts, and the catalogue now offers them.
	items := decode[[]Item](t, as(t, s.HandleListItems, user("usr_owner"), "GET", ""))
	if len(items) != 4 {
		t.Fatalf("%d products stored, want 4", len(items))
	}
	after := decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_owner"), "GET", "", "id", cat.ID))
	if len(after.Items) != 4 || len(after.Edges) != 3 {
		t.Fatalf("catalogue carries %d items and %d edges", len(after.Items), len(after.Edges))
	}

	// And nothing is orderable: publishing refuses drafts, which is the point.
	pub := as(t, s.HandlePublish, user("usr_owner"), "POST", "", "id", cat.ID)
	if pub.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish = %d, want 422 — an import must not make anything orderable", pub.Code)
	}
}

// TestASecondImportDoesNotOverwriteWhatWasMaintained is decision b4: the model
// is where a catalogue starts, not something it follows.
func TestASecondImportDoesNotOverwriteWhatWasMaintained(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleImport, user("usr_owner"), "POST", string(load(t)), "id", cat.ID)

	// Somebody finishes one product: binds its processes and activates it.
	done := `{"id":"notebook","homeCatalog":"` + cat.ID + `","state":"active",` +
		`"texts":{"de":"Notebook"},"approval":{"kind":"none"},` +
		`"provisionProcess":"prov","deprovisionProcess":"deprov"}`
	if rec := as(t, s.HandleSaveItem, user("usr_owner"), "POST", done); rec.Code != http.StatusOK {
		t.Fatalf("save = %d (%s)", rec.Code, rec.Body)
	}

	rec := as(t, s.HandleImport, user("usr_owner"), "POST", string(load(t)), "id", cat.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("second import = %d (%s)", rec.Code, rec.Body)
	}

	items := decode[[]Item](t, as(t, s.HandleListItems, user("usr_owner"), "GET", ""))
	for _, it := range items {
		if it.ID != "notebook" {
			continue
		}
		if it.State != StateActive || it.ProvisionProcess != "prov" {
			t.Fatalf("the maintained product was overwritten: %+v", it)
		}
	}
	// And the import says it left it alone.
	got := decode[Import](t, rec)
	if !strings.Contains(strings.Join(got.Skipped, " "), "notebook") {
		t.Fatalf("the untouched product was not reported: %v", got.Skipped)
	}
}

// TestImportingNeedsTheAuthorityToChangeTheCatalogue.
func TestImportingNeedsTheAuthorityToChangeTheCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	if rec := as(t, s.HandleImport, user("usr_stranger"), "POST", string(load(t)), "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger got %d, want 404", rec.Code)
	}
	if rec := as(t, s.HandleImport, user("usr_owner"), "POST", string(load(t)), "id", "cat_nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown catalogue got %d, want 404", rec.Code)
	}
	if rec := as(t, s.HandleImport, user("usr_owner"), "POST", "not xml", "id", cat.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("rubbish got %d, want 400", rec.Code)
	}
}
