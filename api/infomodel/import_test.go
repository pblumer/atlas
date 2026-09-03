package infomodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// noteFor returns the first note whose message mentions a needle, so a test can
// assert on the sentence a person reads rather than on a note's position in a list.
func noteFor(notes []ImportNote, needle string) (ImportNote, bool) {
	for _, n := range notes {
		if strings.Contains(n.Message, needle) || n.Element == needle {
			return n, true
		}
	}
	return ImportNote{}, false
}

func mustParse(t *testing.T, format string, doc string) ImportResult {
	t.Helper()
	res, err := ParseImport(format, []byte(doc))
	if err != nil {
		t.Fatalf("ParseImport(%q): %v", format, err)
	}
	if v := Validate(res.Model); !v.Valid {
		t.Fatalf("an imported model must be storable, got findings %v", findingCodes(v))
	}
	return res
}

func TestDetectImportFormat(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"json object", "  {\"classes\":[]}", ImportFormatJSON},
		{"xml declaration", "<?xml version=\"1.0\"?><uml:Model/>", ImportFormatXMI},
		{"bare element", "\n<uml:Model/>", ImportFormatXMI},
		{"neither", "Order: id, total", ""},
		{"nothing at all", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectImportFormat([]byte(tc.doc))
			if tc.want == "" {
				if ok {
					t.Fatalf("detected %q in a document that is neither", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("format = %q (%v), want %q", got, ok, tc.want)
			}
		})
	}
}

// TestImportJSONCarriesAnExportedModel is the round trip the JSON format exists for:
// what GET /models/{id} hands out — content, metadata and validation verdict alike —
// goes back in, and only the content survives.
func TestImportJSONCarriesAnExportedModel(t *testing.T) {
	exported := modelResponse{Model: orderModel(), Validation: Validate(orderModel())}
	exported.Revision = 7
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	res := mustParse(t, "", string(raw))
	if res.Format != ImportFormatJSON {
		t.Errorf("format = %q, want json", res.Format)
	}
	if len(res.Model.Classes) != 5 || len(res.Model.Associations) != 2 {
		t.Fatalf("imported %d classes / %d associations, want 5 / 2",
			len(res.Model.Classes), len(res.Model.Associations))
	}
	if res.Model.Name != "Sales" {
		t.Errorf("name = %q, want the name the document carried", res.Model.Name)
	}
	// Identity is the fact the whole model exists for; losing it on import would be
	// silent and unnoticeable until a store or a correlation asked for it.
	order, ok := res.Model.ClassByName("Order")
	if !ok || len(order.Identity) != 1 || order.Identity[0] != "id" {
		t.Fatalf("Order = %#v, want its business key preserved", order)
	}
	// Nothing that belongs to the receiving installation may ride in.
	if res.Model.ID != "" || res.Model.ApplicationID != "" || res.Model.Revision != 0 {
		t.Errorf("the import carried foreign identity: %#v", res.Model)
	}
}

