package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

// c4doc wraps elements and relationships in a minimal Open Exchange envelope.
func c4doc(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <name xml:lang="en">Order landscape</name>
` + body + `</model>`)
}

func c4ByID(t *testing.T, p C4Projection, id string) C4Element {
	t.Helper()
	for _, e := range p.Elements {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no C4 element %q in %#v", id, p.Elements)
	return C4Element{}
}

func droppedIDs(p C4Projection) []string {
	var out []string
	for _, loss := range p.Dropped {
		out = append(out, loss.ID)
	}
	return out
}

const c4Body = `  <elements>
    <element identifier="actor-1" xsi:type="BusinessActor"><name xml:lang="en">Customer</name></element>
    <element identifier="app-1" xsi:type="ApplicationComponent">
      <name xml:lang="en">Order Service</name>
      <documentation xml:lang="en">Takes orders.</documentation>
    </element>
    <element identifier="node-1" xsi:type="Node"><name xml:lang="en">Order database</name></element>
    <element identifier="cap-1" xsi:type="Capability"><name xml:lang="en">Fulfil orders</name></element>
  </elements>
  <relationships>
    <relationship identifier="r-serve" source="app-1" target="actor-1" xsi:type="Serving"><name xml:lang="en">serves</name></relationship>
    <relationship identifier="r-comp" source="app-1" target="node-1" xsi:type="Composition"/>
    <relationship identifier="r-real" source="app-1" target="cap-1" xsi:type="Realization"/>
  </relationships>
`

// The mapping is small and explicit (ADR-0211 §8): a person, a system, a container.
// C4 is notation-poor, which is exactly why a projection onto it can be honest.
func TestProjectToC4MapsTheSupportedVocabulary(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if got := c4ByID(t, p, "actor-1"); got.Type != C4Person || got.Name != "Customer" {
		t.Errorf("actor = %+v", got)
	}
	if got := c4ByID(t, p, "app-1"); got.Type != C4SoftwareSystem || got.Description != "Takes orders." {
		t.Errorf("component = %+v", got)
	}
	if got := c4ByID(t, p, "node-1"); got.Type != C4Container {
		t.Errorf("node = %+v", got)
	}
}

// An export must name what it is a projection of, and at which revision. A C4
// picture circulating without that is a claim about an architecture nobody can
// trace back — and it is not the authored artefact.
func TestProjectToC4LabelsItsSource(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if p.SourceModelID != "model-1" || p.SourceRevision != 4 {
		t.Errorf("source = %q rev %d", p.SourceModelID, p.SourceRevision)
	}
	if p.SourceNotation != NotationArchiMate32 || p.Notation != NotationC4Projection {
		t.Errorf("notations = %q -> %q", p.SourceNotation, p.Notation)
	}
	if p.MappingVersion != C4MappingVersion {
		t.Errorf("mapping version = %d", p.MappingVersion)
	}
}

// Loss is reported, never hidden. A projection that silently dropped what it could
// not express is exactly the failure mode ADR-0211 §8's ban on notations-as-themes
// exists to prevent: it would look complete and be wrong.
func TestProjectToC4ReportsWhatItCannotExpress(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	dropped := droppedIDs(p)
	if len(dropped) != 2 {
		t.Fatalf("dropped = %#v, want the capability and the realization", p.Dropped)
	}
	joined := strings.Join(dropped, ",")
	if !strings.Contains(joined, "cap-1") || !strings.Contains(joined, "r-real") {
		t.Errorf("dropped = %v, want the Capability and the Realization", dropped)
	}
	for _, loss := range p.Dropped {
		if loss.Reason == "" || loss.SourceType == "" {
			t.Errorf("loss without a reason or a type: %+v", loss)
		}
	}
}

// Composition becomes C4's nesting rather than an arrow: a container inside a
// system is how C4 says what ArchiMate says with a composition, and drawing it as
// a relationship instead would misstate the structure.
func TestProjectToC4TurnsCompositionIntoNesting(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if got := c4ByID(t, p, "node-1").Parent; got != "app-1" {
		t.Errorf("parent = %q, want the composing system", got)
	}
	for _, rel := range p.Relationships {
		if rel.ID == "r-comp" {
			t.Errorf("composition also drawn as a relationship: %+v", rel)
		}
	}
}

func TestProjectToC4MapsADirectedRelationship(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	var found bool
	for _, rel := range p.Relationships {
		if rel.ID == "r-serve" {
			found = true
			if rel.Source != "app-1" || rel.Target != "actor-1" || rel.Name != "serves" {
				t.Errorf("relationship = %+v", rel)
			}
		}
	}
	if !found {
		t.Errorf("serving relationship missing from %#v", p.Relationships)
	}
}

// A relationship whose end was dropped cannot be drawn, and saying so is more
// useful than an arrow into nothing.
func TestProjectToC4DropsARelationshipWithADroppedEnd(t *testing.T) {
	p, err := ProjectToC4(c4doc(`  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent"><name xml:lang="en">Order Service</name></element>
    <element identifier="cap-1" xsi:type="Capability"><name xml:lang="en">Fulfil</name></element>
  </elements>
  <relationships>
    <relationship identifier="r-serve" source="app-1" target="cap-1" xsi:type="Serving"/>
  </relationships>
`), "model-1", 1)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if len(p.Relationships) != 0 {
		t.Errorf("relationships = %#v, want none drawn into a dropped element", p.Relationships)
	}
	var reason string
	for _, loss := range p.Dropped {
		if loss.ID == "r-serve" {
			reason = loss.Reason
		}
	}
	if !strings.Contains(reason, "cap-1") {
		t.Errorf("reason = %q, want it to name the end that was not projected", reason)
	}
}

// The projection is one-directional by construction: it carries no way back, and
// says so in the payload so a consumer cannot mistake it for a source document.
func TestProjectToC4IsMarkedReadOnly(t *testing.T) {
	p, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"readOnly":true`) {
		t.Errorf("projection does not declare itself read-only: %s", body)
	}
}

func TestProjectToC4RefusesMalformedInput(t *testing.T) {
	if _, err := ProjectToC4([]byte("<model><elements>"), "m", 1); err == nil {
		t.Error("truncated XML accepted")
	}
	if _, err := ProjectToC4(make([]byte, MaxXMLBytes+1), "m", 1); err == nil {
		t.Error("oversized document accepted")
	}
}

// Output order is stable: a projection that reordered between two identical
// requests could not be diffed, and an export would churn.
func TestProjectToC4IsDeterministic(t *testing.T) {
	first, err := ProjectToC4(c4doc(c4Body), "model-1", 4)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	second, _ := ProjectToC4(c4doc(c4Body), "model-1", 4)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Errorf("projection is not stable:\n%s\n%s", a, b)
	}
}
