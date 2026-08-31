package panorama

import (
	"strings"
	"testing"
)

// quirky is deliberately awkward: a comment, irregular indentation, an attribute
// order no serializer would choose, a foreign property, and a processing
// instruction. A decode/encode round trip would normalise all of it away — which is
// exactly what ADR-0189 §2 forbids, since Atlas must never silently discard
// standard content it does not model.
const quirky = `<?xml version="1.0" encoding="UTF-8"?>
<!-- exported by another tool; do not reformat -->
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
   xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Quirky</name>
  <elements>
        <element xsi:type="ApplicationComponent" identifier="app-1">
      <name xml:lang="en">Order Service</name>
          <properties>
        <property propertyDefinitionRef="p-owner"><value>Team Payments</value></property>
      </properties>
    </element>
    <element identifier="bp-1" xsi:type="BusinessProcess">
      <name xml:lang="en">Fulfil order</name>
    </element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="p-owner" type="string"><name>Owner</name></propertyDefinition>
  </propertyDefinitions>
  <views>
    <diagrams/>
  </views>
</model>`

func mustSet(t *testing.T, doc []byte, element, key string, values ...string) []byte {
	t.Helper()
	out, err := SetBinding(doc, element, key, values)
	if err != nil {
		t.Fatalf("SetBinding(%s, %s): %v", element, key, err)
	}
	return out
}

// The guarantee the whole approach exists for: everything the edit did not touch
// comes back exactly as it went in. Comments, indentation, attribute order and
// foreign properties all survive, because the writer splices bytes rather than
// re-serialising a parsed model.
func TestSetBindingLeavesUntouchedBytesIdentical(t *testing.T) {
	out := mustSet(t, []byte(quirky), "bp-1", KeyProcessID, "order-fulfilment")
	got := string(out)

	for _, fragment := range []string{
		"<!-- exported by another tool; do not reformat -->",
		`        <element xsi:type="ApplicationComponent" identifier="app-1">`,
		`          <properties>`,
		`<property propertyDefinitionRef="p-owner"><value>Team Payments</value></property>`,
		"  <views>\n    <diagrams/>\n  </views>",
	} {
		if !strings.Contains(got, fragment) {
			t.Errorf("edit disturbed untouched content; missing:\n%s\n\ngot:\n%s", fragment, got)
		}
	}
	// The document grew only by what was inserted.
	if len(out) <= len(quirky) {
		t.Errorf("document did not grow: %d -> %d", len(quirky), len(out))
	}
}