// TestImportJSONStatesWhatItRefused is the discipline this whole feature rests on:
// a document from elsewhere says things the subset does not author, and every one of
// them is dropped *with a sentence*, never silently.
func TestImportJSONStatesWhatItRefused(t *testing.T) {
	doc := `{
      "name": "Foreign",
      "classes": [
        {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id","tags"],
         "attributes":[{"name":"id","type":"string","multiplicity":"1"},
                       {"name":"tags","type":"string","multiplicity":"0..*"},
                       {"name":"note","type":"string","multiplicity":"0..5"},
                       {"name":"parent","type":"Order","multiplicity":"0..1"},
                       {"name":"handler","type":"Clerk","multiplicity":"0..1"}]},
        {"id":"c2","name":"OrderStatus","stereotype":"enumeration","literals":["draft","draft","open"]},
        {"id":"c3","name":"Ledger","stereotype":"entity","attributes":[]},
        {"id":"c4","name":"","stereotype":"businessObject"}
      ],
      "associations": [
        {"id":"a1","kind":"association","from":{"classId":"c1"},"to":{"classId":"c2"}},
        {"id":"a2","kind":"dependency","from":{"classId":"c1"},"to":{"classId":"c1"}},
        {"id":"a3","kind":"association","from":{"classId":"c1"},"to":{"classId":"c9"}}
      ],
      "stores": [{"id":"s1","name":"Ledger store","class":"Ledger","mode":"write"}]
    }`

	res := mustParse(t, ImportFormatJSON, doc)
	if len(res.Model.Classes) != 2 {
		t.Fatalf("kept %d classes, want Order and OrderStatus", len(res.Model.Classes))
	}
	if len(res.Model.Associations) != 0 {
		t.Errorf("kept %d associations, want none of the three", len(res.Model.Associations))
	}
	if len(res.Model.Stores) != 0 {
		t.Errorf("kept a store whose class was dropped: %#v", res.Model.Stores)
	}

	for _, needle := range []string{
		"entity",      // a stereotype this build does not author
		"dependency",  // a relationship kind it does not author
		"c9",          // an end naming a class the document does not contain
		"enumeration", // an association pointing at one
		"0..5",        // a multiplicity outside the subset
		"Clerk",       // an attribute typed by nothing in the document
		"draft",       // the repeated literal
	} {
		if _, ok := noteFor(res.Notes, needle); !ok {
			t.Errorf("nothing was said about %q; notes = %#v", needle, res.Notes)
		}
	}

	order, _ := res.Model.ClassByName("Order")
	if len(order.Identity) != 1 || order.Identity[0] != "id" {
		t.Errorf("identity = %v, want the list-valued member dropped from the key", order.Identity)
	}
	if _, ok := attributeNamed(order, "parent"); ok {
		t.Error("an attribute typed as its own class survived")
	}
}

func attributeNamed(c *Class, name string) (Attribute, bool) {
	if c == nil {
		return Attribute{}, false
	}
	for _, a := range c.Attributes {
		if a.Name == name {
			return a, true
		}
	}
	return Attribute{}, false
}

const salesXMI = `<?xml version="1.0" encoding="UTF-8"?>
<uml:Model xmi:version="20131001" xmlns:xmi="http://www.omg.org/spec/XMI/20131001"
           xmlns:uml="http://www.eclipse.org/uml2/5.0.0/UML" xmi:id="_m" name="Sales">
  <packagedElement xmi:type="uml:Class" xmi:id="_customer" name="Customer">
    <ownedAttribute xmi:id="_cust_number" name="number" isID="true">
      <type xmi:type="uml:PrimitiveType" href="pathmap://UML_LIBRARIES/UMLPrimitiveTypes.library.uml#String"/>
      <lowerValue xmi:type="uml:LiteralInteger" value="1"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="1"/>
    </ownedAttribute>
    <ownedAttribute xmi:id="_cust_since" name="customerSince" type="_date">
      <lowerValue xmi:type="uml:LiteralInteger" value="0"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="1"/>
    </ownedAttribute>
    <ownedAttribute xmi:id="_cust_orders" name="orders" type="_order" association="_places">
      <lowerValue xmi:type="uml:LiteralInteger" value="0"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="*"/>
    </ownedAttribute>
    <ownedOperation xmi:id="_op" name="recalculate"/>
  </packagedElement>
  <packagedElement xmi:type="uml:Class" xmi:id="_record" name="Record" isAbstract="true">
    <ownedAttribute xmi:id="_rec_created" name="createdOn" type="_date"/>
  </packagedElement>
  <packagedElement xmi:type="uml:Class" xmi:id="_order" name="Order">
    <ownedAttribute xmi:id="_ord_id" name="id" isID="true" type="_string"/>
    <ownedAttribute xmi:id="_ord_status" name="status" type="_status"/>
    <ownedAttribute xmi:id="_ord_ship" name="shipTo" type="_address">
      <lowerValue xmi:type="uml:LiteralInteger" value="0"/>
    </ownedAttribute>
    <ownedAttribute xmi:id="_ord_tags" name="tags" type="_string">
      <lowerValue xmi:type="uml:LiteralInteger" value="0"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="5"/>
    </ownedAttribute>
    <generalization xmi:id="_gen" general="_record"/>
  </packagedElement>
  <packagedElement xmi:type="uml:Class" xmi:id="_line" name="OrderLine">
    <ownedAttribute xmi:id="_line_qty" name="quantity" type="_integer"/>
  </packagedElement>
  <packagedElement xmi:type="uml:DataType" xmi:id="_address" name="Address">
    <ownedAttribute xmi:id="_addr_city" name="city" type="_string"/>
  </packagedElement>
  <packagedElement xmi:type="uml:Enumeration" xmi:id="_status" name="OrderStatus">
    <ownedLiteral xmi:id="_l1" name="draft"/>
    <ownedLiteral xmi:id="_l2" name="approved"/>
  </packagedElement>
  <packagedElement xmi:type="uml:Interface" xmi:id="_iface" name="Payable"/>
  <packagedElement xmi:type="uml:PrimitiveType" xmi:id="_string" name="String"/>
  <packagedElement xmi:type="uml:PrimitiveType" xmi:id="_integer" name="Integer"/>
  <packagedElement xmi:type="uml:PrimitiveType" xmi:id="_date" name="Date"/>
  <packagedElement xmi:type="uml:Association" xmi:id="_places" memberEnd="_cust_orders _ord_customer">
    <ownedEnd xmi:id="_ord_customer" name="customer" type="_customer">
      <lowerValue xmi:type="uml:LiteralInteger" value="1"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="1"/>
    </ownedEnd>
  </packagedElement>
  <packagedElement xmi:type="uml:Association" xmi:id="_contains" memberEnd="_ord_lines _line_order">
    <ownedEnd xmi:id="_ord_lines" name="lines" type="_line">
      <lowerValue xmi:type="uml:LiteralInteger" value="1"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="*"/>
    </ownedEnd>
    <ownedEnd xmi:id="_line_order" name="order" type="_order" aggregation="composite">
      <lowerValue xmi:type="uml:LiteralInteger" value="1"/>
      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="1"/>
    </ownedEnd>
  </packagedElement>
</uml:Model>`

