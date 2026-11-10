package calendar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Resolve turns a compiled calendar task into a [Job]: the input object evaluated
// against the instance's variables. It is engine work by necessity — FEEL is compiled
// at deploy and only the engine has the scope chain. jobKey and nodeID make the create
// key of a reservation.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey, jobKey uint64, nodeID string) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("calendar: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("calendar: read variables: %w", err)
	}
	connector := cp.Intern(detail.Connector)
	if detail.CalendarConnector.Expr != nil {
		connector = resolveValue(detail.CalendarConnector, elementInstanceKey, vars)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("calendar: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	name := cp.Intern(detail.CalendarOp)
	op, ok := catalog.Lookup(name)
	if !ok {
		return Job{}, fmt.Errorf("calendar: unknown operation %q", name)
	}
	if detail.CalendarInput.Expr == nil {
		return Job{}, fmt.Errorf("calendar: the task has no input")
	}
	v, err := detail.CalendarInput.Expr.Eval(bindVars(elementInstanceKey, vars, detail.CalendarInput.Expr.Inputs()))
	if err != nil {
		return Job{}, fmt.Errorf("calendar: evaluate the input: %w", err)
	}
	kind, _, text := expr.Classify(v)
	if kind != expr.KindJSON {
		return Job{}, fmt.Errorf("calendar: the input evaluated to a %v, not an object", kind)
	}
	j, err := decodeInput(name, op, text)
	if err != nil {
		return Job{}, err
	}
	j.Connector = connector
	j.Operation = name
	j.ResultVariable = cp.Intern(detail.ResultVar)
	j.ErrorCodes = cp.CaughtErrorCodes()
	if op.Create {
		j.CreateKey = CreateKey(nodeID, jobKey)
	}
	return j, nil
}

// minuteKeys are the input's whole-number values. FEEL has one number type, so a value
// computed by an expression may arrive as 60.0; it is taken when it is whole.
var minuteKeys = []string{"durationMinutes", "gridMinutes", "bufferMinutes", "minLeadMinutes", "maxResults"}

// decodeInput reads an evaluated input object into a job. A null value is an absent
// one — an =expression over a variable that is not set — and a key the operation does
// not take is refused, as the compiler refused it for a literal key.
func decodeInput(name string, op catalog.Op, text string) (Job, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var in map[string]any
	if err := dec.Decode(&in); err != nil || in == nil {
		return Job{}, fmt.Errorf("calendar: the input is not a JSON object")
	}
	keys := make([]string, 0, len(in))
	for k, v := range in {
		if v == nil {
			delete(in, k)
			continue
		}
		keys = append(keys, k)
	}
	if err := catalog.CheckInputKeys(name, keys); err != nil {
		return Job{}, fmt.Errorf("%w; set the variable an =expression reads before this task", err)
	}
	for _, k := range minuteKeys {
		n, ok := in[k].(json.Number)
		if !ok {
			continue
		}
		f, err := n.Float64()
		if err != nil || f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
			return Job{}, fmt.Errorf("calendar: %s is %s; it must be a whole number", k, n)
		}
		in[k] = int32(f)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("calendar: the input has a value of the wrong type: %v", err)
	}
	return j, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, as it does for every other worker.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL value against the scope's variables and
// coerces it to its string form. A FEEL null becomes the empty string.
func resolveValue(rv compiler.RestExpr, scope uint64, vars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(scope, vars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	kind, _, text := expr.Classify(v)
	if kind == expr.KindNull {
		return ""
	}
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
