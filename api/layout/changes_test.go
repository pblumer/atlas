package layout

import (
	"reflect"
	"strings"
	"testing"
)

// v1 is the older version of a small approval process as bpmn-js writes it: ids on
// event definitions, incoming/outgoing hints, prefixed elements.
const changesV1 = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Definitions_1" targetNamespace="http://atlas/bpmn">
  <bpmn:message id="Msg_1" name="antwort"/>
  <bpmn:process id="p" name="Freigabe" isExecutable="true">
    <bpmn:startEvent id="start" name="Antrag"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="check" name="Prüfen">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="check" retries="3"/>
        <zeebe:ioMapping>
          <zeebe:input source="=amount" target="total"/>
          <zeebe:input source="=customer" target="who"/>
        </zeebe:ioMapping>
      </bpmn:extensionElements>
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:intermediateCatchEvent id="wait" name="Antwort">
      <bpmn:incoming>f2</bpmn:incoming><bpmn:outgoing>f3</bpmn:outgoing>
      <bpmn:messageEventDefinition id="MessageEventDefinition_0a1" messageRef="Msg_1"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:task id="old" name="Ablegen"/>
    <bpmn:endEvent id="end" name="Fertig"><bpmn:incoming>f3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <bpmn:sequenceFlow id="f2" sourceRef="check" targetRef="wait">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= amount &gt; 100</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="wait" targetRef="end"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="p">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="150" y="100" width="36" height="36"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`

// changesV2 is the next version written by hand: unprefixed BPMN, a different
// prefix for zeebe, no incoming/outgoing hints, a moved diagram, the elements in
// another order — and these changes to the model:
//
//   - check: retries 3 → 5, the "who" input removed, a header added, renamed
//   - wait: listens for another message, and its event definition lost its id
//   - old: removed; review: added (a user task) and wired in between wait and end
//   - f2: condition changed; f3: now ends at review
//   - end: unchanged apart from the hints, so not reported
const changesV2 = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:z="http://camunda.org/schema/zeebe/1.0" id="Definitions_9" targetNamespace="http://elsewhere" exporter="hand">
  <message id="Msg_1" name="antwort"/>
  <message id="Msg_2" name="rueckfrage"/>
  <process id="p" name="Freigabe" isExecutable="true">
    <sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <startEvent id="start" name="Antrag"/>
    <serviceTask id="check" name="Antrag prüfen">
      <extensionElements>
        <z:taskDefinition retries="5" type="check"/>
        <z:taskHeaders><z:header key="prio" value="hoch"/></z:taskHeaders>
        <z:ioMapping>
          <z:input source="=amount" target="total"/>
        </z:ioMapping>
      </extensionElements>
    </serviceTask>
    <intermediateCatchEvent id="wait" name="Antwort">
      <messageEventDefinition messageRef="Msg_2"/>
    </intermediateCatchEvent>
    <userTask id="review" name="Nachprüfen"/>
    <endEvent id="end" name="Fertig"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="wait">
      <conditionExpression xsi:type="tFormalExpression">= amount &gt; 500</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="review"/>
    <sequenceFlow id="f4" sourceRef="review" targetRef="end"/>
  </process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="p">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="10" y="10" width="36" height="36"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</definitions>`