// TestImportXMIReadsAClassDiagram is the format a UML tool actually hands over.
func TestImportXMIReadsAClassDiagram(t *testing.T) {
	res := mustParse(t, "", salesXMI)
	if res.Format != ImportFormatXMI {
		t.Fatalf("format = %q, want xmi", res.Format)
	}
	if res.Model.Name != "Sales" {
		t.Errorf("name = %q, want the model's own name", res.Model.Name)
	}

	names := map[string]Class{}
	for _, c := range res.Model.Classes {
		names[c.Name] = c
	}
	for _, want := range []string{"Customer", "Order", "OrderLine", "Record", "Address", "OrderStatus"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("class %q is missing; got %v", want, names)
		}
	}
	if got := names["Address"].Stereotype; got != StereotypeValueType {
		t.Errorf("Address is a %q, want a value type — a uml:DataType has no identity of its own", got)
	}
	if got := names["OrderStatus"].Stereotype; got != StereotypeEnumeration {
		t.Errorf("OrderStatus is a %q, want an enumeration", got)
	}
	if got := names["OrderStatus"].Literals; len(got) != 2 || got[0] != "draft" {
		t.Errorf("literals = %v, want draft and approved", got)
	}

	customer := names["Customer"]
	number, ok := attributeNamed(&customer, "number")
	if !ok || number.Type != TypeString || number.Multiplicity != MultOne {
		t.Errorf("Customer.number = %#v, want a required string", number)
	}
	if len(customer.Identity) != 1 || customer.Identity[0] != "number" {
		t.Errorf("identity = %v, want the isID attribute to become the business key", customer.Identity)
	}
	since, ok := attributeNamed(&customer, "customerSince")
	if !ok || since.Type != TypeDate || since.Multiplicity != MultOptional {
		t.Errorf("Customer.customerSince = %#v, want an optional date", since)
	}
	// An ownedAttribute that is an association end belongs to the association, not to
	// the class: keeping both would state the relationship twice.
	if _, ok := attributeNamed(&customer, "orders"); ok {
		t.Error("an association end was also kept as an attribute")
	}

	line := names["OrderLine"]
	if qty, ok := attributeNamed(&line, "quantity"); !ok || qty.Type != TypeNumber {
		t.Errorf("OrderLine.quantity = %#v, want a number — UML's Integer has no separate Atlas primitive", qty)
	}

	order := names["Order"]
	// isID with no multiplicity stated: the document does say this member identifies
	// the instance, so reading it as optional would throw the business key away.
	if id, ok := attributeNamed(&order, "id"); !ok || id.Multiplicity != MultOne {
		t.Errorf("Order.id = %#v, want the identifier read as required", id)
	}
	if len(order.Identity) != 1 || order.Identity[0] != "id" {
		t.Errorf("Order identity = %v, want the business key kept", order.Identity)
	}
	if ship, ok := attributeNamed(&order, "shipTo"); !ok || ship.Type != "Address" {
		t.Errorf("Order.shipTo = %#v, want the value type by name", ship)
	}
	if st, ok := attributeNamed(&order, "status"); !ok || st.Type != "OrderStatus" {
		t.Errorf("Order.status = %#v, want the enumeration by name", st)
	}
}

