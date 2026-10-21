package teamsphone

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Job is a Teams Phone task with everything already evaluated: which tenant, which
// operation, and the operation's values. There is nowhere in a Job to put a tenant id,
// a client id or a certificate, which is what makes "the engine holds no Teams
// credential" a property of the type.
type Job struct {
	// Connector names the tenant the *worker* is configured for.
	Connector string `json:"connector"`
	// Operation is one of [OpNames]; the compiler refused an unknown one at deploy.
	Operation string `json:"operation"`
	// Args are the authored values and the compiled settings, keyed by attribute name.
	// A literal arrives as text; a FEEL value keeps its shape, so a list stays a list.
	Args map[string]any `json:"args,omitempty"`
	// ResultVariable names the process variable the operation's answer is written to;
	// empty means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the task's
	// process catches. A refusal under one of them is thrown as a BPMN error; any other
	// is a failure (ADR-0461). They travel with the job because a worker has no compiled
	// process to ask.
	ErrorCodes []string `json:"errorCodes,omitempty"`
}

// Resolve turns a compiled Teams Phone task into a [Job]: every authored value evaluated
// against the variables the task sees up its scope chain (ADR-0068). It is engine work by
// necessity — FEEL is compiled at deploy and only the engine has the scope chain.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("teamsphone: task has no detail")
	}
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("teamsphone: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	args := make(map[string]any, len(detail.TeamsPhoneArgs))
	for _, kv := range detail.TeamsPhoneArgs {
		args[kv.Name] = resolveValue(kv.Val, piKey, scopeVars)
	}
	return Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.TeamsPhoneOp),
		Args:           args,
		ResultVariable: cp.Intern(detail.ResultVar),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The tenant lookup comes first: an unconfigured name is the actionable failure, and
// reporting it ahead of anything about the request keeps the message pointed at the fix
// (ADR-0158). Then the request is normalized and checked against [Ops] — here, once,
// rather than in each [Client] — so a mock and a real tenant refuse the same requests
// with the same words.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("teamsphone", j.Connector)
	}
	req, err := Prepare(j)
	if err != nil {
		return nil, thrown(j, err)
	}
	var out map[string]any
	if users, _ := req.Args["users"].([]string); req.Operation == "resolve-users" && len(users) == 0 {
		// Nothing to resolve is an answer, not a sign-in: an empty list of agents is a
		// queue without agents, and spending ten seconds of PowerShell to learn that
		// would make the empty case the slowest one.
		out = map[string]any{"ids": []string{}, "notFound": []string{}}
	} else {
		out, err = client.Do(ctx, req)
		if err != nil {
			return nil, thrown(j, err)
		}
	}
	if j.ResultVariable == "" {
		return map[string]any{}, nil
	}
	return map[string]any{j.ResultVariable: out}, nil
}

// Request is one operation with its values in the shapes the operation takes: text as
// string, lists as []string, holidays as []Holiday, settings as their canonical words
// (booleans as bool). A [Client] receives nothing it has to interpret further.
type Request struct {
	Operation string         `json:"operation"`
	Args      map[string]any `json:"args"`
	// Reconcile names, sorted, the values of [Op.Reconciled] the model named: what an
	// existing object is brought to. Any other value applies only to an object the
	// request creates, and an existing one keeps what it has.
	Reconcile []string `json:"reconcile,omitempty"`
}

