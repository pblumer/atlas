package formgen

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// A BPMN model with the three things a form generator has to read out of one: what the
// process is for, what each step is called and documented as, and the names the model
// already uses for its data. It is deliberately not deployable — a draft under the
// author's hands rarely is, and reading one must not require a compile.
const sampleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="urlaubsantrag" name="Urlaubsantrag" isExecutable="true">
    <bpmn:documentation>Ein Mitarbeiter beantragt Urlaub; die Führungskraft entscheidet.</bpmn:documentation>
    <bpmn:startEvent id="StartEvent_1" name="Antrag gestellt">
      <bpmn:documentation>Startet, wenn ein Mitarbeiter den Antrag absendet.</bpmn:documentation>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_Pruefen" name="Antrag prüfen">
      <bpmn:documentation>Die Führungskraft entscheidet über den Antrag.</bpmn:documentation>
      <bpmn:extensionElements>
        <zeebe:formDefinition formId="urlaub-pruefen" />
        <zeebe:ioMapping>
          <zeebe:input source="=antragsteller" target="mitarbeiter" />
          <zeebe:output source="=entscheidung" target="entscheidung" />
        </zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:sequenceFlow id="Flow_ja" name="genehmigt" sourceRef="Task_Pruefen" targetRef="EndEvent_1">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression"
        xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">=entscheidung = "genehmigt"</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:endEvent id="EndEvent_1" name="Entschieden" />
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D_1">
    <bpmndi:BPMNPlane id="P_1" bpmnElement="urlaubsantrag">
      <bpmndi:BPMNShape id="Shape_1" bpmnElement="StartEvent_1" />
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// The outline is what the process says about itself, and every part of it is the
// modeller's own words — which is the whole argument for reading the diagram rather than
// asking the author to retype it into a prompt.
func TestReadProcessTakesTheModellersOwnWords(t *testing.T) {
	p := ReadProcess([]byte(sampleBPMN))

	if p.ID != "urlaubsantrag" || p.Name != "Urlaubsantrag" {
		t.Fatalf("identity = %q/%q", p.ID, p.Name)
	}
	if !strings.Contains(p.Documentation, "Führungskraft entscheidet") {
		t.Errorf("process documentation = %q", p.Documentation)
	}
	task, ok := p.Element("Task_Pruefen")
	if !ok {
		t.Fatalf("Task_Pruefen not among %d elements", len(p.Elements))
	}
	if task.Kind != "userTask" || task.Name != "Antrag prüfen" {
		t.Errorf("task = %+v", task)
	}
	if !strings.Contains(task.Documentation, "entscheidet über den Antrag") {
		t.Errorf("task documentation = %q", task.Documentation)
	}
	if task.FormID != "urlaub-pruefen" {
		t.Errorf("formId = %q, want the form the task already binds", task.FormID)
	}
	flow, ok := p.Element("Flow_ja")
	if !ok || !strings.Contains(flow.Condition, `entscheidung = "genehmigt"`) {
		t.Errorf("flow = %+v, want its condition — it is where a model says what it decides on", flow)
	}
}

// The variable names are the point of reading the model at all: a form whose keys match
// what the process already calls its data needs no mapping afterwards, and a generator
// that invents `vacationDays` for a process that says `urlaubstage` has made work.
func TestReadProcessCollectsTheNamesTheModelAlreadyUses(t *testing.T) {
	p := ReadProcess([]byte(sampleBPMN))
	got := strings.Join(p.Variables, ",")
	for _, want := range []string{"mitarbeiter", "entscheidung"} {
		if !strings.Contains(got, want) {
			t.Errorf("variables = %v, want %q among them", p.Variables, want)
		}
	}
}

// Diagram interchange is not the process. Its shapes carry ids and would otherwise
// double every element in the outline — and an outline that lists StartEvent_1 twice is
// a prompt saying the process has two of them.
func TestReadProcessLeavesTheDiagramOut(t *testing.T) {
	p := ReadProcess([]byte(sampleBPMN))
	for _, e := range p.Elements {
		if strings.HasPrefix(e.Kind, "BPMN") || e.ID == "Shape_1" || e.ID == "P_1" {
			t.Errorf("outline carries diagram element %+v", e)
		}
	}
	if n := len(p.Elements); n != 4 {
		t.Errorf("elements = %d (%+v), want the four flow elements", n, p.Elements)
	}
}

