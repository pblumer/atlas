package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
)

// The rule follows the data, not the name (ADR-0435 §6): of the elements below, only
// those that receive a catalogued signal carrying personal data are listeners the
// rule governs, whichever way they receive it.
func TestTheListenerRuleFollowsThePersonalDataNotTheName(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="S_personal" name="atlas.test.personal"/>
  <signal id="S_plain" name="atlas.test.plain"/>
  <signal id="S_message" name="atlas.test.message"/>
  <signal id="S_unknown" name="atlas.test.unknown"/>
  <process id="p" isExecutable="true">
    <startEvent id="start"><signalEventDefinition signalRef="S_personal"/></startEvent>
    <intermediateCatchEvent id="catch_plain"><signalEventDefinition signalRef="S_plain"/></intermediateCatchEvent>
    <intermediateCatchEvent id="catch_message"><signalEventDefinition signalRef="S_message"/></intermediateCatchEvent>
    <intermediateCatchEvent id="catch_unknown"><signalEventDefinition signalRef="S_unknown"/></intermediateCatchEvent>
    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
    <boundaryEvent id="bound" attachedToRef="work" cancelActivity="false"><signalEventDefinition signalRef="S_personal"/></boundaryEvent>
    <intermediateThrowEvent id="throw"><signalEventDefinition signalRef="S_personal"/></intermediateThrowEvent>
    <endEvent id="end"/>
    <endEvent id="end_b"/>
    <subProcess id="esp" triggeredByEvent="true">
      <startEvent id="esp_start" isInterrupting="false"><signalEventDefinition signalRef="S_personal"/></startEvent>
      <endEvent id="esp_end"/>
      <sequenceFlow id="e1" sourceRef="esp_start" targetRef="esp_end"/>
    </subProcess>
    <sequenceFlow id="f1" sourceRef="start" targetRef="catch_plain"/>
    <sequenceFlow id="f2" sourceRef="catch_plain" targetRef="catch_message"/>
    <sequenceFlow id="f3" sourceRef="catch_message" targetRef="catch_unknown"/>
    <sequenceFlow id="f4" sourceRef="catch_unknown" targetRef="work"/>
    <sequenceFlow id="f5" sourceRef="work" targetRef="throw"/>
    <sequenceFlow id="f6" sourceRef="throw" targetRef="end"/>
    <sequenceFlow id="f7" sourceRef="bound" targetRef="end_b"/>
  </process>
</definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	field := func(name string, d eventcatalog.Data) eventcatalog.Field {
		return eventcatalog.Field{Name: name, Type: "string", Always: true, Data: d}
	}
	catalogue := map[string]eventcatalog.Entry{
		"atlas.test.personal": {Type: "atlas.test.personal", Channels: []eventcatalog.Channel{eventcatalog.Signal},
			Payload: []eventcatalog.Field{field("ref", eventcatalog.NotPersonal), field("email", eventcatalog.PersonalData)}},
		"atlas.test.plain": {Type: "atlas.test.plain", Channels: []eventcatalog.Channel{eventcatalog.Signal},
			Payload: []eventcatalog.Field{field("ref", eventcatalog.NotPersonal)}},
		// Personal data, but not a signal: a signal of that name is not this event.
		"atlas.test.message": {Type: "atlas.test.message", Channels: []eventcatalog.Channel{eventcatalog.Message},
			Payload: []eventcatalog.Field{field("email", eventcatalog.PersonalData)}},
	}
	lookup := func(name string) (eventcatalog.Entry, bool) { e, ok := catalogue[name]; return e, ok }

	got := personalListenersOf([]*compiler.CompiledProcess{cp}, lookup)
	byElement := map[string]personalListener{}
	for _, l := range got {
		byElement[l.Element] = l
	}
	email := []string{"email"}
	want := map[string]personalListener{
		"start":     {Element: "start", Event: "atlas.test.personal", Role: compiler.SignalStarts, Personal: email},
		"bound":     {Element: "bound", Event: "atlas.test.personal", Role: compiler.SignalBoundary, Personal: email},
		"esp_start": {Element: "esp_start", Event: "atlas.test.personal", Role: compiler.SignalEventSubProcess, Personal: email},
	}
	if !reflect.DeepEqual(byElement, want) {
		t.Errorf("listeners the rule governs:\n got %+v\nwant %+v", byElement, want)
	}

	sentence := want["start"].sentence([]string{"modeler", "user"})
	if sentence != "start listens to atlas.test.personal, which carries personal data (email). "+
		"Deploying a listener on it needs the admin role; you have modeler, user." {
		t.Errorf("sentence = %q", sentence)
	}
	if got := want["start"].sentence(nil); !strings.HasSuffix(got, "you have none.") {
		t.Errorf("a caller without roles: %q", got)
	}
}
