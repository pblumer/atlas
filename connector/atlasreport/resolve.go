package atlasreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/atlasreport/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Resolve turns a compiled Atlas report task into a [Job]: the input object evaluated
// against the instance's variables. It is engine work by necessity — FEEL is compiled at
// deploy and only the engine has the scope chain.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("atlasreport: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("atlasreport: read variables: %w", err)
	}
	connector := cp.Intern(detail.Connector)
	if detail.AtlasReportConnector.Expr != nil {
		connector = resolveValue(detail.AtlasReportConnector, elementInstanceKey, vars)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("atlasreport: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	name := cp.Intern(detail.AtlasReportOp)
	if _, ok := catalog.Lookup(name); !ok {
		return Job{}, fmt.Errorf("atlasreport: unknown operation %q", name)
	}
	j := Job{}
	if detail.AtlasReportInput.Expr != nil {
		v, err := detail.AtlasReportInput.Expr.Eval(bindVars(elementInstanceKey, vars, detail.AtlasReportInput.Expr.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("atlasreport: evaluate the input: %w", err)
		}
		kind, _, text := expr.Classify(v)
		if kind != expr.KindJSON {
			return Job{}, fmt.Errorf("atlasreport: the input evaluated to a %v, not an object", kind)
		}
		if j, err = DecodeInput(name, text); err != nil {
			return Job{}, err
		}
	} else if err := catalog.CheckInputKeys(name, nil); err != nil {
		return Job{}, err
	}
	j.Connector = connector
	j.Operation = name
	j.ResultVariable = cp.Intern(detail.ResultVar)
	j.ErrorCodes = cp.CaughtErrorCodes()
	return j, nil
}

// DecodeInput reads an evaluated input object into a job and checks its values. A null
// value is an absent one — an =expression over a variable that is not set — and a key
// the operation does not take is refused, as the compiler refused it for a literal key.
func DecodeInput(name, text string) (Job, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var in map[string]any
	if err := dec.Decode(&in); err != nil || in == nil {
		return Job{}, fmt.Errorf("atlasreport: the input is not a JSON object")
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
	if n, ok := in[catalog.InBuckets].(json.Number); ok {
		f, err := n.Float64()
		if err != nil || f != math.Trunc(f) {
			return Job{}, fmt.Errorf("atlasreport: buckets is %s; it must be a whole number", n)
		}
		if err := catalog.CheckBuckets(int64(f)); err != nil {
			return Job{}, err
		}
		in[catalog.InBuckets] = int32(f)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("atlasreport: the input has a value of the wrong type: %v", err)
	}
	if err := CheckJob(j); err != nil {
		return Job{}, err
	}
	return j, nil
}

// CheckJob checks the values a deploy could not see because an =expression made them.
func CheckJob(j Job) error {
	if j.Buckets > 0 && j.Bucket == "" {
		return fmt.Errorf("atlasreport: buckets is set but bucket is not; name day, week or month")
	}
	if j.Bucket != "" {
		if j.Buckets == 0 {
			return fmt.Errorf("atlasreport: bucket %q is set but buckets is not; say how many", j.Bucket)
		}
		if err := catalog.CheckBucket(j.Bucket); err != nil {
			return err
		}
		if j.From != "" {
			return fmt.Errorf("atlasreport: a series of buckets ends at to and counts back; from is not taken with buckets")
		}
	}
	if j.Language != "" {
		if err := catalog.CheckLanguage(j.Language); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{"from": j.From, "to": j.To} {
		if v != "" {
			if _, err := parseMoment(v); err != nil {
				return fmt.Errorf("atlasreport: %s: %w", k, err)
			}
		}
	}
	return nil
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
