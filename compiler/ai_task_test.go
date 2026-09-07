package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:agentConnector> extension is an ai task (ADR-0256):
// one call to a language model, one answer into one variable. It is the ordinary use of a
// model in a business
// process — summarise this, classify that, extract these fields — and it needs no
// toolbox and no rounds, because a step with tools is the ad-hoc container ADR-0253
// describes instead.
//
// What travels is the question and where the answer goes. The endpoint, the credential
// and the wire format stay on the Worker (ADR-0041/0069/0168); the *model* does not,
// because which model to ask is what the step is about.
const aiTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:agentConnector connector="acme" model="claude-haiku-4-5"
                              prompt="=&quot;Klassifiziere: &quot; + antrag.text"
                              resultVariable="kategorie"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseAiTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(aiTaskBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != AiTaskJobType {
		t.Errorf("jobType = %q, want %q", got, AiTaskJobType)
	}
	// Not the agent's job type. An ai task's completion is variables; an agent round's
	// completion is tool calls the engine turns into activations (ADR-0253/0254). The
	// whole point of a second index is that nothing has to look inside a payload to
	// tell which of the two it is holding.
	if d.JobType == AgentJobTypeIndex {
		t.Fatal("an ai task compiled onto the agent round's job type; the two shapes must not share an index")
	}
	if d.JobType != AiTaskJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved AiTaskJobTypeIndex %d", d.JobType, AiTaskJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "acme" {
		t.Errorf("connector = %q, want acme", got)
	}
	if got := cp.Intern(d.AgentModel); got != "claude-haiku-4-5" {
		t.Errorf("model = %q, want the authored model name", got)
	}
	if d.AgentPrompt.Expr == nil {
		t.Errorf("prompt = %+v, want a compiled FEEL expression", d.AgentPrompt)
	}
	if got := cp.Intern(d.ResultVar); got != "kategorie" {
		t.Errorf("resultVariable = %q, want kategorie", got)
	}
	// No credential and no endpoint reached the compiled process: a provider is
	// configured on the Worker, never authored (ADR-0041/0069).
	if d.Auth != -1 {
		t.Errorf("Auth = %d, want -1: an ai task authors no credential", d.Auth)
	}
	if d.Url.Literal != "" || d.Url.Expr != nil {
		t.Errorf("Url = %+v, want unset: an ai task authors no endpoint", d.Url)
	}
}

// TestAiTaskLiteralPromptIsLiteral: the prompt takes the same literal-or-FEEL toggle
// every other authored connector value takes, so the common case — a fixed
// instruction — needs no expression syntax.
func TestAiTaskLiteralPromptIsLiteral(t *testing.T) {
	cp, d := compileAiTask(t, `connector="acme" prompt="Fasse den Antrag zusammen." resultVariable="a"`)
	if d.AgentPrompt.Expr != nil || d.AgentPrompt.Literal != "Fasse den Antrag zusammen." {
		t.Errorf("prompt = %+v, want the literal instruction", d.AgentPrompt)
	}
	_ = cp
}

// TestAiTaskRetriesAreTheTasks: retries is the one attribute both hosts spell and only
// this one honours, which is why the container refuses it — here it must actually reach
// the compiled job (ADR-0135).
func TestAiTaskRetriesAreTheTasks(t *testing.T) {
	_, d := compileAiTask(t, `connector="acme" prompt="Fasse zusammen" resultVariable="a" retries="5"`)
	if d.Retries != 5 {
		t.Errorf("retries = %d, want the authored 5", d.Retries)
	}
}

// TestAiTaskWithoutAModelInheritsTheWorkers is the whole reason the model moved onto
// the element: a task that names none is not a task naming the empty model, it is a
// task that asks whatever the Worker is configured for. Those are different answers,
// and -1 is how the compiled process says the second one.
func TestAiTaskWithoutAModelInheritsTheWorkers(t *testing.T) {
	_, d := compileAiTask(t, `connector="acme" prompt="Fasse zusammen" resultVariable="a"`)
	if d.AgentModel != -1 {
		t.Errorf("model = %d, want -1 so the Worker's configured model runs", d.AgentModel)
	}
}

// TestTwoAiTasksShareOneWorkerAndDifferentModels is the multiplication ADR-0255 caused
// and this record removes: a classification wants a small model and the advice beside
// it wants a strong one, against the same account and the same key. Under a model
// pinned to the Worker that needed two Workers differing in one string.
func TestTwoAiTasksShareOneWorkerAndDifferentModels(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="klein">
      <bpmn:extensionElements><atlas:agentConnector connector="acme" model="claude-haiku-4-5" prompt="Klassifiziere" resultVariable="k"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="gross">
      <bpmn:extensionElements><atlas:agentConnector connector="acme" model="claude-opus-5" prompt="Berate" resultVariable="b"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="klein"/>
    <bpmn:sequenceFlow id="f2" sourceRef="klein" targetRef="gross"/>
    <bpmn:sequenceFlow id="f3" sourceRef="gross" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	first := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	second := cp.Flow(cp.Outgoing(first)[0]).Target
	a := cp.ConnectorTask(cp.Node(first).Detail)
	b := cp.ConnectorTask(cp.Node(second).Detail)
	if cp.Intern(a.Connector) != "acme" || cp.Intern(b.Connector) != "acme" {
		t.Fatalf("connectors = %q/%q, want both acme", cp.Intern(a.Connector), cp.Intern(b.Connector))
	}
	if cp.Intern(a.AgentModel) != "claude-haiku-4-5" || cp.Intern(b.AgentModel) != "claude-opus-5" {
		t.Errorf("models = %q/%q, want the two authored ones", cp.Intern(a.AgentModel), cp.Intern(b.AgentModel))
	}
}