// Holiday is one date range on which an auto attendant disconnects calls. Start is the
// first day and End the day after the last, both as YYYY-MM-DD.
type Holiday struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Prepare normalizes a job into a [Request] and refuses one the operation cannot carry:
// an unknown operation, a value the operation does not use, a required value that
// resolved to nothing, a list or a holiday that is malformed. Every message names the
// attribute, because the incident it becomes is read by whoever authored it. It applies
// the default of every setting the model left out, for an object the request creates,
// and lists in [Request.Reconcile] what the model named of [Op.Reconciled].
func Prepare(j Job) (Request, error) {
	spec, ok := Ops[j.Operation]
	if !ok {
		return Request{}, fmt.Errorf("teamsphone: unknown operation %q (want %s)", j.Operation, strings.Join(OpNames(), ", "))
	}
	allowed := map[string]bool{}
	for _, names := range [][]string{spec.Required, spec.Optional, spec.Settings} {
		for _, n := range names {
			allowed[n] = true
		}
	}
	for name := range j.Args {
		if !allowed[name] {
			return Request{}, fmt.Errorf("teamsphone: %s does not use %q", j.Operation, name)
		}
	}
	args := map[string]any{}
	var reconcile []string
	for _, name := range append(append([]string{}, spec.Required...), spec.Optional...) {
		if contains(spec.Reconciled, name) {
			raw, named := j.Args[name]
			if !named {
				// Not named is "leave it as it is", which only the value's absence says.
				continue
			}
			reconcile = append(reconcile, name)
			if raw == nil {
				return Request{}, fmt.Errorf("teamsphone: %s: %s resolved to nothing, and %s sets an existing one's %s to it; set the variable it reads before this task, or leave %s out to keep them as they are", j.Operation, name, spec.Label, name, name)
			}
		}
		if raw := j.Args[name]; raw == nil && Attributes[name] == List && contains(spec.Required, name) {
			// An empty list is an answer, but a required list that resolved to nothing is a
			// variable the task read before anybody set it; read as empty, it would make a
			// leaver path report success having removed nothing.
			return Request{}, fmt.Errorf("teamsphone: %s needs %s, and it resolved to nothing; set the variable it reads before this task", j.Operation, name)
		}
		v, err := normalize(name, Attributes[name], j.Args[name])
		if err != nil {
			return Request{}, fmt.Errorf("teamsphone: %s: %w", j.Operation, err)
		}
		// An empty list is an answer — a queue with no agents, nobody to resolve — but
		// an empty name is not: it is a variable the task read before anybody set it.
		if s, ok := v.(string); ok && s == "" {
			if contains(spec.Required, name) {
				return Request{}, fmt.Errorf("teamsphone: %s needs %s, and it resolved to nothing; set the variable it reads before this task", j.Operation, name)
			}
			continue
		}
		args[name] = v
	}
	for _, name := range spec.Settings {
		raw, err := text(name, j.Args[name])
		if err != nil {
			return Request{}, fmt.Errorf("teamsphone: %s: %w", j.Operation, err)
		}
		raw = strings.ToLower(raw)
		set := Settings[name]
		if raw == "" {
			raw = set.Default
		} else if contains(spec.Reconciled, name) {
			reconcile = append(reconcile, name)
		}
		if !contains(set.Values, raw) {
			return Request{}, fmt.Errorf("teamsphone: %s has %s %q (want %s)", j.Operation, name, raw, strings.Join(set.Values, ", "))
		}
		switch raw {
		case "true":
			args[name] = true
		case "false":
			args[name] = false
		default:
			args[name] = raw
		}
	}
	if agents, ok := args["agents"].([]string); ok && len(agents) > MaxAgents {
		return Request{}, refused(CodeTooManyAgents, j.Operation, fmt.Sprintf("%d agents are named, and a call queue takes at most %d directly", len(agents), MaxAgents))
	}
	sort.Strings(reconcile)
	return Request{Operation: j.Operation, Args: args, Reconcile: reconcile}, nil
}

// normalize reads one resolved value in the shape its attribute takes.
func normalize(name string, shape Shape, raw any) (any, error) {
	switch shape {
	case List:
		return stringList(name, raw)
	case Holidays:
		return holidays(raw)
	case PhoneNumber:
		return phoneNumber(name, raw)
	case Limit:
		return limit(name, raw)
	default:
		s, err := text(name, raw)
		return s, err
	}
}

// text reads a single value. A number keeps its decimal text; a list or an object is
// refused, because flattening one into a name is a value nobody meant.
func text(name string, raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case json.Number:
		return v.String(), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", fmt.Errorf("%s must be one value, not a list or an object", name)
	}
}

