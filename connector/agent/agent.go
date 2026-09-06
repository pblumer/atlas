// Package agent is the Worker Type behind an agent-driven ad-hoc subprocess
// (ADR-0253): it works the round job the container parks on, asks a model which of the
// container's tools to run next, and hands that choice back as the job's completion.
//
// No model and no provider SDK lives here. ADR-0117 made that a decision rather than an
// omission — Atlas is a workflow engine, not an AI product — so this package talks to a
// [Model], an interface one call wide, and the process that satisfies it holds the
// endpoint and the credential. The engine's own dependency list stays free of a vendor,
// and the next model is a Worker configuration rather than a fork.
//
// What the model is offered is not this package's invention either: the tools are the
// contained activities the compiler indexed (ADR-0253 phase 1), their names are the
// element ids, and their descriptions are the modeler's own <bpmn:documentation>. This
// package only translates that index into the shape a model expects.
package agent

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// Tool is one contained activity as the model sees it. The name is the activity's BPMN
// id — what the model must name to call it, and what the engine resolves the choice
// against — and Description is the activity's documentation, which is what the model
// actually reads to decide whether this is the tool for the job.
type Tool struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
}

// Param is one declared input of a tool: the shape of <atlas:agentParam>, carried to the
// model so it knows what it has to supply.
type Param struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// Request is one round put to the model: what the container is for, which tools it may
// call, and what the calls it already made returned. Results grows by one entry per tool
// call as the rounds go by — it is the agent's memory of its own run, and the reason a
// round can build on the one before it.
type Request struct {
	Goal    string            `json:"goal"`
	Context map[string]string `json:"context,omitempty"`
	Tools   []Tool            `json:"tools"`
	Results []string          `json:"results,omitempty"`
	Round   int               `json:"round"`
}

// Decision is what the model answered. Exactly one of the two is the answer: tool calls
// mean another round, and an empty ToolCalls with Outputs (or with nothing) means the run
// is finished — which is why the zero value is a valid ending rather than a failure.
type Decision struct {
	ToolCalls []model.ToolCall
	Outputs   []model.VariableValue
}

// Model is the whole of this package's dependency on inference: one round in, one
// decision out. An implementation holds the endpoint and the credential; the tests here
// hold a script.
type Model interface {
	Decide(ctx context.Context, req Request) (Decision, error)
}

// Toolbox translates a compiled agent-driven container's tool index into what the model
// is offered. It is deliberately a pure function of the compiled process: the same index
// the runtime activates from is the one the model chooses from, which is what keeps the
// two from ever disagreeing about what exists.
//
// A tool with no documentation is offered anyway, with an empty description — the
// compiler already warns about it (RuleAgentTool), and refusing to run a model on a
// half-documented process would be a worse answer than running it badly.
func Toolbox(cp *compiler.CompiledProcess, containerElementId int32) ([]Tool, error) {
	node := cp.Node(containerElementId)
	if node.Type != compiler.TypeAdHocSubProcess {
		return nil, fmt.Errorf("agent: element %q is not an ad-hoc subprocess", cp.ElementBpmnId(containerElementId))
	}
	d := cp.AdHoc(node.Detail)
	if !d.AgentDriven {
		return nil, fmt.Errorf("agent: ad-hoc subprocess %q is not agent-driven", cp.ElementBpmnId(containerElementId))
	}
	tools := make([]Tool, 0, len(d.Tools))
	for _, t := range d.Tools {
		tool := Tool{
			Name:        cp.ElementBpmnId(t.Element),
			Description: cp.ElementDocumentation(t.Element),
			Params:      make([]Param, 0, len(t.Params)),
		}
		for _, p := range t.Params {
			tool.Params = append(tool.Params, Param{
				Name:        cp.Intern(p.Name),
				Type:        cp.Intern(p.Type),
				Description: cp.Intern(p.Description),
				Required:    p.Required,
			})
		}
		tools = append(tools, tool)
	}
	return tools, nil
}
