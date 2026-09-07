package agent

import (
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Round is a resolved round as it travels to a worker: what the model is asked, which of
// the worker's configured providers to ask it through, and which language model to ask.
//
// Neither name is content — neither reaches a model as part of the question — which is
// why both sit beside the [Request] rather than in it. Connector is the `connector` of
// <atlas:agentConnector>, the same way a business rule task names its decision service,
// and it is what lets one worker hold an Anthropic endpoint and an OpenAI one and send
// each container to the provider it was modelled against. Model is that element's
// `model`, empty when the container named none, in which case the provider's own
// configured model runs (ADR-0256).
type Round struct {
	Request
	Connector string
	Model     string
}

// Resolve turns a parked agent round into everything the decision needs, with nothing
// of the engine left in it (ADR-0254).
//
// It is the division ADR-0168 draws, applied to a round. The toolbox lives in the
// compiled process — element ids, the modeler's <bpmn:documentation>, the declared
// parameters — and what the earlier rounds' calls returned lives in the container's
// scope. Neither is anything a worker has. What the worker has is the model endpoint
// and the credential behind it, and neither of those travels.
//
// The [Request] it carries is the whole of what the model is put — a resolved round
// *is* the request, and a second struct with the same five fields would only give the
// two a way to drift apart.
//
// Both halves call it. The in-process handler resolves and decides in one go; a worker
// leasing the round gets the same values as its job payload — so what a round means is
// decided here, once, rather than twice in step.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, ei *model.ElementInstanceValue,
	elementInstanceKey uint64) (Round, error) {
	tools, err := Toolbox(cp, ei.ElementId)
	if err != nil {
		// Not an agent-driven container: an error rather than an empty round, because
		// an empty round is a thing an agent could legitimately be given, and this is
		// not that — it is a job that should never have been resolved here.
		return Round{}, err
	}
	node := cp.Node(ei.ElementId)
	d := cp.AdHoc(node.Detail)
	results := collectedResults(store, elementInstanceKey)
	return Round{
		Request: Request{
			// The container's own documentation is what this agent is for. The modeler
			// writes it for the next human; it is the same sentence the model reads.
			Goal:    cp.ElementDocumentation(ei.ElementId),
			Context: contextValues(store, cp, d.AgentContext, elementInstanceKey),
			Tools:   tools,
			Results: results,
			Round:   len(results) + 1,
		},
		Connector: cp.Intern(d.AgentWorker),
		Model:     cp.Intern(d.AgentModel),
	}, nil
}

// contextNotSet is what a named variable that is not there travels as. It is sent rather
// than dropped because "the process meant to tell you this and had nothing" is a different
// fact from "you were never told", and only the first one lets an agent say so instead of
// inventing a value (ADR-draft-what-an-agent-may-read).
const contextNotSet = "(not set)"

// contextValues reads the process variables this agent was given by name, up the
// container's scope chain, nearest scope winning (ADR-0068) — the same walk every other
// authored value takes.
//
// A name is authored on the element, so the set is one a reviewer chose and can read off
// the diagram; this function's whole job is to look up that list and nothing wider. An
// agent that names none gets nil, which the prompt renders as no context section at all.
func contextValues(store state.Reader, cp *compiler.CompiledProcess, names []int32, containerKey uint64) map[string]string {
	if len(names) == 0 {
		return nil
	}
	scope, err := state.VisibleVariablesMap(store, containerKey)
	if err != nil {
		// The round is still worth putting: an agent told nothing is the behaviour this
		// record replaces, not a new failure, and failing the job here would park a
		// container over a read the next round would retry anyway.
		scope = nil
	}
	out := make(map[string]string, len(names))
	for _, idx := range names {
		name := cp.Intern(idx)
		if name == "" {
			continue
		}
		v, ok := scope[name]
		if !ok {
			out[name] = contextNotSet
			continue
		}
		out[name] = contextText(v)
	}
	return out
}

// contextText is a stored variable in the form a model reads: its string form, which is
// what the prompt renders and what a JSON variable is anyway.
//
// A boolean needs its own arm because Classify hands it back in the bool slot with an
// empty text — right for a caller deciding a branch, and an empty string for a caller
// writing a sentence, which is what this one is doing.
func contextText(v model.VariableValue) string {
	kind, b, text := expr.Classify(expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text))
	if kind == expr.KindBool {
		if b {
			return "true"
		}
		return "false"
	}
	return text
}

