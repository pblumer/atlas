package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// nestedDataAssocXML runs a script task inside an embedded subprocess that reads the
// process-level data object, adds to it, and writes it back — the read-modify-write
// a model uses to keep a register. Self-completing: no worker is involved, so the
// instance runs to the end on its own.
const nestedDataAssocXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <process id="nested-data" isExecutable="true">
    <dataObject id="DO_reg" name="reg" isCollection="true"/>
    <dataObjectReference id="Ref_seed" name="reg" dataObjectRef="DO_reg"/>
    <dataObjectReference id="Ref_read" name="reg" dataObjectRef="DO_reg"/>
    <dataObjectReference id="Ref_write" name="reg" dataObjectRef="DO_reg"/>
    <startEvent id="start"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= [1]" resultVariable="seeded"/></extensionElements>
      <dataOutputAssociation id="doa_seed">
        <targetRef>Ref_seed</targetRef>
        <assignment><from>= [1]</from></assignment>
      </dataOutputAssociation>
    </scriptTask>
    <subProcess id="sub">
      <startEvent id="sub_start"/>
      <scriptTask id="sub_task">
        <extensionElements><zeebe:script expression="= 1" resultVariable="ran"/></extensionElements>
        <dataInputAssociation id="dia_sub">
          <sourceRef>Ref_read</sourceRef>
          <assignment><to>cur</to></assignment>
        </dataInputAssociation>
        <dataOutputAssociation id="doa_sub">
          <targetRef>Ref_write</targetRef>
          <assignment><from>= append(cur, 2)</from></assignment>
        </dataOutputAssociation>
      </scriptTask>
      <endEvent id="sub_end"/>
      <sequenceFlow id="sf1" sourceRef="sub_start" targetRef="sub_task"/>
      <sequenceFlow id="sf2" sourceRef="sub_task" targetRef="sub_end"/>
    </subProcess>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="sub"/>
    <sequenceFlow id="f3" sourceRef="sub" targetRef="end"/>
  </process>
</definitions>`

// TestDataAssociationsInsideSubprocessReadAndWrite is the end-to-end half of the
// compiler regression: an activity nested in a subprocess must read the enclosing
// process's data object and write it back. Data objects are keyed by process
// instance, so the nesting is invisible to the store — but the associations used to
// be dropped at compile time, and the symptom was a task that appeared to run and
// changed nothing.
//
// The assertion is on the object's value, not on the task having executed: a
// dropped association leaves the task running perfectly and the object untouched.
func TestDataAssociationsInsideSubprocessReadAndWrite(t *testing.T) {
	dir := t.TempDir()
	cp, err := compiler.Parse(1, 1, strings.NewReader(nestedDataAssocXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := openHarness(t, dir)
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1) // the first minted key is the process instance
	reg := readDataObject(t, h.store, scope, "reg")
	if reg == nil {
		t.Fatal("data object 'reg' was never written")
	}
	if got, want := reg.Text, "[1,2]"; got != want {
		t.Errorf("reg = %s, want %s — the subprocess task's data associations did not take effect", got, want)
	}
}
