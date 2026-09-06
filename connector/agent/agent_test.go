package agent_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type clock struct{ t int64 }

func (c *clock) Now() int64 { c.t++; return c.t }

// scriptedModel answers each round from a script and records what it was asked. It is the
// whole of the inference in these tests: no endpoint, no credential, no network — which is
// the point of Model being one call wide.
type scriptedModel struct {
	answers []agent.Decision
	seen    []agent.Request
	err     error
}

func (m *scriptedModel) Decide(_ context.Context, req agent.Request) (agent.Decision, error) {
	m.seen = append(m.seen, req)
	if m.err != nil {
		return agent.Decision{}, m.err
	}
	if len(m.answers) == 0 {
		return agent.Decision{}, nil // out of script: the agent says it is done
	}
	a := m.answers[0]
	m.answers = m.answers[1:]
	return a, nil
}

// agentProcess compiles start → adhoc{zinsen_holen, historie_lesen} → end from XML, so the
// tool names, their documentation and their declared parameters are the real thing rather
// than something the test asserts into place.
const agentProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
	 xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true">
    <documentation>Ermittle die aktuellen Hypothekarzinsen der Bank.</documentation>
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
    <adHocSubProcess id="adhoc">
      <documentation>Ermittle die aktuell gültigen Zinssätze je Laufzeit.</documentation>
      <extensionElements>
        <atlas:agentConnector connector="anthropic_pb" resultCollection="toolCallResults" resultElement="=toolCallId"/>
      </extensionElements>
      <serviceTask id="zinsen_holen">
        <documentation>Liest die Zinstabelle einer Bank.</documentation>
        <extensionElements>
          <zeebe:taskDefinition type="scrape"/>
          <atlas:agentParam name="url" type="string" required="true" description="Die Zinsseite"/>
        </extensionElements>
      </serviceTask>
      <serviceTask id="historie_lesen">
        <documentation>Gibt die zuletzt erfassten Sätze zurück.</documentation>
        <extensionElements>
          <zeebe:taskDefinition type="history"/>
          <atlas:agentParam name="limit" type="number" required="true"/>
        </extensionElements>
      </serviceTask>
    </adHocSubProcess>
  </process>
