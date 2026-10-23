package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The installation's models for the inventories (ADR-0447, ADR-0446): a thrower in
// one project, a catcher in another deployed twice, and a model that speaks for atlas.
const (
	throwerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="S1" name="order.cancelled"/>
  <signal id="S2" name="typo.sent"/>
  <message id="M1" name="invoice.ready"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
  <message id="M2" name="lost.msg"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
  <process id="thrower" name="Thrower" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="cancel"><signalEventDefinition signalRef="S1"/></intermediateThrowEvent>
    <intermediateThrowEvent id="typo"><signalEventDefinition signalRef="S2"/></intermediateThrowEvent>
    <sendTask id="invoice" messageRef="M1"/>
    <endEvent id="lost"><messageEventDefinition messageRef="M2"/></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="cancel"/>
    <sequenceFlow id="f2" sourceRef="cancel" targetRef="typo"/>
    <sequenceFlow id="f3" sourceRef="typo" targetRef="invoice"/>
    <sequenceFlow id="f4" sourceRef="invoice" targetRef="lost"/>
  </process>
</definitions>`

	catcherBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="S1" name="order.cancelled"/>
  <signal id="S3" name="typo.sennt"/>
  <signal id="S4" name="old.name"/>
  <message id="M1" name="invoice.ready"/>
  <message id="M3" name="invoice.recalled"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
  <process id="catcher" name="Catcher" isExecutable="true">
    <startEvent id="go"><messageEventDefinition messageRef="M1"/></startEvent>
    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
    <boundaryEvent id="stop" attachedToRef="work"><signalEventDefinition signalRef="S1"/></boundaryEvent>
    <intermediateCatchEvent id="wait"><signalEventDefinition signalRef="S3"/></intermediateCatchEvent>
    %s
    <endEvent id="e"/>
    <endEvent id="e2"/>
    <subProcess id="recall" triggeredByEvent="true">
      <startEvent id="recalled" isInterrupting="false"><messageEventDefinition messageRef="M3"/></startEvent>
      <endEvent id="re"/>
      <sequenceFlow id="r1" sourceRef="recalled" targetRef="re"/>
    </subProcess>
    <sequenceFlow id="f1" sourceRef="go" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="wait"/>
    %s
    <sequenceFlow id="f9" sourceRef="stop" targetRef="e2"/>
  </process>
</definitions>`

	rogueBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="S5" name="atlas.user.requested"/>
  <message id="M4" name="atlas.order.advanced"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
  <process id="rogue" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="speak"><signalEventDefinition signalRef="S5"/></intermediateThrowEvent>
    <endEvent id="advance"><messageEventDefinition messageRef="M4"/></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="speak"/>
    <sequenceFlow id="f2" sourceRef="speak" targetRef="advance"/>
  </process>
</definitions>`
)

// spacerBPMN throws a name with a trailing space and catches it without one: the
// typo the inventory exists to find, which the engine treats as two names.
const spacerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="S6" name="spaced "/>
  <signal id="S7" name="spaced"/>
  <process id="spacer" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="say"><signalEventDefinition signalRef="S6"/></intermediateThrowEvent>
    <intermediateCatchEvent id="hear"><signalEventDefinition signalRef="S7"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="say"/>
    <sequenceFlow id="f2" sourceRef="say" targetRef="hear"/>
    <sequenceFlow id="f3" sourceRef="hear" targetRef="e"/>
  </process>
</definitions>`

// catcherVersion is the catcher, with the catch on old.name (v1) or without it (v2).
func catcherVersion(withOld bool) string {
	if withOld {
		return strings.Replace(strings.Replace(catcherBPMN, "%s",
			`<intermediateCatchEvent id="old"><signalEventDefinition signalRef="S4"/></intermediateCatchEvent>`, 1), "%s",
			`<sequenceFlow id="f3" sourceRef="wait" targetRef="old"/><sequenceFlow id="f4" sourceRef="old" targetRef="e"/>`, 1)
	}
	return strings.Replace(strings.Replace(catcherBPMN, "%s", "", 1), "%s",
		`<sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>`, 1)
}

// inventoryServer is a server with atlas's system processes and the installation's
// models above, each deployed into its project.
func inventoryServer(t *testing.T) *Server {
	t.Helper()
	srv := eventServer(t, WithSystemProcesses())
	for _, m := range []struct{ xml, project string }{
		{throwerBPMN, "p-sales"},
		{catcherVersion(true), "p-billing"},
		{catcherVersion(false), "p-billing"},
		{rogueBPMN, "p-sales"},
		{spacerBPMN, "p-sales"},
	} {
		var compErr, persistErr error
		srv.do(func() { _, compErr, persistErr = srv.deployModel([]byte(m.xml), nil, 1, m.project, "") })
		if compErr != nil || persistErr != nil {
			t.Fatalf("deploy into %s: %v %v", m.project, compErr, persistErr)
		}
	}
	return srv
}

func rulesOf(fs []modelEventFinding) map[string]string {
	out := map[string]string{}
	for _, f := range fs {
		out[f.Rule] = f.Message
	}
	return out
}

// TestTheSignalsOfTheInstallationAreListedWithTheirFindings: every own signal name is
// listed with every version's points, an atlas.* name only with a throw by a model
// that is not atlas's own, and the newest versions decide the findings (ADR-0447).
func TestTheSignalsOfTheInstallationAreListedWithTheirFindings(t *testing.T) {
	srv := inventoryServer(t)
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/signals", "", "")
	var resp signalsResp
	if code != http.StatusOK || json.Unmarshal(body, &resp) != nil {
		t.Fatalf("signals = %d %s", code, body)
	}
	byName := map[string]signalNameView{}
	for _, g := range resp.Signals {
		byName[g.Name] = g
	}

	cancelled := byName["order.cancelled"]
	rules := rulesOf(cancelled.Findings)
	if msg, ok := rules[findingSignalCrossesProjects]; !ok || !strings.Contains(msg, "project p-sales") ||
		!strings.Contains(msg, "project p-billing") || !strings.Contains(msg, "interrupting receiver there (catcher v2)") {
		t.Errorf("order.cancelled crossing = %q (findings %+v)", msg, cancelled.Findings)
	}
	if _, ok := rules[findingSignalUnreceived]; ok {
		t.Errorf("order.cancelled is received, yet reported unreceived: %+v", cancelled.Findings)
	}
	var boundary *deployedPointView
	for i, p := range cancelled.Points {
		if p.ElementID == "stop" && p.Newest {
			boundary = &cancelled.Points[i]
		}
	}
	if boundary == nil || boundary.Role != "boundary" || boundary.Interrupting == nil || !*boundary.Interrupting ||
		boundary.ProjectID != "p-billing" || boundary.Version != 2 {
		t.Errorf("the catcher's boundary = %+v", boundary)
	}

	if _, ok := rulesOf(byName["typo.sent"].Findings)[findingSignalUnreceived]; !ok {
		t.Errorf("typo.sent findings = %+v, want it unreceived", byName["typo.sent"].Findings)
	}
	if _, ok := rulesOf(byName["typo.sennt"].Findings)[findingSignalUnthrown]; !ok {
		t.Errorf("typo.sennt findings = %+v, want it unthrown", byName["typo.sennt"].Findings)
	}

	// Only the first version catches old.name: it is listed, and decides nothing.
	old := byName["old.name"]
	if len(old.Points) != 1 || old.Points[0].Newest || old.Points[0].Version != 1 || len(old.Findings) != 0 {
		t.Errorf("old.name = %+v, want v1's catch listed, not newest, without findings", old)
	}

	// The system intake's own throw is the catalogue's; the rogue's is the
	// installation's, and no receiver of an atlas.* name is shown.
	req := byName["atlas.user.requested"]
	if len(req.Points) != 1 || req.Points[0].ProcessID != "rogue" || req.Points[0].Role != "throw" {
		t.Errorf("atlas.user.requested points = %+v, want the rogue's throw alone", req.Points)
	}
	if rules := rulesOf(req.Findings); len(rules) != 1 || rules[findingSignalReservedName] == "" {
		t.Errorf("atlas.user.requested findings = %+v, want the reserved name alone", req.Findings)
	}

	// A name is the engine's, exactly: one that differs by a space is another name,
	// thrown and never received, beside one received and never thrown.
	if _, ok := rulesOf(byName["spaced "].Findings)[findingSignalUnreceived]; !ok {
		t.Errorf(`"spaced " findings = %+v, want it unreceived`, byName["spaced "].Findings)
	}
	if _, ok := rulesOf(byName["spaced"].Findings)[findingSignalUnthrown]; !ok {
		t.Errorf(`"spaced" findings = %+v, want it unthrown`, byName["spaced"].Findings)
	}

	if role := routeRoleOf(t, srv, "GET /api/v1/signals"); role != RoleModeler {
		t.Errorf("the signals route is %s, want modeler", role)
	}
}

// TestTheMessagesOfTheInstallationAreGroupedWithTheirFindings: message-sources names
// a model's own throws and message event subprocesses, and the grouped view finds
// what is lost, what crosses a project and what speaks for atlas (ADR-0446).
func TestTheMessagesOfTheInstallationAreGroupedWithTheirFindings(t *testing.T) {
	srv := inventoryServer(t)

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/message-sources", "", "")
	var rows []messageSourceView
	if code != http.StatusOK || json.Unmarshal(body, &rows) != nil {
		t.Fatalf("message sources = %d %s", code, body)
	}
	var sawThrow, sawSubprocess bool
	for _, r := range rows {
		if r.SourceKind == sourceProcessThrow && r.MessageName == "invoice.ready" {
			sawThrow = r.ProcessID == "thrower" && r.ElementID == "invoice" && r.Element == "throw" && r.Role == "throw" &&
				r.Version == 1 && r.Newest && r.ProjectID == "p-sales" && r.Enabled
		}
		if r.SourceKind == sourceWaitingProcess && r.MessageName == "invoice.recalled" && r.Newest {
			sawSubprocess = r.ElementID == "recalled" && r.Role == "event-subprocess" && r.Element == "catch" &&
				r.Interrupting != nil && !*r.Interrupting
		}
	}
	if !sawThrow || !sawSubprocess {
		t.Fatalf("message sources lack the send task (%v) or the event subprocess (%v): %s", sawThrow, sawSubprocess, body)
	}

	code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/message-names", "", "")
	var resp messageNamesResp
	if code != http.StatusOK || json.Unmarshal(body, &resp) != nil {
		t.Fatalf("message names = %d %s", code, body)
	}
	byName := map[string]messageNameView{}
	for _, g := range resp.Messages {
		byName[g.Name] = g
	}

	ready := rulesOf(byName["invoice.ready"].Findings)
	if _, ok := ready[findingMessageCrossesProjects]; !ok {
		t.Errorf("invoice.ready findings = %+v, want it crossing projects", byName["invoice.ready"].Findings)
	}
	if _, ok := ready[findingMessageUnreceived]; ok {
		t.Errorf("invoice.ready is waited for, yet reported unreceived")
	}
	if msg := rulesOf(byName["lost.msg"].Findings)[findingMessageUnreceived]; !strings.Contains(msg, "thrower v1") {
		t.Errorf("lost.msg unreceived = %q", msg)
	}
	// Nothing sends invoice.recalled that a definition shows, and that is no finding:
	// the publish route may send any name.
	if fs := byName["invoice.recalled"].Findings; len(fs) != 0 {
		t.Errorf("invoice.recalled findings = %+v, want none", fs)
	}
	if _, ok := rulesOf(byName["atlas.order.advanced"].Findings)[findingMessageReservedName]; !ok {
		t.Errorf("atlas.order.advanced findings = %+v, want the reserved name", byName["atlas.order.advanced"].Findings)
	}

	// The grouped view is the flat one's rows, no more and no fewer.
	n := 0
	for _, g := range resp.Messages {
		n += len(g.Sources)
	}
	named := 0
	for _, r := range rows {
		if strings.TrimSpace(r.MessageName) != "" {
			named++
		}
	}
	if n != named {
		t.Errorf("the grouped view holds %d rows, the flat listing %d", n, named)
	}
	if role := routeRoleOf(t, srv, "GET /api/v1/message-names"); role != RoleModeler {
		t.Errorf("the message names route is %s, want modeler", role)
	}
}

// TestTwoProcessesWaitingForOneNameAreListedInProcessOrder: the rows of one name and
// kind are ordered by process before version, so the picker's line does not reshuffle
// between two renders of the same state.
func TestTwoProcessesWaitingForOneNameAreListedInProcessOrder(t *testing.T) {
	srv := eventServer(t)
	for _, id := range []string{"b-wait", "a-wait"} {
		xml := strings.ReplaceAll(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="M" name="same.name"/>
  <process id="ID" isExecutable="true">
    <startEvent id="go"><messageEventDefinition messageRef="M"/></startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="go" targetRef="e"/>
  </process>
</definitions>`, "ID", id)
		var compErr, persistErr error
		srv.do(func() { _, compErr, persistErr = srv.deployModel([]byte(xml), nil, 1, "", "") })
		if compErr != nil || persistErr != nil {
			t.Fatalf("deploy %s: %v %v", id, compErr, persistErr)
		}
	}
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/message-sources", "", "")
	var rows []messageSourceView
	if code != http.StatusOK || json.Unmarshal(body, &rows) != nil {
		t.Fatalf("message sources = %d %s", code, body)
	}
	var order []string
	for _, r := range rows {
		if r.MessageName == "same.name" {
			order = append(order, r.ProcessID)
		}
	}
	if strings.Join(order, ",") != "a-wait,b-wait" {
		t.Fatalf("same.name is waited for by %v, want a-wait before b-wait", order)
	}
}
