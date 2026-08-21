package entra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Op describes one Entra lifecycle operation: how it reaches Graph, and what a model
// must author for it. Keeping this a table rather than a switch is what lets the
// compiler, the modeler hints and the worker agree on the same rules — a new
// operation is a row, not three edits that can disagree.
type Op struct {
	// Method and the path builder produce the Graph request.
	Method string
	// NeedsUser, NeedsGroup and NeedsAttributes are what the compiler validates.
	NeedsUser       bool
	NeedsGroup      bool
	NeedsAttributes bool
	// Describes the operation for an error message.
	Label string
}

// Ops is the operation table. The set covers a joiner/mover/leaver lifecycle: create
// and read an account, change it, enable and disable it, delete it, and move it in
// and out of groups.
var Ops = map[string]Op{
	"create-user":         {Method: "POST", NeedsAttributes: true, Label: "create a user"},
	"get-user":            {Method: "GET", NeedsUser: true, Label: "read a user"},
	"update-user":         {Method: "PATCH", NeedsUser: true, NeedsAttributes: true, Label: "update a user"},
	"delete-user":         {Method: "DELETE", NeedsUser: true, Label: "delete a user"},
	"enable":              {Method: "PATCH", NeedsUser: true, Label: "enable an account"},
	"disable":             {Method: "PATCH", NeedsUser: true, Label: "disable an account"},
	"add-group-member":    {Method: "POST", NeedsUser: true, NeedsGroup: true, Label: "add a group member"},
	"remove-group-member": {Method: "DELETE", NeedsUser: true, NeedsGroup: true, Label: "remove a group member"},
}

// OpNames lists the operations, sorted, for the error messages that have to say what
// was expected.
func OpNames() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Job is an Entra task with everything already evaluated. There is nowhere here to
// put a tenant id, a client id or a client secret, which is what makes "the engine
// holds no Entra credential" a property of the type (ADR-0172).
type Job struct {
	// Connector names the tenant the *worker* is configured for.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	// UserID is a user principal name or object id; GroupID an object id.
	UserID  string `json:"userId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	// Attributes is the resolved JSON body for create-user and update-user.
	Attributes     map[string]any `json:"attributes,omitempty"`
	ResultVariable string         `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Entra connector task into a [Job]: the authored ids
// evaluated against the instance's variables, and the attributes object read up the
// task's scope chain. It is engine work by necessity — FEEL is compiled at deploy and
// only the engine has the scope chain.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("entra: connector task has no detail")
	}
	op := cp.Intern(detail.EntraOp)
	spec, ok := Ops[op]
	if !ok {
		return Job{}, fmt.Errorf("entra: unknown operation %q (want %s)", op, strings.Join(OpNames(), ", "))
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("entra: read variables: %w", err)
	}
	j := Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      op,
		UserID:         resolveValue(detail.EntraUserID, elementInstanceKey, vars),
		GroupID:        resolveValue(detail.EntraGroupID, elementInstanceKey, vars),
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	if spec.NeedsAttributes {
		name := cp.Intern(detail.EntraAttributesVar)
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("entra: attributes variable %q is not set at this task", name)
		}
		attrs, err := attributesOf(v, name)
		if err != nil {
			return Job{}, err
		}
		j.Attributes = attrs
	}
	return j, nil
}

// attributesOf reads the attributes variable as a JSON object. A list or a scalar is
// refused: Graph takes an object of directory properties, and sending anything else
// would be a 400 an operator has to decode from the far side.
func attributesOf(v model.VariableValue, name string) (map[string]any, error) {
	if v.Kind != model.VarJSON {
		return nil, fmt.Errorf("entra: attributes variable %q must be an object of directory properties, not a %v", name, v.Kind)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(v.Text)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("entra: attributes variable %q is not a JSON object: %w", name, err)
	}
	if out == nil {
		return nil, fmt.Errorf("entra: attributes variable %q is null", name)
	}
	return out, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The connector lookup comes first, as it does for mail and SQL: an unconfigured name
// is the actionable failure, and reporting it ahead of anything about the request
// keeps the message pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("entra", j.Connector)
	}
	spec, ok := Ops[j.Operation]
	if !ok {
		return nil, fmt.Errorf("entra: unknown operation %q (want %s)", j.Operation, strings.Join(OpNames(), ", "))
	}
	if err := checkRequired(j, spec); err != nil {
		return nil, err
	}
	path, body := request(j, client.BaseURL())
	res, err := client.Call(ctx, spec.Method, path, body)
	if err != nil {
		return nil, err
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: res}, nil
}

// checkRequired repeats the compiler's shape rules on the worker. The compiler
// catches an under-specified model at deploy; this catches a job built by hand or by
// an older engine, and turns what would be a confusing Graph 404 into a sentence
// naming the missing field.
func checkRequired(j Job, spec Op) error {
	if spec.NeedsUser && strings.TrimSpace(j.UserID) == "" {
		return fmt.Errorf("entra: operation %q resolved no userId", j.Operation)
	}
	if spec.NeedsGroup && strings.TrimSpace(j.GroupID) == "" {
		return fmt.Errorf("entra: operation %q resolved no groupId", j.Operation)
	}
	if spec.NeedsAttributes && len(j.Attributes) == 0 {
		return fmt.Errorf("entra: operation %q resolved no attributes", j.Operation)
	}
	return nil
}

// request builds the Graph path and body for one operation. baseURL is needed only by
// add-group-member, whose body carries an absolute @odata.id.
func request(j Job, baseURL string) (path string, body any) {
	user := url.PathEscape(strings.TrimSpace(j.UserID))
	group := url.PathEscape(strings.TrimSpace(j.GroupID))
	switch j.Operation {
	case "create-user":
		return "/users", j.Attributes
	case "get-user", "delete-user":
		return "/users/" + user, nil
	case "update-user":
		return "/users/" + user, j.Attributes
	case "enable":
		return "/users/" + user, map[string]any{"accountEnabled": true}
	case "disable":
		return "/users/" + user, map[string]any{"accountEnabled": false}
	case "add-group-member":
		return "/groups/" + group + "/members/$ref",
			map[string]any{"@odata.id": strings.TrimRight(baseURL, "/") + "/directoryObjects/" + user}
	default: // remove-group-member — the only one left, guarded by the Ops lookup
		return "/groups/" + group + "/members/" + user + "/$ref", nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key, as it does for every other connector.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL connector value against the scope's
// variables and coerces it to its string form, the same way every other connector's
// authored values are read. A FEEL null — an absent variable or a failed evaluation —
// becomes the empty string, which the required-field checks then report by name.
func resolveValue(rv compiler.RestExpr, scope uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(scope, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// bindVars turns the named variables from a scope into a FEEL binding. A name absent
// from the scope is left unbound (FEEL null); the reserved name processInstanceKey
// binds to the scope's own key as a string.
func bindVars(scope uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation.
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

// scopeChainVars reads the variables an element sees up its scope chain (nearest
// wins), through the shared walk every job worker uses (ADR-0068).
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	return state.VisibleVariablesMap(store, elementInstanceKey)
}
