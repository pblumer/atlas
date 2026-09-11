package panorama

import (
	"strings"
	"testing"
)

// The business-architecture bindings (ADR-0305) are what make a drawing and the
// capability register the same architecture seen twice rather than two
// architectures. An architect draws a Capability; the register holds a record with a
// scope, an owner and SLAs; without a binding nothing connects them but a name, and a
// name is exactly what is renamed.

const capabilityDoc = `  <elements>
    <element identifier="cap-1" xsi:type="Capability">
      <name xml:lang="en">Underwrite a loan</name>
      <properties>
        <property propertyDefinitionRef="p-cap"><value>loan-underwriting</value></property>
      </properties>
    </element>
    <element identifier="vs-1" xsi:type="ValueStream">
      <name xml:lang="en">Consumer lending</name>
      <properties>
        <property propertyDefinitionRef="p-vs"><value>consumer-loan</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-cap" type="string"><name>atlas.capabilityKey</name></propertyDefinition>
    <propertyDefinition identifier="p-vs" type="string"><name>atlas.valueStreamKey</name></propertyDefinition>
  </propertyDefinitions>
`

// TestExtractBindingsReadsBusinessArchitectureKeys is the core read: the two keys
// arrive as ordinary ArchiMate properties, like every other binding.
func TestExtractBindingsReadsBusinessArchitectureKeys(t *testing.T) {
	set, err := ExtractBindings(model(capabilityDoc))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if got := bound(t, set, "cap-1", KeyCapabilityKey); len(got) != 1 || got[0] != "loan-underwriting" {
		t.Errorf("capability binding = %v, want [loan-underwriting]", got)
	}
	if got := bound(t, set, "vs-1", KeyValueStreamKey); len(got) != 1 || got[0] != "consumer-loan" {
		t.Errorf("value stream binding = %v, want [consumer-loan]", got)
	}
	if len(set.Problems) != 0 {
		t.Errorf("problems = %#v, want none", set.Problems)
	}
}

// The element pairing is part of the contract rather than a convention (ADR-0189 §4):
// a capability key on a BusinessProcess means nothing, and carrying it would let a
// nonsense binding travel to another tool looking official.
//
// The two are tested against each other's element on purpose. Both are
// strategy-layer behaviour elements and both bind a key from the same register, so
// they are the pair most likely to be made interchangeable by a later edit — and the
// one where doing so would be least visible.
func TestBusinessArchitectureKeysAreRefusedOnTheWrongElement(t *testing.T) {
	for _, tc := range []struct {
		name, elementType, key string
	}{
		{"capability key on a value stream", "ValueStream", "atlas.capabilityKey"},
		{"value stream key on a capability", "Capability", "atlas.valueStreamKey"},
		{"capability key on a business process", "BusinessProcess", "atlas.capabilityKey"},
		{"value stream key on an application component", "ApplicationComponent", "atlas.valueStreamKey"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, err := ExtractBindings(model(`  <elements>
    <element identifier="e-1" xsi:type="` + tc.elementType + `">
      <properties>
        <property propertyDefinitionRef="p-1"><value>some-key</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-1" type="string"><name>` + tc.key + `</name></propertyDefinition>
  </propertyDefinitions>
`))
			if err != nil {
				t.Fatalf("ExtractBindings: %v", err)
			}
			if len(set.Bindings) != 0 {
				t.Errorf("bindings = %#v, want none: the key is not meaningful on %s", set.Bindings, tc.elementType)
			}
			if len(set.Problems) == 0 {
				t.Fatalf("no problem reported; a refused declaration must be said out loud, not dropped")
			}
			if msg := set.Problems[0].Message; !strings.Contains(msg, tc.key) || !strings.Contains(msg, tc.elementType) {
				t.Errorf("problem = %q, want it to name both the key and the element type", msg)
			}
		})
	}
}

// TestBusinessArchitectureKeysAreInTheContract holds the two keys to the public key
// list. A key that extracts but is not in BindingKeys is a key the editor never
// offers and no client can discover — present in the parser and absent from the
// contract, which is the shape a half-landed binding takes.
func TestBusinessArchitectureKeysAreInTheContract(t *testing.T) {
	want := map[string]bool{KeyCapabilityKey: false, KeyValueStreamKey: false}
	for _, key := range BindingKeys() {
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("BindingKeys() omits %q", key)
		}
	}
}

// A binding resolves against a catalog map of its own kind. Looking a capability key
// up among processes would report every one as missing, which reads as a broken model
// rather than as a broken resolver — so each key gets its own map, and this is the
// assertion that they are wired to the right ones.
func TestCapabilityBindingsResolveAgainstTheirOwnCatalogs(t *testing.T) {
	catalog := Catalog{
		Capabilities: map[string]ResourceRef{
			"loan-underwriting": {ID: "loan-underwriting", Name: "Underwrite a loan", CanView: true},
		},
		ValueStreams: map[string]ResourceRef{
			"consumer-loan": {ID: "consumer-loan", Name: "Consumer lending", CanView: true},
		},
	}
	set, err := ExtractBindings(model(capabilityDoc))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	res := ResolveBindings(set, catalog)
	if res.Unresolved != 0 {
		t.Fatalf("unresolved = %d, want 0; both ids are in the catalog", res.Unresolved)
	}
	names := map[string]string{}
	for _, b := range res.Bindings {
		for _, v := range b.Values {
			if v.Status != StatusResolved {
				t.Errorf("%s = %s, want resolved", v.Value, v.Status)
			}
			names[v.Value] = v.Name
		}
	}
	if names["loan-underwriting"] != "Underwrite a loan" || names["consumer-loan"] != "Consumer lending" {
		t.Errorf("resolved names = %v", names)
	}
}

// A key bound to a record that is not in the register is *missing*, not unsupported:
// the resolver looked. That distinction is what sends somebody to fix a typo rather
// than to wait for a feature.
func TestCapabilityBindingToAnUnknownKeyIsMissing(t *testing.T) {
	set, err := ExtractBindings(model(capabilityDoc))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	res := ResolveBindings(set, Catalog{
		Capabilities: map[string]ResourceRef{},
		ValueStreams: map[string]ResourceRef{},
	})
	if res.Unresolved != 2 {
		t.Fatalf("unresolved = %d, want 2", res.Unresolved)
	}
	for _, b := range res.Bindings {
		for _, v := range b.Values {
			if v.Status != StatusMissing {
				t.Errorf("%s = %s, want missing — the register was read and holds no such key", v.Value, v.Status)
			}
		}
	}
}

// The mesh draws the runtime landscape: applications, processes, workers. A
// capability is a statement about what the organisation must be able to do, which is
// a different altitude — and the overlay's own comment says a key absent from it
// binds something the mesh does not draw, which is not absence. Reporting a
// capability as drift would invent drift that is not there, so this asserts the
// absence is the decision it is rather than something a later edit should "fix".
func TestBusinessArchitectureKeysAreNotMeshOverlayKinds(t *testing.T) {
	for _, key := range []string{KeyCapabilityKey, KeyValueStreamKey} {
		if kind, ok := overlayKind[key]; ok {
			t.Errorf("overlayKind[%q] = %q; the mesh draws the runtime landscape, not the business architecture above it", key, kind)
		}
	}
}