func TestChangesListsEveryDifferenceByElement(t *testing.T) {
	got, err := Changes([]byte(changesV1), []byte(changesV2))
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Kind: ChangeAdded, ID: "Msg_2", Type: "message", Name: "rueckfrage"},
		{Kind: ChangeChanged, ID: "check", Type: "serviceTask", Name: "Antrag prüfen", Parent: "p", Details: []Detail{
			{Field: "name", Kind: ChangeChanged, Before: "Prüfen", After: "Antrag prüfen"},
			{Field: "extensionElements/zeebe:taskDefinition@retries", Kind: ChangeChanged, Before: "3", After: "5"},
			{Field: "extensionElements/zeebe:taskHeaders", Kind: ChangeAdded},
			{Field: "extensionElements/zeebe:taskHeaders/zeebe:header", Kind: ChangeAdded},
			{Field: "extensionElements/zeebe:taskHeaders/zeebe:header@key", Kind: ChangeAdded, After: "prio"},
			{Field: "extensionElements/zeebe:taskHeaders/zeebe:header@value", Kind: ChangeAdded, After: "hoch"},
			// Two inputs were told apart by their target; the one left is the
			// same field it was, so only the removed one is reported.
			{Field: "extensionElements/zeebe:ioMapping/zeebe:input[who]", Kind: ChangeRemoved},
			{Field: "extensionElements/zeebe:ioMapping/zeebe:input[who]@source", Kind: ChangeRemoved, Before: "=customer"},
			{Field: "extensionElements/zeebe:ioMapping/zeebe:input[who]@target", Kind: ChangeRemoved, Before: "who"},
		}},
		{Kind: ChangeChanged, ID: "wait", Type: "intermediateCatchEvent", Name: "Antwort", Parent: "p", Details: []Detail{
			{Field: "messageEventDefinition@messageRef", Kind: ChangeChanged, Before: "Msg_1", After: "Msg_2"},
		}},
		{Kind: ChangeAdded, ID: "review", Type: "userTask", Name: "Nachprüfen", Parent: "p"},
		{Kind: ChangeChanged, ID: "f2", Type: "sequenceFlow", Parent: "p", Details: []Detail{
			{Field: "conditionExpression", Kind: ChangeChanged, Before: "= amount > 100", After: "= amount > 500"},
		}},
		{Kind: ChangeChanged, ID: "f3", Type: "sequenceFlow", Parent: "p", Details: []Detail{
			{Field: "targetRef", Kind: ChangeChanged, Before: "end", After: "review"},
		}},
		{Kind: ChangeAdded, ID: "f4", Type: "sequenceFlow", Parent: "p"},
		{Kind: ChangeRemoved, ID: "old", Type: "task", Name: "Ablegen", Parent: "p"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes =\n%s\nwant\n%s", dumpChanges(got), dumpChanges(want))
	}
}

// The same model, reformatted, re-prefixed, re-stamped and with its picture moved, is
// no change at all — the property that makes an empty list mean something.
func TestChangesIgnoresWhatIsNotTheModel(t *testing.T) {
	got, err := Changes([]byte(storedModel), []byte(movedDiagram))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Changes of the same model = %s, want none", dumpChanges(got))
	}
	got, err = Changes([]byte(changesV2), []byte(changesV2))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Changes of a document with itself = %s, want none", dumpChanges(got))
	}
}