</definitions>`

func compile(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(400, 1, strings.NewReader(agentProcess))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

func elementIdOf(t *testing.T, cp *compiler.CompiledProcess, bpmnID string) int32 {
	t.Helper()
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		if cp.ElementBpmnId(id) == bpmnID {
			return id
		}
	}
	t.Fatalf("no element %q", bpmnID)
	return -1
}

// TestToolboxIsTheCompiledIndex: what the model is offered comes straight out of the
// compiled container — names are element ids, descriptions are the modeler's own
// documentation, parameters are what <atlas:agentParam> declared. Nothing here is invented
// by the worker, which is what keeps the offered set and the activatable set the same set.
func TestToolboxIsTheCompiledIndex(t *testing.T) {
	cp := compile(t)
	tools, err := agent.Toolbox(cp, elementIdOf(t, cp, "adhoc"))
	if err != nil {
		t.Fatalf("Toolbox: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	byName := map[string]agent.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	scrape, ok := byName["zinsen_holen"]
	if !ok {
		t.Fatalf("tools = %v, want one named zinsen_holen", byName)
	}
	if scrape.Description != "Liest die Zinstabelle einer Bank." {
		t.Errorf("description = %q, want the activity's documentation", scrape.Description)
	}
	if len(scrape.Params) != 1 {
		t.Fatalf("params = %v, want one", scrape.Params)
	}
	if p := scrape.Params[0]; p.Name != "url" || p.Type != "string" || !p.Required || p.Description == "" {
		t.Errorf("param = %+v, want the declared url/string/required one with its prose", p)
	}
}

// TestToolboxRefusesWhatIsNotAnAgentContainer: the worker is registered on one job type,
// but a clear error beats a confusing empty toolbox if it is ever pointed elsewhere.
func TestToolboxRefusesWhatIsNotAnAgentContainer(t *testing.T) {
	cp := compile(t)
	if _, err := agent.Toolbox(cp, elementIdOf(t, cp, "zinsen_holen")); err == nil {
		t.Error("Toolbox on a service task succeeded, want an error")
	}
}

// TestAgentRunsTwoRounds is the end-to-end: a real engine, a real job runner, a real
// compiled process — and a scripted model. Round one calls a tool; the tool is worked;
// round two answers and the container completes, carrying the answer out.
func TestAgentRunsTwoRounds(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close(); _ = log.Close() }()

	cp := compile(t)
	p := engine.New(1, log, store, &clock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	m := &scriptedModel{answers: []agent.Decision{
		{ToolCalls: []model.ToolCall{{
			Tool:      "zinsen_holen",
			CallId:    "call-1",
			Arguments: []model.VariableValue{{Name: "url", Kind: model.VarString, Text: "https://example.ch"}},
		}}},
		{Outputs: []model.VariableValue{{Name: "befund", Kind: model.VarString, Text: "1.33 %"}}},
	}}

	runner := job.NewRunner(store, p)
	runner.HandleCompleting(compiler.AgentJobTypeIndex, func(s state.Reader) job.CompletingHandler {
		return agent.Handler(s, func(uint64) *compiler.CompiledProcess { return cp }, m)
	})
	// The tool itself is a plain worker: it does its work and says nothing about rounds.
	runner.Handle(cp.ServiceTask(cp.Node(elementIdOf(t, cp, "zinsen_holen")).Detail).JobType,
		func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Drive rounds until the instance finishes: each pass works whatever job is offered.
	for i := 0; i < 8; i++ {
		if _, err := runner.PollOnce(); err != nil {
			t.Fatalf("PollOnce %d: %v", i, err)
		}
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle %d: %v", i, err)
		}
		n, err := store.ActiveProcessInstanceCount()
		if err != nil {
			t.Fatalf("ActiveProcessInstanceCount: %v", err)
		}
		if n == 0 {
			break
		}
	}

	if n, _ := store.ActiveProcessInstanceCount(); n != 0 {
		t.Fatalf("the instance did not finish: %d still active", n)
	}
	if len(m.seen) != 2 {
		t.Fatalf("model rounds = %d, want 2", len(m.seen))
	}
	// Round one is asked with the toolbox and nothing collected yet.
	if len(m.seen[0].Tools) != 2 || len(m.seen[0].Results) != 0 || m.seen[0].Round != 1 {
		t.Errorf("round 1 request = %+v, want two tools, no results, round 1", m.seen[0])
	}
	if m.seen[0].Goal == "" {
		t.Error("round 1 carried no goal — the container's documentation is what the agent is for")
	}
	// Round two sees what the first round's call returned: the loop's memory.
	if len(m.seen[1].Results) != 1 || !strings.Contains(m.seen[1].Results[0], "call-1") {
		t.Errorf("round 2 results = %v, want the first call's result", m.seen[1].Results)
	}
	if m.seen[1].Round != 2 {
		t.Errorf("round 2 request said round %d", m.seen[1].Round)
	}
}

// TestAgentModelErrorFailsTheJob: a model that cannot be reached must fail the job —
// retried while retries remain, then an incident (ADR-0061) — rather than return an empty
// decision, which would end the agent's run as if it had answered.
func TestAgentModelErrorFailsTheJob(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close(); _ = log.Close() }()

	cp := compile(t)
	p := engine.New(1, log, store, &clock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	m := &scriptedModel{err: context.DeadlineExceeded}
	runner := job.NewRunner(store, p)
	runner.HandleCompleting(compiler.AgentJobTypeIndex, func(s state.Reader) job.CompletingHandler {
		return agent.Handler(s, func(uint64) *compiler.CompiledProcess { return cp }, m)
	})

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Work the round job until its retries are spent; the last failure raises an incident.
	for i := 0; i < 6; i++ {
		if _, err := runner.PollOnce(); err != nil {
			t.Fatalf("PollOnce %d: %v", i, err)
		}
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle %d: %v", i, err)
		}
	}

	var incidents int
	if err := store.Incidents(func(uint64, *model.IncidentValue) error {
		incidents++
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if incidents == 0 {
		t.Error("an unreachable model left no incident — the run would look finished")
	}
	if n, _ := store.ActiveProcessInstanceCount(); n != 1 {
		t.Error("the instance did not stay parked on the failed round")
	}
}
