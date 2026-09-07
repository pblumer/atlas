package worker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/agent"
)

// agentEnvPrefix is where an agent worker's models live. It is the convention every
// credentialed kind uses (ADR-0050), and it is read here rather than in the engine for
// the reason ADR-0164 gives: a round is one model call, minutes long and able to hang,
// so it must never happen on the core loop. There is no in-process handler to offload —
// this kind only ever runs out here.
const agentEnvPrefix = "ATLAS_AGENT_"

// Protocol names for ATLAS_AGENT_<NAME>_PROTOCOL. A provider is a wire format and an
// endpoint, not a code path: which one a name speaks is deployment configuration.
const (
	agentProtocolMessages        = "messages"
	agentProtocolChatCompletions = "chat-completions"
)

// agentModelsFromEnv builds the models this worker can ask.
// ATLAS_AGENT_CONNECTORS lists the names; each name contributes its own configuration
// under ATLAS_AGENT_<NAME>_, and the name is what <atlas:agentConnector connector="…">
// in the process model refers to — so a container reaches the model it was modelled
// against, exactly as a business rule task reaches its named decision service.
//
//	ATLAS_AGENT_CONNECTORS=anthropic_pb,openai_pb
//	ATLAS_AGENT_ANTHROPIC_PB_API_KEY=…                       # required unless _ENDPOINT is set
//	ATLAS_AGENT_ANTHROPIC_PB_MODEL=claude-opus-5             # optional for messages
//	ATLAS_AGENT_OPENAI_PB_PROTOCOL=chat-completions
//	ATLAS_AGENT_OPENAI_PB_MODEL=gpt-4o                       # required for chat-completions
//	ATLAS_AGENT_OPENAI_PB_API_KEY=…
//
// and, for a gateway that does not behave quite like the provider it imitates:
// _ENDPOINT, _AUTH (x-api-key or bearer), _THINKING (off), _MAX_TOKENS,
// _ANSWER_VARIABLE.
//
// A key is required unless an endpoint is named, and the asymmetry is deliberate:
// pointing at a provider's public endpoint with no credential is certainly a
// misconfiguration, while a self-hosted or in-cluster endpoint may legitimately be
// open. That is temis's reasoning about its optional token, applied the other way
// round.
func agentModelsFromEnv(env func(string) string) (map[string]agent.Model, []string, error) {
	names := splitAndTrim(env(agentEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: no models and no error, which the caller
		// reports as a kind this worker does not serve. A worker told to serve agents
		// and holding no model would otherwise poll a queue it can never drain.
		return nil, nil, nil
	}
	models := make(map[string]agent.Model, len(names))
	for _, name := range names {
		key := agentEnvPrefix + envFold(name) + "_"
		var (
			protocol = strings.TrimSpace(env(key + "PROTOCOL"))
			endpoint = strings.TrimSpace(env(key + "ENDPOINT"))
			apiKey   = env(key + "API_KEY")
			modelID  = strings.TrimSpace(env(key + "MODEL"))
			auth     = strings.TrimSpace(env(key + "AUTH"))
			answer   = strings.TrimSpace(env(key + "ANSWER_VARIABLE"))
		)
		if apiKey == "" && endpoint == "" {
			return nil, nil, fmt.Errorf("worker: agent model %q has no credential: set %sAPI_KEY, "+
				"or %sENDPOINT if that endpoint needs none", name, key, key)
		}
		maxTokens, err := agentMaxTokens(env(key + "MAX_TOKENS"))
		if err != nil {
			return nil, nil, fmt.Errorf("worker: agent model %q: %s%v", name, key, err)
		}
		if err := agentAuthScheme(auth); err != nil {
			return nil, nil, fmt.Errorf("worker: agent model %q: %s%v", name, key, err)
		}
		switch protocol {
		case "", agentProtocolMessages:
			models[name] = &agent.HTTPModel{
				Endpoint: endpoint, APIKey: apiKey, Model: modelID,
				MaxTokens: maxTokens, AnswerVariable: answer, Auth: auth,
				Thinking: strings.TrimSpace(env(key + "THINKING")),
			}
		case agentProtocolChatCompletions:
			if modelID == "" {
				// There is no default here on purpose: which model an account may use
				// is not something Atlas can know, and a guess would surface as a 404
				// on the first round of a running process rather than at startup.
				return nil, nil, fmt.Errorf("worker: agent model %q speaks %s and names no model: set %sMODEL",
					name, agentProtocolChatCompletions, key)
			}
			models[name] = &agent.ChatCompletionsModel{
				Endpoint: endpoint, APIKey: apiKey, Model: modelID,
				MaxTokens: maxTokens, AnswerVariable: answer, Auth: auth,
			}
		default:
			return nil, nil, fmt.Errorf("worker: agent model %q speaks unknown protocol %q: use %q or %q",
				name, protocol, agentProtocolMessages, agentProtocolChatCompletions)
		}
	}
	return models, names, nil
}

func agentMaxTokens(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("MAX_TOKENS is %q, want a positive whole number", raw)
	}
	return n, nil
}

func agentAuthScheme(auth string) error {
	switch auth {
	case "", agent.AuthAPIKey, agent.AuthBearer:
		return nil
	}
	return fmt.Errorf("AUTH is %q, want %q or %q", auth, agent.AuthAPIKey, agent.AuthBearer)
}

