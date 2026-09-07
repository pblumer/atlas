package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

// ADR-0256, worker half: an ai task is one call to a language model, and the answer lands
// in the variable the *model file*
// named — not the one this worker is configured to answer rounds into. That rename is the
// whole difference between the two job types out here, and it is the reason a task is
// worth its own runner rather than a flag on the round's.

// choosingModel answers from a script and records both names it was routed by: the
// provider it was picked out of the map as, and the language model it was asked for.
type choosingModel struct {
	provider   string
	model      string
	routedTo   *string
	askedModel *string
	answer     agent.Decision
	err        error
}

func (m choosingModel) Decide(context.Context, agent.Request) (agent.Decision, error) {
	*m.routedTo = m.provider
	*m.askedModel = m.model
	if m.err != nil {
		return agent.Decision{}, m.err
	}
	return m.answer, nil
}

func (m choosingModel) ForModel(id string) agent.Model {
	if id == "" {
		return m
	}
	m.model = id
	return m
}

func answering(text string) agent.Decision {
	return agent.Decision{Outputs: []model.VariableValue{
		{Name: agent.DefaultAnswerVariable, Kind: model.VarString, Text: text},
	}}
}

func aiTaskJob(fields map[string]any) Job {
	return Job{Connector: &ConnectorPayload{Kind: "agent", Fields: fields}}
}

// The answer lands under the variable the task named, whatever the adapter called it.
// A round answers into the worker's configured variable because a round's answer is how
// the container ends; a task's answer is the point of the task, so the model names it.
func TestAnAiTaskAnswersIntoTheVariableTheModelNamed(t *testing.T) {
	var routed, asked string
	models := map[string]agent.Model{"anthropic_pb": choosingModel{
		provider: "anthropic_pb", model: "claude-opus-5",
		routedTo: &routed, askedModel: &asked,
		answer: answering("Dachsanierung"),
	}}

	vars, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "anthropic_pb", "prompt": "Klassifiziere", "resultVariable": "kategorie",
	}), models)
	if err != nil {
		t.Fatalf("RunAiTask: %v", err)
	}
	if got := vars["kategorie"]; got != "Dachsanierung" {
		t.Errorf("variables = %v, want the answer under the authored name", vars)
	}
	if _, wrong := vars[agent.DefaultAnswerVariable]; wrong {
		t.Errorf("variables = %v, still carry the adapter's own name; the rename is the task's whole point", vars)
	}
}

// The task's model reaches the adapter. This is the multiplication ADR-0255 caused and
// the record removes: two tasks, two models, one provider — one endpoint and one key.
func TestTwoAiTasksAskTwoModelsThroughOneProvider(t *testing.T) {
	var routed, asked string
	models := map[string]agent.Model{"anthropic_pb": choosingModel{
		provider: "anthropic_pb", model: "claude-opus-5",
		routedTo: &routed, askedModel: &asked, answer: answering("ok"),
	}}
	for _, want := range []string{"claude-haiku-4-5", "claude-opus-5"} {
		if _, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
			"connector": "anthropic_pb", "model": want,
			"prompt": "Frage", "resultVariable": "a",
		}), models); err != nil {
			t.Fatalf("RunAiTask(%s): %v", want, err)
		}
		if routed != "anthropic_pb" {
			t.Errorf("routed to %q, want the one provider both tasks share", routed)
		}
		if asked != want {
			t.Errorf("asked model %q, want the one the task authored (%q)", asked, want)
		}
	}
}

// A task that names no model is asked of whatever the provider is configured for. That
// is what makes the Worker's model a default rather than the only source.
func TestAnAiTaskWithoutAModelAsksTheProvidersOwn(t *testing.T) {
	var routed, asked string
	models := map[string]agent.Model{"anthropic_pb": choosingModel{
		provider: "anthropic_pb", model: "claude-opus-5",
		routedTo: &routed, askedModel: &asked, answer: answering("ok"),
	}}
	if _, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "anthropic_pb", "prompt": "Frage", "resultVariable": "a",
	}), models); err != nil {
		t.Fatalf("RunAiTask: %v", err)
	}
	if asked != "claude-opus-5" {
		t.Errorf("asked model %q, want the provider's configured default", asked)
	}
}

// fixedModel serves exactly one language model and does not implement ModelChooser — the
// shape of a local runtime with one file loaded.
type fixedModel struct{ asked *bool }

func (m fixedModel) Decide(context.Context, agent.Request) (agent.Decision, error) {
	*m.asked = true
	return answering("etwas"), nil
}

// An adapter that cannot honour the authored model must say so, not answer from a
// different one. A wrong model is a wrong answer, and a wrong answer that looks right is
// the worst thing this can produce — so the job fails and nothing is asked at all.
func TestAnAiTaskNamingAModelAnAdapterCannotServeFails(t *testing.T) {
	var asked bool
	models := map[string]agent.Model{"lokal": fixedModel{asked: &asked}}
	_, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "lokal", "model": "claude-opus-5",
		"prompt": "Frage", "resultVariable": "a",
	}), models)
	if err == nil {
		t.Fatal("a task naming a model this adapter cannot serve succeeded")
	}
	if !strings.Contains(err.Error(), "claude-opus-5") || !strings.Contains(err.Error(), "lokal") {
		t.Errorf("err = %v, want it to name both the model asked for and the connector", err)
	}
	if asked {
		t.Error("the adapter was asked anyway, from a model nobody chose")
	}
	// The same adapter serves a task that names no model: declining to choose is not
	// declining to work.
	if _, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "lokal", "prompt": "Frage", "resultVariable": "a",
	}), models); err != nil {
		t.Fatalf("RunAiTask with no model named: %v", err)
	}
}