// stringList reads a list of names: a FEEL list of strings, or one text separated by
// commas, semicolons or line breaks. Blank entries and repeats are dropped, so a list a
// form assembled with a trailing comma is the list its author meant.
func stringList(name string, raw any) ([]string, error) {
	var items []string
	switch v := raw.(type) {
	case nil:
	case string:
		items = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
	case []any:
		for i, e := range v {
			s, err := text(name, e)
			if err != nil {
				return nil, fmt.Errorf("%s entry %d: %w", name, i+1, err)
			}
			items = append(items, s)
		}
	case []string:
		items = v
	default:
		return nil, fmt.Errorf("%s must be a list or comma-separated text, not a %T", name, raw)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out, nil
}

// holidays reads the holiday list: a FEEL list of {name, start, end} contexts, or the
// JSON text of one. end may be left out for a single day.
func holidays(raw any) ([]Holiday, error) {
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return []Holiday{}, nil
		}
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		var decoded any
		if err := dec.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("holidays must be a list of {name, start, end}: %v", err)
		}
		raw = decoded
	}
	if raw == nil {
		return []Holiday{}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("holidays must be a list of {name, start, end}, not a %T", raw)
	}
	out := make([]Holiday, 0, len(list))
	seen := map[string]bool{}
	for i, e := range list {
		obj, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("holiday %d must be an object {name, start, end}", i+1)
		}
		h := Holiday{}
		var err error
		if h.Name, err = text("holiday name", obj["name"]); err != nil {
			return nil, err
		}
		if h.Start, err = text("holiday start", obj["start"]); err != nil {
			return nil, err
		}
		if h.End, err = text("holiday end", obj["end"]); err != nil {
			return nil, err
		}
		if h.Name == "" {
			return nil, fmt.Errorf("holiday %d has no name", i+1)
		}
		if seen[strings.ToLower(h.Name)] {
			// Teams names a schedule after it, and two schedules of one name on one
			// auto attendant are one too many to tell apart in the admin center.
			return nil, fmt.Errorf("holiday %q is named twice", h.Name)
		}
		seen[strings.ToLower(h.Name)] = true
		start, err := isoDate(h.Start)
		if err != nil {
			return nil, fmt.Errorf("holiday %q start: %w", h.Name, err)
		}
		if h.End == "" {
			h.End = start.AddDate(0, 0, 1).Format(dateLayout)
		}
		end, err := isoDate(h.End)
		if err != nil {
			return nil, fmt.Errorf("holiday %q end: %w", h.Name, err)
		}
		if !end.After(start) {
			return nil, fmt.Errorf("holiday %q ends on %s, which is not after its start %s; end is the day after the last holiday", h.Name, h.End, h.Start)
		}
		out = append(out, h)
	}
	return out, nil
}

// phoneNumberRe is what Teams takes: digits with an optional leading + and an optional
// ;ext= extension, which Direct Routing numbers may carry.
var phoneNumberRe = regexp.MustCompile(`^\+?[0-9]{3,15}(;ext=[0-9]{1,10})?$`)

// phoneNumber reads a telephone number as people write one and hands it on as Teams
// takes it. A "(0)" after a country code is refused rather than dropped or kept: kept, it
// dials a different number; dropped, it guesses at what the author meant.
func phoneNumber(name string, raw any) (string, error) {
	s, err := text(name, raw)
	if err != nil || s == "" {
		return s, err
	}
	if strings.Contains(s, "(0)") {
		return "", fmt.Errorf("%s %q carries a (0), which is not dialled after a country code; write the number without it", name, s)
	}
	s = strings.TrimPrefix(strings.ToLower(s), "tel:")
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '/', '(', ')', '\u00a0':
			return -1
		}
		return r
	}, s)
	if !phoneNumberRe.MatchString(s) {
		return "", fmt.Errorf("%s %q is no phone number Teams takes (digits with an optional leading + and an optional ;ext=)", name, s)
	}
	return s, nil
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// limit reads how many entries an answer may hold. Unset is the default; anything but a
// whole number from 1 to MaxFreeNumbers is refused rather than clamped, because a model
// asking for 2000 numbers and getting 500 without a word would read the page as the
// whole inventory.
func limit(name string, raw any) (int, error) {
	s, err := text(name, raw)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return DefaultFreeNumbers, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxFreeNumbers {
		return 0, fmt.Errorf("%s must be a whole number from 1 to %d, not %q", name, MaxFreeNumbers, s)
	}
	return n, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key (mirrors the engine/REST builtin).
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue resolves one authored value to what it should be sent as, keeping a FEEL
// value's shape: a list stays a list and a context an object, because agents and holidays
// are lists and flattening them to text would hand the worker a string to take apart
// again. A literal is text verbatim. A FEEL null — an absent variable or a failed
// evaluation — is nil, which [Prepare] reports as a required value that resolved to
// nothing.
func resolveValue(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) any {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return nil
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
// from the chain is left unbound (FEEL null); processInstanceKey binds to the instance's
// key as a string.
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
// evaluation (mirrors the S3 and Discord workers' mapping so the enums evolve
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
