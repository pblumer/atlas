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
  <signal id="Sig_kill" name="kill"/>
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
    <boundaryEvent id="bound_kill" attachedToRef="work"><signalEventDefinition signalRef="Sig_kill"/></boundaryEvent>
    <intermediateThrowEvent id="throw_sig"><signalEventDefinition signalRef="Sig_done"/></intermediateThrowEvent>
    <endEvent id="end_sig"><signalEventDefinition signalRef="Sig_done"/></endEvent>
    <endEvent id="end_b"/>
    <endEvent id="end_k"/>
    <subProcess id="esp" triggeredByEvent="true">
      <startEvent id="esp_start" isInterrupting="false"><signalEventDefinition signalRef="Sig_req"/></startEvent>
      <endEvent id="esp_end"/>
      <sequenceFlow id="e1" sourceRef="esp_start" targetRef="esp_end"/>
    </subProcess>
    <subProcess id="esp_k" triggeredByEvent="true">
      <startEvent id="esp_kill" isInterrupting="true"><signalEventDefinition signalRef="Sig_kill"/></startEvent>
      <endEvent id="esp_k_end"/>
      <sequenceFlow id="e2" sourceRef="esp_kill" targetRef="esp_k_end"/>
    </subProcess>
    <sequenceFlow id="f0" sourceRef="start_sig" targetRef="join"/>
    <sequenceFlow id="f0b" sourceRef="start_msg" targetRef="join"/>
    <sequenceFlow id="f1" sourceRef="join" targetRef="catch_sig"/>
    <sequenceFlow id="f2" sourceRef="catch_sig" targetRef="catch_msg"/>
    <sequenceFlow id="f3" sourceRef="catch_msg" targetRef="work"/>
    <sequenceFlow id="f4" sourceRef="work" targetRef="throw_sig"/>
    <sequenceFlow id="f5" sourceRef="throw_sig" targetRef="end_sig"/>
    <sequenceFlow id="f6" sourceRef="bound_sig" targetRef="end_b"/>
    <sequenceFlow id="f7" sourceRef="bound_kill" targetRef="end_k"/>
  </process>
</definitions>`

func TestSignalPointsNamesEveryThrowAndReceiver(t *testing.T) {
	cp := parseShape(t, signalPointsXML)
	got := map[string]compiler.SignalPoint{}
	for _, sp := range cp.SignalPoints() {
		got[sp.Element] = sp
	}
	want := map[string]compiler.SignalPoint{
		"start_sig":  {Element: "start_sig", SignalName: "atlas.user.requested", Role: compiler.SignalStarts},
		"catch_sig":  {Element: "catch_sig", SignalName: "stop", Role: compiler.SignalCatches},
		"bound_sig":  {Element: "bound_sig", SignalName: "stop", Role: compiler.SignalBoundary},
		"bound_kill": {Element: "bound_kill", SignalName: "kill", Role: compiler.SignalBoundary, Interrupting: true},
		"throw_sig":  {Element: "throw_sig", SignalName: "done", Role: compiler.SignalThrows},
		"end_sig":    {Element: "end_sig", SignalName: "done", Role: compiler.SignalThrows},
		"esp_start":  {Element: "esp_start", SignalName: "atlas.user.requested", Role: compiler.SignalEventSubProcess},
		"esp_kill":   {Element: "esp_kill", SignalName: "kill", Role: compiler.SignalEventSubProcess, Interrupting: true},
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

// One process with every way an element sends or receives a message (ADR-0446 §1):
// a start, an intermediate catch, a receive task, an interrupting boundary, a
// non-interrupting event subprocess, and the three senders — a throw event, a send
// task naming a message, and a message end event.
const messagePointsXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_go" name="go"/>
  <message id="Msg_more" name="more"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_reply" name="reply"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_halt" name="halt"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_note" name="note"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_poke" name="poke"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_sent" name="sent"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <message id="Msg_bye" name="bye"><extensionElements><zeebe:subscription correlationKey="=key"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="start_msg"><messageEventDefinition messageRef="Msg_go"/></startEvent>
    <intermediateCatchEvent id="catch_msg"><messageEventDefinition messageRef="Msg_more"/></intermediateCatchEvent>
    <receiveTask id="recv" messageRef="Msg_reply"/>
    <boundaryEvent id="bound_halt" attachedToRef="recv"><messageEventDefinition messageRef="Msg_halt"/></boundaryEvent>
    <intermediateThrowEvent id="throw_msg"><messageEventDefinition messageRef="Msg_poke"/></intermediateThrowEvent>
    <sendTask id="send_msg" messageRef="Msg_sent"/>
    <endEvent id="end_msg"><messageEventDefinition messageRef="Msg_bye"/></endEvent>
    <endEvent id="end_h"/>
    <subProcess id="esp" triggeredByEvent="true">
      <startEvent id="esp_note" isInterrupting="false"><messageEventDefinition messageRef="Msg_note"/></startEvent>
      <endEvent id="esp_end"/>
      <sequenceFlow id="e1" sourceRef="esp_note" targetRef="esp_end"/>
    </subProcess>
    <sequenceFlow id="f1" sourceRef="start_msg" targetRef="catch_msg"/>
    <sequenceFlow id="f2" sourceRef="catch_msg" targetRef="recv"/>
    <sequenceFlow id="f3" sourceRef="recv" targetRef="throw_msg"/>
    <sequenceFlow id="f4" sourceRef="throw_msg" targetRef="send_msg"/>
    <sequenceFlow id="f5" sourceRef="send_msg" targetRef="end_msg"/>
    <sequenceFlow id="f6" sourceRef="bound_halt" targetRef="end_h"/>
  </process>
</definitions>`

