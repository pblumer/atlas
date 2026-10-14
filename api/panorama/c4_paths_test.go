package panorama

import (
	"strings"
	"testing"
)

// TestProjectToC4DropsARelationshipC4CannotDraw: both ends are projected, but C4 has
// no arrow for the kind, so the loss is reported rather than drawn as something it
// is not.
func TestProjectToC4DropsARelationshipC4CannotDraw(t *testing.T) {
	p, err := ProjectToC4(c4doc(`  <elements>
    <element identifier="app-1" xsi:type="ApplicationComponent"/>
    <element identifier="node-1" xsi:type="Node"/>
  </elements>
  <relationships>
    <relationship identifier="r-spec" source="node-1" target="app-1" xsi:type="Specialization"/>
  </relationships>
`), "m1", 1)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if len(p.Relationships) != 0 || len(p.Dropped) != 1 {
		t.Fatalf("relationships = %v, dropped = %v; want the specialization dropped", p.Relationships, p.Dropped)
	}
	if loss := p.Dropped[0]; loss.ID != "r-spec" || loss.Reason != "C4 has no relationship for an ArchiMate Specialization" {
		t.Fatalf("dropped = %+v, want r-spec with the reason naming its kind", loss)
	}
}

// TestProjectToC4OrdersRelationshipsByID: a projection is something people commit, so
// the document's own order must not leak into it.
func TestProjectToC4OrdersRelationshipsByID(t *testing.T) {
	p, err := ProjectToC4(c4doc(`  <elements>
    <element identifier="actor-1" xsi:type="BusinessActor"/>
    <element identifier="app-1" xsi:type="ApplicationComponent"/>
  </elements>
  <relationships>
    <relationship identifier="r-z" source="app-1" target="actor-1" xsi:type="Serving"/>
    <relationship identifier="r-a" source="actor-1" target="app-1" xsi:type="Triggering"/>
  </relationships>
`), "m1", 1)
	if err != nil {
		t.Fatalf("ProjectToC4: %v", err)
	}
	if len(p.Relationships) != 2 || p.Relationships[0].ID != "r-a" || p.Relationships[1].ID != "r-z" {
		t.Fatalf("relationships = %+v, want r-a then r-z", p.Relationships)
	}
}

// TestEveryArchiMateParserRefusesExcessiveNesting: the projection, the binding reader
// and the binding writer each parse the stored document themselves, so each carries
// the depth bound — and the directive refusal — rather than trusting the gate it
// was validated at.
func TestEveryArchiMateParserRefusesExcessiveNesting(t *testing.T) {
	deep := []byte(`<model xmlns="` + ExchangeNamespace + `" identifier="m"><name>Deep</name>` +
		strings.Repeat("<x>", maxXMLDepth+1) + strings.Repeat("</x>", maxXMLDepth+1) + `</model>`)
	directive := []byte(`<?xml version="1.0"?><!DOCTYPE model><model xmlns="` + ExchangeNamespace +
		`" identifier="m"><name>x</name></model>`)

	parsers := map[string]func([]byte) error{
		"ProjectToC4":     func(d []byte) error { _, err := ProjectToC4(d, "m", 1); return err },
		"ExtractBindings": func(d []byte) error { _, err := ExtractBindings(d); return err },
		"SetBinding": func(d []byte) error {
			_, err := SetBinding(d, "e1", "atlas.applicationId", []string{"app-1"})
			return err
		},
	}
	for name, parse := range parsers {
		t.Run(name, func(t *testing.T) {
			if err := parse(deep); err == nil || !strings.Contains(err.Error(), "maximum XML depth") {
				t.Errorf("deep document: err = %v, want the depth bound", err)
			}
			if err := parse(directive); err == nil || !strings.Contains(err.Error(), "directives are not allowed") {
				t.Errorf("directive: err = %v, want the directive refusal", err)
			}
		})
	}
}