// An ai task offers no tools, so a model that called one has answered a question nobody
// asked — and there is no container to activate it in. Refusing beats writing a tool call
// into a variable as if it were prose.
func TestAnAiTaskRefusesAToolCall(t *testing.T) {
	var routed, asked string
	models := map[string]agent.Model{"anthropic_pb": choosingModel{
		provider: "anthropic_pb", routedTo: &routed, askedModel: &asked,
		answer: agent.Decision{ToolCalls: []model.ToolCall{{Tool: "irgendwas", CallId: "1"}}},
	}}
	_, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "anthropic_pb", "prompt": "Frage", "resultVariable": "a",
	}), models)
	if err == nil {
		t.Fatal("a tool call for an ai task succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "tool") {
		t.Errorf("err = %v, want it to say a tool was called", err)
	}
}

// Neither nothing nor several: the adapters answer with exactly one value or fail, so any
// other count is a broken adapter, and renaming the wrong one of several would put text
// under a name that promises something else.
func TestAnAiTaskWantsExactlyOneAnswer(t *testing.T) {
	var routed, asked string
	for name, decision := range map[string]agent.Decision{
		"nothing": {},
		"two": {Outputs: []model.VariableValue{
			{Name: "a", Kind: model.VarString, Text: "x"},
			{Name: "b", Kind: model.VarString, Text: "y"},
		}},
	} {
		models := map[string]agent.Model{"anthropic_pb": choosingModel{
			provider: "anthropic_pb", routedTo: &routed, askedModel: &asked, answer: decision,
		}}
		if _, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
			"connector": "anthropic_pb", "prompt": "Frage", "resultVariable": "a",
		}), models); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}

// A job that arrived with no resolved payload names the reason: this server is not
// resolving ai tasks. That is a deployment answer an operator can act on, unlike a nil
// dereference.
func TestAnAiTaskWithNoPayloadSaysSo(t *testing.T) {
	if _, err := RunAiTask(context.Background(), Job{}, map[string]agent.Model{}); err == nil {
		t.Fatal("an unresolved ai task succeeded, want an error")
	}
}

// And a worker holding no model at all says that, rather than reporting it as a name it
// does not hold.
func TestAnAiTaskOnAWorkerWithNoModelSaysSo(t *testing.T) {
	_, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "anthropic_pb", "prompt": "Frage", "resultVariable": "a",
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("err = %v, want it to say this worker holds no model", err)
	}
}

// The same refusal when the task named no connector either: a worker with one provider
// serves it unambiguously, but if that provider cannot be asked for the model the task
// named, the diagnostic has to name *something* an operator can look up — so it says which
// connector was used, including the case where it was used by being the only one.
func TestAnUnnamedConnectorStillNamesItselfWhenItCannotChoose(t *testing.T) {
	var asked bool
	_, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"model": "claude-opus-5", "prompt": "Frage", "resultVariable": "a",
	}), map[string]agent.Model{"lokal": fixedModel{asked: &asked}})
	if err == nil {
		t.Fatal("a task naming a model the only adapter cannot serve succeeded")
	}
	if !strings.Contains(err.Error(), "only configured connector") {
		t.Errorf("err = %v, want it to say the worker's only connector was the one used", err)
	}
}

// A payload that lost the prompt or the result variable on the way fails before a provider
// is called. The compiler requires both, so this is the seam having lost something rather
// than an author's mistake — and asking a model an empty question costs a call to learn
// nothing.
func TestAnAiTaskWithAHalfPayloadNeverReachesTheProvider(t *testing.T) {
	var asked bool
	models := map[string]agent.Model{"lokal": fixedModel{asked: &asked}}
	for name, fields := range map[string]map[string]any{
		"no prompt": {"connector": "lokal", "resultVariable": "a"},
		"no result": {"connector": "lokal", "prompt": "Frage"},
	} {
		if _, err := RunAiTask(context.Background(), aiTaskJob(fields), models); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
	if asked {
		t.Error("a provider was called for a payload that was already incomplete")
	}
}

// The container's authored model reaches the adapter too. ADR-0256 put `model` on both
// hosts, and a round that carried it no further than the payload would leave an
// agent-driven container unable to choose — which is the half of the record that is easy
// to forget, because the ai task is the visible half.
func TestARoundAsksTheModelItsContainerNamed(t *testing.T) {
	var routed, asked string
	models := map[string]agent.Model{"anthropic_pb": choosingModel{
		provider: "anthropic_pb", model: "claude-opus-5",
		routedTo: &routed, askedModel: &asked,
		answer: agent.Decision{Outputs: []model.VariableValue{
			{Name: "antwort", Kind: model.VarString, Text: "fertig"},
		}},
	}}
	j := Job{Connector: &ConnectorPayload{Kind: "agent", Fields: map[string]any{
		"connector": "anthropic_pb", "model": "claude-haiku-4-5", "goal": "g", "round": 1,
		"tools": []any{map[string]any{"name": "t"}},
	}}}
	if _, err := RunAgentRound(context.Background(), j, models); err != nil {
		t.Fatalf("RunAgentRound: %v", err)
	}
	if asked != "claude-haiku-4-5" {
		t.Errorf("asked model %q, want the one the container authored", asked)
	}
}
