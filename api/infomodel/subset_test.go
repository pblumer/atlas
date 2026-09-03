package infomodel

import "testing"

// TestSubsetRefusesEnumerationInAssociations pins the rule that most separates
// this metamodel from a box-and-line drawing tool: an «enumeration» is a closed
// set of values, not a thing with its own existence, so it appears as an
// attribute's *type* and never at the end of an association. Refusing it is a
// statement about what an enumeration is, not a limit of this build — and the
// refusal has to say which of the two it is.
func TestSubsetRefusesEnumerationInAssociations(t *testing.T) {
	for _, kind := range AssociationKinds() {
		ok, refusal := AllowAssociation(kind.Kind, StereotypeEnumeration, StereotypeBusinessObject)
		if ok {
			t.Errorf("%s from an enumeration was allowed", kind.Kind)
			continue
		}
		if refusal.Reason != RefusedByNotation {
			t.Errorf("%s from an enumeration refused as %q, want %q — UML says so, this build does not",
				kind.Kind, refusal.Reason, RefusedByNotation)
		}
		if ok, _ := AllowAssociation(kind.Kind, StereotypeBusinessObject, StereotypeEnumeration); ok {
			t.Errorf("%s to an enumeration was allowed", kind.Kind)
		}
	}
}

// TestSubsetWholePartRules covers the two relationships that say something about
// ownership: a value type has no identity of its own, so it can be a part but
// never the whole that owns parts.
func TestSubsetWholePartRules(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		from, to   string
		want       bool
		wantReason string
	}{
		{"an order owns its lines", KindComposition, StereotypeBusinessObject, StereotypeBusinessObject, true, ""},
		{"an order owns an address value", KindComposition, StereotypeBusinessObject, StereotypeValueType, true, ""},
		{"a value type owns nothing", KindComposition, StereotypeValueType, StereotypeBusinessObject, false, RefusedByNotation},
		{"a value type aggregates nothing", KindAggregation, StereotypeValueType, StereotypeBusinessObject, false, RefusedByNotation},
		{"a customer groups orders", KindAggregation, StereotypeBusinessObject, StereotypeBusinessObject, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, refusal := AllowAssociation(tt.kind, tt.from, tt.to)
			if ok != tt.want {
				t.Fatalf("AllowAssociation(%s, %s, %s) = %v, want %v", tt.kind, tt.from, tt.to, ok, tt.want)
			}
			if !ok && refusal.Reason != tt.wantReason {
				t.Errorf("refusal reason = %q, want %q", refusal.Reason, tt.wantReason)
			}
			if !ok && refusal.Message == "" {
				t.Error("a refusal with no message teaches nobody the notation")
			}
		})
	}
}

// TestSubsetGeneralizationIsBetweenLikeThings covers "is-a": a specialization and
// its general are the same kind of thing, because the specific one has to be
// usable everywhere the general one is.
func TestSubsetGeneralizationIsBetweenLikeThings(t *testing.T) {
	if ok, _ := AllowAssociation(KindGeneralization, StereotypeBusinessObject, StereotypeBusinessObject); !ok {
		t.Error("a business object may specialize a business object")
	}
	if ok, _ := AllowAssociation(KindGeneralization, StereotypeValueType, StereotypeValueType); !ok {
		t.Error("a value type may specialize a value type")
	}
	ok, refusal := AllowAssociation(KindGeneralization, StereotypeValueType, StereotypeBusinessObject)
	if ok {
		t.Fatal("a value type must not specialize a business object — it would gain an identity it cannot have")
	}
	if refusal.Reason != RefusedByNotation {
		t.Errorf("reason = %q, want %q", refusal.Reason, RefusedByNotation)
	}
}

// TestSubsetRefusesWhatIsOutOfSubset keeps "Atlas does not author this" apart
// from "UML forbids this". The two are different answers and a user acts on them
// differently: one is a feature request, the other is a modeling mistake.
func TestSubsetRefusesWhatIsOutOfSubset(t *testing.T) {
	ok, refusal := AllowAssociation("Dependency", StereotypeBusinessObject, StereotypeBusinessObject)
	if ok {
		t.Fatal("an unknown association kind was allowed")
	}
	if refusal.Reason != RefusedOutOfSubset {
		t.Errorf("reason = %q, want %q — UML has dependencies; this build does not author them",
			refusal.Reason, RefusedOutOfSubset)
	}
	if ok, refusal := AllowAssociation(KindAssociation, "Interface", StereotypeBusinessObject); ok {
		t.Error("an unknown stereotype was allowed")
	} else if refusal.Reason != RefusedOutOfSubset {
		t.Errorf("reason = %q, want %q", refusal.Reason, RefusedOutOfSubset)
	}
}

// TestAuthoringSubsetIsSelfDescribing checks the payload the browser is served:
// it must carry the palette, the kinds, the multiplicities and the primitives the
// canvas offers, and it must say in words that it is a subset. A canvas that
// carries its own copy of any of this is how a diagram gets drawn that the server
// then refuses.
func TestAuthoringSubsetIsSelfDescribing(t *testing.T) {
	s := AuthoringSubset()
	if s.Version != SubsetVersion {
		t.Errorf("Version = %d, want %d", s.Version, SubsetVersion)
	}
	if len(s.Stereotypes) == 0 || len(s.AssociationKinds) == 0 ||
		len(s.Primitives) == 0 || len(s.Multiplicities) == 0 {
		t.Fatalf("subset is missing a table: %+v", s)
	}
	if len(s.Limits) == 0 {
		t.Error("the subset must state what it does not author")
	}
	for _, l := range s.Limits {
		if l.Area == "" || l.Reason == "" {
			t.Errorf("limit %+v does not say what is limited or why", l)
		}
	}
	// Every stereotype the palette offers must be one AllowAssociation knows, or the
	// canvas can offer a shape the matrix cannot rule on.
	for _, st := range s.Stereotypes {
		if !knownStereotype(st.Stereotype) {
			t.Errorf("palette offers stereotype %q the matrix does not know", st.Stereotype)
		}
	}
}
