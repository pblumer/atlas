package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// mustCompile compiles a FEEL expression for a test or fails.
func mustCompile(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	e, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("CompileAuto(%q): %v", src, err)
	}
	return e
}

// nodeByBpmnId finds the compiled node id whose source BPMN id matches, or fails.
func nodeByBpmnId(t *testing.T, cp *compiler.CompiledProcess, bpmnId string) int32 {
	t.Helper()
	for id := int32(0); id < 64; id++ {
		if cp.ElementBpmnId(id) == bpmnId {
			return id
		}
	}
	t.Fatalf("node %q not found", bpmnId)
	return -1
}

// TestBuilderAddIOMappings checks the programmatic builder groups a node's input
// and output mappings into the shared arrays reachable via the accessors, and that
// a node with no mappings reports empty slices (ADR-0068).
func TestBuilderAddIOMappings(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("worker", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddInputMapping(task, "amount", mustCompile(t, "order.total"))
	b.AddOutputMapping(task, "orderStatus", mustCompile(t, "result.status"))

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ins := cp.IOInputs(task)
	if len(ins) != 1 {
		t.Fatalf("input mappings = %d, want 1", len(ins))
	}
	if got := cp.Intern(ins[0].Target); got != "amount" {
		t.Errorf("input Target = %q, want amount", got)
	}
	if ins[0].Source == nil {
		t.Error("input Source = nil, want compiled FEEL")
	}

	outs := cp.IOOutputs(task)
	if len(outs) != 1 {
		t.Fatalf("output mappings = %d, want 1", len(outs))
	}
	if got := cp.Intern(outs[0].Target); got != "orderStatus" {
		t.Errorf("output Target = %q, want orderStatus", got)
	}
	if outs[0].Source == nil {
		t.Error("output Source = nil, want compiled FEEL")
	}

	// A node with no mappings reports empty slices.
	if got := cp.IOInputs(start); len(got) != 0 {
		t.Errorf("start input mappings = %d, want 0", len(got))
	}
	if got := cp.IOOutputs(start); len(got) != 0 {
		t.Errorf("start output mappings = %d, want 0", len(got))
	}
}

// TestParseIOMapping parses a zeebe:ioMapping with an input and an output on a
// service task into per-node input/output mapping tables (ADR-0068, phase 3).
func TestParseIOMapping(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="Start"/>
    <serviceTask id="T">
      <extensionElements>
        <zeebe:taskDefinition type="worker"/>
        <zeebe:ioMapping>
          <zeebe:input source="=order.total" target="amount"/>
          <zeebe:output source="=result.status" target="orderStatus"/>
        </zeebe:ioMapping>
      </extensionElements>
    </serviceTask>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="T"/>
    <sequenceFlow id="f2" sourceRef="T" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := nodeByBpmnId(t, cp, "T")

	ins := cp.IOInputs(node)
	if len(ins) != 1 {
		t.Fatalf("input mappings = %d, want 1", len(ins))
	}
	if got := cp.Intern(ins[0].Target); got != "amount" {
		t.Errorf("input Target = %q, want amount", got)
	}
	if ins[0].Source == nil {
		t.Error("input Source = nil, want compiled FEEL for =order.total")
	}

	outs := cp.IOOutputs(node)
	if len(outs) != 1 {
		t.Fatalf("output mappings = %d, want 1", len(outs))
	}
	if got := cp.Intern(outs[0].Target); got != "orderStatus" {
		t.Errorf("output Target = %q, want orderStatus", got)
	}
	if outs[0].Source == nil {
		t.Error("output Source = nil, want compiled FEEL for =result.status")
	}
}

// TestParseIOMappingScriptAndUserTasks checks the generic ioMapping is wired for
// script and user tasks too, not just service tasks (ADR-0068).
func TestParseIOMappingScriptAndUserTasks(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="Start"/>
    <scriptTask id="S">
      <extensionElements>
        <zeebe:script expression="=1 + 1" resultVariable="sum"/>
        <zeebe:ioMapping>
          <zeebe:input source="=a" target="localA"/>
        </zeebe:ioMapping>
      </extensionElements>
    </scriptTask>
    <userTask id="U">
      <extensionElements>
        <zeebe:ioMapping>
          <zeebe:output source="=approved" target="decision"/>
        </zeebe:ioMapping>
      </extensionElements>
    </userTask>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="S"/>
    <sequenceFlow id="f2" sourceRef="S" targetRef="U"/>
    <sequenceFlow id="f3" sourceRef="U" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := nodeByBpmnId(t, cp, "S")
	if ins := cp.IOInputs(s); len(ins) != 1 || cp.Intern(ins[0].Target) != "localA" {
		t.Errorf("script task input mappings = %+v, want one targeting localA", ins)
	}
	u := nodeByBpmnId(t, cp, "U")
	if outs := cp.IOOutputs(u); len(outs) != 1 || cp.Intern(outs[0].Target) != "decision" {
		t.Errorf("user task output mappings = %+v, want one targeting decision", outs)
	}
}

// TestParseIOMappingErrors rejects a mapping with no target and one with an
// uncompilable source, exactly like a bad script-task expression fails deploy.
func TestParseIOMappingErrors(t *testing.T) {
	cases := map[string]string{
		"input no target":  `<zeebe:input source="=x" target=""/>`,
		"input no source":  `<zeebe:input source="" target="v"/>`,
		"input bad source": `<zeebe:input source="=1 +" target="v"/>`,
		"output no target": `<zeebe:output source="=x" target=""/>`,
		"output bad src":   `<zeebe:output source="=1 +" target="v"/>`,
	}
	for name, mapping := range cases {
		t.Run(name, func(t *testing.T) {
			model := `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="Start"/>
    <serviceTask id="T">
      <extensionElements>
        <zeebe:taskDefinition type="worker"/>
        <zeebe:ioMapping>` + mapping + `</zeebe:ioMapping>
      </extensionElements>
    </serviceTask>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="T"/>
    <sequenceFlow id="f2" sourceRef="T" targetRef="End"/>
  </process>
</definitions>`
			if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
				t.Fatalf("Parse err = nil, want an error (%s)", name)
			}
		})
	}
}
