package infomodel

import (
	"encoding/json"
	"strings"
	"testing"
)

// obj builds one data object as the graph reads it.
func obj(name, class, state, value string) ObjectValue {
	return ObjectValue{Name: name, Class: class, State: state, Value: json.RawMessage(value)}
}

func nodeByID(g Graph, id string) (ObjectNode, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return ObjectNode{}, false
}

func linkBetween(g Graph, from, to string) (ObjectLink, bool) {
	for _, l := range g.Links {
		if l.From == from && l.To == to {
			return l, true
		}
	}
	return ObjectLink{}, false
}

// salesModel is orderModel plus the two things an object diagram needs: a
// reference member on Order naming its Customer, and a role on the composition so
// the lines have somewhere to live in the value.
func salesModel(t *testing.T) *Vocabulary {
	t.Helper()
	m := orderModel()
	// Order.customer holds a Customer's business key — the reference an object
	// diagram resolves into a link.
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "customer", Type: TypeString, Multiplicity: MultOptional})
	// The plain association's To end already carries the role "orders"; give the
	// From end the member name Order refers to a Customer by.
	m.Associations[0].From.Role = "customer"
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}
	return NewVocabulary([]Model{m})
}

// TestObjectGraphNodesCarryTheirClassAndKey is the base of the whole picture: a
// data object becomes a UML object node — `order : Order` — with the attributes the
// class declares and the business key marked, because the key is what makes this
// object *this* one.
func TestObjectGraphNodesCarryTheirClassAndKey(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "approved", `{"id":"ORD-1","placedOn":"2026-09-02","status":"approved"}`),
	}, salesModel(t))

	n, ok := nodeByID(g, "order")
	if !ok {
		t.Fatalf("no node for order: %+v", g.Nodes)
	}
	if n.Label != "order : Order" {
		t.Errorf("Label = %q, want the UML object reading", n.Label)
	}
	if n.State != "approved" {
		t.Errorf("State = %q, want the BPMN data state", n.State)
	}
	if n.Key != "ORD-1" {
		t.Errorf("Key = %q, want the business key's value", n.Key)
	}
	// Attributes come from the class, in the order it declares them, so two objects
	// of one class read alike rather than in whatever order their JSON happened to be.
	var names []string
	for _, a := range n.Attributes {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "id,placedOn,status,shipTo,customer" {
		t.Errorf("attributes = %v, want the class's own order", names)
	}
	for _, a := range n.Attributes {
		if a.Name == "id" && !a.Key {
			t.Error("the key attribute is not marked as one")
		}
		if a.Name == "placedOn" && a.Value != "2026-09-02" {
			t.Errorf("placedOn = %q", a.Value)
		}
		// An attribute the value does not carry is shown as absent rather than blank:
		// "not set" and "set to empty" are different facts about a datum.
		if a.Name == "shipTo" && !a.Absent {
			t.Error("an attribute the value lacks is not marked absent")
		}
	}
}

// TestObjectGraphCompositionBecomesNestedObjects covers the one relationship a
// value can carry inside itself: an Order owns its lines, so the lines are objects
// in this instance and the diagram shows them as such.
func TestObjectGraphCompositionBecomesNestedObjects(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "", `{"id":"ORD-1","lines":[{"quantity":2},{"quantity":5}]}`),
	}, salesModel(t))

	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want the order and its two lines: %+v", len(g.Nodes), g.Nodes)
	}
	first, ok := nodeByID(g, "order.lines[0]")
	if !ok {
		t.Fatalf("no node for the first line: %+v", g.Nodes)
	}
	if first.Class != "OrderLine" || !first.Nested {
		t.Errorf("line node = %+v, want a nested OrderLine", first)
	}
	// A nested object has no business key of its own — it is identified by the whole
	// that owns it, which is what composition means.
	if first.Key != "" {
		t.Errorf("a composed part carries a key of its own: %q", first.Key)
	}
	l, ok := linkBetween(g, "order", "order.lines[0]")
	if !ok {
		t.Fatalf("no link to the first line: %+v", g.Links)
	}
	if l.Kind != KindComposition || l.Label != "lines" {
		t.Errorf("link = %+v, want a composition labelled with the role", l)
	}
	if l.Via != ViaContainment {
		t.Errorf("Via = %q, want containment — the part is inside the whole's value", l.Via)
	}
}

