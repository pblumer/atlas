package agent

import (
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Resolve turns a parked agent round into everything the decision needs, with nothing
// of the engine left in it (ADR-draft-agent-rounds-on-a-worker).
//
// It is the division ADR-0168 draws, applied to a round. The toolbox lives in the
// compiled process — element ids, the modeler's <bpmn:documentation>, the declared
// parameters — and what the earlier rounds' calls returned lives in the container's
// scope. Neither is anything a worker has. What the worker has is the model endpoint
// and the credential behind it, and neither of those travels.
//
// It returns a [Request] rather than a type of its own: a resolved round *is* the
// request put to the model, and a second struct with the same five fields would only
// give the two a way to drift apart.
//
// Both halves call it. The in-process handler resolves and decides in one go; a worker
// leasing the round gets the same values as its job payload — so what a round means is
// decided here, once, rather than twice in step.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, ei *model.ElementInstanceValue,
	elementInstanceKey uint64) (Request, error) {
	tools, err := Toolbox(cp, ei.ElementId)
	if err != nil {
		// Not an agent-driven container: an error rather than an empty round, because
		// an empty round is a thing an agent could legitimately be given, and this is
		// not that — it is a job that should never have been resolved here.
		return Request{}, err
	}
	results := collectedResults(store, elementInstanceKey)
	return Request{
		// The container's own documentation is what this agent is for. The modeler
		// writes it for the next human; it is the same sentence the model reads.
		Goal:    cp.ElementDocumentation(ei.ElementId),
		Tools:   tools,
		Results: results,
		Round:   len(results) + 1,
	}, nil
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
// kind's arm, rather than reaching into Request's shape.
func ResolveJobPayload(r Request) map[string]any {
	return map[string]any{
		"goal":    r.Goal,
		"context": r.Context,
		"tools":   r.Tools,
		"results": r.Results,
		"round":   r.Round,
	}
}

// RequestFromPayload rebuilds a round from what travelled, for a worker that leased it
// rather than one running in the engine. It is the mirror of ResolveJobPayload and the
// reason the payload's field names live in one file: a worker and the engine cannot
// disagree about them without this function failing loudly in a test.
func RequestFromPayload(fields map[string]any) (Request, error) {
	req := Request{}
	if v, ok := fields["goal"].(string); ok {
		req.Goal = v
	}
	switch v := fields["round"].(type) {
	case int:
		req.Round = v
	case float64: // a round that came back over JSON
		req.Round = int(v)
	}
	if req.Round < 1 {
		return Request{}, fmt.Errorf("agent: payload names no round")
	}
	req.Context = stringMap(fields["context"])
	req.Results = stringSlice(fields["results"])
	tools, err := toolsFromPayload(fields["tools"])
	if err != nil {
		return Request{}, err
	}
	req.Tools = tools
	return req, nil
}

func stringMap(v any) map[string]string {
	raw, ok := v.(map[string]any)
	if !ok || len(raw) == 0 {
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
