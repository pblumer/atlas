package compiler_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// One process with every way an element throws or receives a signal, and a message
// start and catch: what the event catalogue (ADR-0435) reads to find who listens.
const signalPointsXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="Sig_req" name="atlas.user.requested"/>
  <signal id="Sig_stop" name="stop"/>
  <signal id="Sig_done" name="done"/>
  <message id="Msg_go" name="go"/>
  <message id="Msg_more" name="more"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="start_sig"><signalEventDefinition signalRef="Sig_req"/></startEvent>
    <startEvent id="start_msg"><messageEventDefinition messageRef="Msg_go"/></startEvent>
    <exclusiveGateway id="join"/>
    <intermediateCatchEvent id="catch_sig"><signalEventDefinition signalRef="Sig_stop"/></intermediateCatchEvent>
    <intermediateCatchEvent id="catch_msg"><messageEventDefinition messageRef="Msg_more"/></intermediateCatchEvent>
    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
    <boundaryEvent id="bound_sig" attachedToRef="work" cancelActivity="false"><signalEventDefinition signalRef="Sig_stop"/></boundaryEvent>
    <intermediateThrowEvent id="throw_sig"><signalEventDefinition signalRef="Sig_done"/></intermediateThrowEvent>
    <endEvent id="end_sig"><signalEventDefinition signalRef="Sig_done"/></endEvent>
    <endEvent id="end_b"/>
    <subProcess id="esp" triggeredByEvent="true">
      <startEvent id="esp_start" isInterrupting="false"><signalEventDefinition signalRef="Sig_req"/></startEvent>
      <endEvent id="esp_end"/>
      <sequenceFlow id="e1" sourceRef="esp_start" targetRef="esp_end"/>
    </subProcess>
    <sequenceFlow id="f0" sourceRef="start_sig" targetRef="join"/>
    <sequenceFlow id="f0b" sourceRef="start_msg" targetRef="join"/>
    <sequenceFlow id="f1" sourceRef="join" targetRef="catch_sig"/>
    <sequenceFlow id="f2" sourceRef="catch_sig" targetRef="catch_msg"/>
    <sequenceFlow id="f3" sourceRef="catch_msg" targetRef="work"/>
    <sequenceFlow id="f4" sourceRef="work" targetRef="throw_sig"/>
    <sequenceFlow id="f5" sourceRef="throw_sig" targetRef="end_sig"/>
    <sequenceFlow id="f6" sourceRef="bound_sig" targetRef="end_b"/>
  </process>
</definitions>`

func TestSignalPointsNamesEveryThrowAndReceiver(t *testing.T) {
	cp := parseShape(t, signalPointsXML)
	got := map[string]compiler.SignalPoint{}
	for _, sp := range cp.SignalPoints() {
		got[sp.Element] = sp
	}
	want := map[string]compiler.SignalPoint{
		"start_sig": {Element: "start_sig", SignalName: "atlas.user.requested", Role: compiler.SignalStarts},
		"catch_sig": {Element: "catch_sig", SignalName: "stop", Role: compiler.SignalCatches},
		"bound_sig": {Element: "bound_sig", SignalName: "stop", Role: compiler.SignalBoundary},
		"throw_sig": {Element: "throw_sig", SignalName: "done", Role: compiler.SignalThrows},
		"end_sig":   {Element: "end_sig", SignalName: "done", Role: compiler.SignalThrows},
		"esp_start": {Element: "esp_start", SignalName: "atlas.user.requested", Role: compiler.SignalEventSubProcess},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SignalPoints:\n got %+v\nwant %+v", got, want)
	}
	for el, sp := range got {
		if receives := sp.Role != compiler.SignalThrows; sp.Receives() != receives {
			t.Errorf("%s: Receives() = %v, want %v", el, sp.Receives(), receives)
		}
	}
}

func TestMessageReceiversListsStartsFirst(t *testing.T) {
	cp := parseShape(t, signalPointsXML)
	want := []compiler.MessagePoint{
		{Element: "start_msg", MessageName: "go", Start: true},
		{Element: "catch_msg", MessageName: "more"},
	}
	if got := cp.MessageReceivers(); !reflect.DeepEqual(got, want) {
		t.Errorf("MessageReceivers = %+v, want %+v", got, want)
	}
}
