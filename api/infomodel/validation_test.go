package infomodel

import (
	"strings"
	"testing"
)

// orderModel is a small, valid model: a Customer places Orders, an Order owns its
// Lines and carries an Address value and a Status enumeration.
func orderModel() Model {
	return Model{
		ID: "m1", ApplicationID: "app", Name: "Sales",
		Classes: []Class{
			{ID: "c1", Name: "Customer", Stereotype: StereotypeBusinessObject, Identity: []string{"number"},
				Attributes: []Attribute{
					{Name: "number", Type: TypeString, Multiplicity: MultOne},
					{Name: "name", Type: TypeString, Multiplicity: MultOne},
				}},
			{ID: "c2", Name: "Order", Stereotype: StereotypeBusinessObject, Identity: []string{"id"},
				Attributes: []Attribute{
					{Name: "id", Type: TypeString, Multiplicity: MultOne},
					{Name: "placedOn", Type: TypeDate, Multiplicity: MultOptional},
					{Name: "status", Type: "OrderStatus", Multiplicity: MultOne},
					{Name: "shipTo", Type: "Address", Multiplicity: MultOptional},
				}},
			{ID: "c3", Name: "OrderLine", Stereotype: StereotypeBusinessObject,
				Attributes: []Attribute{{Name: "quantity", Type: TypeNumber, Multiplicity: MultOne}}},
			{ID: "c4", Name: "Address", Stereotype: StereotypeValueType,
				Attributes: []Attribute{{Name: "city", Type: TypeString, Multiplicity: MultOne}}},
			{ID: "c5", Name: "OrderStatus", Stereotype: StereotypeEnumeration,
				Literals: []string{"draft", "approved", "rejected"}},
		},
		Associations: []Association{
			{ID: "a1", Kind: KindAssociation, Name: "places",
				From: End{ClassID: "c1", Role: "customer", Multiplicity: MultOne},
				To:   End{ClassID: "c2", Role: "orders", Multiplicity: MultMany}},
			{ID: "a2", Kind: KindComposition,
				From: End{ClassID: "c2", Role: "order", Multiplicity: MultOne},
				To:   End{ClassID: "c3", Role: "lines", Multiplicity: MultAtLeast1}},
		},
	}
}

// findingCodes returns the codes of a validation result, for terse assertions.
func findingCodes(res ValidationResult) []string {
	out := []string{}
	for _, f := range res.Findings {
		out = append(out, f.Code)
	}
	return out
}

