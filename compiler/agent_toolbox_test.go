package compiler

import (
	"strings"
	"testing"
)

// agentAdHoc wraps an ad-hoc subprocess body in a minimal runnable process. The
// container is reached from a start event and leads to an end event, so the model is
// structurally complete and the reachability walk has nothing to complain about.
func agentAdHoc(body string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	         xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
	         xmlns:atlas="http://atlas/schema/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    ` + body + `
	  </process>
	</definitions>`
}

// twoToolAdHoc is the shape the record describes: an agent-driven container whose two
// unconnected contained activities are its tools, each documented and each declaring the
// parameters the model has to supply.
const twoToolAdHoc = `<adHocSubProcess id="adhoc">
  <extensionElements>
    <atlas:agentConnector connector="anthropic_pb" resultCollection="toolCallResults" resultElement="=toolCallResult"/>
  </extensionElements>
  <serviceTask id="zinsen_holen">
    <documentation>Liest die Zinstabelle einer Bank. Nutze es, wenn du aktuelle Sätze brauchst.</documentation>
    <extensionElements>
      <zeebe:taskDefinition type="scrape"/>
      <atlas:agentParam name="url" type="string" required="true" description="Vollständige https-URL der Zinsseite"/>
      <atlas:agentParam name="maxRows" type="number" description="Wie viele Zeilen höchstens"/>
    </extensionElements>
  </serviceTask>
  <serviceTask id="historie_lesen">
    <documentation>Gibt die zuletzt erfassten Sätze zurück.</documentation>
    <extensionElements>
      <zeebe:taskDefinition type="history"/>
      <atlas:agentParam name="limit" type="number" required="true"/>
    </extensionElements>
  </serviceTask>
</adHocSubProcess>`

// TestAgentDrivenAdHocCompiles is the core Phase 1 contract: the <atlas:agentConnector>
// on an ad-hoc marks the container agent-driven, its worker and result-collection
// configuration interns, and every entry activity becomes a tool. The entry index is
// unchanged — the tools *are* the entry activities, which is what keeps the runtime's
// activation loop the one that already exists (ADR-0253).
func TestAgentDrivenAdHocCompiles(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(twoToolAdHoc)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	adhoc := nodeByBpmnId(t, cp, "adhoc")
	d := cp.AdHoc(adhoc.Detail)

	if !d.AgentDriven {
		t.Fatal("AgentDriven = false, want true: the agentConnector extension marks the container")
	}
	if got := cp.Intern(d.AgentWorker); got != "anthropic_pb" {
		t.Errorf("AgentWorker = %q, want anthropic_pb", got)
	}
	if got := cp.Intern(d.ResultCollection); got != "toolCallResults" {
		t.Errorf("ResultCollection = %q, want toolCallResults", got)
	}
	if d.ResultElement == nil {
		t.Error("ResultElement = nil, want the compiled FEEL expression")
	}

	// Both contained activities are entry activities, so both are tools.
	if n := len(cp.AdHocEntries(adhoc.ElementId)); n != 2 {
		t.Errorf("entry activities = %d, want 2", n)
	}
	if len(d.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(d.Tools))
	}
	byId := map[string]AgentTool{}
	for _, tool := range d.Tools {
		byId[cp.ElementBpmnId(tool.Element)] = tool
	}
	if _, ok := byId["zinsen_holen"]; !ok {
		t.Fatalf("tools = %v, want one for zinsen_holen", byId)
	}
	if _, ok := byId["historie_lesen"]; !ok {
		t.Fatalf("tools = %v, want one for historie_lesen", byId)
	}

	// A tool's description is the activity's own <documentation> — the sentence the
	// modeler wrote for the next human is the one the model reads. It is already
	// interned per element (ADR-0025), so a tool carries no copy of it.
	doc := cp.ElementDocumentation(byId["zinsen_holen"].Element)
	if !strings.HasPrefix(doc, "Liest die Zinstabelle") {
		t.Errorf("tool description = %q, want the activity's documentation", doc)
	}
}

// TestAgentToolParametersIntern checks the parameter declaration: name, type, the
// optional description and the required flag, in document order.
func TestAgentToolParametersIntern(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(twoToolAdHoc)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail)
	var scrape AgentTool
	for _, tool := range d.Tools {
		if cp.ElementBpmnId(tool.Element) == "zinsen_holen" {
			scrape = tool
		}
	}
	if len(scrape.Params) != 2 {
		t.Fatalf("params = %d, want 2", len(scrape.Params))
	}
	url := scrape.Params[0]
	if got := cp.Intern(url.Name); got != "url" {
		t.Errorf("params[0].Name = %q, want url", got)
	}
	if got := cp.Intern(url.Type); got != "string" {
		t.Errorf("params[0].Type = %q, want string", got)
	}
	if !url.Required {
		t.Error("params[0].Required = false, want true")
	}
	if got := cp.Intern(url.Description); !strings.HasPrefix(got, "Vollständige") {
		t.Errorf("params[0].Description = %q, want the declared prose", got)
	}
	rows := scrape.Params[1]
	if got := cp.Intern(rows.Name); got != "maxRows" {
		t.Errorf("params[1].Name = %q, want maxRows", got)
	}
	if rows.Required {
		t.Error("params[1].Required = true, want false — required is opt-in")
	}
}

// TestAgentToolsAreEntryActivitiesOnly: an activity a sequence flow reaches inside the
// container is run by that flow, not chosen by the model, so it is not a tool. This is
// the same predicate the entry index uses, which is the point — the two cannot drift.
func TestAgentToolsAreEntryActivitiesOnly(t *testing.T) {
	const body = `<adHocSubProcess id="adhoc">
	  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>
	  <serviceTask id="a"><documentation>A tool.</documentation>
	    <extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	  <serviceTask id="b"><documentation>Runs after a.</documentation>
	    <extensionElements><zeebe:taskDefinition type="tb"/></extensionElements></serviceTask>
	  <sequenceFlow id="inner" sourceRef="a" targetRef="b"/>
	</adHocSubProcess>`
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(body)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail)
	if len(d.Tools) != 1 {
		t.Fatalf("tools = %d, want 1 (b is reached by a flow, not chosen)", len(d.Tools))
	}
	if got := cp.ElementBpmnId(d.Tools[0].Element); got != "a" {
		t.Errorf("tool = %q, want a", got)
	}
}

// TestPlainAdHocIsNotAgentDriven guards the existing element: an ad-hoc without the
// extension keeps ADR-0138's semantics exactly, tools and all.
func TestPlainAdHocIsNotAgentDriven(t *testing.T) {
	const body = `<adHocSubProcess id="adhoc">
	  <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	</adHocSubProcess>`
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(body)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail)
	if d.AgentDriven {
		t.Error("AgentDriven = true, want false for a plain ad-hoc")
	}
	if d.AgentWorker != -1 || d.ResultCollection != -1 || d.ResultElement != nil || d.Tools != nil {
		t.Errorf("plain ad-hoc carries agent configuration: %+v", d)
	}
	if n := len(cp.AdHocEntries(nodeByBpmnId(t, cp, "adhoc").ElementId)); n != 1 {
		t.Errorf("entry activities = %d, want 1 — the entry index is unchanged", n)
	}
}

// TestAgentAdHocDeployErrors: the model faults that make an agent-driven container
// unrunnable, each refused at deploy with a message that names the fix.
func TestAgentAdHocDeployErrors(t *testing.T) {
	tool := `<serviceTask id="a"><documentation>A tool.</documentation>
	  <extensionElements><zeebe:taskDefinition type="ta"/>%s</extensionElements></serviceTask>`
	cases := []struct {
		name string
		body string
		want string
	}{{
		// An agent with no tools is an agent task, and ADR-0117 already has the shape
		// for one — say so rather than compiling a container that can never act.
		name: "no tools",
		body: `<adHocSubProcess id="adhoc">
		  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>
		</adHocSubProcess>`,
		want: "no entry activity",
	}, {
		name: "parameter without a name",
		body: `<adHocSubProcess id="adhoc">
		  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>` +
			strings.Replace(tool, "%s", `<atlas:agentParam type="string"/>`, 1) +
			`</adHocSubProcess>`,
		want: "agentParam without a name",
	}, {
		name: "unknown parameter type",
		body: `<adHocSubProcess id="adhoc">
		  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>` +
			strings.Replace(tool, "%s", `<atlas:agentParam name="x" type="date"/>`, 1) +
			`</adHocSubProcess>`,
		want: `type "date"`,
	}, {
		name: "duplicate parameter name",
		body: `<adHocSubProcess id="adhoc">
		  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>` +
			strings.Replace(tool, "%s",
				`<atlas:agentParam name="x" type="string"/><atlas:agentParam name="x" type="number"/>`, 1) +
			`</adHocSubProcess>`,
		want: "declares the parameter \"x\" twice",
	}, {
		// The round boundary is the scope drain, so "let the rest finish" has no
		// meaning: the next round has not been asked for yet.
		name: "cancelRemainingInstances false",
		body: `<adHocSubProcess id="adhoc" cancelRemainingInstances="false">
		  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>` +
			strings.Replace(tool, "%s", "", 1) +
			`</adHocSubProcess>`,
		want: "cancelRemainingInstances",
	}, {
		name: "agentConnector without a worker",
		body: `<adHocSubProcess id="adhoc">
		  <extensionElements><atlas:agentConnector/></extensionElements>` +
			strings.Replace(tool, "%s", "", 1) +
			`</adHocSubProcess>`,
		want: "names no connector",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(agentAdHoc(tc.body)))
			if err == nil {
				t.Fatalf("Parse succeeded, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestUndocumentedToolWarns: a tool whose description is empty is a real defect — the
// model is told a tool exists and not what it is for — but it is a quality defect, not
// an unrunnable model, so it is a Problems-panel warning rather than a deploy error.
func TestUndocumentedToolWarns(t *testing.T) {
	const body = `<adHocSubProcess id="adhoc">
	  <extensionElements><atlas:agentConnector connector="w"/></extensionElements>
	  <serviceTask id="undokumentiert">
	    <extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	  <serviceTask id="dokumentiert"><documentation>Tut etwas Nützliches.</documentation>
	    <extensionElements><zeebe:taskDefinition type="tb"/></extensionElements></serviceTask>
	</adHocSubProcess>`
	problems, err := ValidateModel(strings.NewReader(agentAdHoc(body)))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	var found []Problem
	for _, p := range problems {
		if p.Rule == RuleAgentTool {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("agent-tool problems = %v, want exactly one (for the undocumented tool)", found)
	}
	if found[0].Element != "undokumentiert" {
		t.Errorf("problem element = %q, want undokumentiert", found[0].Element)
	}
	if found[0].Severity != SeverityWarning {
		t.Errorf("severity = %q, want a warning — an undescribed tool still runs", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "documentation") {
		t.Errorf("message = %q, want it to name the missing documentation", found[0].Message)
	}
}
