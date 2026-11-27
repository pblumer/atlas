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
	"time"

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
	// Outputs are the fields the answer must carry; empty means free text
	// (ADR-0567).
	Outputs []Output `json:"outputs,omitempty"`
	// History is the run's earlier rounds as they happened, and Changed the context
	// values the model has not been told yet. Both are set only when every earlier
	// round is on the record whole; without them a round is put the old way, from
	// Results (ADR-0567).
	History []PastRound       `json:"history,omitempty"`
	Changed map[string]string `json:"changed,omitempty"`
	// System replaces what an adapter would otherwise tell the model about the
	// situation it is in. Empty is the normal case and the one every runtime caller
	// uses: a round and an ai task each have a standing prompt below, written for
	// them.
	//
	// It exists because both of those prompts open by saying the model is one step
	// inside a running business process, and that is not true of every caller any
	// more. Design-time form generation (ADR-0260) asks this same
	// [Model], through this same adapter, from an authoring screen where there is no
	// instance, no token and no variable to answer into — and a model told it is
	// inside a process it is not inside answers as if it were. Overriding the sentence
	// is cheaper and more honest than a second transport that would drift from this
	// one.
	//
	// It is not serialized into a job payload: a round's framing is this package's to
	// state, and a request that travelled from the engine never carries one.
	System string `json:"-"`
	// Timeout bounds this one call when the model states one on the element
	// (ADR-0566). Zero leaves the adapter's own default. It is not
	// serialized as part of the request: it travels beside it, in milliseconds, because
	// it is a property of the call and not something the model is told.
	Timeout time.Duration `json:"-"`
}

// Decision is what the model answered. Exactly one of the two is the answer: tool calls
// mean another round, and an empty ToolCalls with Outputs (or with nothing) means the run
// is finished — which is why the zero value is a valid ending rather than a failure.
type Decision struct {
	ToolCalls []model.ToolCall
	Outputs   []model.VariableValue
	// Call is the adapter's account of the call that produced this decision: which
	// model answered, what it read and wrote, and the texts that went each way. It is
	// what becomes the durable record of the round (ADR-0566).
	Call Call
}

// Usage is what one call read and wrote, in tokens, in one shape for every provider.
// InputTokens is everything the model read, cached or not — providers disagree about
// whether their own input figure includes the cached part, and a budget summed over
// figures that mean different things would mean nothing. CacheReadTokens and
// CacheWriteTokens are the parts of it served from and written to the provider's cache.
type Usage struct {
	InputTokens      uint64
	OutputTokens     uint64
	CacheReadTokens  uint64
	CacheWriteTokens uint64
}

// Call is one call to a model as the adapter that made it saw it. Everything in it is
// an account: the engine records it and cannot check it.
type Call struct {
	Model      string // the model that answered, as the provider named it
	StopReason string // why it stopped, in the provider's own word
	Usage      Usage
	System     string // the standing instruction it was given
	Prompt     string // what this call added to the conversation
	Answer     string // the words it wrote, beside or instead of tool calls
	// Wire names the format Turn is in; Turn is the model's whole turn as the provider
	// returned it, kept so the next round can replay it unchanged.
	Wire string
	Turn string
	// Fresh says the call was put as a new conversation rather than as a continuation
	// of the rounds before it. The next round is replayed from the last fresh one on.
	Fresh bool
}

// Model is the whole of this package's dependency on inference: one round in, one
// decision out. An implementation holds the endpoint and the credential; the tests here
// hold a script.
type Model interface {
	Decide(ctx context.Context, req Request) (Decision, error)
}

// ModelChooser is a [Model] that can be asked for a particular language model instead of
// the one it is configured for. Both shipped adapters implement it, because for both the
// model id is one field of the request body.
//
// It is a second interface rather than a method on [Model] so that an adapter which
// genuinely serves one model — a local runtime with one file loaded — can decline by not
// implementing it, and be *told* to decline: a caller that cannot honour an authored
// model must fail the job rather than quietly ask a different one. That is the point of
// letting a task name a model at all (ADR-0256).
type ModelChooser interface {
	// ForModel returns this model asked to use the named language model. An empty id
	// means the configured one, so callers need no special case.
	ForModel(id string) Model
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
