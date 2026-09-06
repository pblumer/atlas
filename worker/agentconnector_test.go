package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/agent"
)

// ADR-0254 phase 3: the agent Worker Type registers here and nowhere else.
//
// It is the only kind with no in-process form at all. Every other arm of
// BuiltinConnectors moves work off the loop that Atlas *could* run itself; this one was
// never allowed on it, because a round is one model call — minutes long, able to hang —
// which is the clearest case ADR-0164 has. So what these tests guard is that the kind
// is usable out here and that a misconfiguration is found at startup, while an operator
// is still watching, rather than by a process parked on a round nobody can decide.

// A worker told to serve agents but holding no model says so, rather than subscribing
// to a queue it can never drain. Agent containers then wait for a worker that *can*
// decide them — the same answer mail and temis give, and the reason it is not an error
// is the same: this worker very likely serves other kinds too.
func TestAnAgentWorkerWithNoModelReportsItselfUnconfigured(t *testing.T) {
	built, err := BuiltinConnectors(envMap(nil), "agent")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(built.Handlers) != 0 {
		t.Errorf("handlers = %v, want none: a worker holding no model must not lease rounds it cannot decide", built.Handlers)
	}
	if len(built.Unconfigured) != 1 || built.Unconfigured[0] != "agent" {
		t.Errorf("Unconfigured = %v, want [agent] so the operator is told which kind is idle", built.Unconfigured)
	}
}

// The kind registers under the agent job type and its configured names are reported, so
// the Workers view shows what this worker can actually be asked for.
func TestAnAgentWorkerRegistersItsModels(t *testing.T) {
	built, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_AGENT_CONNECTORS":           "anthropic_pb,openai_pb",
		"ATLAS_AGENT_ANTHROPIC_PB_API_KEY": "sk-ant",
		"ATLAS_AGENT_OPENAI_PB_PROTOCOL":   "chat-completions",
		"ATLAS_AGENT_OPENAI_PB_MODEL":      "gpt-4o",
		"ATLAS_AGENT_OPENAI_PB_API_KEY":    "sk-openai",
	}), "agent")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.AgentJobType]; !ok {
		t.Fatalf("handlers = %v, want one under %s", built.Handlers, compiler.AgentJobType)
	}
	if len(built.Handlers) != 1 {
		t.Errorf("handlers = %v, want only the agent job type", built.Handlers)
	}
	if len(built.Names) != 2 {
		t.Errorf("names = %v, want both configured models on the Workers view", built.Names)
	}
}

// Two providers from one worker, which is the point of naming them: a container
// modelled against Anthropic and one modelled against OpenAI are both served here, each
// speaking its own wire format.
func TestAWorkerHoldsBothProviders(t *testing.T) {
	models, _, err := agentModelsFromEnv(envMap(map[string]string{
		"ATLAS_AGENT_CONNECTORS":           "anthropic_pb,openai_pb",
		"ATLAS_AGENT_ANTHROPIC_PB_API_KEY": "sk-ant",
		"ATLAS_AGENT_OPENAI_PB_PROTOCOL":   "chat-completions",
		"ATLAS_AGENT_OPENAI_PB_MODEL":      "gpt-4o",
		"ATLAS_AGENT_OPENAI_PB_API_KEY":    "sk-openai",
	}))
	if err != nil {
		t.Fatalf("agentModelsFromEnv: %v", err)
	}
	if _, ok := models["anthropic_pb"].(*agent.HTTPModel); !ok {
		t.Errorf("anthropic_pb = %T, want the Messages adapter (the default protocol)", models["anthropic_pb"])
	}
	if _, ok := models["openai_pb"].(*agent.ChatCompletionsModel); !ok {
		t.Errorf("openai_pb = %T, want the Chat-Completions adapter", models["openai_pb"])
	}
}

