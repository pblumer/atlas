package agent_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// An ai task is a round with no tools, and that is the claim worth pinning: it is why the
// same two provider adapters serve both without a second wire format, and why an adapter
// offered nothing to call answers in words (ADR-0256).
func TestATaskIsARoundWithNoTools(t *testing.T) {
	req := agent.TaskRequest(agent.Task{Prompt: "Fasse den Antrag zusammen."})
	if len(req.Tools) != 0 {
		t.Errorf("tools = %v, want none: a step with tools is the ad-hoc container, not an ai task", req.Tools)
	}
	if req.Goal != "Fasse den Antrag zusammen." {
		t.Errorf("goal = %q, want the authored prompt", req.Goal)
	}
	if req.Round != 1 {
		t.Errorf("round = %d, want 1: an ai task asks once", req.Round)
	}
	if len(req.Results) != 0 {
		t.Errorf("results = %v, want none: there is no earlier round to remember", req.Results)
	}
}

// The payload is the seam between the engine and a worker, so both directions are pinned
// together: what ResolveTask produced must be what a worker rebuilds, field for field.
func TestATaskSurvivesTheJobPayload(t *testing.T) {
	want := agent.Task{
		Connector:      "acme",
		Model:          "claude-haiku-4-5",
		Prompt:         "Klassifiziere: Dachsanierung",
		ResultVariable: "kategorie",
		RequestID:      "4711",
	}
	got, err := agent.TaskFromPayload(agent.TaskJobPayload(want))
	if err != nil {
		t.Fatalf("TaskFromPayload: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// A task that named no model travels as one that named no model. Empty is the answer
// "ask whatever the provider is configured for", and it must not become anything else on
// the way — a worker that filled it in here would ask a model nobody chose.
func TestATaskWithoutAModelTravelsWithout(t *testing.T) {
	got, err := agent.TaskFromPayload(agent.TaskJobPayload(agent.Task{
		Connector: "acme", Prompt: "Fasse zusammen", ResultVariable: "a",
	}))
	if err != nil {
		t.Fatalf("TaskFromPayload: %v", err)
	}
	if got.Model != "" {
		t.Errorf("model = %q, want empty so the provider's configured model runs", got.Model)
	}
}

// The compiler requires a prompt and a result variable, so their absence in a payload is
// this seam having lost something rather than an author's mistake. It fails here instead
// of asking a provider a question with nowhere to put the answer.
func TestATaskPayloadMissingItsHalvesIsRefused(t *testing.T) {
	for name, fields := range map[string]map[string]any{
		"no prompt": {"connector": "acme", "resultVariable": "a"},
		"no result": {"connector": "acme", "prompt": "Fasse zusammen"},
	} {
		if _, err := agent.TaskFromPayload(fields); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}

// ForModel is what lets one Worker serve a cheap classification and a strong piece of
// advice against one endpoint and one key. The copy is the point: a worker holds one
// adapter and works many jobs at once, so asking for a model must not write the field
// the next job is about to read.
func TestForModelCopiesRatherThanWrites(t *testing.T) {
	for name, m := range map[string]agent.Model{
		"messages":         &agent.HTTPModel{Model: "claude-opus-5"},
		"chat-completions": &agent.ChatCompletionsModel{Model: "gpt-4o"},
	} {
		chooser, ok := m.(agent.ModelChooser)
		if !ok {
			t.Fatalf("%s: the shipped adapter does not implement ModelChooser", name)
		}
		asked := chooser.ForModel("claude-haiku-4-5")
		if asked == m {
			t.Errorf("%s: ForModel returned the adapter itself; a concurrent job would see the wrong model", name)
		}
		if got := modelIDOf(t, asked); got != "claude-haiku-4-5" {
			t.Errorf("%s: asked model = %q, want the one the task named", name, got)
		}
		if got, want := modelIDOf(t, m), map[string]string{"messages": "claude-opus-5", "chat-completions": "gpt-4o"}[name]; got != want {
			t.Errorf("%s: the original now reads %q, want %q — ForModel must not write in place", name, got, want)
		}
		// Naming no model is not a third case for a caller to handle: it is the adapter
		// as configured.
		if same := chooser.ForModel(""); same != m {
			t.Errorf("%s: ForModel(\"\") returned a copy; an unnamed model is the configured one", name)
		}
	}
}

func modelIDOf(t *testing.T, m agent.Model) string {
	t.Helper()
	switch v := m.(type) {
	case *agent.HTTPModel:
		return v.Model
	case *agent.ChatCompletionsModel:
		return v.Model
	}
	t.Fatalf("unexpected adapter %T", m)
	return ""
}

// A compile-time note as much as a test: an adapter that serves exactly one model may
// decline to implement ModelChooser, and the worker is then required to fail rather than
// answer from a model nobody chose. This is the shape of such an adapter.
type oneModelOnly struct{}

func (oneModelOnly) Decide(context.Context, agent.Request) (agent.Decision, error) {
	return agent.Decision{}, nil
}

func TestAnAdapterMayServeExactlyOneModel(t *testing.T) {
	var m agent.Model = oneModelOnly{}
	if _, ok := m.(agent.ModelChooser); ok {
		t.Error("oneModelOnly implements ModelChooser; the point of the second interface is that it need not")
	}
}

// aiTaskProcess is start → ai task → end, with a FEEL prompt over an instance variable, so
// what ResolveTask evaluates is the real compiled expression rather than something the
// test asserts into place.
const aiTaskProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	 xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="einordnen"/>
    <sequenceFlow id="f2" sourceRef="einordnen" targetRef="e"/>
    <serviceTask id="einordnen">
      <extensionElements>
        <atlas:agentConnector connector="anthropic_pb" model="claude-haiku-4-5"
                              prompt="=&quot;Klassifiziere: &quot; + betreff"
                              resultVariable="kategorie"/>
      </extensionElements>
    </serviceTask>
  </process>
</definitions>`

// ResolveTask is the engine's whole contribution to an ai task: the prompt with its FEEL
// evaluated against the variables the task sees, plus the two names that route it. What it
// must not contain is an endpoint or a credential — those are the Worker's, and there is
// nowhere in a Task to put them (ADR-0168, ADR-0041/0069).
func TestResolveTaskEvaluatesThePromptAgainstTheInstance(t *testing.T) {
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

	cp, err := compiler.Parse(401, 1, strings.NewReader(aiTaskProcess))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := engine.New(1, log, store, &clock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "betreff", Kind: model.VarString, Text: "Dachsanierung"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var jobKey uint64
	if err := store.ActivatableJobs(compiler.AiTaskJobTypeIndex, func(k uint64) error {
		jobKey = k
		return nil
	}); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if jobKey == 0 {
		t.Fatal("no job parked under the ai task job type; the task never reached a worker")
	}
	jv, ok, err := store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	ei, ok, err := store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		t.Fatalf("GetElementInstance: ok=%v err=%v", ok, err)
	}

	task, err := agent.ResolveTask(store, cp, cp.ConnectorTask(cp.Node(ei.ElementId).Detail), ei, jv.ElementInstanceKey, jobKey)
	if err != nil {
		t.Fatalf("ResolveTask: %v", err)
	}
	if task.Prompt != "Klassifiziere: Dachsanierung" {
		t.Errorf("prompt = %q, want the FEEL evaluated over the instance's variables", task.Prompt)
	}
	if task.Connector != "anthropic_pb" {
		t.Errorf("connector = %q, want the authored Worker name", task.Connector)
	}
	if task.Model != "claude-haiku-4-5" {
		t.Errorf("model = %q, want the model the task authored", task.Model)
	}
	if task.ResultVariable != "kategorie" {
		t.Errorf("resultVariable = %q, want kategorie", task.ResultVariable)
	}
	if task.RequestID == "" {
		t.Error("requestId is empty; a call repeated after a lease elapsed would look like a second question")
	}
}

// A task with no detail is refused rather than resolved into an empty question. An empty
// prompt is not something a model should be asked.
func TestResolveTaskWithoutADetailIsRefused(t *testing.T) {
	if _, err := agent.ResolveTask(nil, nil, nil, nil, 1, 1); err == nil {
		t.Error("ResolveTask with no detail succeeded, want an error")
	}
}

// The payload and the struct are two spellings of one contract, kept in step by nothing.
// api's TestEveryPayloadArmSendsTheWholeResolvedJob makes this check for every kind whose
// arm writes its map inline; an ai task's map lives here instead, so the check does too.
//
// It caught two real defects in other kinds: a field added to the struct and to the
// handler but not to the map compiles, deploys, leases — and the worker reads a zero
// value.
func TestTheTaskPayloadIsTheWholeTask(t *testing.T) {
	sent := map[string]bool{}
	for k := range agent.TaskJobPayload(agent.Task{}) {
		sent[k] = true
	}
	ty := reflect.TypeOf(agent.Task{})
	for i := 0; i < ty.NumField(); i++ {
		name, _, _ := strings.Cut(ty.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("Task.%s carries no json name; a worker could not read it", ty.Field(i).Name)
		}
		if !sent[name] {
			t.Errorf("Task carries %q, but TaskJobPayload does not send it — the worker reads a zero value and acts on it", name)
		}
		delete(sent, name)
	}
	for name := range sent {
		t.Errorf("TaskJobPayload sends %q, which Task has no field for — nothing on the far side reads it", name)
	}
}
