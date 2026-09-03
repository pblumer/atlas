package googlesheets_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	gs "github.com/pblumer/atlas/connector/googlesheets"
)

// TestGoogleSheetsOpsMatchTheWorkerType is the drift guard between this package's [Ops]
// table and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/googlesheets imports compiler,
// so the dependency only runs one way — which is why the rules exist twice. The check
// is therefore behavioural: for every operation, a model supplying exactly what Ops
// says is required must compile, and a model missing any one of those values must not.
func TestGoogleSheetsOpsMatchTheWorkerType(t *testing.T) {
	// attrsFor builds the smallest model that satisfies one operation, optionally
	// leaving out the one value named by omit.
	attrsFor := func(op string, spec gs.Op, omit string) string {
		parts := []string{`connector="acme"`, `operation="` + op + `"`}
		add := func(name, attr, value string) {
			if omit != name {
				parts = append(parts, attr+`="`+value+`"`)
			}
		}
		if spec.NeedsSpreadsheet {
			add("spreadsheet", "spreadsheet", "1Bxi")
		}
		if spec.NeedsTitle {
			add("title", "title", "Anträge")
		}
		if spec.NeedsSheet {
			add("sheet", "sheet", "Eingang")
		}
		if spec.NeedsRange {
			add("range", "range", "A2:F")
		}
		if spec.NeedsValues {
			add("values", "values", "=zeilen")
		}
		if spec.NeedsResult {
			add("result", "resultVariable", "ergebnis")
		}
		return strings.Join(parts, " ")
	}
	compile := func(attrs string) error {
		bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><atlas:googleSheetsConnector ` + attrs + `/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
		_, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
		return err
	}

	for op, spec := range gs.Ops {
		if err := compile(attrsFor(op, spec, "")); err != nil {
			t.Errorf("%s: a model supplying exactly what Ops requires did not compile: %v", op, err)
		}
		// Each required value, left out one at a time: the compiler must refuse.
		required := map[string]bool{
			"spreadsheet": spec.NeedsSpreadsheet,
			"title":       spec.NeedsTitle,
			"sheet":       spec.NeedsSheet,
			"range":       spec.NeedsRange,
			"values":      spec.NeedsValues,
			"result":      spec.NeedsResult,
		}
		for name, need := range required {
			if !need {
				continue
			}
			if err := compile(attrsFor(op, spec, name)); err == nil {
				t.Errorf("%s: a model omitting %s compiled; Ops says it is required", op, name)
			}
		}
		// The optional half: what the operation *takes* must be accepted, and what it
		// does not take must be refused. This is the half that is easy to forget, and
		// the one whose absence lets a value be silently dropped at call time.
		optional := []struct {
			attr, value string
			takes       bool
		}{
			{"sheet", "Eingang", spec.NeedsSheet || spec.TakesSheet},
			{"folder", "abc", spec.TakesFolder},
			{"columns", "a,b", spec.TakesColumns},
			{"valueInput", "raw", spec.TakesInput},
			{"header", "true", spec.TakesHeader},
			{"resultVariable", "ergebnis", spec.NeedsResult || spec.TakesResult},
		}
		for _, o := range optional {
			attrs := attrsFor(op, spec, "") + ` ` + o.attr + `="` + o.value + `"`
			err := compile(attrs)
			if o.takes && err != nil {
				t.Errorf("%s: Ops says it takes %s, but the compiler refused it: %v", op, o.attr, err)
			}
			if !o.takes && err == nil {
				t.Errorf("%s: Ops says it does not take %s, but the compiler accepted it", op, o.attr)
			}
		}
	}
}

// TestEveryOpIsImplemented: an operation in the table that the client does not
// implement would compile and then fail on the first token. The fake answers nothing,
// so a routed operation fails on the *transport*; only an unrouted one is reported as
// unknown, which is what this separates.
func TestEveryOpIsImplemented(t *testing.T) {
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: "http://127.0.0.1:1", DriveBase: "http://127.0.0.1:1"})
	for op := range gs.Ops {
		_, err := c.Do(t.Context(), gs.Request{Operation: op, Spreadsheet: "1B", Range: "A1", Sheet: "S", Title: "T"})
		if err == nil {
			t.Errorf("%s: want a transport error against a dead endpoint, got nil", op)
			continue
		}
		if strings.Contains(err.Error(), "unknown operation") {
			t.Errorf("%s is in Ops but the client does not implement it", op)
		}
	}
}

// TestOpNamesAreSorted: the list is what an error message shows an author, and an
// order that changed per run would make two identical failures look different.
func TestOpNamesAreSorted(t *testing.T) {
	names := gs.OpNames()
	if len(names) != len(gs.Ops) {
		t.Fatalf("OpNames() has %d entries, Ops has %d", len(names), len(gs.Ops))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("OpNames() is not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