// A gateway that speaks the Messages format but authenticates its own way — the case
// the two knobs exist for. Configuration, not a third adapter.
func TestAMessagesGatewayCanBeConfiguredWithABearerTokenAndNoThinking(t *testing.T) {
	models, _, err := agentModelsFromEnv(envMap(map[string]string{
		"ATLAS_AGENT_CONNECTORS":        "router",
		"ATLAS_AGENT_ROUTER_ENDPOINT":   "https://openrouter.ai/api/v1/messages",
		"ATLAS_AGENT_ROUTER_API_KEY":    "sk-router",
		"ATLAS_AGENT_ROUTER_MODEL":      "anthropic/claude-opus-5",
		"ATLAS_AGENT_ROUTER_AUTH":       "bearer",
		"ATLAS_AGENT_ROUTER_THINKING":   "off",
		"ATLAS_AGENT_ROUTER_MAX_TOKENS": "8192",
	}))
	if err != nil {
		t.Fatalf("agentModelsFromEnv: %v", err)
	}
	m, ok := models["router"].(*agent.HTTPModel)
	if !ok {
		t.Fatalf("router = %T, want the Messages adapter", models["router"])
	}
	if m.Auth != agent.AuthBearer || m.Thinking != agent.ThinkingOff {
		t.Errorf("model = %+v, want the bearer scheme and thinking off", m)
	}
	if m.Endpoint != "https://openrouter.ai/api/v1/messages" || m.MaxTokens != 8192 {
		t.Errorf("model = %+v, want the configured endpoint and cap", m)
	}
}

// Every misconfiguration is found here, at startup, while the operator is still
// watching. Discovering any of these per job would spend a retry budget learning what
// was knowable before the first poll.
func TestAgentMisconfigurationIsRefusedAtStartup(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"a name with no credential and no endpoint": {
			env:  map[string]string{"ATLAS_AGENT_CONNECTORS": "a"},
			want: "API_KEY",
		},
		"chat-completions naming no model": {
			env: map[string]string{
				"ATLAS_AGENT_CONNECTORS": "a",
				"ATLAS_AGENT_A_API_KEY":  "sk",
				"ATLAS_AGENT_A_PROTOCOL": "chat-completions",
			},
			want: "MODEL",
		},
		"an unknown protocol": {
			env: map[string]string{
				"ATLAS_AGENT_CONNECTORS": "a",
				"ATLAS_AGENT_A_API_KEY":  "sk",
				"ATLAS_AGENT_A_PROTOCOL": "responses",
			},
			want: "unknown protocol",
		},
		"an unknown auth scheme": {
			env: map[string]string{
				"ATLAS_AGENT_CONNECTORS": "a",
				"ATLAS_AGENT_A_API_KEY":  "sk",
				"ATLAS_AGENT_A_AUTH":     "basic",
			},
			want: "AUTH",
		},
		"a token cap that is not a number": {
			env: map[string]string{
				"ATLAS_AGENT_CONNECTORS":   "a",
				"ATLAS_AGENT_A_API_KEY":    "sk",
				"ATLAS_AGENT_A_MAX_TOKENS": "viele",
			},
			want: "MAX_TOKENS",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := agentModelsFromEnv(envMap(tc.env))
			if err == nil {
				t.Fatalf("agentModelsFromEnv succeeded, want a startup error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q so an operator knows what to set", err, tc.want)
			}
		})
	}
}

// An endpoint that needs no credential is served: a self-hosted or in-cluster model has
// no key to give, and demanding one would refuse a deployment that works. The asymmetry
// with the case above is the point — a *public* default with no key is certainly wrong,
// a named endpoint with no key may well be right.
func TestAnEndpointWithoutACredentialIsAllowed(t *testing.T) {
	models, _, err := agentModelsFromEnv(envMap(map[string]string{
		"ATLAS_AGENT_CONNECTORS":     "local",
		"ATLAS_AGENT_LOCAL_ENDPOINT": "http://llm.internal:8080/v1/messages",
	}))
	if err != nil {
		t.Fatalf("agentModelsFromEnv: %v", err)
	}
	if len(models) != 1 {
		t.Errorf("models = %v, want the one self-hosted endpoint", models)
	}
}