// TestObjectGraphReferenceResolvesByBusinessKey is what the business key is *for*:
// two objects in one instance are linked because one holds the other's key. Without
// identity there is nothing to match on and the line cannot be drawn.
func TestObjectGraphReferenceResolvesByBusinessKey(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "", `{"id":"ORD-1","customer":"C-7"}`),
		obj("buyer", "Customer", "", `{"number":"C-7","name":"Acme"}`),
	}, salesModel(t))

	l, ok := linkBetween(g, "order", "buyer")
	if !ok {
		t.Fatalf("no link from order to its customer: %+v", g.Links)
	}
	if l.Kind != KindAssociation || l.Label != "customer" {
		t.Errorf("link = %+v, want an association labelled with the role", l)
	}
	if l.Via != ViaKey {
		t.Errorf("Via = %q, want key — it was matched on the business key", l.Via)
	}
	if len(g.Unresolved) != 0 {
		t.Errorf("nothing should be unresolved: %+v", g.Unresolved)
	}
}

// TestObjectGraphUnresolvedReferenceIsAFactNotAFault is the honest half: a
// reference to something this instance does not hold is not an error, it is the
// boundary of what one instance can see. Saying so is what makes the picture
// trustworthy — and it is exactly the boundary a data store removes.
func TestObjectGraphUnresolvedReferenceIsAFactNotAFault(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "", `{"id":"ORD-1","customer":"C-99"}`),
		obj("buyer", "Customer", "", `{"number":"C-7"}`),
	}, salesModel(t))

	if _, ok := linkBetween(g, "order", "buyer"); ok {
		t.Error("a reference was linked to an object whose key does not match it")
	}
	if len(g.Unresolved) != 1 {
		t.Fatalf("unresolved = %+v, want the one dangling reference", g.Unresolved)
	}
	u := g.Unresolved[0]
	if u.From != "order" || u.Role != "customer" || u.Value != "C-99" || u.Class != "Customer" {
		t.Errorf("unresolved = %+v, want it to name where, what and which class", u)
	}
}

// TestObjectGraphCollectionRootBecomesOneNodePerElement covers an isCollection data
// object: a list of things is a list of objects, and an object diagram shows
// objects.
func TestObjectGraphCollectionRootBecomesOneNodePerElement(t *testing.T) {
	m := orderModel()
	m.Classes = append(m.Classes, Class{ID: "c6", Name: "Item", Stereotype: StereotypeBusinessObject,
		Attributes: []Attribute{{Name: "sku", Type: TypeString, Multiplicity: MultOne}}})
	g := ObjectGraph([]ObjectValue{
		obj("items", "Item", "", `[{"sku":"A"},{"sku":"B"}]`),
	}, NewVocabulary([]Model{m}))

	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want one per element: %+v", len(g.Nodes), g.Nodes)
	}
	if n, ok := nodeByID(g, "items[1]"); !ok || n.Label != "items[1] : Item" {
		t.Errorf("second element = %+v", n)
	}
}

// TestObjectGraphUntypedObjectsStillAppear pins the degradation the ADR asks for:
// an object whose type nothing resolves is still part of what the instance carries,
// so it is drawn — with its value and without links, because nothing says how it
// relates to anything.
func TestObjectGraphUntypedObjectsStillAppear(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("note", "", "drafted", `"remember to call"`),
		obj("order", "Order", "", `{"id":"ORD-1"}`),
	}, salesModel(t))

	n, ok := nodeByID(g, "note")
	if !ok {
		t.Fatalf("an untyped object was dropped: %+v", g.Nodes)
	}
	if n.Class != "" || n.Label != "note" {
		t.Errorf("node = %+v, want it labelled by name alone", n)
	}
	// Rendered the way an attribute's value is: a scalar as itself, so the diagram
	// reads the same whether a value came from a modelled member or an untyped one.
	if n.Value != "remember to call" {
		t.Errorf("Value = %q, want the value it holds", n.Value)
	}
	if len(n.Attributes) != 0 {
		t.Errorf("an untyped object has no class to take attributes from: %+v", n.Attributes)
	}
}

// TestObjectGraphWithoutAVocabularyIsStillTheInstance covers an application that
// has modelled nothing: the diagram degrades to what it can still say — which
// objects exist and what they hold — rather than to nothing.
func TestObjectGraphWithoutAVocabularyIsStillTheInstance(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "approved", `{"id":"ORD-1"}`),
	}, NewVocabulary(nil))
	if len(g.Nodes) != 1 || len(g.Links) != 0 {
		t.Fatalf("graph = %+v", g)
	}
	if g.Nodes[0].Class != "" {
		t.Errorf("a class was invented without a model: %q", g.Nodes[0].Class)
	}
	if !g.Degraded {
		t.Error("the graph does not say it is showing less than it could")
	}
}

