package infomodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustSchema(t *testing.T, m Model, class string) (map[string]any, Projection) {
	t.Helper()
	p, err := SchemaFor(m, class)
	if err != nil {
		t.Fatalf("SchemaFor(%s): %v", class, err)
	}
	raw, err := json.Marshal(p.Schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return out, p
}

func lossAreas(p Projection) string {
	var b strings.Builder
	for _, n := range p.Loss {
		b.WriteString(n.Area)
		b.WriteString("|")
	}
	return b.String()
}

// TestSchemaProjectsAttributes is the core of the projection: a class becomes an
// object schema whose properties are its attributes, typed and required exactly as
// their multiplicity says.
func TestSchemaProjectsAttributes(t *testing.T) {
	schema, _ := mustSchema(t, orderModel(), "Order")

	if got := schema["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v, want draft 2020-12", got)
	}
	if got := schema["title"]; got != "Order" {
		t.Errorf("title = %v, want Order", got)
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("no properties: %v", schema)
	}

	id, _ := props["id"].(map[string]any)
	if id["type"] != "string" {
		t.Errorf("id = %v, want a string", id)
	}
	// A date is a string with a format, not a bespoke type — that is what makes the
	// projection a *standard* schema rather than an Atlas dialect.
	placedOn, _ := props["placedOn"].(map[string]any)
	if placedOn["type"] != "string" || placedOn["format"] != "date" {
		t.Errorf("placedOn = %v, want a date-formatted string", placedOn)
	}

	// Required follows multiplicity: 1 and 1..* are required, 0..1 and 0..* are not.
	req := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		req[r.(string)] = true
	}
	if !req["id"] || !req["status"] {
		t.Errorf("required = %v, want id and status", schema["required"])
	}
	if req["placedOn"] || req["shipTo"] {
		t.Errorf("required = %v, must not contain the optional members", schema["required"])
	}
}

// TestSchemaProjectsEnumerationsAndValueTypes covers the two non-default
// stereotypes: an enumeration becomes an enum of its literals, a value type becomes
// a nested object definition.
func TestSchemaProjectsEnumerationsAndValueTypes(t *testing.T) {
	schema, _ := mustSchema(t, orderModel(), "Order")
	props := schema["properties"].(map[string]any)

	status, _ := props["status"].(map[string]any)
	ref, _ := status["$ref"].(string)
	if ref != "#/$defs/OrderStatus" {
		t.Fatalf("status = %v, want a $ref to OrderStatus", status)
	}
	defs, _ := schema["$defs"].(map[string]any)
	enum, _ := defs["OrderStatus"].(map[string]any)
	vals, _ := enum["enum"].([]any)
	if len(vals) != 3 || vals[0] != "draft" {
		t.Errorf("OrderStatus = %v, want its three literals", enum)
	}

	addr, _ := defs["Address"].(map[string]any)
	if addr == nil || addr["type"] != "object" {
		t.Errorf("Address = %v, want an object definition", addr)
	}
}

// TestSchemaProjectsCompositionAsContainment is the one relationship a tree can
// carry: a whole that owns its parts contains them, so the parts are a property of
// the whole's value. The role name is what the property is called.
func TestSchemaProjectsCompositionAsContainment(t *testing.T) {
	schema, p := mustSchema(t, orderModel(), "Order")
	props := schema["properties"].(map[string]any)

	lines, _ := props["lines"].(map[string]any)
	if lines == nil {
		t.Fatalf("composition did not project; properties = %v", props)
	}
	if lines["type"] != "array" {
		t.Errorf("lines = %v, want an array (the end is 1..*)", lines)
	}
	items, _ := lines["items"].(map[string]any)
	if items["$ref"] != "#/$defs/OrderLine" {
		t.Errorf("lines.items = %v, want a $ref to OrderLine", items)
	}
	if lines["minItems"] != float64(1) {
		t.Errorf("lines.minItems = %v, want 1 (the end is 1..*)", lines["minItems"])
	}
	// Containment is not loss, so it must not be reported as such.
	if strings.Contains(lossAreas(p), "Composition") {
		t.Errorf("composition was reported as loss: %v", p.Loss)
	}
}

