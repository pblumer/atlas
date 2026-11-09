package pubinterface

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// kreditBPMN is a publisher's process with every kind of entry point ADR-0373 names,
// an error that is caught inside and one that is not, an escalation, a reply
// message and four root-scope ends.
const kreditBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_antrag" name="kredit.antrag"/>
  <bpmn:message id="Msg_unterlagen" name="kredit.unterlagen">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:message id="Msg_rueckfrage" name="kredit.rueckfrage">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:message id="Msg_freigabe" name="kredit.freigabe">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:message id="Msg_storno" name="kredit.storno">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:message id="Msg_abbruch" name="kredit.abbruch">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:message id="Msg_bescheid" name="kredit.bescheid"/>
  <bpmn:error id="Err_rej" errorCode="REJECTED"/>
  <bpmn:error id="Err_int" errorCode="INTERNAL"/>
  <bpmn:escalation id="Esc_late" escalationCode="LATE"/>
  <bpmn:process id="kredit" isExecutable="true">
    <bpmn:dataObject id="DO_unterlagen" name="unterlagen" itemSubjectRef="Unterlagen"/>
    <bpmn:dataObjectReference id="Ref_unterlagen" dataObjectRef="DO_unterlagen"/>
    <bpmn:startEvent id="start"><bpmn:messageEventDefinition messageRef="Msg_antrag"/></bpmn:startEvent>
    <bpmn:serviceTask id="pruefen">
      <bpmn:extensionElements><zeebe:taskDefinition type="pruefen"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="storno" attachedToRef="pruefen">
      <bpmn:messageEventDefinition messageRef="Msg_storno"/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="endStorno"/>
    <bpmn:receiveTask id="unterlagen" messageRef="Msg_unterlagen">
      <bpmn:dataOutputAssociation><bpmn:targetRef>Ref_unterlagen</bpmn:targetRef></bpmn:dataOutputAssociation>
    </bpmn:receiveTask>
    <bpmn:receiveTask id="rueckfrage" messageRef="Msg_rueckfrage">
      <bpmn:extensionElements><zeebe:ioMapping><zeebe:output source="= antwort.text" target="rueckfrageText"/><zeebe:output source="= anhang" target="anhang"/></zeebe:ioMapping></bpmn:extensionElements>
    </bpmn:receiveTask>
    <bpmn:intermediateCatchEvent id="freigabe">
      <bpmn:messageEventDefinition messageRef="Msg_freigabe"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:intermediateThrowEvent id="spaet"><bpmn:escalationEventDefinition escalationRef="Esc_late"/></bpmn:intermediateThrowEvent>
    <bpmn:subProcess id="intern">
      <bpmn:startEvent id="iStart"/>
      <bpmn:endEvent id="iErr"><bpmn:errorEventDefinition errorRef="Err_int"/></bpmn:endEvent>
      <bpmn:sequenceFlow id="if1" sourceRef="iStart" targetRef="iErr"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="internCatch" attachedToRef="intern"><bpmn:errorEventDefinition errorRef="Err_int"/></bpmn:boundaryEvent>
    <bpmn:exclusiveGateway id="gw" default="fNok"/>
    <bpmn:endEvent id="bescheid"><bpmn:messageEventDefinition messageRef="Msg_bescheid"/></bpmn:endEvent>
    <bpmn:endEvent id="abgelehnt"><bpmn:errorEventDefinition errorRef="Err_rej"/></bpmn:endEvent>
    <bpmn:endEvent id="nachFehler"/>
    <bpmn:subProcess id="abbruchHandler" triggeredByEvent="true">
      <bpmn:startEvent id="aStart" isInterrupting="true"><bpmn:messageEventDefinition messageRef="Msg_abbruch"/></bpmn:startEvent>
      <bpmn:endEvent id="aEnd"><bpmn:terminateEventDefinition/></bpmn:endEvent>
      <bpmn:sequenceFlow id="af1" sourceRef="aStart" targetRef="aEnd"/>
    </bpmn:subProcess>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pruefen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="pruefen" targetRef="unterlagen"/>
    <bpmn:sequenceFlow id="f3" sourceRef="storno" targetRef="endStorno"/>
    <bpmn:sequenceFlow id="f4" sourceRef="unterlagen" targetRef="rueckfrage"/>
    <bpmn:sequenceFlow id="f5" sourceRef="rueckfrage" targetRef="freigabe"/>
    <bpmn:sequenceFlow id="f5b" sourceRef="freigabe" targetRef="spaet"/>
    <bpmn:sequenceFlow id="f6" sourceRef="spaet" targetRef="intern"/>
    <bpmn:sequenceFlow id="f7" sourceRef="intern" targetRef="gw"/>
    <bpmn:sequenceFlow id="f8" sourceRef="internCatch" targetRef="nachFehler"/>
    <bpmn:sequenceFlow id="fOk" sourceRef="gw" targetRef="bescheid"><bpmn:conditionExpression>= ok</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fNok" sourceRef="gw" targetRef="abgelehnt"/>
  </bpmn:process>