// TestObjectGraphUnsetObjectIsShownAsUnset covers a seeded object nothing has
// written: it exists, so it is on the diagram, and it says it is empty rather than
// being silently missing.
func TestObjectGraphUnsetObjectIsShownAsUnset(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "received", ""),
		obj("note", "", "drafted", ""),
	}, salesModel(t))
	n, ok := nodeByID(g, "order")
	if !ok || !n.Unset {
		t.Fatalf("node = %+v, want it present and marked unset", n)
	}
	if n.State != "received" {
		t.Errorf("an unset object still has a data state: %q", n.State)
	}
	if n.Label != "order : Order" {
		t.Errorf("Label = %q — an unset object still knows what it is", n.Label)
	}
	// And one that is both unset and untyped is labelled by name alone: there is
	// nothing else true about it yet.
	untyped, ok := nodeByID(g, "note")
	if !ok || untyped.Label != "note" || untyped.Class != "" || !untyped.Unset {
		t.Errorf("node = %+v, want a bare unset node", untyped)
	}
}

// TestObjectGraphStopsAtItsLimits pins the two guards that keep a derived picture
// from running away: nesting depth, and the number of nodes drawn at all.
func TestObjectGraphStopsAtItsLimits(t *testing.T) {
	// A class that owns its own kind: without a depth guard this recurses forever.
	m := Model{
		ID: "m", Name: "Tree",
		Classes: []Class{{ID: "c1", Name: "Node", Stereotype: StereotypeBusinessObject,
			Attributes: []Attribute{{Name: "label", Type: TypeString, Multiplicity: MultOne}}}},
		Associations: []Association{{ID: "a1", Kind: KindComposition,
			From: End{ClassID: "c1", Multiplicity: MultOne},
			To:   End{ClassID: "c1", Role: "child", Multiplicity: MultOptional}}},
	}
	deep := `{"label":"1","child":{"label":"2","child":{"label":"3","child":{"label":"4","child":{"label":"5","child":{"label":"6","child":{"label":"7"}}}}}}}`
	g := ObjectGraph([]ObjectValue{obj("root", "Node", "", deep)}, NewVocabulary([]Model{m}))
	if len(g.Nodes) > maxObjectDepth+1 {
		t.Errorf("nodes = %d, deeper than the guard allows", len(g.Nodes))
	}
	if !g.Truncated {
		t.Error("a graph that stopped early does not say so")
	}
}

// TestObjectGraphRendersEveryValueShape covers the renderings a diagram has to be
// able to show: a number kept exact (a business key is routinely one, and matching
// it as a float would lose digits), a boolean, and a structure the model has no
// class for.
func TestObjectGraphRendersEveryValueShape(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "total", Type: TypeNumber, Multiplicity: MultOptional},
		Attribute{Name: "urgent", Type: TypeBoolean, Multiplicity: MultOptional},
		Attribute{Name: "tags", Type: TypeString, Multiplicity: MultMany})
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "", `{"id":"ORD-1","total":10000000000000000001,"urgent":false,"tags":["a","b"]}`),
	}, NewVocabulary([]Model{m}))

	n, _ := nodeByID(g, "order")
	byName := map[string]ObjectAttribute{}
	for _, a := range n.Attributes {
		byName[a.Name] = a
	}
	if got := byName["total"].Value; got != "10000000000000000001" {
		t.Errorf("total = %q — a number lost digits on the way to the diagram", got)
	}
	if byName["urgent"].Value != "false" {
		t.Errorf("urgent = %q, want false", byName["urgent"].Value)
	}
	if byName["tags"].Value != `["a","b"]` {
		t.Errorf("tags = %q, want the list as JSON", byName["tags"].Value)
	}
}

// TestObjectGraphTypedObjectHoldingAScalar covers a class-typed object whose value
// is not a structure at all — an association wrote a bare number into it. There is
// no attribute list to show, so the node shows what it holds.
func TestObjectGraphTypedObjectHoldingAScalar(t *testing.T) {
	g := ObjectGraph([]ObjectValue{obj("order", "Order", "approved", `42`)}, salesModel(t))
	n, ok := nodeByID(g, "order")
	if !ok {
		t.Fatalf("nodes = %+v", g.Nodes)
	}
	if n.Class != "Order" || n.Value != "42" || len(n.Attributes) != 0 {
		t.Errorf("node = %+v, want a classed node showing its bare value", n)
	}
}

// TestObjectGraphMultiValuedReference covers a member holding several keys: each
// one is matched on its own, so one object can link to several.
func TestObjectGraphMultiValuedReference(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "customers", Type: TypeString, Multiplicity: MultMany})
	m.Associations[0].From.Role = "customers"
	g := ObjectGraph([]ObjectValue{
		obj("order", "Order", "", `{"id":"ORD-1","customers":["C-1","C-9"]}`),
		obj("a", "Customer", "", `{"number":"C-1"}`),
		obj("b", "Customer", "", `{"number":"C-2"}`),
	}, NewVocabulary([]Model{m}))

	if _, ok := linkBetween(g, "order", "a"); !ok {
		t.Errorf("the matching reference was not linked: %+v", g.Links)
	}
	if len(g.Unresolved) != 1 || g.Unresolved[0].Value != "C-9" {
		t.Errorf("unresolved = %+v, want only the key nothing here carries", g.Unresolved)
	}
}