// scriptedModel answers a round from a script, so routing can be tested without a
// wire format in the way.
type scriptedModel struct {
	name   string
	asked  *string
	answer agent.Decision
}

func (s scriptedModel) Decide(_ context.Context, _ agent.Request) (agent.Decision, error) {
	*s.asked = s.name
	return s.answer, nil
}

// The round goes to the model its container was modelled against. This is what the
// name on <atlas:agentConnector> is *for*: with two models configured, sending a
// container to the wrong one would change the agent's reach and its cost, neither of
// which the modeller chose.
func TestARoundGoesToTheModelItWasModelledAgainst(t *testing.T) {
	var asked string
	models := map[string]agent.Model{
		"anthropic_pb": scriptedModel{name: "anthropic_pb", asked: &asked},
		"openai_pb":    scriptedModel{name: "openai_pb", asked: &asked},
	}
	j := Job{Connector: &ConnectorPayload{Kind: "agent", Fields: map[string]any{
		"connector": "openai_pb", "goal": "g", "round": 1,
		"tools": []any{map[string]any{"name": "t"}},
	}}}

	if _, err := RunAgentRound(context.Background(), j, models); err != nil {
		t.Fatalf("RunAgentRound: %v", err)
	}
	if asked != "openai_pb" {
		t.Errorf("asked %q, want the model the container names", asked)
	}
}

// And when it names one this worker does not hold, the round fails naming what the
// worker *does* hold — an operator reading the incident can tell a typo from a worker
// pointed at the wrong deployment.
func TestARoundNamingAnUnheldModelSaysWhatIsHeld(t *testing.T) {
	var asked string
	models := map[string]agent.Model{"anthropic_pb": scriptedModel{name: "anthropic_pb", asked: &asked}}
	j := Job{Connector: &ConnectorPayload{Kind: "agent", Fields: map[string]any{
		"connector": "openai_pb", "goal": "g", "round": 1,
		"tools": []any{map[string]any{"name": "t"}},
	}}}

	_, err := RunAgentRound(context.Background(), j, models)
	if err == nil {
		t.Fatal("a round naming a model this worker does not hold succeeded")
	}
	for _, want := range []string{"openai_pb", "anthropic_pb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to name %q", err, want)
		}
	}
	if asked != "" {
		t.Errorf("asked %q, want no model asked at all", asked)
	}
}

// A container naming nothing is served by the only model a worker holds, because that
// is unambiguous and it is what a single-model deployment looks like. With two it is
// not, and guessing is exactly the thing the name exists to prevent.
func TestAnUnnamedRoundNeedsAnUnambiguousWorker(t *testing.T) {
	var asked string
	fields := map[string]any{"goal": "g", "round": 1, "tools": []any{map[string]any{"name": "t"}}}
	j := Job{Connector: &ConnectorPayload{Kind: "agent", Fields: fields}}

	one := map[string]agent.Model{"only": scriptedModel{name: "only", asked: &asked}}
	if _, err := RunAgentRound(context.Background(), j, one); err != nil {
		t.Fatalf("RunAgentRound with one model: %v", err)
	}
	if asked != "only" {
		t.Errorf("asked %q, want the worker's only model", asked)
	}

	two := map[string]agent.Model{
		"a": scriptedModel{name: "a", asked: &asked},
		"b": scriptedModel{name: "b", asked: &asked},
	}
	if _, err := RunAgentRound(context.Background(), j, two); err == nil {
		t.Error("an unnamed round picked one of two models; which one an agent runs on must not be map order")
	}
}

// A round leased from a server that does not resolve agent containers carries no
// payload at all. Saying which side is missing is what turns a silent park into
// something an operator can fix.
func TestAnUnresolvedRoundSaysSo(t *testing.T) {
	_, err := RunAgentRound(context.Background(), Job{}, map[string]agent.Model{
		"a": scriptedModel{name: "a", asked: new(string)},
	})
	if err == nil || !strings.Contains(err.Error(), "resolved") {
		t.Errorf("err = %v, want it to say the round arrived unresolved", err)
	}
}