// RunAgentRound decides one round of an agent-driven ad-hoc subprocess and reports the
// choice back as an [Outcome] (ADR-0253/ADR-0254).
//
// It is the second runner that completes with more than variables, and for a reason of
// the same shape as the first: a round's answer is usually a *choice of activities*,
// which variables cannot express. What it reports is an account, not a decision the
// engine takes on trust — the container is read from the lease, and every tool name is
// resolved against that container's compiled index before anything is activated. A
// worker cannot widen an agent's reach.
//
// It is exported for the same reason RunMailJob is: the environment is only the default
// place a worker's models come from, and a caller embedding this package may build them
// its own way.
func RunAgentRound(ctx context.Context, j Job, models map[string]agent.Model) (Outcome, error) {
	if j.Connector == nil {
		return Outcome{}, fmt.Errorf("agent: the job carried no resolved round; is this server resolving agent containers?")
	}
	round, err := agent.RoundFromPayload(j.Connector.Fields)
	if err != nil {
		return Outcome{}, err
	}
	m, err := agentModelFor(round.Connector, round.Model, models)
	if err != nil {
		return Outcome{}, err
	}
	decision, err := m.Decide(ctx, round.Request)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{}
	for _, call := range decision.ToolCalls {
		rep := ToolCallReport{Tool: call.Tool, CallId: call.CallId}
		if len(call.Arguments) > 0 {
			rep.Arguments = make(map[string]any, len(call.Arguments))
			for _, v := range call.Arguments {
				rep.Arguments[v.Name] = variableValue(v)
			}
		}
		out.ToolCalls = append(out.ToolCalls, rep)
	}
	if len(decision.Outputs) > 0 {
		out.Variables = make(map[string]any, len(decision.Outputs))
		for _, v := range decision.Outputs {
			out.Variables[v.Name] = variableValue(v)
		}
	}
	return out, nil
}

// agentModelFor picks the provider a round or task was modelled against, and then the
// language model it named.
//
// A model that names no provider is served by the only one this worker holds, because
// that is unambiguous and it is what a single-provider deployment looks like. With more
// than one it is not, and guessing would send an agent to an endpoint whose reach and
// cost the modeller never chose — so the job fails, naming what this worker actually
// holds.
//
// The language model is the second, finer choice (ADR-0256): naming none runs whatever
// the provider is configured for, which is how a deployment sets a house default. Naming
// one asks an adapter that cannot honour it to say so rather than answer from a model
// nobody chose — a wrong model is a wrong answer, and a wrong answer that looks right is
// the worst thing this package can produce.
func agentModelFor(provider, modelID string, models map[string]agent.Model) (agent.Model, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("agent: this worker holds no model")
	}
	m, err := agentProviderFor(provider, models)
	if err != nil {
		return nil, err
	}
	if modelID == "" {
		return m, nil
	}
	chooser, ok := m.(agent.ModelChooser)
	if !ok {
		return nil, fmt.Errorf("agent: the model asks for %q, but this worker's %s serves only the model it is configured for",
			modelID, agentProviderName(provider))
	}
	return chooser.ForModel(modelID), nil
}

// agentProviderFor is the first half of that choice: the named provider, or the only one.
func agentProviderFor(name string, models map[string]agent.Model) (agent.Model, error) {
	if name == "" {
		if len(models) == 1 {
			for _, m := range models {
				return m, nil
			}
		}
		return nil, fmt.Errorf("agent: the model names no connector and this worker holds %s",
			agentModelNames(models))
	}
	m, ok := models[name]
	if !ok {
		return nil, fmt.Errorf("agent: this worker holds no model named %q; it holds %s",
			name, agentModelNames(models))
	}
	return m, nil
}

// agentProviderName names the provider in a diagnostic, including the case where the
// model named none and the worker's single one was used.
func agentProviderName(name string) string {
	if name == "" {
		return "only configured connector"
	}
	return "connector " + strconv.Quote(name)
}

// RunAiTask works one ai task: one call to a language model, the answer into the variable
// the model named (ADR-0256).
//
// It shares everything below the question with RunAgentRound — the same provider map, the
// same adapters, the same wire — because a one-shot call is a round offered no tools, and
// an adapter with nothing to call answers in words.
//
// The one thing it does not share is where the answer goes. A round answers into whatever
// variable this worker is configured for; a task answers into the variable the *model*
// named, which is the point of the task. So the single output is renamed here, and a
// decision that came back as a tool call is refused: an ai task offered no tools, and a
// model that called one anyway has answered a question nobody asked.
func RunAiTask(ctx context.Context, j Job, models map[string]agent.Model) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("agent: the job carried no resolved ai task; is this server resolving ai tasks?")
	}
	task, err := agent.TaskFromPayload(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	m, err := agentModelFor(task.Connector, task.Model, models)
	if err != nil {
		return nil, err
	}
	decision, err := m.Decide(ctx, agent.TaskRequest(task))
	if err != nil {
		return nil, err
	}
	if len(decision.ToolCalls) > 0 {
		return nil, fmt.Errorf("agent: the model called %d tool(s) for an ai task, which offers none",
			len(decision.ToolCalls))
	}
	if len(decision.Outputs) != 1 {
		// The adapters answer with exactly one output or fail (answerDecision), so this
		// is a broken adapter rather than a bad answer — and renaming the wrong one of
		// several would put text under a name that promises something else.
		return nil, fmt.Errorf("agent: the model answered with %d values, want exactly one", len(decision.Outputs))
	}
	return map[string]any{task.ResultVariable: variableValue(decision.Outputs[0])}, nil
}

func agentModelNames(models map[string]agent.Model) string {
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, strconv.Quote(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