// TestSchemaReportsWhatItDropped is the discipline the whole projection rests on:
// it is derived, never authored, and it states what it could not carry. A silent
// projection is the one that misleads.
func TestSchemaReportsWhatItDropped(t *testing.T) {
	_, p := mustSchema(t, orderModel(), "Order")
	areas := lossAreas(p)
	// An association is a reference between two things that exist separately; a JSON
	// document is a tree and cannot hold one.
	if !strings.Contains(areas, "Association") {
		t.Errorf("loss = %v, want the plain association reported", p.Loss)
	}
	// The business key has no JSON Schema keyword, and it is the most important thing
	// the model knows — so failing to report it would be the worst omission.
	if !strings.Contains(areas, "Business key") {
		t.Errorf("loss = %v, want the business key reported", p.Loss)
	}
	for _, n := range p.Loss {
		if n.Reason == "" {
			t.Errorf("loss note %+v does not say why", n)
		}
	}
}

// TestSchemaInlinesInheritedAttributes covers generalization: a value of the
// specific type carries the general type's members too, so the schema has them —
// and says that a validator can no longer tell the two apart.
func TestSchemaInlinesInheritedAttributes(t *testing.T) {
	m := orderModel()
	m.Classes = append(m.Classes, Class{ID: "c6", Name: "ExpressOrder", Stereotype: StereotypeBusinessObject,
		Attributes: []Attribute{{Name: "deliverBy", Type: TypeDate, Multiplicity: MultOne}}})
	m.Associations = append(m.Associations, Association{ID: "g1", Kind: KindGeneralization,
		From: End{ClassID: "c6"}, To: End{ClassID: "c2"}})
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}

	schema, p := mustSchema(t, m, "ExpressOrder")
	props := schema["properties"].(map[string]any)
	if props["deliverBy"] == nil {
		t.Error("its own attribute is missing")
	}
	if props["id"] == nil || props["status"] == nil {
		t.Errorf("inherited attributes are missing: %v", props)
	}
	req := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		req[r.(string)] = true
	}
	if !req["id"] {
		t.Error("an inherited required attribute must stay required")
	}
	if !strings.Contains(lossAreas(p), "Generalization") {
		t.Errorf("loss = %v, want the flattened hierarchy reported", p.Loss)
	}
}

// TestSchemaHandlesRecursion pins that a class that contains its own kind projects
// through $defs rather than expanding forever.
func TestSchemaHandlesRecursion(t *testing.T) {
	m := Model{
		ID: "m", Name: "Tree",
		Classes: []Class{{ID: "c1", Name: "Node", Stereotype: StereotypeBusinessObject,
			Attributes: []Attribute{{Name: "label", Type: TypeString, Multiplicity: MultOne}}}},
		Associations: []Association{{ID: "a1", Kind: KindComposition,
			From: End{ClassID: "c1", Multiplicity: MultOne},
			To:   End{ClassID: "c1", Role: "children", Multiplicity: MultMany}}},
	}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}
	schema, _ := mustSchema(t, m, "Node")
	props := schema["properties"].(map[string]any)
	children, _ := props["children"].(map[string]any)
	items, _ := children["items"].(map[string]any)
	if items["$ref"] != "#/$defs/Node" {
		t.Errorf("children.items = %v, want a $ref back to Node", items)
	}
	defs, _ := schema["$defs"].(map[string]any)
	if defs["Node"] == nil {
		t.Errorf("$defs is missing the recursive class: %v", defs)
	}
}

// TestSchemaRefusesAnUnknownOrInvalidClass keeps the projection from inventing an
// answer: it derives from a model, so it cannot project a class that is not in one,
// nor a model that does not yet make sense.
func TestSchemaRefusesAnUnknownOrInvalidClass(t *testing.T) {
	if _, err := SchemaFor(orderModel(), "Invoice"); err == nil {
		t.Error("projecting an unknown class succeeded")
	}
	m := orderModel()
	m.Classes[1].Attributes[3].Type = "Adress"
	if _, err := SchemaFor(m, "Order"); err == nil {
		t.Error("projecting an invalid model succeeded — a schema derived from a broken model is a lie")
	}
}