// Malformed XML is the normal state of a draft mid-edit. Reading one must degrade to
// "nothing to say about this process" rather than fail the generation the author asked
// for — the prose they typed is still a perfectly good brief on its own.
func TestReadProcessSurvivesRubbish(t *testing.T) {
	p := ReadProcess([]byte("<bpmn:definitions><bpmn:process id=\"x\" name=\"X\"><bpmn:userTask id=\"t\""))
	if p.ID != "x" {
		t.Errorf("id = %q, want what was readable before the truncation", p.ID)
	}
	if p := ReadProcess(nil); p.ID != "" || len(p.Elements) != 0 {
		t.Errorf("empty XML = %+v, want an empty outline", p)
	}
}

// The outline goes into a prompt, so it has to render as something a model reads rather
// than as a Go struct — and the step the form is for has to stand out from the other
// twenty.
func TestDescribePutsTheStepTheFormIsForFirst(t *testing.T) {
	p := ReadProcess([]byte(sampleBPMN))
	text := p.Describe("Task_Pruefen")

	if !strings.Contains(text, "urlaubsantrag") || !strings.Contains(text, "Führungskraft entscheidet") {
		t.Errorf("description does not carry the process:\n%s", text)
	}
	head, rest, _ := strings.Cut(text, "Every step in the process")
	if !strings.Contains(head, "Antrag prüfen") {
		t.Errorf("the step the form is for is not called out before the rest:\n%s", text)
	}
	if !strings.Contains(rest, "EndEvent_1") {
		t.Errorf("the remaining steps are missing:\n%s", text)
	}
	if !strings.Contains(text, "mitarbeiter") {
		t.Errorf("the variable names are missing:\n%s", text)
	}
}

// Naming no element is the start-form case: the form starts the process, so the process
// as a whole is what it is for.
func TestDescribeWithoutAnElementIsTheStartFormCase(t *testing.T) {
	text := ReadProcess([]byte(sampleBPMN)).Describe("")
	if strings.Contains(text, "The form is for this step") {
		t.Errorf("named no step but claimed one:\n%s", text)
	}
	if !strings.Contains(text, "Urlaubsantrag") {
		t.Errorf("description lost the process:\n%s", text)
	}
}

// A model is not a prompt budget. An outline of a 400-element process would crowd out
// the author's own brief, so it is bounded — and says that it is, rather than ending
// mid-sentence as if the process stopped there.
func TestDescribeIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="big" name="Big">`)
	for i := 0; i < maxOutlineElements+40; i++ {
		b.WriteString(`<bpmn:task id="t`)
		b.WriteString(strings.Repeat("0", 1))
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`" name="Schritt"/>`)
	}
	b.WriteString(`</bpmn:process></bpmn:definitions>`)

	p := ReadProcess([]byte(b.String()))
	if len(p.Elements) != maxOutlineElements {
		t.Fatalf("elements = %d, want the cap %d", len(p.Elements), maxOutlineElements)
	}
	if text := p.Describe(""); !strings.Contains(text, "further steps not listed") {
		t.Errorf("a truncated outline does not say so:\n%s", text)
	}
}

// Extension elements carry ids too — an Atlas worker configuration, a Zeebe form
// binding, a temis decision reference — and none of them is a step. Membership of the
// BPMN namespace is what tells them apart, which is why it is a whitelist: the set of
// things that are not the process grows with every extension.
func TestReadProcessTakesOnlyTheProcessesOwnElements(t *testing.T) {
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
		xmlns:atlas="http://atlas.example/schema">
	  <bpmn:message id="Msg_1" name="Buchung" />
	  <bpmn:process id="p" name="P">
	    <bpmn:serviceTask id="Task_1" name="Buchen">
	      <bpmn:extensionElements><atlas:restConnector id="Cfg_1" connector="crm" resultVariable="buchung" /></bpmn:extensionElements>
	    </bpmn:serviceTask>
	    <bpmn:intermediateCatchEvent id="Catch_1" name="Warten">
	      <bpmn:timerEventDefinition id="Timer_1"><bpmn:timeDuration>PT1H</bpmn:timeDuration></bpmn:timerEventDefinition>
	    </bpmn:intermediateCatchEvent>
	  </bpmn:process>
	</bpmn:definitions>`))

	var ids []string
	for _, e := range p.Elements {
		ids = append(ids, e.ID)
	}
	if len(ids) != 2 || ids[0] != "Task_1" || ids[1] != "Catch_1" {
		t.Errorf("elements = %v, want only the two steps", ids)
	}
	// The extension is not a step, but the variable it writes is still a name the
	// process uses — which is the whole reason to look inside one.
	if len(p.Variables) != 1 || p.Variables[0] != "buchung" {
		t.Errorf("variables = %v, want the result variable the extension names", p.Variables)
	}
}

// Documentation is written for people and occasionally runs to pages. The first of it
// carries what a form needs; the rest would crowd out the author's own brief.
func TestLongProseIsCutAndSaysSo(t *testing.T) {
	long := strings.Repeat("Sehr ausführliche Beschreibung. ", 200)
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <bpmn:process id="p"><bpmn:documentation>` + long + `</bpmn:documentation></bpmn:process></bpmn:definitions>`))

	if n := len([]rune(p.Documentation)); n > maxDocRunes+1 {
		t.Errorf("documentation is %d runes, want it bounded", n)
	}
	if !strings.HasSuffix(p.Documentation, "…") {
		t.Errorf("prose was cut without saying so: %q", p.Documentation)
	}
}

