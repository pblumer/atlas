package panorama

import "testing"

// The served relationship table and the one the exporter writes from are the same
// rows. They are two shapes of one table on purpose (see archiNotationRelations), and
// this is what holds them to it: the canvas draws its arrowheads from the served
// shape and the document is written from the other, so a row that existed in one and
// not the other would put a Serving arrowhead on a line the file calls Triggering —
// and nothing on either surface would say which of the two was wrong.
func TestServedRelationsAreTheExporterOwnTable(t *testing.T) {
	notation, ok := NotationByID(NotationArchiMate32)
	if !ok {
		t.Fatal("the ArchiMate notation is not in the table")
	}
	if len(notation.Relations) != len(archiRelations) {
		t.Fatalf("served %d relations, the exporter has %d",
			len(notation.Relations), len(archiRelations))
	}
	for edge, want := range archiRelations {
		got, served := notation.Relations[edge]
		if !served {
			t.Errorf("%s is exported as %s and served to nobody", edge, want.Type)
			continue
		}
		if got.Type != want.Type || got.Flip != want.Flip {
			t.Errorf("%s is served as %+v and exported as %+v", edge, got, want)
		}
		if got.Name == "" {
			t.Errorf("%s is served with no name for a reader", edge)
		}
	}
}

// Every relationship row is about an edge kind the mesh actually produces. A row
// keyed on a kind that cannot occur is a mapping for a picture nobody can draw, and
// it would go on looking correct for exactly as long as nobody checked.
func TestRelationsAreKeyedOnEdgeKindsTheMeshProduces(t *testing.T) {
	kinds := map[string]bool{EdgeContains: true, EdgeCalls: true, EdgeUses: true}
	for _, notation := range Notations() {
		for edge := range notation.Relations {
			if !kinds[edge] {
				t.Errorf("%s maps the edge kind %q, which the mesh never emits",
					notation.ID, edge)
			}
		}
	}
}

// A caller that edits what it was handed must not be editing the table the next
// request is answered from. Copying the slice only ever copied the row headers, so
// the maps and the loss list inside them were shared with every later caller — an
// edit anywhere would have changed the mapping for everybody, silently, because
// nothing reads the table back to check it. All three are covered here rather than
// only the one this change added: the hole is the same hole.
func TestAServedNotationCannotBeEditedThroughItsMaps(t *testing.T) {
	first, _ := NotationByID(NotationArchiMate32)
	first.Relations[EdgeCalls] = NotationRelation{Name: "Flow", Type: "Flow"}
	first.Types[KindProcess] = NotationType{Name: "Business Process", Type: "BusinessProcess"}
	if len(first.Loss) > 0 {
		first.Loss[0] = "nothing is lost at all"
	}

	second, _ := NotationByID(NotationArchiMate32)
	if got := second.Relations[EdgeCalls].Type; got != "Triggering" {
		t.Errorf("a caller edited the relationship table: calls is now %q", got)
	}
	if got := second.Types[KindProcess].Type; got != "ApplicationProcess" {
		t.Errorf("a caller edited the element table: a process is now %q", got)
	}
	if len(second.Loss) > 0 && second.Loss[0] == "nothing is lost at all" {
		t.Error("a caller edited the loss list, which is the one list that must not move")
	}

	// The same through the list the picker is built from, which is the other way in.
	for _, n := range Notations() {
		if n.ID == NotationArchiMate32 {
			n.Relations[EdgeUses] = NotationRelation{Type: "Access"}
		}
	}
	third, _ := NotationByID(NotationArchiMate32)
	if got := third.Relations[EdgeUses].Type; got != "Serving" {
		t.Errorf("the listed table is shared: uses is now %q", got)
	}
}
