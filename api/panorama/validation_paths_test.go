package panorama

import (
	"fmt"
	"strings"
	"testing"
)

// The refusals of Validate that no fixture reached: the envelope limits, the rest of
// the "what is this file" list, and the per-concept checks inside a model.

// exchangeModel wraps body in an Open Exchange root with an identity and a name, so
// each case below is refused for the one thing it is about.
func exchangeModel(body string) []byte {
	return []byte(`<model xmlns="` + ExchangeNamespace + `" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">` +
		`<name>M</name>` + body + `</model>`)
}

func TestValidateRefusesAnEmptyOrOversizedDocument(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "XML document is empty"},
		{"oversized", make([]byte, MaxXMLBytes+1), fmt.Sprintf("exceeds the %d byte limit", MaxXMLBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate(tc.data)
			if result.Valid || len(result.Problems) != 1 || !strings.Contains(result.Problems[0].Message, tc.want) {
				t.Fatalf("Validate(%s) = %#v, want the one problem %q", tc.name, result.Problems, tc.want)
			}
		})
	}
}

// TestValidateNamesTheRestOfTheFilesPeopleArriveWith completes the list in
// TestValidateNamesWhatTheDocumentActuallyIs: each is refused with what it is, once.
func TestValidateNamesTheRestOfTheFilesPeopleArriveWith(t *testing.T) {
	for _, tc := range []struct {
		name string
		xml  string
		want string
	}{
		{"a decision model",
			`<?xml version="1.0"?><definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/"/>`,
			"DMN decision model"},
		{"an Enterprise Architect export",
			`<?xml version="1.0"?><EAModel xmlns="http://www.sparxsystems.com/profiles"/>`,
			"Enterprise Architect"},
		{"an Office document",
			`<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`,
			"Microsoft Office document"},
		{"an XHTML page",
			`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`,
			"HTML page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate([]byte(tc.xml))
			if result.Valid || len(result.Problems) != 1 || !strings.Contains(result.Problems[0].Message, tc.want) {
				t.Fatalf("problems = %#v, want exactly one naming %q", result.Problems, tc.want)
			}
		})
	}
}

// TestValidateChecksEveryConcept: each concept owes an identifier and a type from the
// standard vocabulary, and a relationship owes both ends.
func TestValidateChecksEveryConcept(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"an element without an identifier",
			`<elements><element xsi:type="ApplicationComponent"/></elements>`,
			"element identifier is required"},
		{"an element without a type",
			`<elements><element identifier="e1"/></elements>`,
			`element "e1" has no xsi:type`},
		{"a relationship without a type",
			`<elements><element identifier="a" xsi:type="Node"/><element identifier="b" xsi:type="Node"/></elements>` +
				`<relationships><relationship identifier="r1" source="a" target="b"/></relationships>`,
			`relationship "r1" has no xsi:type`},
		{"a relationship of an unknown type",
			`<elements><element identifier="a" xsi:type="Node"/><element identifier="b" xsi:type="Node"/></elements>` +
				`<relationships><relationship identifier="r1" source="a" target="b" xsi:type="Hates"/></relationships>`,
			`unknown ArchiMate relationship type "Hates"`},
		{"a relationship without a source",
			`<elements><element identifier="b" xsi:type="Node"/></elements>` +
				`<relationships><relationship identifier="r1" target="b" xsi:type="Serving"/></relationships>`,
			`relationship "r1" source is required`},
		{"a connection to no relationship",
			`<views><diagrams><view identifier="v" xsi:type="Diagram">` +
				`<connection identifier="c1" relationshipRef="r-missing" xsi:type="Relationship"/>` +
				`</view></diagrams></views>`,
			`relationshipRef "r-missing" does not exist`},
		{"a second root after the model",
			`</model><model xmlns="` + ExchangeNamespace + `" identifier="m2"><name>N</name>`,
			"content after the model root element"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate(exchangeModel(tc.body))
			if result.Valid || !problemsContain(result.Problems, tc.want) {
				t.Fatalf("problems = %#v, want one containing %q", result.Problems, tc.want)
			}
		})
	}
}

// TestValidateIgnoresForeignExtensions: an exchange file may carry another tool's
// elements in its own namespace, and those are stored untouched rather than judged.
func TestValidateIgnoresForeignExtensions(t *testing.T) {
	result := Validate(exchangeModel(`<x:layout xmlns:x="urn:tool:extras"><x:element/></x:layout>`))
	if !result.Valid {
		t.Fatalf("problems = %#v, want a model with a foreign extension to pass", result.Problems)
	}
	if result.Elements != 0 {
		t.Fatalf("Elements = %d, want the extension's <element> not counted", result.Elements)
	}
}

// TestValidateNeedsARootElement: a document of nothing but a comment is no model.
func TestValidateNeedsARootElement(t *testing.T) {
	result := Validate([]byte(`<?xml version="1.0"?><!-- nothing here -->`))
	if result.Valid || !problemsContain(result.Problems, "has no root element") {
		t.Fatalf("problems = %#v, want the missing root named", result.Problems)
	}
}

// TestValidateCapsItsProblemList: a document wrong in a thousand places gets a
// readable answer, not a thousand lines.
func TestValidateCapsItsProblemList(t *testing.T) {
	var b strings.Builder
	b.WriteString("<elements>")
	for i := 0; i < maxValidationProblems+20; i++ {
		fmt.Fprintf(&b, `<element identifier="e%d" xsi:type="NoSuchType"/>`, i)
	}
	b.WriteString("</elements>")
	result := Validate(exchangeModel(b.String()))
	if result.Valid || len(result.Problems) != maxValidationProblems {
		t.Fatalf("problems = %d, want the list capped at %d", len(result.Problems), maxValidationProblems)
	}
}
