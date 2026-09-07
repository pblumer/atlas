package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A Discord task resolved into plain values, and the function that performs it.
//
// This is ADR-0168's split applied to a chat platform, and it is what makes the Discord
// Worker Type an ordinary external worker rather than one the engine alone can run.
// Finding the task's detail in the compiled process and evaluating every authored value
// against the variables the task sees up its scope chain (ADR-0068/0174) needs the
// compiled process and the store, which only the engine has — so [Resolve] does it and
// produces plain values. The bot token is never among them: what travels is the
// worker's *name*, and [Run] looks that name up in the registry the caller was built
// with.
//
// A Discord worker can therefore hold a token the engine has never seen, and post as
// whichever bot the operator configured it with, without that identity ever reaching
// the engine's process.

// Job is a Discord task with everything already evaluated: which worker, which
// operation, and the operation's values.
//
// Every field here is model-authored or instance-derived. There is nowhere in a Job to
// put a bot token, and that is a property of the type rather than of the code that
// fills it in.
type Job struct {
	// Connector names the Discord Worker the *worker* is configured for. A name and
	// not a token, for the reason the type doc gives.
	Connector string `json:"connector"`
	// Operation is one of [OpNames]; the compiler refused an unknown one at deploy.
	Operation string `json:"operation"`
	// Channel is the channel id the operation acts in; a thread is a channel too.
	Channel string `json:"channel,omitempty"`
	// Message addresses one message, or — on create-thread — the message the thread
	// hangs under.
	Message string `json:"message,omitempty"`
	Content string `json:"content,omitempty"`
	// Name is a new thread's title.
	Name string `json:"name,omitempty"`
	// After is a list's exclusive lower bound and MaxResults its cap; the compiler has
	// already applied the default.
	After      string `json:"after,omitempty"`
	MaxResults int32  `json:"maxResults,omitempty"`
	// Fields are extra request-body properties, each keeping the JSON shape its FEEL
	// value had.
	Fields map[string]any `json:"fields,omitempty"`
	// Nonce is the job key, sent on a created message so an at-least-once retry carries
	// the same value and its duplicate is recognizable.
	Nonce string `json:"nonce,omitempty"`
	// ResultVariable names the process variable Discord's answer is written to; empty
	// means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Discord task into a [Job]: the authored operation and every
// value it carries, evaluated against the variables the task sees. It is engine work by
// necessity — FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in the
// store.
//
// It does not re-validate the operation. The compiler refused an unknown one at deploy
// and [Run]'s client refuses one it does not implement with the list of the ones it
// does; a third check would only be a third message for the same fault.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("discord: task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate every
	// authored value against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("discord: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	return Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.DiscordOp),
		Channel:        resolveValue(detail.DiscordChannel, piKey, scopeVars),
		Message:        resolveValue(detail.DiscordMessage, piKey, scopeVars),
		Content:        resolveValue(detail.DiscordContent, piKey, scopeVars),
		Name:           resolveValue(detail.DiscordName, piKey, scopeVars),
		After:          resolveValue(detail.DiscordAfter, piKey, scopeVars),
		MaxResults:     detail.DiscordMaxResults,
		Fields:         resolveFields(detail.DiscordFields, piKey, scopeVars),
		Nonce:          strconv.FormatUint(jobKey, 10),
		ResultVariable: cp.Intern(detail.ResultVar),
	}, nil
}

// Run performs a resolved job through the caller's own registry and answers with what
// Discord returned (nil for an operation Discord answers with no content). It is the
// whole of the worker's half, and the in-process path calls it too, so there is one
// definition of what a resolved Discord task means rather than two that drift.
//
// The worker lookup comes first: an unconfigured name is the more actionable of the
// failures a job can carry here, and reporting it ahead of anything the operation itself
// might be missing keeps the message an operator sees pointed at the fix (ADR-0158).
func Run(ctx context.Context, j Job, reg *Registry) (any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("discord", j.Connector)
	}
	return client.Do(ctx, Request{
		Operation:  j.Operation,
		Channel:    j.Channel,
		Message:    j.Message,
		Content:    j.Content,
		Name:       j.Name,
		After:      j.After,
		MaxResults: j.MaxResults,
		Fields:     j.Fields,
		Nonce:      j.Nonce,
	})
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key (mirrors the engine/REST builtin), so an authored value can reference
// processInstanceKey — which is what puts a traceable back-reference into a message.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an authored worker value into a string: a literal verbatim, or a
// FEEL expression evaluated over the scope's variables and coerced to its string form.
// A FEEL null — an absent variable or a failed evaluation — becomes the empty string,
// matching the engine's null-propagating contract (as the REST, SharePoint and Jira
// workers' fields do).
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

// resolveFields resolves the task's extra body properties, keeping each value's JSON
// shape rather than flattening everything to a string.
//
// The shape follows from the FEEL value's *kind*, not from what its text looks like. A
// list stays a list (embeds, components), an object stays an object (allowed_mentions),
// a boolean stays a boolean (tts), and everything else is a string. The alternative —
// trying to parse each resolved string as JSON — would turn a message that happened to
// begin with "{" into a different kind of value, which is a defect nobody would find
// twice.
func resolveFields(kvs []compiler.RestKV, piKey uint64, scopeVars map[string]model.VariableValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[kv.Name] = resolveField(kv.Val, piKey, scopeVars)
	}
	return out
}

// resolveField resolves one body property to the JSON value it should be sent as.
func resolveField(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) any {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	kind, b, text := expr.Classify(v)
	switch kind {
	case expr.KindBool:
		return b
	case expr.KindNumber:
		return json.Number(text)
	case expr.KindJSON:
		var decoded any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			// Classify said JSON, so this cannot normally happen; sending the text is
			// still better than sending nothing and calling the property set.
			return text
		}
		return decoded
	case expr.KindNull:
		return nil
	default:
		return text
	}
}

// bindVars turns the named variables the task sees into a FEEL binding. A name absent
// from the chain is left unbound (FEEL null); the reserved name processInstanceKey
// binds to the process instance's key as a string.
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
// evaluation (mirrors the Jira worker's mapping so the two enums evolve independently).
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

// resultVariable turns what Discord returned into the process variable named by the
// task's result variable. The value is canonicalized through the same expr path as any
// other variable (a scalar stays a scalar, an object or array becomes a structured
// VarJSON), so it round-trips on replay exactly like a REST response.
func resultVariable(name string, body any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(body))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind (mirrors the Jira
// worker's mapping so the two enums evolve independently).
func toVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}
