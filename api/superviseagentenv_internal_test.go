package api

import (
	"strings"
	"testing"
)

// What an agent model's record holds, and what a supervised agent worker is handed at
// spawn (ADR-draft-agent-models-are-console-workers).
//
// The agent is the first Worker Type whose configuration has a part that is neither an
// endpoint nor a credential nor derivable from either — the model name — and the record
// exists so that part is visible and changeable in the Console instead of buried in a
// vault bundle or an environment variable on the engine.

// The payoff: an operator adds a model in the Console and the supervised worker can ask
// it, having been told nothing by hand.
func TestASupervisedAgentWorkerIsHandedTheModelsFromTheStore(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("anthropic-key", "sk-ant-test"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	_ = srv.connectors.Save(connector{
		ID: "1", Name: "anthropic_pb", Kind: connectorKindAgent, Enabled: true, CreatedAt: 1,
		CredentialsRef: "anthropic-key", Provider: agentProtocolMessages, Model: "claude-opus-5",
	})

	env := envOf(t, srv.agentWorkerEnv())
	if got := env["ATLAS_AGENT_ANTHROPIC_PB_API_KEY"]; got != "sk-ant-test" {
		t.Errorf("API key = %q, want the value out of the vault", got)
	}
	if got := env["ATLAS_AGENT_ANTHROPIC_PB_MODEL"]; got != "claude-opus-5" {
		t.Errorf("model = %q, want what the record names", got)
	}
	if got := env["ATLAS_AGENT_CONNECTORS"]; got != "anthropic_pb" {
		t.Errorf("ATLAS_AGENT_CONNECTORS = %q, want the model's name", got)
	}
	// Messages is the default, so nothing is rendered for it: a supervised worker's
	// environment is as short as a hand-written one would be.
	if _, ok := env["ATLAS_AGENT_ANTHROPIC_PB_PROTOCOL"]; ok {
		t.Errorf("environment = %v, want no PROTOCOL for the default wire format", env)
	}
	if _, ok := env["ATLAS_AGENT_ANTHROPIC_PB_ENDPOINT"]; ok {
		t.Errorf("environment = %v, want no ENDPOINT when the record names none", env)
	}
}

// A second wire format is rendered, because it is not the default — and its endpoint
// with it, when the operator overrode one.
func TestASupervisedAgentWorkerIsToldANonDefaultWireFormat(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("openai-key", "sk-openai"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	_ = srv.connectors.Save(connector{
		ID: "1", Name: "openai_pb", Kind: connectorKindAgent, Enabled: true, CreatedAt: 1,
		CredentialsRef: "openai-key", Provider: agentProtocolChatCompletions,
		Model: "gpt-4o", Endpoint: "https://openrouter.ai/api/v1/chat/completions",
	})

	env := envOf(t, srv.agentWorkerEnv())
	if got := env["ATLAS_AGENT_OPENAI_PB_PROTOCOL"]; got != agentProtocolChatCompletions {
		t.Errorf("protocol = %q, want the non-default wire format", got)
	}
	if got := env["ATLAS_AGENT_OPENAI_PB_ENDPOINT"]; got != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("endpoint = %q, want the gateway the operator named", got)
	}
	if got := env["ATLAS_AGENT_OPENAI_PB_MODEL"]; got != "gpt-4o" {
		t.Errorf("model = %q, want what the record names", got)
	}
}

// A disabled model is not handed over, and a record with neither a key nor an endpoint
// is left out rather than passed half-filled — the worker then starts without it and the
// Console shows it as configured-not-working, instead of a token failing mid-run.
func TestAnUnusableAgentModelIsNotHandedToTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("k", "sk"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	_ = srv.connectors.Save(connector{
		ID: "1", Name: "off", Kind: connectorKindAgent, Enabled: false, CreatedAt: 1,
		CredentialsRef: "k", Model: "claude-opus-5",
	})
	_ = srv.connectors.Save(connector{
		ID: "2", Name: "nothing", Kind: connectorKindAgent, Enabled: true, CreatedAt: 2,
		Model: "claude-opus-5", // no key, no endpoint
	})
	_ = srv.connectors.Save(connector{
		ID: "3", Name: "rules", Kind: connectorKindTemis, Enabled: true, CreatedAt: 3, Endpoint: "http://x",
	})

	if env := srv.agentWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing: no usable agent model is configured", env)
	}
}

// A self-hosted endpoint may legitimately need no key, and is handed over. It is the
// asymmetry the worker itself draws: a public default with no credential is certainly
// wrong, a named endpoint with none may well be right.
func TestAnAgentEndpointWithoutAKeyIsStillHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{
		ID: "1", Name: "local", Kind: connectorKindAgent, Enabled: true, CreatedAt: 1,
		Endpoint: "http://llm.internal:8080/v1/messages",
	})

	env := envOf(t, srv.agentWorkerEnv())
	if got := env["ATLAS_AGENT_LOCAL_ENDPOINT"]; got != "http://llm.internal:8080/v1/messages" {
		t.Errorf("endpoint = %q, want the self-hosted endpoint", got)
	}
	if _, ok := env["ATLAS_AGENT_LOCAL_API_KEY"]; ok {
		t.Errorf("environment = %v, want no API_KEY: a blank one reads as a configured blank key", env)
	}
}

// The record's rules, refused where the operator is looking rather than at the worker's
// startup — a record the Console accepts and the child then rejects moves that discovery
// to a log nobody is reading.
func TestAnAgentRecordIsValidatedTheWayTheWorkerWouldBe(t *testing.T) {
	for name, tc := range map[string]struct {
		in   createConnectorParams
		want string // "" means the record is accepted
	}{
		"a key and the default wire format": {
			in: createConnectorParams{CredentialsRef: "k"},
		},
		"an endpoint and no key": {
			in: createConnectorParams{Endpoint: "http://llm.internal"},
		},
		"neither a key nor an endpoint": {
			in: createConnectorParams{}, want: "credentialsRef",
		},
		"chat-completions naming no model": {
			in:   createConnectorParams{CredentialsRef: "k", Provider: agentProtocolChatCompletions},
			want: "must name its model",
		},
		"chat-completions naming one": {
			in: createConnectorParams{CredentialsRef: "k", Provider: agentProtocolChatCompletions, Model: "gpt-4o"},
		},
		"a wire format nothing speaks": {
			in: createConnectorParams{CredentialsRef: "k", Provider: "responses"}, want: "neither",
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := tc.in
			got := validateAgentConnector(&p)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("validateAgentConnector = %q, want the record accepted", got)
				}
				// An accepted record always names a wire format, so nothing downstream
				// has to re-derive the default.
				if p.Provider == "" {
					t.Errorf("provider = %q, want the default filled in", p.Provider)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("validateAgentConnector = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// And on an edit, not only on a create: switching an existing model to chat-completions
// makes its model name required, and accepting that edit would leave a Worker the
// supervised child refuses at startup.
func TestEditingAnAgentRecordIsHeldToTheSameRules(t *testing.T) {
	rec := connector{
		Name: "m", Kind: connectorKindAgent, CredentialsRef: "k",
		Provider: agentProtocolChatCompletions, // and no model
	}
	if msg := normalizeConnectorUpdate(&rec); msg == "" {
		t.Fatal("an agent edit that names no model was accepted")
	}
	rec.Model = "gpt-4o"
	if msg := normalizeConnectorUpdate(&rec); msg != "" {
		t.Errorf("normalizeConnectorUpdate = %q, want the completed record accepted", msg)
	}
}
