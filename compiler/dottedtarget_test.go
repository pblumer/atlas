package compiler

import (
	"strings"
	"testing"
)

// dottedModel is one process whose every kind of write target can be given a name, so a
// test can hand it a dotted one and see whether the deploy notices.
func dottedModel(result, inputMap, outputMap, inputElement, outputCollection string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <scriptTask id="Rechne" name="Gesamt bilden">
      <incoming>f1</incoming><outgoing>f2</outgoing>
      <extensionElements>
        <zeebe:script expression="=1" resultVariable="` + result + `"/>
        <zeebe:ioMapping>
          <zeebe:input source="=1" target="` + inputMap + `"/>
          <zeebe:output source="=1" target="` + outputMap + `"/>
        </zeebe:ioMapping>
      </extensionElements>
    </scriptTask>
    <serviceTask id="Loop">
      <incoming>f2</incoming><outgoing>f3</outgoing>
      <extensionElements><zeebe:taskDefinition type="w"/></extensionElements>
      <multiInstanceLoopCharacteristics>
        <extensionElements><zeebe:loopCharacteristics inputCollection="=xs" inputElement="` + inputElement +
		`" outputCollection="` + outputCollection + `"/></extensionElements>
      </multiInstanceLoopCharacteristics>
    </serviceTask>
    <endEvent id="e"><incoming>f3</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="Rechne"/>
    <sequenceFlow id="f2" sourceRef="Rechne" targetRef="Loop"/>
    <sequenceFlow id="f3" sourceRef="Loop" targetRef="e"/>
  </process>
</definitions>`
}

// dottedProblems compiles the model and returns its variable.dotted-target findings. A
// model refused outright (Parse fails on validation errors) reports through the error, so
// both routes are folded into one answer: what the author is told.
func dottedProblems(t *testing.T, model string) []Problem {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(model))
	if err != nil {
		if !strings.Contains(err.Error(), RuleDottedTarget) {
			t.Fatalf("Parse failed for another reason: %v", err)
		}
		// Refused at parse; re-read the findings from a model that compiles, below.
		return nil
	}
	var out []Problem
	for _, p := range Validate(cp) {
		if p.Rule == RuleDottedTarget {
			out = append(out, p)
		}
	}
	return out
}

// TestDottedTargetIsRefused covers the mistake this rule exists for: a script task asked
// to add a field to a structure by writing `customers.gesamtumsatz`. Atlas writes a
// variable of exactly that name, beside the `customers` the author meant to extend — no
// error, no field, and a variable list that reads as if it had worked.
func TestDottedTargetIsRefused(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(dottedModel("customers.gesamtumsatz", "in", "out", "item", "results")))
	if err == nil {
		t.Fatal("a dotted result variable deployed clean")
	}
	msg := err.Error()
	for _, want := range []string{RuleDottedTarget, "customers.gesamtumsatz", "not a path", "customers"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
}

// TestEveryWriteTargetIsChecked: the result variable is the one an author reaches for
// first, but every other name a model writes to is the same trap — an I/O mapping's
// target, and a loop's input element and output collection.
func TestEveryWriteTargetIsChecked(t *testing.T) {
	for name, model := range map[string]string{
		"input mapping":    dottedModel("r", "a.b", "out", "item", "results"),
		"output mapping":   dottedModel("r", "in", "c.d", "item", "results"),
		"loop input":       dottedModel("r", "in", "out", "k.item", "results"),
		"loop output":      dottedModel("r", "in", "out", "item", "r.out"),
		"all of them":      dottedModel("x.y", "a.b", "c.d", "k.item", "r.out"),
		"result of a task": dottedModel("x.y", "in", "out", "item", "results"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(model)); err == nil {
				t.Errorf("a dotted %s deployed clean", name)
			}
		})
	}
}

// TestPlainTargetsAreQuiet: the rule is about the dot, not about writing variables. A
// model whose targets are ordinary names must compile without a word — including one
// whose *expressions* read paths, which is what dots are for.
func TestPlainTargetsAreQuiet(t *testing.T) {
	model := dottedModel("gesamtumsatz", "task_counter", "ergebnis", "customer", "bewertungen")
	cp, err := Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps := dottedProblems(t, model); len(ps) != 0 {
		t.Errorf("plain names raised %d findings: %+v", len(ps), ps)
	}
	if HasErrors(Validate(cp)) {
		t.Errorf("a model with plain targets does not deploy: %+v", Validate(cp))
	}
}

// dottedConnectorFindings builds a one-task process around add and returns its
// variable.dotted-target findings, so a connector's own result fields can be tested
// without a BPMN dialect for every connector kind.
func dottedConnectorFindings(t *testing.T, add func(b *Builder) int32) []Problem {
	t.Helper()
	b := NewBuilder(1, "p", 1)
	start, task, end := b.AddStartEvent(), add(b), b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var out []Problem
	for _, p := range Validate(cp) {
		if p.Rule == RuleDottedTarget {
			out = append(out, p)
		}
	}
	return out
}

// TestConnectorResultsAreReadByJobType: a connector task carries every connector's
// fields in one struct, and each connector reads only its own. The rest are left at the
// zero value — which for an interned name is not "none" (-1) but index 0, the first
// reserved job type, "io.atlas.dmn". Reading them blind therefore accused every clio,
// REST and mail task in the estate of a dotted target it does not have.
func TestConnectorResultsAreReadByJobType(t *testing.T) {
	// A clio read task: it sets ResultVar and nothing CSV or LDIF.
	if ps := dottedConnectorFindings(t, func(b *Builder) int32 {
		return b.AddClioReadTask("c", "subject", "events", 10, 3)
	}); len(ps) != 0 {
		t.Errorf("a clio task was accused of a dotted target it does not have: %+v", ps)
	}
	// The same task with a genuinely dotted result is still caught.
	if ps := dottedConnectorFindings(t, func(b *Builder) int32 {
		return b.AddClioReadTask("c", "subject", "audit.events", 10, 3)
	}); len(ps) != 1 {
		t.Errorf("a dotted clio result raised %d findings, want 1: %+v", len(ps), ps)
	}
	// And a CSV task's own result field, which is where the rows land, is read.
	if ps := dottedConnectorFindings(t, func(b *Builder) int32 {
		return b.AddCsvConnectorTask(CsvConfig{Source: "csvText", Result: "kunden.rows"})
	}); len(ps) != 1 {
		t.Errorf("a dotted CSV result raised %d findings, want 1: %+v", len(ps), ps)
	}
	if ps := dottedConnectorFindings(t, func(b *Builder) int32 {
		return b.AddLdifConnectorTask(LdifConfig{Source: "ldifText", Result: "dir.entries"})
	}); len(ps) != 1 {
		t.Errorf("a dotted LDIF result raised %d findings, want 1: %+v", len(ps), ps)
	}
}