func TestImportXMIReadsRelationships(t *testing.T) {
	res := mustParse(t, ImportFormatXMI, salesXMI)
	byID := map[string]string{}
	for _, c := range res.Model.Classes {
		byID[c.ID] = c.Name
	}

	kinds := map[string]Association{}
	for _, a := range res.Model.Associations {
		kinds[byID[a.From.ClassID]+" "+a.Kind+" "+byID[a.To.ClassID]] = a
	}
	places, ok := kinds["Customer association Order"]
	if !ok {
		t.Fatalf("the Customer–Order association is missing; got %v", keysOf(kinds))
	}
	if places.From.Multiplicity != MultOne || places.To.Multiplicity != MultMany {
		t.Errorf("ends = %v / %v, want 1 and 0..*", places.From.Multiplicity, places.To.Multiplicity)
	}
	if places.From.Role != "customer" || places.To.Role != "orders" {
		t.Errorf("roles = %q / %q, want the property names as roles", places.From.Role, places.To.Role)
	}
	// The composite end is the *whole*, so the line has to run Order → OrderLine.
	if _, ok := kinds["Order composition OrderLine"]; !ok {
		t.Errorf("the composition is missing or reversed; got %v", keysOf(kinds))
	}
	// A <generalization> is owned by the specific class and names the general one.
	if _, ok := kinds["Order generalization Record"]; !ok {
		t.Errorf("the generalization is missing or reversed; got %v", keysOf(kinds))
	}
}

func keysOf(m map[string]Association) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestImportXMIStatesWhatItCannotCarry(t *testing.T) {
	res := mustParse(t, ImportFormatXMI, salesXMI)
	for _, needle := range []string{
		"Payable",     // a uml:Interface — out of subset
		"recalculate", // an operation — behaviour belongs to the BPMN model
		"tags",        // 0..5, a bounded multiplicity the subset does not have
		"Record",      // abstract, which this build does not author
	} {
		if _, ok := noteFor(res.Notes, needle); !ok {
			t.Errorf("nothing was said about %q; notes = %#v", needle, res.Notes)
		}
	}
	if tags, _ := attributeNamed(classPtr(res.Model, "Order"), "tags"); tags.Multiplicity != MultMany {
		t.Errorf("tags = %q, want it widened to 0..* rather than dropped", tags.Multiplicity)
	}
}

func classPtr(m Model, name string) *Class {
	c, _ := m.ClassByName(name)
	return c
}

// TestImportXMILaysOutAModelWithNoGeometry: XMI carries the model, and the diagram
// lives in a tool-specific file beside it. A class at 0,0 is a class nobody can read,
// so the import arranges them — and says it did.
func TestImportXMILaysOutAModelWithNoGeometry(t *testing.T) {
	res := mustParse(t, ImportFormatXMI, salesXMI)
	seen := map[string]bool{}
	for _, c := range res.Model.Classes {
		at := positionKey(c.X, c.Y)
		if seen[at] {
			t.Fatalf("two classes are stacked at %s", at)
		}
		seen[at] = true
	}
	if _, ok := noteFor(res.Notes, "laid out"); !ok {
		t.Errorf("the layout was not stated; notes = %#v", res.Notes)
	}
}

