package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

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
	b.AddDataOutputAssociation(task, "order", nil, "approved")

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
