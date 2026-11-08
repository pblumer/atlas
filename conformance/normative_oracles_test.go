package conformance

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// TestCON2ExecutionOracles asserts individual BPMN section 13 behavioral outcomes, not the
// golden file that -update can silently rewrite. These are specific normative
// variants; they are not proof of complete OMG Process Execution Conformance.
func TestCON2ExecutionOracles(t *testing.T) {
	cases := []struct {
		name, clause string
		must, absent  []string
		visits        map[string]int
	}{
		{
			name: "event-gateway-message", clause: "13.4.4",
			must: []string{"end_msg"}, absent: []string{"end_timeout"},
			visits: map[string]int{"end_msg": 1},
		},
		{
			name: "event-gateway-timer", clause: "13.4.4",
			must: []string{"end_timeout"}, absent: []string{"end_msg"},
			visits: map[string]int{"end_timeout": 1},
		},
		{
			name: "parallel-independent", clause: "13.4.2",
			must: []string{"fork", "set_x", "set_y", "end"},
			visits: map[string]int{"fork": 1, "join": 2, "end": 1},
		},
		{
			name: "inclusive-gateway", clause: "13.4.3",
			must: []string{"priority", "eu_check", "end"},
			visits: map[string]int{"join": 2, "end": 1},
		},
		{
			name: "boundary-error", clause: "13.5",
			must: []string{"on_error", "handled"},
			visits: map[string]int{"handled": 1},
		},
		{
			name: "boundary-message-noninterrupting", clause: "13.5",
			must: []string{"notified", "done"},
			visits: map[string]int{"notified": 1, "done": 1},
		},
	}
	registered := make(map[string]Scenario, len(Scenarios))
	for _, scenario := range Scenarios {
		registered[scenario.Name] = scenario
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, ok := registered[tc.name]
			if !ok {
				t.Fatalf("normative section 13 case missing registered scenario %q", tc.name)
			}
			data, err := sc.load()
			if err != nil {
				t.Fatalf("load %s: %v", tc.name, err)
			}
			got, err := Run(t.TempDir(), data, sc.Root, sc.Start, sc.Driver)
			if err != nil {
				t.Fatalf("execute section %s of OMG BPMN 2.0.2: %v", tc.clause, err)
			}
			if got.State != model.PICompleted {
				t.Fatalf("section %s: state %v, want completed", tc.clause, got.State)
			}
			visits := make(map[string]int)
			for _, id := range got.Path {
				visits[id]++
			}
			for _, id := range tc.must {
				if visits[id] == 0 {
					t.Errorf("section %s: path %v omitted required activity %q", tc.clause, got.Path, id)
				}
			}
			for _, id := range tc.absent {
				if visits[id] != 0 {
					t.Errorf("section %s: path %v incorrectly executed losing branch %q", tc.clause, got.Path, id)
				}
			}
			for id, count := range tc.visits {
				if visits[id] != count {
					t.Errorf("section %s: activity %q visits = %d, want %d; path=%v",
						tc.clause, id, visits[id], count, got.Path)
				}
			}
		})
	}
}

// TestCON2UnsupportedVariantsHaveExactDiagnostics ensures a valid but
// unsupported BPMN construct fails with an actionable rule and element.
// Rejection is diagnostic evidence, never positive conformance credit.
func TestCON2UnsupportedVariantsHaveExactDiagnostics(t *testing.T) {
	eventXML, err := modelsFS.ReadFile("models/event-based-gateway.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	gatewayXML := func(attrs string) []byte {
		old := []byte(`<eventBasedGateway id="gw"/>`)
		updated := []byte(`<eventBasedGateway id="gw" ` + attrs + `/>`)
		if !bytes.Contains(eventXML, old) {
			t.Fatal("base event gateway fixture changed")
		}
		return bytes.Replace(eventXML, old, updated, 1)
	}
	conditionalXML := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p" isExecutable="true"><startEvent id="s"><conditionalEventDefinition><condition>=true</condition></conditionalEventDefinition></startEvent><endEvent id="end"/><sequenceFlow id="f" sourceRef="s" targetRef="end"/></process></definitions>`)
	cases := []struct {
		name, rule, element string
		xml                 []byte
	}{
		{"event-gateway-parallel", compiler.RuleEventGatewayKind, "gw", gatewayXML(`eventGatewayType="Parallel"`)},
		{"event-gateway-instantiating", compiler.RuleEventGatewayKind, "gw", gatewayXML(`instantiate="true"`)},
		{"root-conditional-start", compiler.RuleConditionalStart, "s", conditionalXML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, parseErr := compiler.Parse(7, 1, bytes.NewReader(tc.xml))
			var diagnostics *compiler.ValidationError
			if !errors.As(parseErr, &diagnostics) {
				t.Fatalf("Parse: expected ValidationError, got %v", parseErr)
			}
			found := false
			for _, problem := range diagnostics.Problems {
				if problem.Rule != tc.rule || problem.Element != tc.element {
					continue
				}
				found = true
				if problem.Severity != compiler.SeverityError {
					t.Errorf("%s has severity %v, want error", tc.rule, problem.Severity)
				}
				if strings.TrimSpace(problem.Message) == "" {
					t.Errorf("%s has empty diagnostic message", tc.rule)
				}
			}
			if !found {
				t.Fatalf("missing rule %q on %q; diagnostics: %+v", tc.rule, tc.element, diagnostics.Problems)
			}
			problems, err := compiler.ValidateModel(bytes.NewReader(tc.xml))
			if err != nil {
				t.Fatalf("Modeler validation: %v", err)
			}
			alsoFound := false
			for _, problem := range problems {
				alsoFound = alsoFound || (problem.Rule == tc.rule &&
					problem.Element == tc.element && problem.Severity == compiler.SeverityError)
			}
			if !alsoFound {
				t.Fatalf("Modeler returned %v, missing %s on %s", problems, tc.rule, tc.element)
			}
		})
	}
}

// TestCON2ComplexGatewayIsNotAFalsePositive verifies a known valid, unimplemented
// construct is rejected by name rather than silently interpreted as XOR.
func TestCON2ComplexGatewayIsNotAFalsePositive(t *testing.T) {
	xml := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p" isExecutable="true"><startEvent id="start"/><complexGateway id="complex"/><endEvent id="end"/><sequenceFlow id="f1" sourceRef="start" targetRef="complex"/><sequenceFlow id="f2" sourceRef="complex" targetRef="end"/></process></definitions>`)
	_, err := compiler.Parse(7, 1, bytes.NewReader(xml))
	if err == nil || !strings.Contains(err.Error(), "complexGateway") ||
		!strings.Contains(err.Error(), "complex") {
		t.Fatalf("Complex Gateway must be explicitly refused by id; got %v", err)
	}
	// A direct test is a diagnostic oracle only, not a normative support pass.
	// Keep the unsupported construct explicit in the candidate register.
}
