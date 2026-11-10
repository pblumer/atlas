package compiler

import (
	"errors"
	"strings"
	"testing"
)

// ADR-0547 part 2: an element-local compile fault carries its element, without its
// message, its rule or its fatality on reload changing.

const dupIDModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/><endEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="s"/>
  </process></definitions>`

const complexModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="start"/><complexGateway id="complex"/><endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="complex"/>
    <sequenceFlow id="f2" sourceRef="complex" targetRef="end"/>
  </process></definitions>`

func TestElementErrorKeepsTheMessageAndNamesTheElement(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(dupIDModel))
	if err == nil {
		t.Fatal("a duplicate id compiled")
	}
	if got, want := err.Error(), `compiler: duplicate element id "s"`; got != want {
		t.Fatalf("message = %q, want it unchanged: %q", got, want)
	}
	var ee *ElementError
	if !errors.As(err, &ee) || ee.Element != "s" {
		t.Fatalf("errors.As(%v) = %+v, want element s", err, ee)
	}
}

func TestValidateModelAnchorsElementLocalFaults(t *testing.T) {
	cases := []struct{ name, xml, element string }{
		{"duplicate-id", dupIDModel, "s"},
		// An unsupported element is refused by name, now also by element.
		{"complex-gateway", complexModel, "complex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps, err := ValidateModel(strings.NewReader(tc.xml))
			if err != nil {
				t.Fatal(err)
			}
			if len(ps) != 1 || ps[0].Rule != RuleCompile || ps[0].Severity != SeverityError || ps[0].Element != tc.element {
				t.Fatalf("problems = %+v, want one %s error on %q", ps, RuleCompile, tc.element)
			}
		})
	}
}

// A process-level fault is not any one element's: it stays unanchored.
func TestProcessLevelCompileFaultStaysUnanchored(t *testing.T) {
	ps, err := ValidateModel(strings.NewReader(ttlXML(` instanceTtl="not-a-duration"`)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Rule != RuleCompile || ps[0].Element != "" {
		t.Fatalf("problems = %+v, want one unanchored %s error", ps, RuleCompile)
	}
}

// The anchor is a diagnostic only: a reload of a stored model with an element-local
// fault still fails, as ADR-0177 decided, because nothing was built to bring back.
func TestElementLocalFaultStillFailsAReload(t *testing.T) {
	if _, _, err := ReloadNamed(1, 1, strings.NewReader(dupIDModel), "p"); err == nil {
		t.Fatal("ReloadNamed brought back a model with a duplicate id")
	}
}
