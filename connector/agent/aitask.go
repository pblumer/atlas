package agent

import (
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// An ai task is the other half of this package: one call to a language model, one answer
// into one variable, no tools and no rounds (ADR-0256).
//
// It shares everything below the question. A [Task] is put to the same [Model] interface
// as a round, through the same two provider adapters, over the same wire — because a
// one-shot call *is* a round with no tools, and an adapter offered no tools has nothing
// to call and answers in words. That is not a coincidence to exploit but the shape of the
// thing: ADR-0253 defined a round as a decision among tools, and the degenerate case of
// choosing among none is answering.
//
// What differs is where the answer goes. A round's answer ends the container and lands in
// whatever variable the *worker* is configured to answer into; a task's answer is the
// point of the task, so the *model* names the variable and the worker renames what came
// back. That is the same division as everywhere else: what the step is about is authored,
// what the deployment is is configured (ADR-0168).

// Task is a resolved ai task as it travels to a worker: the question with its FEEL
// already evaluated, the provider to ask it through, the language model to ask, and the
// variable the answer belongs in.
//
// There is nowhere in a Task to put a credential, and that is a property of the type
// rather than of the code that fills it in (ADR-0041/0069) — the same guarantee mail's
// Job makes.
type Task struct {
	// Connector names the agent Worker's own configured provider. A name and not an
	// endpoint on purpose: an endpoint would be half a credential.
	Connector string `json:"connector"`
	// Model names the language model to ask, empty when the task named none — in which
	// case whatever the provider is configured for runs.
	Model string `json:"model,omitempty"`
	// Prompt is the question, already evaluated against the variables the task saw.
	Prompt string `json:"prompt"`
	// ResultVariable is where the answer lands. It is required at compile time, so an
	// empty one here means something upstream lost it.
	ResultVariable string `json:"resultVariable"`
	// RequestID is the job key, so a call repeated after a lease elapsed is identifiable
	// as the same one rather than looking like a second question — mail's MessageID
	// exactly, and for its reason.
	RequestID string `json:"requestId,omitempty"`
}

// ResolveTask turns a compiled ai task into a [Task]: the authored prompt evaluated
// against the variables the task sees. It is engine work by necessity — FEEL is compiled
// at deploy (I5) and the scope lives in the store — and it is the whole of what the
// engine contributes, because the endpoint and the credential are the worker's
// (ADR-0168).
func ResolveTask(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail,
	ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Task, error) {
	if detail == nil {
		return Task{}, fmt.Errorf("agent: ai task has no detail")
	}
	// The variables the task sees, read once up its scope chain so its own input-mapped
	// locals shadow what it inherits (ADR-0068).
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Task{}, fmt.Errorf("agent: read variables for element %d: %w", elementInstanceKey, err)
	}
	return Task{
		Connector:      cp.Intern(detail.Connector),
		Model:          cp.Intern(detail.AgentModel),
		Prompt:         resolveValue(detail.AgentPrompt, ei.ProcessInstanceKey, scopeVars),
		ResultVariable: cp.Intern(detail.ResultVar),
		RequestID:      strconv.FormatUint(jobKey, 10),
	}, nil
}

// TaskJobPayload is the resolved task as the flat map a leased job's payload carries. It
// exists for [ResolveJobPayload]'s reason: the field names are stated once, here, so the
// engine and a worker cannot disagree about them.
func TaskJobPayload(t Task) map[string]any {
	return map[string]any{
		"connector":      t.Connector,
		"model":          t.Model,
		"prompt":         t.Prompt,
		"resultVariable": t.ResultVariable,
		"requestId":      t.RequestID,
	}
}

// TaskFromPayload rebuilds a task from what travelled. Mirror of [TaskJobPayload], and
// the reason both live in one file.
//
// A task with no prompt or nowhere to put the answer is refused rather than sent: the
// compiler requires both, so their absence here is not an author's mistake to report at
// the provider's expense — it is this seam having lost something.
func TaskFromPayload(fields map[string]any) (Task, error) {
	t := Task{}
	t.Connector, _ = fields["connector"].(string)
	t.Model, _ = fields["model"].(string)
	t.Prompt, _ = fields["prompt"].(string)
	t.ResultVariable, _ = fields["resultVariable"].(string)
	t.RequestID, _ = fields["requestId"].(string)
	if t.Prompt == "" {
		return Task{}, fmt.Errorf("agent: payload carries no prompt")
	}
	if t.ResultVariable == "" {
		return Task{}, fmt.Errorf("agent: payload names no result variable")
	}
	return t, nil
}

// TaskRequest is the task put in the shape a [Model] takes. The prompt is the goal,
// there are no tools, and it is round one and only — an adapter offered nothing to call
// answers in words, which is precisely what an ai task wants.
func TaskRequest(t Task) Request {
	return Request{Goal: t.Prompt, Round: 1}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's key,
// as it does in every other connector's evaluation.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an authored field into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A FEEL
// null — an absent variable or a failed evaluation — becomes the empty string, matching
// the engine's null-propagating contract (as the REST and mail workers' fields do).
func resolveValue(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// bindVars turns the named variables the task sees into a FEEL binding. A name absent
// from the chain is left unbound (FEEL null); the reserved name processInstanceKey binds
// to the process instance's key as a string.
func bindVars(piKey uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(piKey, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation (mirrors the other connectors' mapping so the two enums evolve
// independently).
func toExprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}
