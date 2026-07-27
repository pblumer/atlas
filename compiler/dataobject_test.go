package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestProcessId checks a compiled process reports the BPMN process id it was built
// with — the identity a deployment uses to tell one process's versions apart.
func TestProcessId(t *testing.T) {
	b := compiler.NewBuilder(1, "order-process", 3)
	b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ProcessId(); got != "order-process" {
		t.Errorf("ProcessId() = %q, want order-process", got)
	}
}

// TestBuilderAddDataObject checks that data objects added programmatically land in
// the compiled process's data-object table with their strings interned and
// resolvable, and that they are not flow nodes (ADR-0053).
func TestBuilderAddDataObject(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	b.AddStartEvent()
	b.AddDataObject("order", "OrderType", "received", false)
	b.AddDataObject("items", "", "", true)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	dos := cp.DataObjects()
	if len(dos) != 2 {
		t.Fatalf("DataObjects len = %d, want 2", len(dos))
	}

	if got := cp.Intern(dos[0].Name); got != "order" {
		t.Errorf("do[0].Name = %q, want order", got)
	}
	if got := cp.Intern(dos[0].ItemType); got != "OrderType" {
		t.Errorf("do[0].ItemType = %q, want OrderType", got)
	}
	if got := cp.Intern(dos[0].InitialState); got != "received" {
		t.Errorf("do[0].InitialState = %q, want received", got)
	}
	if dos[0].IsCollection {
		t.Error("do[0].IsCollection = true, want false")
	}

	// The untyped, stateless collection interns its empty strings to -1 (Intern → "").
	if got := cp.Intern(dos[1].ItemType); got != "" {
		t.Errorf("do[1].ItemType = %q, want empty", got)
	}
	if got := cp.Intern(dos[1].InitialState); got != "" {
		t.Errorf("do[1].InitialState = %q, want empty", got)
	}
	if !dos[1].IsCollection {
		t.Error("do[1].IsCollection = false, want true")
	}
}

// TestParseDataObject compiles a BPMN model carrying a <dataObject> with a
// <dataState>, and checks the data object reaches the compiled process — proving
// a modeled data object is no longer ignored (ADR-0053).
func TestParseDataObject(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_1" name="order" isCollection="true">
      <dataState name="received"/>
    </dataObject>
    <startEvent id="Start"/>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	dos := cp.DataObjects()
	if len(dos) != 1 {
		t.Fatalf("DataObjects len = %d, want 1", len(dos))
	}
	if got := cp.Intern(dos[0].Name); got != "order" {
		t.Errorf("name = %q, want order", got)
	}
	if got := cp.Intern(dos[0].InitialState); got != "received" {
		t.Errorf("initial state = %q, want received", got)
	}
	if !dos[0].IsCollection {
		t.Error("IsCollection = false, want true")
	}
}

// TestParseDataObjectMultipleReferencesOneObject checks that several
// <dataObjectReference>s pointing at one <dataObject> (the BPMN way to show a data
// object next to several activities) seed a single logical object, and that
// associations through any reference resolve to it (ADR-0053). Also covers a
// second, duplicate-named <dataObject> being folded into the same one.
func TestParseDataObjectMultipleReferencesOneObject(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DO1" name="order"/>
    <dataObject id="DO_dup" name="order"/>
    <dataObjectReference id="Ref_A" dataObjectRef="DO1"/>
    <dataObjectReference id="Ref_C" dataObjectRef="DO_dup"/>
    <startEvent id="s"/>
    <task id="A"><dataOutputAssociation><targetRef>Ref_A</targetRef><assignment><from>=n</from><to>name</to></assignment></dataOutputAssociation></task>
    <task id="C"><dataInputAssociation><sourceRef>Ref_C</sourceRef><assignment><to>ord</to></assignment></dataInputAssociation></task>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="A"/>
    <sequenceFlow id="f2" sourceRef="A" targetRef="C"/>
    <sequenceFlow id="f3" sourceRef="C" targetRef="e"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// One logical object, despite two <dataObject> elements and two references.
	if dos := cp.DataObjects(); len(dos) != 1 || cp.Intern(dos[0].Name) != "order" {
		t.Fatalf("DataObjects = %d, want one named order (%v)", len(dos), dos)
	}
	find := func(id string) int32 {
		for i := int32(0); i < 20; i++ {
			if cp.ElementBpmnId(i) == id {
				return i
			}
		}
		return -1
	}
	// The output association through Ref_A and the input association through Ref_C
	// both resolve to the same object "order".
	out := cp.DataOutputAssociations(find("A"))
	if len(out) != 1 || cp.Intern(out[0].DataObject) != "order" {
		t.Errorf("task A output object = %v, want order", out)
	}
	in := cp.DataInputAssociations(find("C"))
	if len(in) != 1 || cp.Intern(in[0].DataObject) != "order" {
		t.Errorf("task C input object = %v, want order", in)
	}
}