</bpmn:definitions>`

func compile(t *testing.T, src string) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(7, 1, strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

func TestDeriveReadsEveryEntryPointKind(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), map[string]string{"unterlagen": "Unterlagen nachreichen"})
	got := map[string]CandidateEntryPoint{}
	for _, e := range c.EntryPoints {
		got[e.ElementID] = e
	}
	want := map[string]CandidateEntryPoint{
		"start": {ElementID: "start", Kind: KindMessageStart, Starts: true, MessageName: "kredit.antrag",
			Payload: Payload{Basis: BasisUntyped}},
		"storno": {ElementID: "storno", Kind: KindBoundary, MessageName: "kredit.storno", CorrelationKey: "antragId",
			Payload: Payload{Basis: BasisUntyped}},
		"unterlagen": {ElementID: "unterlagen", ElementName: "Unterlagen nachreichen", Kind: KindReceiveTask,
			MessageName: "kredit.unterlagen", CorrelationKey: "antragId",
			Payload: Payload{Basis: BasisInformationModel, Class: "Unterlagen"}},
		"rueckfrage": {ElementID: "rueckfrage", Kind: KindReceiveTask, MessageName: "kredit.rueckfrage",
			CorrelationKey: "antragId", Payload: Payload{Basis: BasisFields, Fields: []string{"anhang", "antwort"}}},
		"freigabe": {ElementID: "freigabe", Kind: KindIntermediateCatch, MessageName: "kredit.freigabe",
			CorrelationKey: "antragId", Payload: Payload{Basis: BasisUntyped}},
		"abbruchHandler": {ElementID: "abbruchHandler", Kind: KindEventSubprocess, MessageName: "kredit.abbruch",
			CorrelationKey: "antragId", Payload: Payload{Basis: BasisUntyped}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entry points:\n got %+v\nwant %+v", got, want)
	}
}

func TestDeriveReadsReturnsAndRootOutcomesOnly(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), nil)
	// INTERNAL is caught by the subprocess's boundary, so it never leaves an instance.
	want := CandidateReturns{Errors: []string{"REJECTED"}, Escalations: []string{"LATE"}, ThrownMessages: []string{"kredit.bescheid"}}
	if !reflect.DeepEqual(c.Returns, want) {
		t.Fatalf("returns = %+v, want %+v", c.Returns, want)
	}
	kinds := map[string]string{}
	for _, o := range c.Outcomes {
		kinds[o.ElementID] = o.Kind
	}
	// The subprocess's error end and the event subprocess's terminate end are not
	// root-scope ends: an instance does not end there.
	wantKinds := map[string]string{"endStorno": OutcomeEnd, "bescheid": OutcomeMessage, "abgelehnt": OutcomeError, "nachFehler": OutcomeEnd}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("outcomes = %v, want %v", kinds, wantKinds)
	}
}

func kreditSelection() Selection {
	return Selection{
		EntryPoints: []Pick{{Name: "antrag", ElementID: "start"}, {Name: "unterlagen", ElementID: "unterlagen"}},
		Outcomes: []Pick{
			{Name: "bewilligt", ElementID: "bescheid"},
			{Name: "abgelehnt", ElementID: "abgelehnt"},
			{Name: "abgebrochen", ElementIDs: []string{"endStorno", "nachFehler"}},
		},
		Replies: []string{"kredit.bescheid"},
	}
}

func TestRealizePublishesNamesNotElements(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), nil)
	contract, binding, problems := Realize(c, "kredit", 7, 1, kreditSelection())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	wantContract := Contract{
		EntryPoints: []EntryPoint{
			{Name: "antrag", Kind: KindMessageStart, Starts: true, Payload: Payload{Basis: BasisUntyped}},
			{Name: "unterlagen", Kind: KindReceiveTask, CorrelationKey: "antragId", Payload: Payload{Basis: BasisInformationModel, Class: "Unterlagen"}},
		},
		Returns:  Returns{Errors: []string{"REJECTED"}, Escalations: []string{"LATE"}, Messages: []string{"kredit.bescheid"}},
		Outcomes: []Outcome{{Name: "abgebrochen", Kind: OutcomeEnd}, {Name: "abgelehnt", Kind: OutcomeError}, {Name: "bewilligt", Kind: OutcomeMessage}},
	}
	if !reflect.DeepEqual(contract, wantContract) {
		t.Fatalf("contract:\n got %+v\nwant %+v", contract, wantContract)
	}
	if binding.ProcessID != "kredit" || binding.DefinitionKey != 7 || len(binding.EntryPoints) != 2 ||
		binding.EntryPoints[1].MessageName != "kredit.unterlagen" || binding.EntryPoints[1].ElementIDs[0] != "unterlagen" ||
		!reflect.DeepEqual(binding.Outcomes[0].ElementIDs, []string{"endStorno", "nachFehler"}) {
		t.Fatalf("binding = %+v", binding)
	}
}

func TestRealizeRefusesAnIncompleteOrInventedSelection(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), nil)
	cases := map[string]struct {
		sel  Selection
		want string
	}{
		"no entry point":   {Selection{Outcomes: kreditSelection().Outcomes}, "at least one entry point"},
		"not a receiver":   {Selection{EntryPoints: []Pick{{Name: "x", ElementID: "pruefen"}}, Outcomes: kreditSelection().Outcomes}, "receives no message"},
		"bad name":         {Selection{EntryPoints: []Pick{{Name: "1x", ElementID: "start"}}, Outcomes: kreditSelection().Outcomes}, "not usable"},
		"twice":            {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "start"}, {Name: "b", ElementID: "start"}}, Outcomes: kreditSelection().Outcomes}, "under two entry points"},
		"unnamed outcome":  {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: kreditSelection().Outcomes[:2]}, "unnamed: endStorno, nachFehler"},
		"mixed kinds":      {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: []Pick{{Name: "o", ElementIDs: []string{"bescheid", "abgelehnt", "endStorno", "nachFehler"}}}}, "different kinds"},
		"invented reply":   {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: kreditSelection().Outcomes, Replies: []string{"nope"}}, "no message this definition throws"},
		"inner end":        {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: append(kreditSelection().Outcomes, Pick{Name: "inner", ElementID: "iErr"})}, "no end event of the process's root scope"},
		"nested start sub": {Selection{EntryPoints: []Pick{{Name: "a", ElementID: "aStart"}}, Outcomes: kreditSelection().Outcomes}, "receives no message"},
	}
	for name, tc := range cases {
		_, _, problems := Realize(c, "kredit", 7, 1, tc.sel)
		if !strings.Contains(strings.Join(problems, "; "), tc.want) {
			t.Errorf("%s: problems = %v, want one containing %q", name, problems, tc.want)
		}
	}
}

func TestContractEqualityIgnoresOrderAndNamesDifferences(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), nil)
	a, _, _ := Realize(c, "kredit", 7, 1, kreditSelection())
	sel := kreditSelection()
	sel.EntryPoints[0], sel.EntryPoints[1] = sel.EntryPoints[1], sel.EntryPoints[0]
	b, _, _ := Realize(c, "kredit", 7, 1, sel)
	if !a.Equal(b) {
		t.Fatal("the same selection in another order is another contract")
	}
	sel.EntryPoints[0].Name = "dokumente"
	d, _, _ := Realize(c, "kredit", 7, 1, sel)
	if a.Equal(d) {
		t.Fatal("a renamed entry point is the same contract")
	}
	diff := strings.Join(a.Differences(d), "; ")
	if !strings.Contains(diff, "entry point dokumente is new") || !strings.Contains(diff, "entry point unterlagen is gone") {
		t.Fatalf("differences = %q", diff)
	}
}
