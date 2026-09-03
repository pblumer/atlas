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

// TestParseDataObjectReferenceWithNoNameFallsBackToItsID covers the reference that
// lost its declaration *and* has no name of its own to stand in with.
//
// This used to be a refusal — a reference whose dataObjectRef named nothing failed
// the whole deploy. It is not one any more, for the reason
// TestDataObjectReferenceStandsInForALostDeclaration sets out: the declaration is the
// half a model can do without, and refusing over it strands a diagram whose meaning
// nobody is unsure about. With no name either there is nothing left to call the
// object but the reference's own id, which is exactly what a nameless <dataObject>
// already falls back to.
func TestParseDataObjectReferenceWithNoNameFallsBackToItsID(t *testing.T) {
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

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	objs := cp.DataObjects()
	if len(objs) != 1 {
		t.Fatalf("DataObjects = %d, want 1", len(objs))
	}
	if got := cp.Intern(objs[0].Name); got != "Ref" {
		t.Errorf("Name = %q, want the reference's own id Ref", got)
	}
	if got := cp.Intern(objs[0].InitialState); got != "x" {
		t.Errorf("InitialState = %q, want x", got)
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

// itemDefinitionBPMN declares its data-object types the way the BPMN specification
// intends and the way the Modeler writes them: an <itemDefinition> at definitions
// level, referenced by itemSubjectRef. The structureRef carries the real name; the
// id is only the reference handle, so a class name that is not a valid XML id still
// travels.
const itemDefinitionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <itemDefinition id="ItemDefinition_Line_item" structureRef="Line item"/>
  <itemDefinition id="Bare"/>
  <process id="p" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"/>
    <dataObject id="DO_line" name="line" itemSubjectRef="ItemDefinition_Line_item"/>
    <dataObject id="DO_bare" name="bare" itemSubjectRef="Bare"/>
    <dataObject id="DO_direct" name="direct" itemSubjectRef="Claim"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestDataObjectItemSubjectRefResolvesThroughItemDefinition pins how a declared
// type is read. BPMN's itemSubjectRef is a *reference* to an <itemDefinition>, and
// that is what a modeling tool writes — so the compiler resolves it, taking the
// definition's structureRef as the type name. A reference naming no itemDefinition
// keeps working as the name itself, which is what every hand-written model and
// every Atlas fixture does.
func TestDataObjectItemSubjectRefResolvesThroughItemDefinition(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(itemDefinitionBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	byName := map[string]string{}
	for _, do := range cp.DataObjects() {
		byName[cp.Intern(do.Name)] = cp.Intern(do.ItemType)
	}
	tests := []struct{ object, want string }{
		{"order", "Order"},
		// The id is a handle; the structureRef is the name, so a class called "Line
		// item" survives a reference id that could not contain a space.
		{"line", "Line item"},
		// An itemDefinition with no structureRef says nothing more than its own id.
		{"bare", "Bare"},
		// And a reference that names no itemDefinition is the type name itself — the
		// shorthand every hand-written model uses.
		{"direct", "Claim"},
	}
	for _, tt := range tests {
		if got := byName[tt.object]; got != tt.want {
			t.Errorf("data object %q: ItemType = %q, want %q", tt.object, got, tt.want)
		}
	}
}

// vendorItemDefinitionBPMN is how MID Innovator (bpanda) declares a type: a bare
// GUID id, no structureRef, and the name in a vendor extension property. This is a
// real export shape, not a hypothetical one — the itemDefinitions in a model exported
// from Innovator 16.2 look exactly like this, down to the property's own id.
const vendorItemDefinitionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpanda="http://www.smartfacts.com" xmlns:ino="http://www.mid.de/spec/Innovator/14.3.1">
  <itemDefinition id="_853994e9-12f5-9cef-bf69-ca3e2b7cb6a8">
    <extensionElements>
      <ino:stereotypename value="businessObject"/>
      <bpanda:property name="Name" value="Incident" id="property_ELNamedElement_Name"/>
      <bpanda:property name="Bearbeitungsstatus" value="in Arbeit" id="label_Editing Status"/>
    </extensionElements>
  </itemDefinition>
  <itemDefinition id="_636d4322-604f-29cb-2beb-0feb766ec43e" structureRef="Order">
    <extensionElements>
      <bpanda:property name="Name" value="Bestellung"/>
    </extensionElements>
  </itemDefinition>
  <itemDefinition id="_7a8d7463-6484-807f-7d6c-e2dbeb598f71">
    <extensionElements>
      <bpanda:property name="Bearbeitungsstatus" value="in Arbeit"/>
    </extensionElements>
  </itemDefinition>
  <process id="p" isExecutable="true">
    <dataObject id="DO_incident" name="incident" itemSubjectRef="_853994e9-12f5-9cef-bf69-ca3e2b7cb6a8"/>
    <dataObject id="DO_order" name="order" itemSubjectRef="_636d4322-604f-29cb-2beb-0feb766ec43e"/>
    <dataObject id="DO_untitled" name="untitled" itemSubjectRef="_7a8d7463-6484-807f-7d6c-e2dbeb598f71"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestDataObjectItemTypeReadsAVendorNameWhenThereIsNoStructureRef pins the third way
// round. BPMN gives an <itemDefinition> no name attribute — a root element carries an
// id and nothing else — so structureRef is the only slot the specification offers for
// the name of the type being declared, and a tool that does not use it puts the name
// in its own namespace instead.
//
// Reading only the id, as this did before, turns every data object in such a model
// into one declaring a type called _853994e9-12f5-9cef-bf69-ca3e2b7cb6a8 — shown that
// way in the Console and then reported as a class nothing models, against a name
// nobody could have modeled.
func TestDataObjectItemTypeReadsAVendorNameWhenThereIsNoStructureRef(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(vendorItemDefinitionBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	byName := map[string]string{}
	for _, do := range cp.DataObjects() {
		byName[cp.Intern(do.Name)] = cp.Intern(do.ItemType)
	}
	tests := []struct{ object, want string }{
		// The name the exporter did record, rather than the GUID beside it.
		{"incident", "Incident"},
		// structureRef still wins: it is the slot the specification names, so a model
		// that fills it means what it says there, whatever a vendor property adds.
		{"order", "Order"},
		// A definition that names itself nowhere still says no more than its own id.
		{"untitled", "_7a8d7463-6484-807f-7d6c-e2dbeb598f71"},
	}
	for _, tt := range tests {
		if got := byName[tt.object]; got != tt.want {
			t.Errorf("data object %q: ItemType = %q, want %q", tt.object, got, tt.want)
		}
	}
}

// danglingDataObjectRefBPMN is a diagram that lost the <dataObject> its reference
// points at — the shape on the canvas survived, the declaration behind it did not.
// This is a real save from the Modeler, reduced: the box reads "Kunde [received]" to
// anybody looking at it, and dataObjectRef names an id the model does not contain.
const danglingDataObjectRefBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <dataObjectReference id="DataObjectReference_11h2i4n" name="Kunde" dataObjectRef="DataObject_0s4i37q">
      <dataState name="received"/>
    </dataObjectReference>
    <task id="t" name="Kunde einlesen">
      <incoming>f1</incoming><outgoing>f2</outgoing>
      <property id="Property_0ohqky9" name="__targetRef_placeholder"/>
      <dataInputAssociation id="DataInputAssociation_0c1cg2c">
        <sourceRef>DataObjectReference_11h2i4n</sourceRef>
        <targetRef>Property_0ohqky9</targetRef>
        <assignment><to xsi:type="tFormalExpression">Kunde</to></assignment>
      </dataInputAssociation>
      <dataOutputAssociation id="DataOutputAssociation_1">
        <targetRef>DataObjectReference_11h2i4n</targetRef>
        <assignment><from xsi:type="tFormalExpression">=customer</from></assignment>
      </dataOutputAssociation>
    </task>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <endEvent id="e"><incoming>f2</incoming></endEvent>
  </process>
</definitions>`

// TestDataObjectReferenceStandsInForALostDeclaration pins that such a model compiles
// and means what it looks like.
//
// A data object is two elements: the <dataObject> that declares it and carries its
// type, and the <dataObjectReference> that puts it on the canvas with its name, its
// data state and its shape. Only the second is drawn, so only the second is visibly
// there — and a model can arrive having lost the first. Before this, the compiler
// refused the whole deploy over it, naming an id nobody had ever typed and offering
// nothing to do about it.
//
// Nothing about such a model is ambiguous: a data object's identity is its name, and
// the name is on the reference. So the reference stands in for its own declaration —
// the same fallback a <dataStoreReference> naming no root element already gets — and
// the associations wire to it. Only the declared type is genuinely lost, and the
// object is seeded without one rather than with a guess.
func TestDataObjectReferenceStandsInForALostDeclaration(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(danglingDataObjectRefBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	objs := cp.DataObjects()
	if len(objs) != 1 {
		t.Fatalf("DataObjects = %d, want 1", len(objs))
	}
	// Named off the reference, in the state the reference declares.
	if got := cp.Intern(objs[0].Name); got != "Kunde" {
		t.Errorf("Name = %q, want Kunde", got)
	}
	if got := cp.Intern(objs[0].InitialState); got != "received" {
		t.Errorf("InitialState = %q, want received", got)
	}
	// The type was on the lost declaration, so it is empty rather than invented.
	if got := cp.Intern(objs[0].ItemType); got != "" {
		t.Errorf("ItemType = %q, want empty", got)
	}

	// And both associations resolve to it, so the task reads and writes the object
	// the diagram shows it reading and writing.
	var task int32 = -1
	for id := int32(0); id < 10; id++ {
		if cp.ElementBpmnId(id) == "t" {
			task = id
			break
		}
	}
	if task < 0 {
		t.Fatal("task node not found")
	}
	out := cp.DataOutputAssociations(task)
	if len(out) != 1 || cp.Intern(out[0].DataObject) != "Kunde" {
		t.Fatalf("output associations = %+v, want one writing Kunde", out)
	}
	if got := cp.Intern(out[0].TargetState); got != "received" {
		t.Errorf("TargetState = %q, want received", got)
	}
	in := cp.DataInputAssociations(task)
	if len(in) != 1 || cp.Intern(in[0].DataObject) != "Kunde" {
		t.Fatalf("input associations = %+v, want one reading Kunde", in)
	}
}

// dataStoreBPMN declares a store the BPMN way: a <dataStore> at definitions level
// and a <dataStoreReference> inside the process that points at it. A second
// reference carries only its own name, which is what a tool that draws the box
// without declaring the root element produces.
const dataStoreBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <dataStore id="Store_orders" name="Orders" isUnlimited="true"/>
  <process id="p" isExecutable="true">
    <dataStoreReference id="Ref_orders" name="order archive" dataStoreRef="Store_orders"/>
    <dataStoreReference id="Ref_bare" name="Invoices"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestDataStoreReferencesCompile pins that a process's data stores are compiled at
// all — until now a <dataStoreReference> was parsed as nothing, so a model could
// name where its data lives and Atlas would not read the sentence.
//
// The name is the *store's*, not the reference's: the reference is one view of the
// store on one diagram and may be labelled for that diagram, while the store is the
// thing every process means when it says Orders.
func TestDataStoreReferencesCompile(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(dataStoreBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stores := cp.DataStores()
	if len(stores) != 2 {
		t.Fatalf("stores = %d, want 2: %+v", len(stores), stores)
	}
	byElement := map[string]string{}
	for _, st := range stores {
		byElement[cp.Intern(st.ElementId)] = cp.Intern(st.Name)
	}
	if got := byElement["Ref_orders"]; got != "Orders" {
		t.Errorf("Ref_orders resolves to %q, want the store's own name Orders", got)
	}
	// A reference with no root element to resolve is its own name — the shorthand a
	// drawing tool produces, and still a usable statement about where data lives.
	if got := byElement["Ref_bare"]; got != "Invoices" {
		t.Errorf("Ref_bare resolves to %q, want Invoices", got)
	}
}

// TestDataStoreDeduplicatesByName covers the same store drawn twice on one diagram:
// two boxes, one store, so the process names it once.
func TestDataStoreDeduplicatesByName(t *testing.T) {
	const twice = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <dataStore id="Store_orders" name="Orders"/>
  <process id="p" isExecutable="true">
    <dataStoreReference id="Ref_a" dataStoreRef="Store_orders"/>
    <dataStoreReference id="Ref_b" dataStoreRef="Store_orders"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(twice))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.DataStores(); len(got) != 1 || cp.Intern(got[0].Name) != "Orders" {
		t.Errorf("stores = %+v, want one Orders", got)
	}
}

// TestParseDataAssociationsInNestedScopes checks that an activity nested inside a
// subprocess keeps its data associations (ADR-0058/0059). Wiring used to walk only
// the process root's element lists, so an association drawn inside any nested scope
// was dropped at compile time — no error, no warning, and at runtime the data object
// stayed empty and the read variable null. The scopes covered here are the three a
// model can nest an activity in: an ordinary subprocess, an event subprocess, and an
// ad-hoc subprocess (and one subprocess inside another, for the recursion itself).
func TestParseDataAssociationsInNestedScopes(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <dataObject id="DO_reg" name="register"/>
    <dataObjectReference id="Ref_reg" dataObjectRef="DO_reg"/>
    <startEvent id="s"/>
    <subProcess id="Sub">
      <startEvent id="subStart"/>
      <scriptTask id="InSub">
        <extensionElements><zeebe:script expression="=reg" resultVariable="reg"/></extensionElements>
        <dataInputAssociation><sourceRef>Ref_reg</sourceRef><assignment><to>reg</to></assignment></dataInputAssociation>
        <dataOutputAssociation><targetRef>Ref_reg</targetRef><assignment><from>=reg</from></assignment></dataOutputAssociation>
      </scriptTask>
      <subProcess id="Deeper">
        <startEvent id="deepStart"/>
        <task id="InDeeper">
          <dataInputAssociation><sourceRef>Ref_reg</sourceRef><assignment><to>deep</to></assignment></dataInputAssociation>
        </task>
        <endEvent id="deepEnd"/>
        <sequenceFlow id="df1" sourceRef="deepStart" targetRef="InDeeper"/>
        <sequenceFlow id="df2" sourceRef="InDeeper" targetRef="deepEnd"/>
      </subProcess>
      <endEvent id="subEnd"/>
      <sequenceFlow id="sf1" sourceRef="subStart" targetRef="InSub"/>
      <sequenceFlow id="sf2" sourceRef="InSub" targetRef="Deeper"/>
      <sequenceFlow id="sf3" sourceRef="Deeper" targetRef="subEnd"/>
    </subProcess>
    <subProcess id="Handler" triggeredByEvent="true">
      <startEvent id="handlerStart">
        <messageEventDefinition messageRef="Msg"/>
      </startEvent>
      <task id="InHandler">
        <dataOutputAssociation><targetRef>Ref_reg</targetRef><assignment><from>=payload</from></assignment></dataOutputAssociation>
      </task>
      <endEvent id="handlerEnd"/>
      <sequenceFlow id="hf1" sourceRef="handlerStart" targetRef="InHandler"/>
      <sequenceFlow id="hf2" sourceRef="InHandler" targetRef="handlerEnd"/>
    </subProcess>
    <adHocSubProcess id="AdHoc">
      <userTask id="InAdHoc">
        <dataInputAssociation><sourceRef>Ref_reg</sourceRef><assignment><to>adhoc</to></assignment></dataInputAssociation>
      </userTask>
    </adHocSubProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="Sub"/>
    <sequenceFlow id="f2" sourceRef="Sub" targetRef="AdHoc"/>
    <sequenceFlow id="f3" sourceRef="AdHoc" targetRef="e"/>
  </process>
  <message id="Msg" name="ev"/>
</definitions>`

	cp, err := compiler.Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := func(id string) int32 {
		for i := int32(0); i < int32(cp.NodeCount()); i++ {
			if cp.ElementBpmnId(i) == id {
				return i
			}
		}
		t.Fatalf("element %q not found in the compiled process", id)
		return -1
	}
	// Reads: one input association each, all resolving to the one logical object.
	for _, tc := range []struct{ element, variable string }{
		{"InSub", "reg"},
		{"InDeeper", "deep"},
		{"InAdHoc", "adhoc"},
	} {
		in := cp.DataInputAssociations(node(tc.element))
		if len(in) != 1 {
			t.Errorf("%s: input associations = %d, want 1 (nested scopes must keep them)", tc.element, len(in))
			continue
		}
		if got := cp.Intern(in[0].DataObject); got != "register" {
			t.Errorf("%s: input object = %q, want register", tc.element, got)
		}
		if got := cp.Intern(in[0].Variable); got != tc.variable {
			t.Errorf("%s: input variable = %q, want %q", tc.element, got, tc.variable)
		}
	}
	// Writes: one output association each.
	for _, element := range []string{"InSub", "InHandler"} {
		out := cp.DataOutputAssociations(node(element))
		if len(out) != 1 {
			t.Errorf("%s: output associations = %d, want 1 (nested scopes must keep them)", element, len(out))
			continue
		}
		if got := cp.Intern(out[0].DataObject); got != "register" {
			t.Errorf("%s: output object = %q, want register", element, got)
		}
	}
}

// TestParseDataAssociationErrorInNestedScope checks a bad association inside a
// subprocess is reported rather than silently dropped: the recursive wiring must
// carry the same rejections the root scope's does.
func TestParseDataAssociationErrorInNestedScope(t *testing.T) {
	const model = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DO_reg" name="register"/>
    <startEvent id="s"/>
    <subProcess id="Sub">
      <startEvent id="subStart"/>
      <task id="InSub">
        <dataOutputAssociation><targetRef>Nope</targetRef></dataOutputAssociation>
      </task>
      <endEvent id="subEnd"/>
      <sequenceFlow id="sf1" sourceRef="subStart" targetRef="InSub"/>
      <sequenceFlow id="sf2" sourceRef="InSub" targetRef="subEnd"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="Sub"/>
    <sequenceFlow id="f2" sourceRef="Sub" targetRef="e"/>
  </process>
</definitions>`

	if _, err := compiler.Parse(1, 1, strings.NewReader(model)); err == nil {
		t.Fatal("Parse accepted a data association naming an unknown target inside a subprocess")
	}
}