// TestObjectGraphReverseReference pins that an association is readable from both
// ends: the From end's role is a member of the To class, so a Customer holding its
// order's key links too.
func TestObjectGraphReverseReference(t *testing.T) {
	m := orderModel()
	m.Classes[0].Attributes = append(m.Classes[0].Attributes,
		Attribute{Name: "lastOrder", Type: TypeString, Multiplicity: MultOptional})
	// The To end's role is the member on the From class (Customer).
	m.Associations[0].To.Role = "lastOrder"
	g := ObjectGraph([]ObjectValue{
		obj("buyer", "Customer", "", `{"number":"C-1","lastOrder":"ORD-1"}`),
		obj("order", "Order", "", `{"id":"ORD-1"}`),
	}, NewVocabulary([]Model{m}))

	l, ok := linkBetween(g, "buyer", "order")
	if !ok {
		t.Fatalf("links = %+v", g.Links)
	}
	if l.Label != "lastOrder" || l.Via != ViaKey {
		t.Errorf("link = %+v", l)
	}
}

// TestObjectGraphGeneralizationIsNotALine covers what an object diagram does *not*
// draw: "is a kind of" is a statement about types, not a relationship between two
// objects, so it never becomes a line here.
func TestObjectGraphGeneralizationIsNotALine(t *testing.T) {
	m := orderModel()
	m.Classes = append(m.Classes, Class{ID: "c6", Name: "ExpressOrder", Stereotype: StereotypeBusinessObject,
		Attributes: []Attribute{{Name: "deliverBy", Type: TypeDate, Multiplicity: MultOne}}})
	m.Associations = append(m.Associations, Association{ID: "g1", Kind: KindGeneralization,
		From: End{ClassID: "c6", Role: "special"}, To: End{ClassID: "c2", Role: "general"}})
	g := ObjectGraph([]ObjectValue{
		obj("rush", "ExpressOrder", "", `{"id":"ORD-9","deliverBy":"2026-09-03"}`),
		obj("order", "Order", "", `{"id":"ORD-1"}`),
	}, NewVocabulary([]Model{m}))
	if len(g.Links) != 0 || len(g.Unresolved) != 0 {
		t.Errorf("a generalization produced %+v / %+v", g.Links, g.Unresolved)
	}
	// The specialization still shows what it inherits.
	n, _ := nodeByID(g, "rush")
	var names []string
	for _, a := range n.Attributes {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "id,placedOn,status,shipTo,deliverBy" {
		t.Errorf("attributes = %v, want the inherited members first", names)
	}
}

// TestObjectGraphRefusesWhatItCannotRead covers a value that is not JSON at all: it
// is treated as unset rather than crashing the picture, because a diagram is a
// read-only view and a broken datum is not worth losing the rest of it over.
func TestObjectGraphRefusesWhatItCannotRead(t *testing.T) {
	g := ObjectGraph([]ObjectValue{
		{Name: "order", Class: "Order", Value: json.RawMessage("{not json")},
		{Name: "empty", Class: "Order", Value: json.RawMessage("null")},
	}, salesModel(t))
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %+v", g.Nodes)
	}
	for _, n := range g.Nodes {
		if !n.Unset {
			t.Errorf("node %s is not marked unset: %+v", n.ID, n)
		}
	}
}

// TestObjectGraphNodeCapStopsAHairball covers the other guard: an instance carrying
// a very long list is truncated rather than drawn as a picture nobody can read.
func TestObjectGraphNodeCapStopsAHairball(t *testing.T) {
	m := orderModel()
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < maxObjectNodes+50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"quantity":1}`)
	}
	b.WriteString("]")
	g := ObjectGraph([]ObjectValue{obj("lines", "OrderLine", "", b.String())}, NewVocabulary([]Model{m}))
	if len(g.Nodes) != maxObjectNodes {
		t.Errorf("nodes = %d, want the cap %d", len(g.Nodes), maxObjectNodes)
	}
	if !g.Truncated {
		t.Error("a capped graph does not say it is a part of one")
	}
}

// TestObjectGraphEmptyInstance covers an instance with no data objects: an empty
// picture, not a nil one, so a caller renders "nothing here" rather than crashing.
func TestObjectGraphEmptyInstance(t *testing.T) {
	g := ObjectGraph(nil, salesModel(t))
	if g.Nodes == nil || g.Links == nil || g.Unresolved == nil {
		t.Errorf("graph = %+v, want empty lists rather than nil", g)
	}
	if len(g.Nodes) != 0 {
		t.Errorf("nodes = %+v", g.Nodes)
	}
}