// Documentation that carries markup is not prose this can read, and a decoder error must
// leave the field empty rather than land a Go error message in a prompt.
func TestDocumentationWithMarkupIsLeftOutRatherThanMangled(t *testing.T) {
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <bpmn:process id="p"><bpmn:documentation>Vor <b>fett</b> nach</bpmn:documentation>
	  <bpmn:task id="t" name="T"/></bpmn:process></bpmn:definitions>`))

	if strings.Contains(p.Documentation, "XML") || strings.Contains(p.Documentation, "error") {
		t.Errorf("documentation = %q, want it empty rather than an error string", p.Documentation)
	}
	if _, ok := p.Element("t"); !ok {
		t.Error("the walk did not carry on past the documentation it could not read")
	}
}

// The name list is a vocabulary, not a dump: past a point it stops helping a model
// choose keys and starts using the room the brief needs.
func TestTheVariableListIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
		xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><bpmn:process id="p"><bpmn:task id="t">
		<bpmn:extensionElements><zeebe:ioMapping>`)
	for i := 0; i < maxOutlineVariables+30; i++ {
		fmt.Fprintf(&b, `<zeebe:input source="=a" target="v%d" />`, i)
	}
	b.WriteString(`</zeebe:ioMapping></bpmn:extensionElements></bpmn:task></bpmn:process></bpmn:definitions>`)

	if p := ReadProcess([]byte(b.String())); len(p.Variables) != maxOutlineVariables {
		t.Errorf("variables = %d, want the cap %d", len(p.Variables), maxOutlineVariables)
	}
}

// A step keeps the first thing said about it. BPMN allows several documentation elements
// and a model that concatenated them would produce prose no one wrote.
func TestTheFirstDocumentationWins(t *testing.T) {
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <bpmn:process id="p"><bpmn:task id="t"><bpmn:documentation>Erste</bpmn:documentation>
	  <bpmn:documentation>Zweite</bpmn:documentation></bpmn:task></bpmn:process></bpmn:definitions>`))

	e, _ := p.Element("t")
	if e.Documentation != "Erste" {
		t.Errorf("documentation = %q, want the first", e.Documentation)
	}
}

// An empty <documentation> is silence, not prose: writing "" into the outline would put
// a heading in the prompt with nothing under it, and hide a real one further down the
// element that BPMN allows to follow it.
func TestEmptyDocumentationIsNotProse(t *testing.T) {
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <bpmn:process id="p"><bpmn:documentation>   </bpmn:documentation>
	  <bpmn:task id="t"><bpmn:documentation></bpmn:documentation>
	  <bpmn:documentation>Doch etwas</bpmn:documentation></bpmn:task></bpmn:process></bpmn:definitions>`))

	if p.Documentation != "" {
		t.Errorf("process documentation = %q, want none", p.Documentation)
	}
	e, _ := p.Element("t")
	if e.Documentation != "Doch etwas" {
		t.Errorf("documentation = %q, want the first one that says something", e.Documentation)
	}
}

// A condition expression carrying markup is not one this can read, and the flow keeps
// its identity rather than picking up an error string.
func TestAnUnreadableConditionLeavesTheFlowAlone(t *testing.T) {
	p := ReadProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <bpmn:process id="p"><bpmn:sequenceFlow id="f" name="ja">
	  <bpmn:conditionExpression>=a <b>und</b> b</bpmn:conditionExpression></bpmn:sequenceFlow>
	  </bpmn:process></bpmn:definitions>`))

	e, ok := p.Element("f")
	if !ok || e.Name != "ja" {
		t.Fatalf("flow = %+v", e)
	}
	if strings.Contains(e.Condition, "XML") {
		t.Errorf("condition = %q, want it empty rather than an error string", e.Condition)
	}
}
