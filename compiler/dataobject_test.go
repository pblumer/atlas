package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestBuilderAddDataObject checks that data objects added programmatically land in
// the compiled process's data-object table with their strings interned and
// resolvable, and that they are not flow nodes (ADR-0051).
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
// a modeled data object is no longer ignored (ADR-0051).
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