// collectedResults reads what this container's earlier tool calls returned — the whole
// of the agent's memory of its own run, because the worker holds nothing between rounds.
// Empty on the first round, and empty when the model named no result collection, which
// is the honest answer in both cases: nothing has come back yet.
func collectedResults(store state.Reader, containerKey uint64) []string {
	var out []string
	_ = state.VisibleVariables(store, containerKey, func(v *model.VariableValue) error {
		if v.Name == resultsVariable && v.Text != "" {
			out = append(out, v.Text)
		}
		return nil
	})
	return out
}

// ResolveJobPayload is the resolved round as the flat map a leased job's payload carries.
// It exists so the API's lease path states the field names once, beside every other
// kind's arm, rather than reaching into Round's shape.
func ResolveJobPayload(r Round) map[string]any {
	return map[string]any{
		"connector": r.Connector,
		"model":     r.Model,
		"goal":      r.Goal,
		"context":   r.Context,
		"tools":     r.Tools,
		"results":   r.Results,
		"round":     r.Round,
	}
}

// RoundFromPayload rebuilds a round from what travelled, for a worker that leased it
// rather than one running in the engine. It is the mirror of ResolveJobPayload and the
// reason the payload's field names live in one file: a worker and the engine cannot
// disagree about them without this function failing loudly in a test.
func RoundFromPayload(fields map[string]any) (Round, error) {
	r := Round{}
	r.Connector, _ = fields["connector"].(string)
	r.Model, _ = fields["model"].(string)
	if v, ok := fields["goal"].(string); ok {
		r.Goal = v
	}
	switch v := fields["round"].(type) {
	case int:
		r.Round = v
	case float64: // a round that came back over JSON
		r.Round = int(v)
	}
	if r.Round < 1 {
		return Round{}, fmt.Errorf("agent: payload names no round")
	}
	r.Context = stringMap(fields["context"])
	r.Results = stringSlice(fields["results"])
	tools, err := toolsFromPayload(fields["tools"])
	if err != nil {
		return Round{}, err
	}
	r.Tools = tools
	return r, nil
}

// stringMap reads a payload's context back, in either shape it can arrive in.
//
// A round that travelled as JSON arrives as map[string]any, which is the case this was
// written for. A round handed straight across in memory still carries the map[string]string
// ResolveJobPayload put there — and reading only the first shape made these two functions,
// documented as mirrors of each other, agree only after a trip through a serializer. That
// is the kind of seam that holds until the day something calls them directly.
func stringMap(v any) map[string]string {
	switch raw := v.(type) {
	case map[string]string:
		if len(raw) == 0 {
			return nil
		}
		out := make(map[string]string, len(raw))
		for k, val := range raw {
			out[k] = val
		}
		return out
	case map[string]any:
		if len(raw) == 0 {
			return nil
		}
		out := make(map[string]string, len(raw))
		for k, val := range raw {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	return nil
}

func stringSlice(v any) []string {
	switch raw := v.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// toolsFromPayload accepts the tools either as they were built (the in-process path
// hands the slice straight over) or as they arrive over JSON.
func toolsFromPayload(v any) ([]Tool, error) {
	switch raw := v.(type) {
	case nil:
		return nil, fmt.Errorf("agent: payload carries no tools")
	case []Tool:
		if len(raw) == 0 {
			return nil, fmt.Errorf("agent: payload carries no tools")
		}
		return raw, nil
	case []any:
		out := make([]Tool, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("agent: a tool in the payload is not an object")
			}
			tool := Tool{}
			tool.Name, _ = m["name"].(string)
			tool.Description, _ = m["description"].(string)
			if tool.Name == "" {
				return nil, fmt.Errorf("agent: a tool in the payload has no name")
			}
			params, _ := m["params"].([]any)
			for _, p := range params {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				param := Param{}
				param.Name, _ = pm["name"].(string)
				param.Type, _ = pm["type"].(string)
				param.Description, _ = pm["description"].(string)
				param.Required, _ = pm["required"].(bool)
				tool.Params = append(tool.Params, param)
			}
			out = append(out, tool)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("agent: payload carries no tools")
		}
		return out, nil
	}
	return nil, fmt.Errorf("agent: payload's tools are of an unexpected shape %T", v)
}
