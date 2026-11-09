package sharepointserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Job is a SharePoint Server task with everything already evaluated: plain values, with
// nowhere to put a web application or an account the Worker runs as.
type Job struct {
	Connector      string         `json:"connector"`
	Operation      string         `json:"operation"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	ResultVariable string         `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the
	// task's process catches (ADR-0461); a worker has no compiled process to ask.
	ErrorCodes []string `json:"errorCodes,omitempty"`
}

// Resolve turns a compiled task into a [Job]. Engine work by necessity: the parameter
// template is FEEL compiled at deploy and only the engine has the scope chain.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("sharepointserver: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("sharepointserver: read variables: %w", err)
	}
	j := Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.SharePointServerOp),
		ResultVariable: cp.Intern(detail.ResultVar),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}
	if detail.SharePointServerParameters.Expr == nil {
		return j, nil
	}
	rv := detail.SharePointServerParameters
	v, err := rv.Expr.Eval(bindVars(elementInstanceKey, vars, rv.Expr.Inputs()))
	if err != nil {
		return Job{}, fmt.Errorf("sharepointserver: evaluate the parameters: %w", err)
	}
	kind, _, text := expr.Classify(v)
	if kind != expr.KindJSON {
		return Job{}, fmt.Errorf("sharepointserver: the parameters evaluated to a %v, not an object of parameters", kind)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	if err := dec.Decode(&j.Parameters); err != nil {
		return Job{}, fmt.Errorf("sharepointserver: the parameters are not a JSON object")
	}
	return j, nil
}

// bindVars turns the named variables of a scope into a FEEL binding; an absent name is
// left unbound (FEEL null), and processInstanceKey binds as it does for every worker.
func bindVars(scope uint64, vars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == "processInstanceKey" {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := vars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

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

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. Everything but the Worker lookup is checked before
// the farm is reached, so a refused job has changed nothing.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("sharepointserver", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("sharepointserver: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
	}
	inv, err := op.Build(j.Operation, j.Parameters)
	if err != nil {
		return nil, err
	}
	out, err := client.Invoke(ctx, inv)
	if err != nil {
		return nil, thrown(j.ErrorCodes, err)
	}
	var value any
	switch op.Result {
	case catalog.ResultNone:
		return nil, nil
	case catalog.ResultList:
		// The bootstrap reads one past the limit: a short list is a wrong answer a
		// process would act on, so an oversized one fails rather than truncates.
		limit, _ := inv.Args["Limit"].(int64)
		if int64(len(out)) > limit {
			return nil, fmt.Errorf("sharepointserver: %s returned more than the limit of %d sites; raise Limit (truncating would be a wrong answer, not a partial one)", j.Operation, limit)
		}
		if out == nil {
			out = []any{}
		}
		value = out
	default:
		if len(out) > 0 {
			value = out[0]
		}
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: value}, nil
}