func positionKey(x, y float64) string {
	return fmt.Sprintf("%.0f,%.0f", x, y)
}

func TestImportRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name, format, doc, want string
	}{
		{"malformed json", ImportFormatJSON, "{not json", "read"},
		{"malformed xml", ImportFormatXMI, "<uml:Model>", "read"},
		{"unknown format", "sql", "{}", "sql"},
		{"neither shape", "", "Order: id", "JSON"},
		{"no classes", ImportFormatJSON, `{"classes":[]}`, "no classes"},
		{"nothing uml", ImportFormatXMI, `<html><body>hi</body></html>`, "no classes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseImport(tc.format, []byte(tc.doc))
			if err == nil {
				t.Fatalf("a %s document was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestImportSanitizesEveryModelItReturns is the guarantee the store depends on: an
// import goes through the same door as a canvas write, so whatever a document says,
// what comes back is storable.
func TestImportSanitizesEveryModelItReturns(t *testing.T) {
	docs := []string{
		`{"classes":[{"name":"A","stereotype":"businessObject","identity":["missing"]}]}`,
		`{"classes":[{"name":"A","stereotype":"enumeration","literals":["x"],
		  "attributes":[{"name":"n","type":"string","multiplicity":"1"}]}]}`,
		`{"classes":[{"name":"A","stereotype":"enumeration"},{"name":"B","stereotype":"businessObject"}]}`,
		`{"classes":[{"id":"c1","name":"A","stereotype":"valueType"},{"id":"c1","name":"B","stereotype":"businessObject"}],
		  "associations":[{"id":"x","kind":"composition","from":{"classId":"c1"},"to":{"classId":"c1"}}]}`,
		`{"classes":[{"id":"c1","name":"A","stereotype":"businessObject"},{"id":"c2","name":"A","stereotype":"businessObject"}]}`,
		`{"classes":[{"id":"c1","name":"A","stereotype":"businessObject"},{"id":"c2","name":"B","stereotype":"businessObject"}],
		  "associations":[{"id":"g1","kind":"generalization","from":{"classId":"c1"},"to":{"classId":"c2"}},
		                  {"id":"g2","kind":"generalization","from":{"classId":"c2"},"to":{"classId":"c1"}}]}`,
		`{"classes":[{"id":"c1","name":"A","stereotype":"businessObject","attributes":[{"name":"k","type":"string","multiplicity":"1"}],"identity":["k"]}],
		  "stores":[{"name":"S","class":"A"},{"name":"S","class":"A"},{"name":"","class":"A"}]}`,
	}
	for _, doc := range docs {
		res, err := ParseImport(ImportFormatJSON, []byte(doc))
		if err != nil {
			continue // a document with nothing importable in it is refused, which is fine
		}
		if v := Validate(res.Model); !v.Valid {
			t.Errorf("%s\n imported to an unstorable model: %v", doc, findingCodes(v))
		}
	}
}

// logisticsXMI is the second dialect, and every difference from salesXMI above is one
// a real tool actually produces: an xmi:XMI envelope instead of a bare model, xsi:type
// instead of xmi:type, bounds as attributes instead of child literals, member ends as
// child elements, a nested package, a type reference as a child element, an applied
// stereotype, and documentation in an ownedComment. It also carries the three
// malformed relationships a hand-edited document arrives with.
const logisticsXMI = `<?xml version="1.0" encoding="UTF-8"?>
<xmi:XMI xmlns:xmi="http://www.omg.org/spec/XMI/20131001"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xmlns:uml="http://www.omg.org/spec/UML/20131001"
         xmlns:atlas="http://atlas.example/profile">
  <uml:Model xmi:id="_m" name="Logistics">
    <ownedComment xmi:id="_c1"><body>What the shipping processes move.</body></ownedComment>
    <packagedElement xsi:type="uml:Package" xmi:id="_pkg" name="Core">
      <packagedElement xsi:type="uml:Class" xmi:id="_ship" name="Shipment">
        <ownedComment xmi:id="_c2"><body>Goods on their way to a customer.</body></ownedComment>
        <ownedAttribute xmi:id="_ship_ref" name="reference" lower="1" upper="1" isID="true">
          <type xmi:idref="_string"/>
        </ownedAttribute>
        <ownedAttribute xmi:id="_ship_weight" name="weight" lower="0" upper="1" type="_real"/>
        <ownedAttribute xmi:id="_ship_dest" name="destination" type="_address"/>
        <ownedAttribute xmi:id="_ship_blank" type="_string"/>
      </packagedElement>
      <packagedElement xsi:type="uml:Class" xmi:id="_leg" name="Leg"/>
      <packagedElement xsi:type="uml:DataType" xmi:id="_address" name="Address">
        <ownedAttribute xmi:id="_addr_city" name="city" type="_string"/>
      </packagedElement>
      <packagedElement xsi:type="uml:Class" xmi:id="_EAID_9F2"/>
      <packagedElement xsi:type="uml:Class" xmi:id="_broken" name="Broken">
        <generalization xmi:id="_g0"/>
      </packagedElement>
      <packagedElement xsi:type="uml:Association" xmi:id="_ship_legs">
        <memberEnd xmi:idref="_end_legs"/>
        <memberEnd xmi:idref="_end_ship"/>
        <ownedEnd xmi:id="_end_legs" name="legs" type="_leg" lower="0" upper="*"/>
        <ownedEnd xmi:id="_end_ship" name="shipment" type="_ship" aggregation="shared" lower="1" upper="1"/>
      </packagedElement>
      <packagedElement xsi:type="uml:Association" xmi:id="_nary" name="ThreeWay" memberEnd="_end_legs _end_ship _end_third"/>
      <packagedElement xsi:type="uml:Association" xmi:id="_dangling" name="Dangling" memberEnd="_end_legs _end_missing"/>
      <packagedElement xsi:type="uml:PrimitiveType" xmi:id="_string" name="String"/>
      <packagedElement xsi:type="uml:PrimitiveType" xmi:id="_real" name="Real"/>
    </packagedElement>
  </uml:Model>
  <atlas:valueType xmi:id="_s1" base_Class="_leg"/>
</xmi:XMI>`

func TestImportXMIReadsTheOtherDialect(t *testing.T) {
	res := mustParse(t, "", logisticsXMI)
	if res.Model.Name != "Logistics" {
		t.Errorf("name = %q, want the model inside the XMI envelope", res.Model.Name)
	}
	if res.Model.Documentation != "What the shipping processes move." {
		t.Errorf("documentation = %q, want the model's ownedComment", res.Model.Documentation)
	}

	ship := classPtr(res.Model, "Shipment")
	if ship == nil {
		t.Fatalf("Shipment is missing; got %d classes", len(res.Model.Classes))
	}
	if ship.Documentation != "Goods on their way to a customer." {
		t.Errorf("Shipment documentation = %q, want its ownedComment", ship.Documentation)
	}
	if ref, ok := attributeNamed(ship, "reference"); !ok || ref.Type != TypeString || ref.Multiplicity != MultOne {
		t.Errorf("Shipment.reference = %#v, want a required string read through <type xmi:idref>", ref)
	}
	if len(ship.Identity) != 1 || ship.Identity[0] != "reference" {
		t.Errorf("identity = %v, want the business key", ship.Identity)
	}
	if w, ok := attributeNamed(ship, "weight"); !ok || w.Type != TypeNumber || w.Multiplicity != MultOptional {
		t.Errorf("Shipment.weight = %#v, want an optional number read from lower/upper attributes", w)
	}
	if dest, ok := attributeNamed(ship, "destination"); !ok || dest.Type != "Address" {
		t.Errorf("Shipment.destination = %#v, want the value type", dest)
	}
	// An applied «valueType» stereotype overrides the metaclass it sits on.
	if leg := classPtr(res.Model, "Leg"); leg == nil || leg.Stereotype != StereotypeValueType {
		t.Errorf("Leg = %#v, want the applied stereotype to win", leg)
	}

	byID := map[string]string{}
	for _, c := range res.Model.Classes {
		byID[c.ID] = c.Name
	}
	if len(res.Model.Associations) != 1 {
		t.Fatalf("kept %d relationships, want only the aggregation", len(res.Model.Associations))
	}
	only := res.Model.Associations[0]
	if only.Kind != KindAggregation || byID[only.From.ClassID] != "Shipment" || byID[only.To.ClassID] != "Leg" {
		t.Errorf("relationship = %s %s %s, want Shipment aggregation Leg",
			byID[only.From.ClassID], only.Kind, byID[only.To.ClassID])
	}

	for _, needle := range []string{
		"Core",         // the package that was flattened
		"ThreeWay",     // three ends
		"_end_missing", // an end the document never defines
		"Broken",       // a generalization naming no general class
		"_EAID_9F2",    // a class with no name
	} {
		if _, ok := noteFor(res.Notes, needle); !ok {
			t.Errorf("nothing was said about %q; notes = %#v", needle, res.Notes)
		}
	}
	if _, ok := noteFor(res.Notes, "attribute with no name"); !ok {
		t.Errorf("the unnamed attribute went unreported; notes = %#v", res.Notes)
	}
}

// TestImportJSONAcceptsAPreviewItPrinted: the import response carries the model under
// a "model" key, and somebody who saved that file hands it straight back.
func TestImportJSONAcceptsAPreviewItPrinted(t *testing.T) {
	first := mustParse(t, ImportFormatXMI, salesXMI)
	raw, err := json.Marshal(ImportResult{Format: first.Format, Model: first.Model, Notes: first.Notes})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	again := mustParse(t, "", string(raw))
	if len(again.Model.Classes) != len(first.Model.Classes) {
		t.Errorf("re-import = %d classes, want %d", len(again.Model.Classes), len(first.Model.Classes))
	}
}

// TestImportCapsItsAccount: a document can be wrong about ten thousand things, and an
// answer that lists all of them is unreadable. The tail is counted, not printed.
func TestImportCapsItsAccount(t *testing.T) {
	var doc strings.Builder
	doc.WriteString(`<uml:Model xmlns:xmi="http://www.omg.org/spec/XMI/20131001" xmlns:uml="http://www.omg.org/spec/UML/20131001" name="Big">`)
	doc.WriteString(`<packagedElement xmi:type="uml:Class" xmi:id="_c" name="Kept"/>`)
	for i := 0; i < maxImportNotes+40; i++ {
		fmt.Fprintf(&doc, `<packagedElement xmi:type="uml:Interface" xmi:id="_i%d" name="Iface%d"/>`, i, i)
	}
	doc.WriteString(`</uml:Model>`)

	res := mustParse(t, ImportFormatXMI, doc.String())
	if len(res.Notes) != maxImportNotes+1 {
		t.Fatalf("notes = %d, want the cap plus the line that counts the rest", len(res.Notes))
	}
	last := res.Notes[len(res.Notes)-1]
	if !strings.Contains(last.Message, "further notes were not listed") {
		t.Errorf("last note = %q, want it to count what it did not print", last.Message)
	}
}

func TestNormalizeMultiplicityKeepsWhatItCan(t *testing.T) {
	cases := []struct {
		raw, want string
		note      bool
	}{
		{"", MultOptional, false},
		{"1", MultOne, false},
		{"0..*", MultMany, false},
		{"0..5", MultMany, true},
		{"2..7", MultAtLeast1, true},
		{"*", MultMany, true},
		{"many", MultOptional, true},
		{"1..x", MultOptional, true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			notes := &noteList{}
			if got := normalizeMultiplicity(tc.raw, "A.b", notes); got != tc.want {
				t.Errorf("normalizeMultiplicity(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			if tc.note != (len(notes.notes) > 0) {
				t.Errorf("notes = %#v, want a note: %v", notes.notes, tc.note)
			}
		})
	}
}

func TestNormalizeEndMultiplicityMaySayNothing(t *testing.T) {
	notes := &noteList{}
	if got := normalizeEndMultiplicity("", "A → B", notes); got != "" {
		t.Errorf("an unstated end multiplicity became %q; an end may leave it unsaid", got)
	}
	if got := normalizeEndMultiplicity("3..9", "A → B", notes); got != MultAtLeast1 {
		t.Errorf("3..9 = %q, want 1..*", got)
	}
	if got := normalizeEndMultiplicity("some", "A → B", notes); got != "" {
		t.Errorf("an unreadable end multiplicity became %q, want it left unsaid", got)
	}
	if len(notes.notes) != 2 {
		t.Errorf("notes = %#v, want one for each end that changed", notes.notes)
	}
}

// TestImportJSONDefaultsAndDrops is the hand-edited document: every field somebody
// leaves out or gets wrong, in one file, and what each of them becomes.
func TestImportJSONDefaultsAndDrops(t *testing.T) {
	doc := `{
      "name": "Mixed",
      "classes": [
        {"id":"c1","name":"Order","literals":["stray"],"x":100,"y":50,
         "attributes":[{"name":"id","type":"string","multiplicity":"1"},
                       {"name":"","type":"string"},
                       {"name":"id","type":"number"},
                       {"name":"note"},
                       {"name":"lines","type":"string","multiplicity":"0..*"}],
         "identity":["id","","lines"]},
        {"id":"c2","name":"Money","stereotype":"valueType","x":400,"y":50,
         "identity":["amount"],
         "attributes":[{"name":"amount","type":"number","multiplicity":"1"}]},
        {"id":"c3","name":"Status","stereotype":"enumeration","literals":["draft","  "],"x":700,"y":50}
      ],
      "associations": [
        {"id":"a1","from":{"classId":"c1"},"to":{"classId":"c2"}},
        {"id":"a2","kind":"generalization","from":{"classId":"c1"},"to":{"classId":"c1"}}
      ],
      "stores": [
        {"id":"s1","name":"Orders","class":"Order","mode":"write"},
        {"id":"s2","name":"Ledger","class":"Order"},
        {"id":"s2","name":"Archive","class":"Order"}
      ]
    }`

	res := mustParse(t, ImportFormatJSON, doc)
	order := classPtr(res.Model, "Order")
	if order == nil || order.Stereotype != StereotypeBusinessObject {
		t.Fatalf("Order = %#v, want a class with no stated kind read as a business object", order)
	}
	if len(order.Literals) != 0 {
		t.Errorf("Order kept literals: %v", order.Literals)
	}
	if len(order.Attributes) != 3 {
		t.Errorf("Order has %d attributes, want id, note and lines", len(order.Attributes))
	}
	if note, ok := attributeNamed(order, "note"); !ok || note.Type != TypeString {
		t.Errorf("Order.note = %#v, want an untyped member read as text", note)
	}
	if len(order.Identity) != 1 || order.Identity[0] != "id" {
		t.Errorf("identity = %v, want the list-valued member out of the key", order.Identity)
	}
	if money := classPtr(res.Model, "Money"); money == nil || len(money.Identity) != 0 {
		t.Errorf("Money = %#v, want a value type with no business key", money)
	}
	if status := classPtr(res.Model, "Status"); status == nil || len(status.Literals) != 1 {
		t.Errorf("Status = %#v, want the blank literal dropped", status)
	}

	if len(res.Model.Associations) != 1 || res.Model.Associations[0].Kind != KindAssociation {
		t.Errorf("associations = %#v, want the unstated kind read as an association and the self-generalization dropped", res.Model.Associations)
	}
	if len(res.Model.Stores) != 2 {
		t.Errorf("stores = %#v, want the write-mode one dropped and the shared id re-issued", res.Model.Stores)
	}
	// A document that placed its classes keeps its layout: an import must not
	// rearrange a diagram somebody arranged.
	if order.X != 100 || order.Y != 50 {
		t.Errorf("Order sits at %v,%v, want the document's own placement", order.X, order.Y)
	}
	if _, ok := noteFor(res.Notes, "laid out"); ok {
		t.Error("a placed model was laid out anyway")
	}
}