// An attribute written out at its schema default says what leaving it out says.
func TestChangesReadsDefaultsAsAbsent(t *testing.T) {
	a := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <task id="t"/><boundaryEvent id="b" attachedToRef="t" cancelActivity="true"><timerEventDefinition/></boundaryEvent>
	</process></definitions>`
	b := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <task id="t"/><boundaryEvent id="b" attachedToRef="t"><timerEventDefinition/></boundaryEvent>
	</process></definitions>`
	got, err := Changes([]byte(a), []byte(b))
	if err != nil || len(got) != 0 {
		t.Fatalf("Changes = %s, %v; want none", dumpChanges(got), err)
	}
	// And the non-default is a change, said as one.
	c := strings.Replace(b, `attachedToRef="t"`, `attachedToRef="t" cancelActivity="false"`, 1)
	got, err = Changes([]byte(a), []byte(c))
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{Kind: ChangeChanged, ID: "b", Type: "boundaryEvent", Parent: "p", Details: []Detail{
		{Field: "cancelActivity", Kind: ChangeAdded, After: "false"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes =\n%s\nwant\n%s", dumpChanges(got), dumpChanges(want))
	}
}

// An element of a sub-process is its own element, named with its container, and one
// that moved into a sub-process says so; a task replaced by another kind keeps its id
// in bpmn-js and is reported as the same element with a new type.
func TestChangesFollowsContainersAndKinds(t *testing.T) {
	a := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <task id="t" name="A"/>
	  <subProcess id="sp"><task id="inner" name="B"/></subProcess>
	</process></definitions>`
	b := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <subProcess id="sp"><userTask id="inner" name="B"/><task id="t" name="A"/></subProcess>
	</process></definitions>`
	got, err := Changes([]byte(a), []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Kind: ChangeChanged, ID: "inner", Type: "userTask", Name: "B", Parent: "sp", Details: []Detail{
			{Field: "type", Kind: ChangeChanged, Before: "task", After: "userTask"},
		}},
		{Kind: ChangeChanged, ID: "t", Type: "task", Name: "A", Parent: "sp", Details: []Detail{
			{Field: "parent", Kind: ChangeChanged, Before: "p", After: "sp"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes =\n%s\nwant\n%s", dumpChanges(got), dumpChanges(want))
	}
}

// Parts with nothing to tell them apart are numbered; a lane's references are told
// apart by the element they name, so adding one does not renumber the others.
func TestChangesNamesRepeatedParts(t *testing.T) {
	a := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <laneSet id="ls"><lane id="l1"><flowNodeRef>t1</flowNodeRef><flowNodeRef>t3</flowNodeRef></lane></laneSet>
	  <task id="t1"><documentation>eins</documentation><documentation>zwei</documentation></task>
	</process></definitions>`
	b := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
	  <laneSet id="ls"><lane id="l1"><flowNodeRef>t1</flowNodeRef><flowNodeRef>t2</flowNodeRef><flowNodeRef>t3</flowNodeRef></lane></laneSet>
	  <task id="t1"><documentation>eins</documentation><documentation>drei</documentation></task>
	</process></definitions>`
	got, err := Changes([]byte(a), []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Kind: ChangeChanged, ID: "l1", Type: "lane", Parent: "ls", Details: []Detail{
			{Field: "flowNodeRef[t2]", Kind: ChangeAdded, After: "t2"},
		}},
		{Kind: ChangeChanged, ID: "t1", Type: "task", Parent: "p", Details: []Detail{
			{Field: "documentation[drei]", Kind: ChangeAdded, After: "drei"},
			{Field: "documentation[zwei]", Kind: ChangeRemoved, Before: "zwei"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes =\n%s\nwant\n%s", dumpChanges(got), dumpChanges(want))
	}
	// Two identical parts cannot be told apart by anything they say: by position.
	c := strings.Replace(b, `<documentation>drei</documentation>`, `<documentation>eins</documentation>`, 1)
	got, err = Changes([]byte(a), []byte(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "t1" || got[1].Details[0].Field != "documentation[2]" {
		t.Fatalf("Changes = %s, want t1's documentation[2] changed", dumpChanges(got))
	}
}

// What the root <definitions> holds beside its elements is reported as a change of
// the document, with no id; its own attributes are the document's metadata and are
// not compared.
func TestChangesReportsTheDocumentsOwnParts(t *testing.T) {
	a := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="A"><process id="p"/></definitions>`
	b := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="B" exporter="x">
	  <import importType="http://www.w3.org/2001/XMLSchema" location="a.xsd" namespace="urn:a"/><process id="p"/></definitions>`
	got, err := Changes([]byte(a), []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "" || got[0].Type != "definitions" || got[0].Kind != ChangeChanged || got[0].Details[0].Field != "import" {
		t.Fatalf("Changes = %s, want the document changed by an import", dumpChanges(got))
	}
}

func TestChangesRefusesMalformedXML(t *testing.T) {
	if _, err := Changes([]byte(changesV1), []byte("<definitions><process>")); err == nil {
		t.Fatal("Changes of a truncated document: no error")
	}
}

func dumpChanges(cs []Change) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("  " + c.Kind + " " + c.Type + " " + c.ID + " name=" + c.Name + " parent=" + c.Parent + "\n")
		for _, d := range c.Details {
			b.WriteString("      " + d.Kind + " " + d.Field + ": " + d.Before + " -> " + d.After + "\n")
		}
	}
	return b.String()
}
