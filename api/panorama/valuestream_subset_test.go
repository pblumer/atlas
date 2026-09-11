package panorama

import "testing"

// A ValueStream was readable but not drawable: the validator accepted one in an
// imported document, and the palette offered no way to create one. That is the worst
// of the three states an element can be in — an architect could open a model
// containing value streams, edit around them, and never be able to add another.
//
// It is the second half of what makes a drawing and the capability register one
// architecture (ADR-0305): a value stream is the ordered activity that meets a
// customer need, and its stages name the capabilities that perform them. Binding a
// key on an element nobody can draw would be a contract with no author.

// TestValueStreamIsAuthorable is the gap, stated.
func TestValueStreamIsAuthorable(t *testing.T) {
	var found *ElementKind
	for _, kind := range AuthorableElements() {
		if kind.Type == "ValueStream" {
			k := kind
			found = &k
		}
	}
	if found == nil {
		t.Fatal("the subset does not author ValueStream, so atlas.valueStreamKey binds an element nobody can draw")
	}
	// Strategy layer and behaviour aspect, which is where ArchiMate 3.2 puts it and
	// where Capability already sits. The rules below are predicates over layer and
	// aspect rather than a table of type pairs, so these two fields are the whole of
	// what decides every relationship a value stream can carry — getting them wrong
	// would not fail loudly, it would quietly permit a different matrix.
	if found.Layer != LayerStrategy {
		t.Errorf("ValueStream layer = %q, want %q", found.Layer, LayerStrategy)
	}
	if found.Aspect != AspectBehavior {
		t.Errorf("ValueStream aspect = %q, want %q", found.Aspect, AspectBehavior)
	}
}

// The validator already accepted ValueStream in a document it read. This asserts the
// two halves now agree, because "readable but not drawable" is precisely the state
// this slice closes and the one a later edit could reopen by removing the palette
// entry without touching the validator.
func TestValueStreamIsBothReadableAndDrawable(t *testing.T) {
	if !archiMateElementTypes["ValueStream"] {
		t.Error("the validator no longer accepts ValueStream in a document")
	}
	if _, ok := kindByType["ValueStream"]; !ok {
		t.Error("the palette no longer offers ValueStream")
	}
}

// A value stream and a capability are both strategy-layer behaviour, so the matrix
// treats them alike — and that is the point rather than a coincidence: the method
// says a value stream's stages *are* capabilities, and composition between them is
// how a drawing says so.
func TestValueStreamConnectsLikeACapability(t *testing.T) {
	for _, tc := range []struct {
		relationship, source, target string
		want                         bool
		why                          string
	}{
		{"Composition", "ValueStream", "Capability", true,
			"a value stream is composed of the capabilities its stages perform"},
		{"Aggregation", "ValueStream", "Capability", true,
			"a capability may serve several value streams, which is what aggregation is for"},
		{"Triggering", "ValueStream", "ValueStream", true,
			"one piece of behaviour causes another"},
		{"Realization", "BusinessProcess", "ValueStream", true,
			"a lower layer realizes a higher one: the business process makes the strategy real"},
		{"Realization", "ValueStream", "BusinessProcess", false,
			"realization does not run uphill — strategy does not realize a business process"},
		{"Access", "ValueStream", "BusinessObject", true,
			"access is behaviour reading or writing something passive, and the rule carries no layer constraint — it says exactly the same of a capability"},
	} {
		got, refusal := MayConnect(tc.relationship, tc.source, tc.target)
		if got != tc.want {
			t.Errorf("MayConnect(%s, %s → %s) = %v, want %v: %s (refusal: %+v)",
				tc.relationship, tc.source, tc.target, got, tc.want, tc.why, refusal)
		}
	}
}

// Whatever the matrix says about a value stream, it must say the same about a
// capability: both are strategy-layer behaviour, so any difference between them is a
// bug in one of the two palette entries rather than a statement about the notation.
// Checked across every drawable relationship and every authorable partner, in both
// directions, because a single wrong field would show up as exactly one asymmetric
// pair and nothing else.
func TestValueStreamAndCapabilityAreTreatedAlike(t *testing.T) {
	for _, rel := range DrawableRelationships() {
		for _, other := range AuthorableElements() {
			if other.Type == "ValueStream" || other.Type == "Capability" {
				continue // Specialization is type equality; comparing the pair to itself proves nothing.
			}
			vsOut, _ := MayConnect(rel.Type, "ValueStream", other.Type)
			capOut, _ := MayConnect(rel.Type, "Capability", other.Type)
			if vsOut != capOut {
				t.Errorf("%s: ValueStream → %s is %v but Capability → %s is %v",
					rel.Type, other.Type, vsOut, other.Type, capOut)
			}
			vsIn, _ := MayConnect(rel.Type, other.Type, "ValueStream")
			capIn, _ := MayConnect(rel.Type, other.Type, "Capability")
			if vsIn != capIn {
				t.Errorf("%s: %s → ValueStream is %v but %s → Capability is %v",
					rel.Type, other.Type, vsIn, other.Type, capIn)
			}
		}
	}
}