// TestParseDataObjectDefaultsToId checks a nameless data object falls back to its
// BPMN id so it stays addressable, and that an absent <dataState> interns to none.
func TestParseDataObjectDefaultsToId(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_1"/>
    <startEvent id="Start"/>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	dos := cp.DataObjects()
	if len(dos) != 1 {
		t.Fatalf("DataObjects len = %d, want 1", len(dos))
	}
	if got := cp.Intern(dos[0].Name); got != "DataObject_1" {
		t.Errorf("name = %q, want DataObject_1 (id fallback)", got)
	}
	if got := cp.Intern(dos[0].InitialState); got != "" {
		t.Errorf("initial state = %q, want empty", got)
	}
}

// TestParseDataOutputAssociation compiles a task with a <dataOutputAssociation>
// targeting a <dataObjectReference> that carries a [approved] data state, and
// checks the association reaches the compiled node with the resolved data-object
// name, the target state, and a compiled value expression (ADR-0058).
func TestParseDataOutputAssociation(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"><dataState name="received"/></dataObject>
    <dataObjectReference id="Ref_approved" dataObjectRef="DataObject_order"><dataState name="approved"/></dataObjectReference>
    <startEvent id="Start"/>
    <task id="Approve">
      <dataOutputAssociation id="doa1">
        <targetRef>Ref_approved</targetRef>
        <assignment><from>=decision</from></assignment>
      </dataOutputAssociation>
    </task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Approve"/>
    <sequenceFlow id="f2" sourceRef="Approve" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Find the Approve task node by its BPMN id.
	var node int32 = -1
	for id := int32(0); ; id++ {
		if cp.ElementBpmnId(id) == "" && id > 10 {
			break
		}
		if cp.ElementBpmnId(id) == "Approve" {
			node = id
			break
		}
	}
	if node < 0 {
		t.Fatal("Approve task node not found")
	}
	assocs := cp.DataOutputAssociations(node)
	if len(assocs) != 1 {
		t.Fatalf("associations = %d, want 1", len(assocs))
	}
	a := assocs[0]
	if got := cp.Intern(a.DataObject); got != "order" {
		t.Errorf("DataObject = %q, want order", got)
	}
	if got := cp.Intern(a.TargetState); got != "approved" {
		t.Errorf("TargetState = %q, want approved", got)
	}
	if a.Value == nil {
		t.Error("Value expr = nil, want compiled FEEL for =decision")
	}
}