func TestMessageReceiversListsStartsFirst(t *testing.T) {
	cp := parseShape(t, signalPointsXML)
	want := []compiler.MessagePoint{
		{Element: "start_msg", MessageName: "go", Role: compiler.MessageStarts},
		{Element: "catch_msg", MessageName: "more", Role: compiler.MessageCatches},
	}
	if got := cp.MessageReceivers(); !reflect.DeepEqual(got, want) {
		t.Errorf("MessageReceivers = %+v, want %+v", got, want)
	}
}

// TestMessagePointsNameEverySenderAndReceiver: a message event subprocess is a
// receiver, named by its start event, and a send task naming a message is a sender
// beside the throw and the end event (ADR-0446 §1).
func TestMessagePointsNameEverySenderAndReceiver(t *testing.T) {
	cp := parseShape(t, messagePointsXML)
	got := map[string]compiler.MessagePoint{}
	for _, mp := range cp.MessageReceivers() {
		got[mp.Element] = mp
	}
	for _, mp := range cp.MessageSenders() {
		if _, dup := got[mp.Element]; dup {
			t.Errorf("%s is both a sender and a receiver", mp.Element)
		}
		got[mp.Element] = mp
	}
	want := map[string]compiler.MessagePoint{
		"start_msg":  {Element: "start_msg", MessageName: "go", Role: compiler.MessageStarts},
		"catch_msg":  {Element: "catch_msg", MessageName: "more", Role: compiler.MessageCatches},
		"recv":       {Element: "recv", MessageName: "reply", Role: compiler.MessageCatches},
		"bound_halt": {Element: "bound_halt", MessageName: "halt", Role: compiler.MessageBoundary, Interrupting: true},
		"esp_note":   {Element: "esp_note", MessageName: "note", Role: compiler.MessageEventSubProcess},
		"throw_msg":  {Element: "throw_msg", MessageName: "poke", Role: compiler.MessageSends},
		"send_msg":   {Element: "send_msg", MessageName: "sent", Role: compiler.MessageSends},
		"end_msg":    {Element: "end_msg", MessageName: "bye", Role: compiler.MessageSends},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("message points:\n got %+v\nwant %+v", got, want)
	}
	for el, mp := range got {
		if receives := mp.Role != compiler.MessageSends; mp.Receives() != receives {
			t.Errorf("%s: Receives() = %v, want %v", el, mp.Receives(), receives)
		}
	}
	if first := cp.MessageReceivers()[0]; first.Role != compiler.MessageStarts {
		t.Errorf("MessageReceivers lists %+v first, want the start", first)
	}
}