func hasCode(res ValidationResult, code string) bool {
	for _, f := range res.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// TestValidateAcceptsAWellFormedModel is the baseline: the model above says
// nothing contradictory and must pass clean.
func TestValidateAcceptsAWellFormedModel(t *testing.T) {
	res := Validate(orderModel())
	if !res.Valid {
		t.Fatalf("a well-formed model was rejected: %v", findingCodes(res))
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings on a clean model: %+v", res.Findings)
	}
}

// TestValidateNamesMustBeUniqueAndUsable covers the rule that makes
// `itemSubjectRef` resolvable at all: a class name is how a BPMN data object names
// its type, so two classes sharing one would make the reference ambiguous.
func TestValidateNamesMustBeUniqueAndUsable(t *testing.T) {
	m := orderModel()
	m.Classes = append(m.Classes, Class{ID: "c6", Name: "Order", Stereotype: StereotypeBusinessObject})
	if res := Validate(m); !hasCode(res, CodeDuplicateClassName) {
		t.Errorf("a duplicate class name was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Classes[0].Name = ""
	if res := Validate(m); !hasCode(res, CodeMissingClassName) {
		t.Errorf("a nameless class was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "id", Type: TypeString, Multiplicity: MultOne})
	if res := Validate(m); !hasCode(res, CodeDuplicateAttribute) {
		t.Errorf("a duplicate attribute name was accepted: %v", findingCodes(res))
	}
}

// TestValidateResolvesAttributeTypes is the check that turns a drawing into a
// model: an attribute typed as a class Atlas cannot find is the modeling mistake
// this catches, and the finding has to name what it looked for.
func TestValidateResolvesAttributeTypes(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes[3].Type = "Adress" // a typo for Address
	res := Validate(m)
	if !hasCode(res, CodeUnknownType) {
		t.Fatalf("an unresolvable attribute type was accepted: %v", findingCodes(res))
	}
	var msg string
	for _, f := range res.Findings {
		if f.Code == CodeUnknownType {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "Adress") {
		t.Errorf("finding %q does not name the type it could not resolve", msg)
	}
}

// TestValidateIdentityRules covers the business key — the part BPMN has no
// equivalent for and the part every cross-process capability rests on. It must
// name real attributes, those attributes must always be present and single, and
// only a class whose instances have an identity may declare one.
func TestValidateIdentityRules(t *testing.T) {
	m := orderModel()
	m.Classes[1].Identity = []string{"orderNumber"} // no such attribute
	if res := Validate(m); !hasCode(res, CodeUnknownIdentityAttribute) {
		t.Errorf("an identity naming no attribute was accepted: %v", findingCodes(res))
	}

	// A key that may be absent identifies nothing.
	m = orderModel()
	m.Classes[1].Attributes[0].Multiplicity = MultOptional
	if res := Validate(m); !hasCode(res, CodeIdentityNotRequired) {
		t.Errorf("an optional attribute was accepted as a business key: %v", findingCodes(res))
	}

	// A key that is a list identifies nothing either.
	m = orderModel()
	m.Classes[1].Attributes[0].Multiplicity = MultAtLeast1
	if res := Validate(m); !hasCode(res, CodeIdentityNotSingular) {
		t.Errorf("a collection attribute was accepted as a business key: %v", findingCodes(res))
	}

	// A value type has no identity of its own to declare.
	m = orderModel()
	m.Classes[3].Identity = []string{"city"}
	if res := Validate(m); !hasCode(res, CodeIdentityNotAllowed) {
		t.Errorf("a value type declared a business key: %v", findingCodes(res))
	}
}

// TestValidateEnumerationShape pins that an enumeration carries literals and not
// attributes, and that the literals are a real set.
func TestValidateEnumerationShape(t *testing.T) {
	m := orderModel()
	m.Classes[4].Attributes = []Attribute{{Name: "code", Type: TypeString, Multiplicity: MultOne}}
	if res := Validate(m); !hasCode(res, CodeEnumerationHasAttributes) {
		t.Errorf("an enumeration with attributes was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Classes[4].Literals = nil
	if res := Validate(m); !hasCode(res, CodeEnumerationEmpty) {
		t.Errorf("an empty enumeration was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Classes[4].Literals = []string{"draft", "draft"}
	if res := Validate(m); !hasCode(res, CodeDuplicateLiteral) {
		t.Errorf("a repeated literal was accepted: %v", findingCodes(res))
	}
}

// TestValidateAppliesTheSubsetMatrix checks that the one table in subset.go is
// what decides a write, so the canvas and the server cannot disagree — and that a
// refusal keeps "out of subset" apart from "the notation says no".
func TestValidateAppliesTheSubsetMatrix(t *testing.T) {
	m := orderModel()
	// Address is a value type; it cannot be the whole that owns parts.
	m.Associations[1].From.ClassID = "c4"
	res := Validate(m)
	if !hasCode(res, CodeRelationshipRefused) {
		t.Fatalf("the matrix was not applied: %v", findingCodes(res))
	}
	for _, f := range res.Findings {
		if f.Code == CodeRelationshipRefused && f.Reason != RefusedByNotation {
			t.Errorf("reason = %q, want %q", f.Reason, RefusedByNotation)
		}
	}
}

// TestValidateRelationshipIntegrity covers the checks that are about a particular
// model rather than the notation: ends that name no class, a class related to
// itself, and a generalization that closes a cycle.
func TestValidateRelationshipIntegrity(t *testing.T) {
	m := orderModel()
	m.Associations[0].To.ClassID = "nope"
	if res := Validate(m); !hasCode(res, CodeUnknownClassRef) {
		t.Errorf("an association end naming no class was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Associations[0].From.ClassID = m.Associations[0].To.ClassID
	m.Associations[0].Kind = KindGeneralization
	if res := Validate(m); !hasCode(res, CodeSelfGeneralization) {
		t.Errorf("a class specializing itself was accepted: %v", findingCodes(res))
	}

	// A → B → A. Neither link is wrong on its own; the pair is.
	m = orderModel()
	m.Associations = []Association{
		{ID: "g1", Kind: KindGeneralization, From: End{ClassID: "c2"}, To: End{ClassID: "c3"}},
		{ID: "g2", Kind: KindGeneralization, From: End{ClassID: "c3"}, To: End{ClassID: "c2"}},
	}
	if res := Validate(m); !hasCode(res, CodeGeneralizationCycle) {
		t.Errorf("a generalization cycle was accepted: %v", findingCodes(res))
	}
}

// TestValidateRejectsValuesOutsideTheSubset covers the tables: a multiplicity or a
// stereotype this build does not author is refused as out of subset, not as a
// modeling error.
func TestValidateRejectsValuesOutsideTheSubset(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes[0].Multiplicity = "2..7"
	res := Validate(m)
	if !hasCode(res, CodeUnknownMultiplicity) {
		t.Fatalf("an unsupported multiplicity was accepted: %v", findingCodes(res))
	}
	for _, f := range res.Findings {
		if f.Code == CodeUnknownMultiplicity && f.Reason != RefusedOutOfSubset {
			t.Errorf("reason = %q, want %q — UML allows 2..7; this build does not", f.Reason, RefusedOutOfSubset)
		}
	}

	m = orderModel()
	m.Classes[0].Stereotype = "interface"
	if res := Validate(m); !hasCode(res, CodeUnknownStereotype) {
		t.Errorf("an unsupported stereotype was accepted: %v", findingCodes(res))
	}
}

// TestValidateFindingsLocateThemselves checks every finding names where it is, so
// the canvas can mark the class or association it belongs to instead of showing a
// list a modeler has to search by hand.
func TestValidateFindingsLocateThemselves(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes[3].Type = "Adress"
	m.Associations[1].From.ClassID = "c4"
	res := Validate(m)
	if len(res.Findings) < 2 {
		t.Fatalf("expected findings on both, got %v", findingCodes(res))
	}
	for _, f := range res.Findings {
		if f.ClassID == "" && f.AssociationID == "" {
			t.Errorf("finding %+v does not say where it is", f)
		}
		if f.Message == "" {
			t.Errorf("finding %+v has no message", f)
		}
	}
}