// TestAiTaskRefusesBadModels is the deploy-time half of the rule. Two of these are the
// container-only attributes: the same extension element hosts an agent-driven ad-hoc,
// and a value only that host reads is refused here rather than dropped, because from
// the author's side a dropped attribute and an honoured one look identical.
func TestAiTaskRefusesBadModels(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no worker":         {`prompt="Fasse zusammen" resultVariable="a"`, "names no connector"},
		"blank worker":      {`connector="  " prompt="Fasse zusammen" resultVariable="a"`, "names no connector"},
		"no prompt":         {`connector="acme" resultVariable="a"`, "needs a prompt"},
		"no result":         {`connector="acme" prompt="Fasse zusammen"`, "needs a resultVariable"},
		"result collection": {`connector="acme" prompt="P" resultVariable="a" resultCollection="ergebnisse"`, "only an agent-driven ad-hoc subprocess reads"},
		"result element":    {`connector="acme" prompt="P" resultVariable="a" resultElement="=x"`, "only an agent-driven ad-hoc subprocess reads"},
		"broken prompt":     {`connector="acme" prompt="=" resultVariable="a"`, "empty FEEL expression"},
	} {
		_, err := Parse(1, 1, strings.NewReader(aiTaskBPMNWith(tc.attrs)))
		if err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should mention %q", name, err, tc.want)
		}
	}
}

// TestAnAgentContainerRefusesTheTasksAttributes is the other direction of the same
// rule. One element with two meanings has to say which one it was handed: a prompt on
// a container has nowhere to go — a round's standing instruction is the Worker's, and
// its answer is a tool call, not a value — so silence there would be configuration
// that looks live and is never read.
func TestAnAgentContainerRefusesTheTasksAttributes(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"prompt":         {`connector="acme" prompt="Entscheide"`, "only an ai service task reads"},
		"resultVariable": {`connector="acme" resultVariable="antwort"`, "only an ai service task reads"},
		"retries":        {`connector="acme" retries="5"`, "only an ai service task reads"},
	} {
		_, err := Parse(1, 1, strings.NewReader(agentContainerBPMN(tc.attrs)))
		if err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should mention %q", name, err, tc.want)
		}
	}
}

// TestAnAgentContainerNamesItsModel: the container takes the same authored model a task
// does, and naming none still means the Worker's own.
func TestAnAgentContainerNamesItsModel(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentContainerBPMN(`connector="acme" model="claude-opus-5"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "ah").Detail)
	if got := cp.Intern(d.AgentModel); got != "claude-opus-5" {
		t.Errorf("model = %q, want the authored model name", got)
	}

	cp, err = Parse(1, 1, strings.NewReader(agentContainerBPMN(`connector="acme"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d := cp.AdHoc(nodeByBpmnId(t, cp, "ah").Detail); d.AgentModel != -1 {
		t.Errorf("model = %d, want -1 so the Worker's configured model runs", d.AgentModel)
	}
}

// aiTaskBPMNWith wraps one set of connector attributes in the smallest process that
// carries a service task.
func aiTaskBPMNWith(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><atlas:agentConnector ` + attrs + `/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// agentContainerBPMN is the other host: an agent-driven ad-hoc with one contained
// activity to offer as a tool, so it compiles as far as the attribute checks.
func agentContainerBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:adHocSubProcess id="ah">
      <bpmn:extensionElements><atlas:agentConnector ` + attrs + `/></bpmn:extensionElements>
      <bpmn:task id="werkzeug"/>
    </bpmn:adHocSubProcess>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="ah"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ah" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// compileAiTask compiles one ai task and returns the compiled process and its detail,
// failing the test if it does not compile.
func compileAiTask(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(aiTaskBPMNWith(attrs)))
	if err != nil {
		t.Fatalf("Parse(%s): %v", attrs, err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp, cp.ConnectorTask(cp.Node(task).Detail)
}

// --- What an agent is given to read (ADR-0257) ---------

// The container names the process variables its agent is given. Names rather than values,
// on the element, for the reason ADR-0253 gives about tools: what an agent may reach is
// the diagram, so what it may read is in the diagram too.
func TestAnAgentContainerNamesWhatItMayRead(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentContainerBPMN(`connector="acme" context="dossier, kunde ,dossier"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "ah").Detail)
	var got []string
	for _, idx := range d.AgentContext {
		got = append(got, cp.Intern(idx))
	}
	// Authored order, trimmed, and a name said twice contributes once: putting the same
	// fact in front of the model twice says nothing more.
	want := []string{"dossier", "kunde"}
	if len(got) != len(want) {
		t.Fatalf("context = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("context[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Naming none is a real design — an agent whose tools fetch what it needs — so it compiles
// to nothing rather than to an error.
func TestAnAgentContainerMayNameNoContext(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentContainerBPMN(`connector="acme"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d := cp.AdHoc(nodeByBpmnId(t, cp, "ah").Detail); len(d.AgentContext) != 0 {
		t.Errorf("context = %#v, want none", d.AgentContext)
	}
}

// And on an ai task the attribute is refused, like the container's other attributes: a
// task's prompt is FEEL over the variables it sees, so it already carries its own data.
func TestAnAiTaskRefusesAContextList(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(aiTaskBPMNWith(`connector="acme" prompt="P" resultVariable="a" context="dossier"`)))
	if err == nil {
		t.Fatal("an ai task naming a context list compiled, want an error")
	}
	if !strings.Contains(err.Error(), "only an agent-driven ad-hoc subprocess reads") {
		t.Errorf("error %q should say the attribute belongs to the container", err)
	}
}
