package device

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/device/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Job is a device task with everything already evaluated: the job's half that travels
// from the server to the device (ADR-0168). Fields holds exactly the values the
// operation uses, typed as the device reads them — a name as a string, on as a
// boolean, a duration as a number — so Execute can refuse anything else.
type Job struct {
	// Connector names the device the *worker* is configured for.
	Connector      string         `json:"connector"`
	Operation      string         `json:"operation"`
	Fields         map[string]any `json:"fields,omitempty"`
	ResultVariable string         `json:"resultVariable,omitempty"`
}

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Resolve turns a compiled device task into a [Job]: the authored values evaluated
// against the instance's variables. It is engine work by necessity — FEEL is compiled at
// deploy and only the engine has the scope chain. A value that evaluates to null is left
// out, so the device reports the operation's missing value rather than acting on an
// empty one.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("device: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("device: read variables: %w", err)
	}
	piKey := elementInstanceKey
	if ei, ok, err := store.GetElementInstance(elementInstanceKey); err == nil && ok {
		piKey = ei.ProcessInstanceKey
	}
	connector := cp.Intern(detail.Connector)
	if detail.DeviceConnector.Expr != nil {
		v, ok := evaluate(detail.DeviceConnector, piKey, vars)
		s, isString := v.(string)
		if !ok || !isString || strings.TrimSpace(s) == "" {
			return Job{}, fmt.Errorf("device: the worker expression evaluated to no name; set the variable it reads before this task")
		}
		connector = s
	}
	j := Job{
		Connector:      connector,
		Operation:      cp.Intern(detail.DeviceOp),
		Fields:         map[string]any{},
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	for _, f := range []struct {
		attr string
		rv   compiler.RestExpr
		lit  func(string) any
	}{
		{catalog.AttrOutput, detail.DeviceOutput, func(s string) any { return s }},
		{catalog.AttrLine, detail.DeviceLine, func(s string) any { return s }},
		{catalog.AttrOn, detail.DeviceOn, func(s string) any { return s == "true" }},
		{catalog.AttrDuration, detail.DeviceDuration, func(s string) any { return json.Number(s) }},
	} {
		if f.rv.Expr == nil {
			if f.rv.Literal != "" {
				j.Fields[f.attr] = f.lit(strings.TrimSpace(f.rv.Literal))
			}
			continue
		}
		if v, ok := evaluate(f.rv, piKey, vars); ok {
			j.Fields[f.attr] = v
		}
	}
	return j, nil
}

// evaluate runs a FEEL value and keeps its type: a boolean as bool, a number as
// json.Number, anything else as its text. A null, or an evaluation that failed, is no
// value.
func evaluate(rv compiler.RestExpr, piKey uint64, vars map[string]model.VariableValue) (any, bool) {
	v, err := rv.Expr.Eval(bindVars(piKey, vars, rv.Expr.Inputs()))
	if err != nil {
		return nil, false
	}
	kind, b, text := expr.Classify(v)
	switch kind {
	case expr.KindNull:
		return nil, false
	case expr.KindBool:
		return b, true
	case expr.KindNumber:
		return json.Number(text), true
	}
	return text, true
}

// Run performs a resolved job on the device and returns the variables the job completes
// with. A job for a device this worker does not hold is refused: retrying it here would
// never succeed.
func Run(ctx context.Context, j Job, devices map[string]*Device, jobKey uint64) (map[string]any, error) {
	d, ok := devices[j.Connector]
	if !ok {
		return nil, refused("this worker holds no device %q", j.Connector)
	}
	out, err := d.Execute(ctx, Request{JobKey: jobKey, Op: j.Operation, Fields: j.Fields})
	if err != nil {
		return nil, err
	}
	if j.ResultVariable == "" || out == nil {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: out}, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, as it does for every other worker.
const builtinProcessInstanceKey = "processInstanceKey"

// bindVars turns the named variables from a scope into a FEEL binding. A name absent
// from the scope is left unbound (FEEL null).
func bindVars(piKey uint64, vars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(piKey, 10))
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