// TestParseDataOutputAssociationTargetPath checks the assignment's <to> member path
// is captured, so a write can target a single field of a structured object (ADR-0060).
func TestParseDataOutputAssociationTargetPath(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="SetName">
      <dataOutputAssociation>
        <targetRef>DataObject_order</targetRef>
        <assignment><from>=customerName</from><to>name</to></assignment>
      </dataOutputAssociation>
    </task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="SetName"/>
    <sequenceFlow id="f2" sourceRef="SetName" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var node int32 = -1
	for id := int32(0); id < 10; id++ {
		if cp.ElementBpmnId(id) == "SetName" {
			node = id
			break
		}
	}
	if node < 0 {
		t.Fatal("SetName task node not found")
	}
	assocs := cp.DataOutputAssociations(node)
	if len(assocs) != 1 {
		t.Fatalf("associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].TargetPath); got != "name" {
		t.Errorf("TargetPath = %q, want name", got)
	}
	if assocs[0].Value == nil {
		t.Error("Value expr = nil, want compiled FEEL for =customerName")
	}
}

// TestParseDataOutputAssociationDirectTarget checks a targetRef that names a data
// object directly (no reference) resolves to that object with no state change, and
// that an association with no <assignment> is a state-only transition (nil value).
func TestParseDataOutputAssociationDirectTarget(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Touch"><dataOutputAssociation><targetRef>DataObject_order</targetRef></dataOutputAssociation></task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Touch"/>
    <sequenceFlow id="f2" sourceRef="Touch" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var node int32 = -1
	for id := int32(0); id < 10; id++ {
		if cp.ElementBpmnId(id) == "Touch" {
			node = id
			break
		}
	}
	if node < 0 {
		t.Fatal("Touch task node not found")
	}
	assocs := cp.DataOutputAssociations(node)
	if len(assocs) != 1 {
		t.Fatalf("associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].DataObject); got != "order" {
		t.Errorf("DataObject = %q, want order", got)
	}
	if got := cp.Intern(assocs[0].TargetState); got != "" {
		t.Errorf("TargetState = %q, want empty (direct target keeps state)", got)
	}
	if assocs[0].Value != nil {
		t.Error("Value expr != nil, want nil (no assignment = state-only)")
	}
}

// TestParseDataOutputAssociationUnknownTarget rejects a targetRef that names
// neither a data object nor a reference — a modeling error caught at compile time.
func TestParseDataOutputAssociationUnknownTarget(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Bad"><dataOutputAssociation><targetRef>Nope</targetRef></dataOutputAssociation></task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Bad"/>
    <sequenceFlow id="f2" sourceRef="Bad" targetRef="End"/>
  </process>
</definitions>`

	if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
		t.Fatal("Parse err = nil, want an error for unknown targetRef")
	}
}

// TestParseDataOutputAssociationUnknownReferenceObject rejects a reference whose
// dataObjectRef points at no declared data object.
func TestParseDataOutputAssociationUnknownReferenceObject(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObjectReference id="Ref" dataObjectRef="DataObject_missing"><dataState name="x"/></dataObjectReference>
    <startEvent id="Start"/>
    <task id="Bad"><dataOutputAssociation><targetRef>Ref</targetRef></dataOutputAssociation></task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Bad"/>
    <sequenceFlow id="f2" sourceRef="Bad" targetRef="End"/>
  </process>
</definitions>`

	if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
		t.Fatal("Parse err = nil, want an error for a reference with an unknown dataObjectRef")
	}
}

// TestParseDataOutputAssociationBadExpr rejects an association whose assignment
// carries a FEEL expression that does not compile.
func TestParseDataOutputAssociationBadExpr(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Bad">
      <dataOutputAssociation>
        <targetRef>DataObject_order</targetRef>
        <assignment><from>=1 +</from></assignment>
      </dataOutputAssociation>
    </task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Bad"/>
    <sequenceFlow id="f2" sourceRef="Bad" targetRef="End"/>
  </process>
</definitions>`

	if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
		t.Fatal("Parse err = nil, want an error for an invalid assignment expression")
	}
}

// TestParseDataInputAssociation compiles a script task with a
// <dataInputAssociation> reading a data object into a variable via an assignment,
// and checks the association reaches the compiled node with the resolved source
// data-object name, the target variable, and a compiled transform (ADR-0059).
func TestParseDataInputAssociation(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Read">
      <dataInputAssociation>
        <sourceRef>DataObject_order</sourceRef>
        <targetRef>orderAmount</targetRef>
        <assignment><from>=order.amount</from></assignment>
      </dataInputAssociation>
    </task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Read"/>
    <sequenceFlow id="f2" sourceRef="Read" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var node int32 = -1
	for id := int32(0); id < 10; id++ {
		if cp.ElementBpmnId(id) == "Read" {
			node = id
			break
		}
	}
	if node < 0 {
		t.Fatal("Read task node not found")
	}
	assocs := cp.DataInputAssociations(node)
	if len(assocs) != 1 {
		t.Fatalf("input associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].DataObject); got != "order" {
		t.Errorf("DataObject = %q, want order", got)
	}
	if got := cp.Intern(assocs[0].Variable); got != "orderAmount" {
		t.Errorf("Variable = %q, want orderAmount", got)
	}
	if assocs[0].Value == nil {
		t.Error("Value expr = nil, want compiled FEEL for =order.amount")
	}
}

// TestParseDataInputAssociationToVariable checks the target variable is taken from
// the assignment's <to> (the Modeler-authored form), which takes precedence over a
// generated <targetRef> (ADR-0059).
func TestParseDataInputAssociationToVariable(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Read">
      <dataInputAssociation>
        <sourceRef>DataObject_order</sourceRef>
        <targetRef>Property_generated</targetRef>
        <assignment><from>=order.amount</from><to>orderAmount</to></assignment>
      </dataInputAssociation>
    </task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Read"/>
    <sequenceFlow id="f2" sourceRef="Read" targetRef="End"/>
  </process>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var node int32 = -1
	for id := int32(0); id < 10; id++ {
		if cp.ElementBpmnId(id) == "Read" {
			node = id
			break
		}
	}
	assocs := cp.DataInputAssociations(node)
	if len(assocs) != 1 {
		t.Fatalf("input associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].Variable); got != "orderAmount" {
		t.Errorf("Variable = %q, want orderAmount (from <to>, not the generated targetRef)", got)
	}
	if assocs[0].Value == nil {
		t.Error("Value expr = nil, want compiled FEEL for =order.amount")
	}
}

// TestParseDataInputAssociationErrors rejects an input association with an unknown
// source data object and one with no target variable.
func TestParseDataInputAssociationErrors(t *testing.T) {
	cases := map[string]string{
		"unknown source": `<dataInputAssociation><sourceRef>Nope</sourceRef><targetRef>v</targetRef></dataInputAssociation>`,
		"no targetRef":   `<dataInputAssociation><sourceRef>DataObject_order</sourceRef></dataInputAssociation>`,
		"bad expr":       `<dataInputAssociation><sourceRef>DataObject_order</sourceRef><targetRef>v</targetRef><assignment><from>=1 +</from></assignment></dataInputAssociation>`,
	}
	for name, assoc := range cases {
		t.Run(name, func(t *testing.T) {
			model := `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DataObject_order" name="order"/>
    <startEvent id="Start"/>
    <task id="Read">` + assoc + `</task>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Read"/>
    <sequenceFlow id="f2" sourceRef="Read" targetRef="End"/>
  </process>
</definitions>`
			if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
				t.Fatalf("Parse err = nil, want an error (%s)", name)
			}
		})
	}
}

// TestBuilderAddDataInputAssociation checks the programmatic builder groups a
// node's input associations into the shared array reachable via the accessor.
func TestBuilderAddDataInputAssociation(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "", "", false)
	b.AddDataInputAssociation(task, "order", "orderVar", nil)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assocs := cp.DataInputAssociations(task)
	if len(assocs) != 1 {
		t.Fatalf("input associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].DataObject); got != "order" {
		t.Errorf("DataObject = %q, want order", got)
	}
	if got := cp.Intern(assocs[0].Variable); got != "orderVar" {
		t.Errorf("Variable = %q, want orderVar", got)
	}
	if got := cp.DataInputAssociations(start); len(got) != 0 {
		t.Errorf("start input associations = %d, want 0", len(got))
	}
}

// TestBuilderAddDataOutputAssociation checks the programmatic builder groups a
// node's output associations into the shared array reachable via the accessor.
func TestBuilderAddDataOutputAssociation(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(task, "order", nil, "approved", "")

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assocs := cp.DataOutputAssociations(task)
	if len(assocs) != 1 {
		t.Fatalf("associations = %d, want 1", len(assocs))
	}
	if got := cp.Intern(assocs[0].DataObject); got != "order" {
		t.Errorf("DataObject = %q, want order", got)
	}
	if got := cp.Intern(assocs[0].TargetState); got != "approved" {
		t.Errorf("TargetState = %q, want approved", got)
	}
	// A node with no associations has an empty slice.
	if got := cp.DataOutputAssociations(start); len(got) != 0 {
		t.Errorf("start associations = %d, want 0", len(got))
	}
}
