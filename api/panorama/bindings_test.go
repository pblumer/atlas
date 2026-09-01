package panorama

import (
	"strings"
	"testing"
)

// model wraps element XML in a minimal but valid Open Exchange envelope.
func model(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Bindings</name>
` + body + `</model>`)
}

// bound is a small helper: the ids bound under one key on one element.
func bound(t *testing.T, set BindingSet, element, key string) []string {
	t.Helper()
	for _, b := range set.Bindings {
		if b.ElementID == element && b.Key == key {
			return b.Values
		}
	}
	return nil
}

const twoDefs = `  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent">
      <name xml:lang="en">Order Service</name>
      <properties>
        <property propertyDefinitionRef="p-app"><value>proj-abc</value></property>
        <property propertyDefinitionRef="p-app"><value>proj-def</value></property>
      </properties>
    </element>
    <element identifier="bp-1" xsi:type="BusinessProcess">
      <name xml:lang="en">Fulfil order</name>
      <properties>
        <property propertyDefinitionRef="p-proc"><value>order-fulfilment</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-app" type="string"><name>atlas.applicationId</name></propertyDefinition>
    <propertyDefinition identifier="p-proc" type="string"><name>atlas.processId</name></propertyDefinition>
  </propertyDefinitions>
`

// TestExtractBindingsReadsNamespacedProperties is the core read: bindings are
// standard ArchiMate properties (ADR-0189 §4), so they arrive through the exchange
// document like any other property and need no Atlas-specific carrier.
func TestExtractBindingsReadsNamespacedProperties(t *testing.T) {
	set, err := ExtractBindings(model(twoDefs))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != "order-fulfilment" {
		t.Errorf("business process binding = %v", got)
	}
	if len(set.Problems) != 0 {
		t.Errorf("problems = %#v, want none", set.Problems)
	}
}

// Repeated values are how the exchange format expresses many-to-many, and ADR-0189
// §4 requires it: one ArchiMate component can be implemented by several Atlas
// process applications. Collapsing them to one would quietly drop an
// implementation.
func TestExtractBindingsKeepsManyToMany(t *testing.T) {
	set, err := ExtractBindings(model(twoDefs))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	got := bound(t, set, "app-1", KeyApplicationID)
	if len(got) != 2 || got[0] != "proj-abc" || got[1] != "proj-def" {
		t.Errorf("application bindings = %v, want both ids in document order", got)
	}
}

// A property whose definition is not atlas.* is somebody else's, and Panorama has
// no business reading or reporting it — it round-trips untouched because the
// document is never rewritten on read.
func TestExtractBindingsIgnoresForeignProperties(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent">
      <properties>
        <property propertyDefinitionRef="p-owner"><value>Team Payments</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-owner" type="string"><name>Owner</name></propertyDefinition>
  </propertyDefinitions>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Bindings) != 0 {
		t.Errorf("bindings = %#v, want none", set.Bindings)
	}
}

// The key set is an allowlist, not a denylist. ADR-0189 §4 forbids credential
// references, tokens and secret values in a binding; an allowlist makes that
// structural — atlas.credentialRef is refused because it was never permitted,
// rather than because somebody remembered to ban it.
func TestExtractBindingsRefusesUnknownAtlasKeys(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent">
      <properties>
        <property propertyDefinitionRef="p-secret"><value>vault://prod/token</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-secret" type="string"><name>atlas.credentialRef</name></propertyDefinition>
  </propertyDefinitions>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Bindings) != 0 {
		t.Errorf("bindings = %#v, want the unknown key refused rather than carried", set.Bindings)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Message, "atlas.credentialRef") {
		t.Fatalf("problems = %#v, want one naming the refused key", set.Problems)
	}
	// The refusal must not echo the value: a rejected secret is still a secret.
	if strings.Contains(set.Problems[0].Message, "vault://prod/token") {
		t.Errorf("problem echoes the rejected value: %q", set.Problems[0].Message)
	}
}

// A key on the wrong element type is a contract error, not a silent no-op: an
// application id on a Node means nothing, and carrying it would let a nonsense
// binding travel to another tool looking official.
func TestExtractBindingsRefusesAKeyOnTheWrongElementType(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="node-1" xsi:type="Node">
      <properties>
        <property propertyDefinitionRef="p-app"><value>proj-abc</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-app" type="string"><name>atlas.applicationId</name></propertyDefinition>
  </propertyDefinitions>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Message, "Node") {
		t.Fatalf("problems = %#v, want one naming the element type", set.Problems)
	}
	if len(set.Bindings) != 0 {
		t.Errorf("bindings = %#v, want none", set.Bindings)
	}
}

// An empty value binds nothing and is a defect in the document rather than a
// binding to "": reported, not carried.
func TestExtractBindingsRefusesAnEmptyValue(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="bp-1" xsi:type="BusinessProcess">
      <properties>
        <property propertyDefinitionRef="p-proc"><value>   </value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-proc" type="string"><name>atlas.processId</name></propertyDefinition>
  </propertyDefinitions>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Bindings) != 0 || len(set.Problems) != 1 {
		t.Errorf("bindings = %#v, problems = %#v", set.Bindings, set.Problems)
	}
}

// propertyDefinitions may appear after the elements that reference them — the
// schema puts the block near the end of the model — so extraction cannot resolve a
// reference on sight and must complete the pass first.
func TestExtractBindingsResolvesDefinitionsDeclaredAfterUse(t *testing.T) {
	set, err := ExtractBindings(model(twoDefs))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Bindings) != 2 {
		t.Fatalf("bindings = %#v, want both elements bound", set.Bindings)
	}
}

// A property pointing at a definition the document never declares is a broken
// document, and saying so is more useful than ignoring it.
func TestExtractBindingsReportsADanglingDefinitionReference(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="bp-1" xsi:type="BusinessProcess">
      <properties>
        <property propertyDefinitionRef="p-missing"><value>x</value></property>
      </properties>
    </element>
  </elements>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Message, "p-missing") {
		t.Errorf("problems = %#v, want one naming the missing definition", set.Problems)
	}
}

// Bindings are bounded like every other parse in this package: a document over the
// limit is refused rather than parsed.
func TestExtractBindingsRefusesAnOversizedDocument(t *testing.T) {
	if _, err := ExtractBindings(make([]byte, MaxXMLBytes+1)); err == nil {
		t.Error("oversized document accepted")
	}
}

// The overlay shows what the architect called a thing, so extraction carries the
// element's own name alongside its bindings. Without it the mesh could only ever
// show Atlas's name for a resource, and a modeled-but-absent element would have no
// name at all — Atlas has none to offer for something it does not have.
func TestExtractBindingsCarriesTheElementName(t *testing.T) {
	set, err := ExtractBindings(model(twoDefs))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	names := map[string]string{}
	for _, b := range set.Bindings {
		names[b.ElementID] = b.ElementName
	}
	if names["app-1"] != "Order Service" || names["bp-1"] != "Fulfil order" {
		t.Errorf("element names = %v", names)
	}
}

// A model's own name, and a relationship's, must not be mistaken for an element's:
// they are different <name> elements in the same document.
func TestExtractBindingsDoesNotBorrowAnotherElementsName(t *testing.T) {
	set, err := ExtractBindings(model(`  <elements>
    <element identifier="bp-1" xsi:type="BusinessProcess">
      <properties>
        <property propertyDefinitionRef="p-proc"><value>order</value></property>
      </properties>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-proc" type="string"><name>atlas.processId</name></propertyDefinition>
  </propertyDefinitions>
`))
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Bindings) != 1 {
		t.Fatalf("bindings = %#v", set.Bindings)
	}
	// This element has no name of its own; it must not inherit the model's or the
	// property definition's.
	if got := set.Bindings[0].ElementName; got != "" {
		t.Errorf("element name = %q, want empty rather than borrowed", got)
	}
}