// An element with no properties block gets one, and the binding is then readable
// through the ordinary extractor — the writer and the reader agree on the format
// rather than each having its own idea of it.
func TestSetBindingCreatesAPropertiesBlockAndRoundTrips(t *testing.T) {
	out := mustSet(t, []byte(quirky), "bp-1", KeyProcessID, "order-fulfilment")

	set, err := ExtractBindings(out)
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if len(set.Problems) != 0 {
		t.Fatalf("problems after write: %#v", set.Problems)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != "order-fulfilment" {
		t.Errorf("round trip = %v", got)
	}
	if result := Validate(out); !result.Valid {
		t.Errorf("written document does not validate: %#v", result.Problems)
	}
}

// Many-to-many is written the way the format expresses it: one property per value.
func TestSetBindingWritesEveryValue(t *testing.T) {
	out := mustSet(t, []byte(quirky), "app-1", KeyApplicationID, "proj-abc", "proj-def")

	set, _ := ExtractBindings(out)
	got := bound(t, set, "app-1", KeyApplicationID)
	if len(got) != 2 || got[0] != "proj-abc" || got[1] != "proj-def" {
		t.Errorf("values = %v, want both in order", got)
	}
}

// Setting a key twice replaces that key and leaves every other property alone —
// including a foreign one on the same element, which is not Atlas's to touch.
func TestSetBindingReplacesOnlyItsOwnKey(t *testing.T) {
	once := mustSet(t, []byte(quirky), "app-1", KeyApplicationID, "proj-abc")
	twice := mustSet(t, once, "app-1", KeyApplicationID, "proj-zzz")

	set, _ := ExtractBindings(twice)
	got := bound(t, set, "app-1", KeyApplicationID)
	if len(got) != 1 || got[0] != "proj-zzz" {
		t.Errorf("values = %v, want only the replacement", got)
	}
	if !strings.Contains(string(twice), `<value>Team Payments</value>`) {
		t.Error("replacing an Atlas binding removed a foreign property")
	}
	if strings.Count(string(twice), "atlas.applicationId") != 1 {
		t.Errorf("definition duplicated:\n%s", twice)
	}
}

// Clearing a binding removes its properties rather than writing an empty value —
// an empty value is a defect the reader already refuses.
func TestSetBindingWithNoValuesClearsTheBinding(t *testing.T) {
	once := mustSet(t, []byte(quirky), "app-1", KeyApplicationID, "proj-abc")
	cleared := mustSet(t, once, "app-1", KeyApplicationID)

	set, _ := ExtractBindings(cleared)
	if got := bound(t, set, "app-1", KeyApplicationID); got != nil {
		t.Errorf("values = %v, want the binding gone", got)
	}
	if !strings.Contains(string(cleared), `<value>Team Payments</value>`) {
		t.Error("clearing an Atlas binding removed a foreign property")
	}
}

// A document with no propertyDefinitions block at all gets one, in the position the
// schema requires: after organizations and before views.
func TestSetBindingCreatesTheDefinitionsBlockBeforeViews(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">No defs</name>
  <elements>
    <element identifier="bp-1" xsi:type="BusinessProcess"><name xml:lang="en">P</name></element>
  </elements>
  <views><diagrams/></views>
</model>`)
	out := mustSet(t, doc, "bp-1", KeyProcessID, "p1")

	text := string(out)
	defsAt := strings.Index(text, "<propertyDefinitions>")
	viewsAt := strings.Index(text, "<views>")
	if defsAt < 0 {
		t.Fatalf("no definitions block written:\n%s", text)
	}
	if defsAt > viewsAt {
		t.Errorf("definitions written after views; the schema sequence puts them before:\n%s", text)
	}
	if result := Validate(out); !result.Valid {
		t.Errorf("written document does not validate: %#v", result.Problems)
	}
}

// The contract is enforced on write as well as on read. Writing a key onto an
// element type it does not belong on would put a nonsense binding into a document
// that then travels to other tools.
func TestSetBindingRefusesAKeyOnTheWrongElementType(t *testing.T) {
	if _, err := SetBinding([]byte(quirky), "bp-1", KeyApplicationID, []string{"proj-abc"}); err == nil {
		t.Error("application id accepted on a business process")
	}
}

func TestSetBindingRefusesAnUnknownKey(t *testing.T) {
	if _, err := SetBinding([]byte(quirky), "app-1", "atlas.credentialRef", []string{"vault://x"}); err == nil {
		t.Error("credential reference accepted as a binding")
	}
}

// A binding on an element the document does not contain is a caller error, not a
// silent no-op that reports success while changing nothing.
func TestSetBindingRefusesAnUnknownElement(t *testing.T) {
	if _, err := SetBinding([]byte(quirky), "nope", KeyProcessID, []string{"p"}); err == nil {
		t.Error("unknown element accepted")
	}
}

func TestSetBindingRefusesAnEmptyValue(t *testing.T) {
	if _, err := SetBinding([]byte(quirky), "bp-1", KeyProcessID, []string{"  "}); err == nil {
		t.Error("blank value accepted")
	}
}

// Values reach the document as XML text, so anything that would change the shape of
// the document has to be escaped rather than pasted.
func TestSetBindingEscapesTheValue(t *testing.T) {
	out := mustSet(t, []byte(quirky), "bp-1", KeyProcessID, `a<b&c"d`)

	if strings.Contains(string(out), `<value>a<b&c"d</value>`) {
		t.Errorf("value written unescaped:\n%s", out)
	}
	set, err := ExtractBindings(out)
	if err != nil {
		t.Fatalf("ExtractBindings after escaping: %v", err)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != `a<b&c"d` {
		t.Errorf("escaped value did not round trip: %v", got)
	}
}

// Archi and other tools write xsi:type with a namespace prefix. Reading only the
// bare form would make every binding from such a document look like it sat on an
// unknown element type — an interop failure that would present as a contract error.
func TestBindingsAcceptAPrefixedElementType(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
       xmlns:archimate="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m1">
  <name xml:lang="en">Prefixed</name>
  <elements>
    <element identifier="bp-1" xsi:type="archimate:BusinessProcess"><name xml:lang="en">P</name></element>
  </elements>
</model>`)
	out := mustSet(t, doc, "bp-1", KeyProcessID, "order")

	set, err := ExtractBindings(out)
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != "order" {
		t.Errorf("prefixed element type did not round trip: %v (problems %#v)", got, set.Problems)
	}
}

// A document may already use the identifier the writer would mint for its
// definition. Reusing it would silently repoint an existing definition at an Atlas
// key, changing the meaning of every property that referenced it.
func TestSetBindingDoesNotStealAnExistingDefinitionIdentifier(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Clash</name>
  <elements>
    <element identifier="bp-1" xsi:type="BusinessProcess"><name xml:lang="en">P</name></element>
  </elements>
  <propertyDefinitions>
    <propertyDefinition identifier="atlas-processId" type="string"><name>Something Else</name></propertyDefinition>
  </propertyDefinitions>
</model>`)
	out := mustSet(t, doc, "bp-1", KeyProcessID, "order")

	text := string(out)
	if !strings.Contains(text, `<name>Something Else</name>`) {
		t.Errorf("the existing definition was repointed:\n%s", text)
	}
	set, err := ExtractBindings(out)
	if err != nil {
		t.Fatalf("ExtractBindings: %v", err)
	}
	if got := bound(t, set, "bp-1", KeyProcessID); len(got) != 1 || got[0] != "order" {
		t.Errorf("binding = %v after identifier clash (problems %#v)", got, set.Problems)
	}
}

// Both entry points refuse malformed input rather than reporting an empty model:
// "this document declares no bindings" and "this document is not XML" are different
// answers, and only one of them is safe to act on.
func TestBindingsRefuseMalformedXML(t *testing.T) {
	broken := []byte(`<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"><elements>`)
	if _, err := ExtractBindings(broken); err == nil {
		t.Error("ExtractBindings accepted truncated XML")
	}
	if _, err := SetBinding(broken, "bp-1", KeyProcessID, []string{"p"}); err == nil {
		t.Error("SetBinding accepted truncated XML")
	}
	if _, err := SetBinding(make([]byte, MaxXMLBytes+1), "bp-1", KeyProcessID, []string{"p"}); err == nil {
		t.Error("SetBinding accepted an oversized document")
	}
}
