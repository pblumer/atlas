package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Job is an Exchange task with everything already evaluated. There is nowhere here to
// put a client secret, a certificate or a password, which is what makes "the engine
// holds no Exchange credential" a property of the type.
type Job struct {
	// Connector names the organization the *worker* is configured for.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	// Cmdlet is the generic read's Get- cmdlet, empty on every other operation.
	Cmdlet string `json:"cmdlet,omitempty"`
	// Parameters are the evaluated cmdlet parameters, as the model authored them —
	// renames, hashtables and fixed values are applied by [Run], on the worker, where
	// the target is known.
	Parameters map[string]any `json:"parameters,omitempty"`
	// MaxResults bounds a listing; the compiler has applied its default. Zero on every
	// other operation.
	MaxResults     int32  `json:"maxResults,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Exchange task into a [Job]: the parameter template evaluated
// against the instance's variables, or the parameters variable read up the task's scope
// chain. It is engine work by necessity — FEEL is compiled at deploy and only the
// engine has the scope chain.
//
// What it does not do is decide the cmdlet: that depends on whether the Worker is an
// Exchange Online tenant or an Exchange Server, which only the worker holding it knows.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("exchange: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("exchange: read variables: %w", err)
	}
	connector := cp.Intern(detail.Connector)
	if detail.ExchangeConnector.Expr != nil {
		connector = resolveValue(detail.ExchangeConnector, elementInstanceKey, vars)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("exchange: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	j := Job{
		Connector:      connector,
		Operation:      cp.Intern(detail.ExchangeOp),
		Cmdlet:         cp.Intern(detail.ExchangeCmdlet),
		MaxResults:     detail.ExchangeMaxResults,
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	switch {
	case detail.ExchangeParameters.Expr != nil:
		params, err := evalParameters(detail.ExchangeParameters, elementInstanceKey, vars)
		if err != nil {
			return Job{}, err
		}
		j.Parameters = params
	default:
		name := cp.Intern(detail.ExchangeParametersVar)
		if name == "" {
			return j, nil
		}
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("exchange: parameters variable %q is not set at this task", name)
		}
		params, err := parametersOf(v, name)
		if err != nil {
			return Job{}, err
		}
		j.Parameters = params
	}
	return j, nil
}

// evalParameters evaluates the parameter template — one FEEL context compiled from the
// model's JSON at deploy — and returns the object it describes.
func evalParameters(rv compiler.RestExpr, scope uint64, vars map[string]model.VariableValue) (map[string]any, error) {
	v, err := rv.Expr.Eval(bindVars(scope, vars, rv.Expr.Inputs()))
	if err != nil {
		return nil, fmt.Errorf("exchange: evaluate the parameters: %w", err)
	}
	kind, _, text := expr.Classify(v)
	if kind != expr.KindJSON {
		return nil, fmt.Errorf("exchange: the parameters evaluated to a %v, not an object of cmdlet parameters", kind)
	}
	return decodeObject(text, "the parameters")
}

// parametersOf reads a parameters variable. A list or a scalar is refused: a cmdlet
// takes named parameters, and anything else would reach Exchange as a call nobody can
// read back from the model.
func parametersOf(v model.VariableValue, name string) (map[string]any, error) {
	if v.Kind != model.VarJSON {
		return nil, fmt.Errorf("exchange: parameters variable %q must be an object of cmdlet parameters, not a %v", name, v.Kind)
	}
	return decodeObject(v.Text, fmt.Sprintf("parameters variable %q", name))
}

func decodeObject(text, what string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("exchange: %s is not a JSON object of cmdlet parameters", what)
	}
	if out == nil {
		return nil, fmt.Errorf("exchange: %s is null", what)
	}
	return out, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The Worker lookup comes first: an unconfigured name is the actionable failure, and
// reporting it ahead of anything about the call keeps the message pointed at the fix.
// Everything after it is checked before Exchange is reached, so a refused job has
// changed nothing.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	backend, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("exchange", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("exchange: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
	}
	inv, err := op.Build(j.Operation, backend.Target(), j.Cmdlet, j.Parameters)
	if err != nil {
		return nil, err
	}
	limit := 0
	if op.Result == catalog.ResultList && j.MaxResults > 0 {
		// One past the cap: enough to know the cap was exceeded, and no more.
		limit = int(j.MaxResults) + 1
		if op.Sized {
			inv.Parameters[catalog.ResultSizeParameter] = limit
		}
	}
	out, err := backend.Invoke(ctx, inv, limit)
	if err != nil {
		return nil, err
	}
	var value any
	switch op.Result {
	case catalog.ResultNone:
		return nil, nil
	case catalog.ResultList:
		// A listing past its cap fails rather than truncates: a short list is a wrong
		// answer, and a process deciding something from it decides it confidently.
		if j.MaxResults > 0 && len(out) > int(j.MaxResults) {
			return nil, fmt.Errorf("exchange: %s returned more than the %d-item maxResults cap; narrow it or raise maxResults (truncating would be a wrong answer, not a partial one)", j.Operation, j.MaxResults)
		}
		if out == nil {
			out = []any{}
		}
		value = out
	default:
		switch {
		case len(out) == 0:
			value = nil
		case len(out) == 1:
			value = out[0]
		case op.NeedsResult:
			// "The" mailbox is one: a read whose identity matched several (a display
			// name two people share) is refused, or the process would go on with
			// whichever Exchange happened to list first.
			return nil, fmt.Errorf("exchange: %s matched %d objects where it addresses one; use an unambiguous identity (an SMTP address or a GUID)", j.Operation, len(out))
		default:
			// A change that answered with several entries — an Active Directory
			// permission is one per right — has been made. Failing the job now would
			// report it as not made, and its retry would meet "already present"; so
			// every entry is kept.
			value = out
		}
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: value}, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, as it does for every other worker.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL value against the scope's variables and
// coerces it to its string form. A FEEL null — an absent variable or a failed
// evaluation — becomes the empty string, which the caller reports.
func resolveValue(rv compiler.RestExpr, scope uint64, vars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(scope, vars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// bindVars turns the named variables from a scope into a FEEL binding. A name absent
// from the scope is left unbound (FEEL null).
func bindVars(scope uint64, vars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := vars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it.
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
