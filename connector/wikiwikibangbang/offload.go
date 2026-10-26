package wikiwikibangbang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Job is a wikiwikibangbang task with everything already evaluated. There is nowhere
// here to put a token, which is what makes "the engine holds no wiki credential" a
// property of the type.
type Job struct {
	// Connector names the wiki the *worker* is configured for.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	// Item is the item an operation acts on, by its id or its key.
	Item     string `json:"item,omitempty"`
	Space    string `json:"space,omitempty"`
	ItemType string `json:"itemType,omitempty"`
	// Parent is the item a created one is placed under, by its id or its key.
	Parent string `json:"parent,omitempty"`
	Title  string `json:"title,omitempty"`
	// Properties is the evaluated properties object: null changes nothing, "" unsets.
	Properties map[string]any `json:"properties,omitempty"`
	// MatchField names the property a create finds an existing item by.
	MatchField string `json:"matchField,omitempty"`
	// Labels replaces an item's labels; nil leaves them, an empty list clears them.
	Labels *[]string `json:"labels,omitempty"`
	// Text and Blocks are a body to write, one or the other.
	Text       string `json:"text,omitempty"`
	Blocks     []any  `json:"blocks,omitempty"`
	Transition string `json:"transition,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Query      string `json:"query,omitempty"`
	// MaxResults bounds a query; the compiler has applied its default. Zero on every
	// other operation.
	MaxResults     int32  `json:"maxResults,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the task's
	// process catches (ADR-0461); they travel because a worker has no compiled process.
	ErrorCodes []string `json:"errorCodes,omitempty"`
	// Redelivered says the job has been leased before (its lease epoch is above one), so
	// an earlier round may already have written. append-to-page and add-comment check
	// for that write before they make it again; on a first delivery they do not.
	Redelivered bool `json:"redelivered,omitempty"`
}

// The BPMN errors an operation ends with when the task's process catches the code
// (ADR-0461). Uncaught, each stays a failure, with its retries and then an incident.
const (
	// CodeItemNotFound: the item does not exist, or the Worker's account may not read
	// it. The wiki answers both alike (its D10), and so does this code.
	CodeItemNotFound = "WIKIWIKIBANGBANG_ITEM_NOT_FOUND"
	// CodeValidationFailed: the wiki refused a value — a required property missing, a
	// value of the wrong kind, a transition not allowed from the item's state, a unique
	// value another item holds, a malformed query. The wiki's sentence is the message.
	CodeValidationFailed = "WIKIWIKIBANGBANG_VALIDATION_FAILED"
)

// Codes lists every code an operation of this worker can throw.
var Codes = []string{CodeItemNotFound, CodeValidationFailed}

// thrown is err as the job ends with it: a refusal the process has a path for becomes
// its BPMN error; anything else stays a failure.
func thrown(j Job, err error) error {
	var nf *notFound
	if errors.As(err, &nf) && slices.Contains(j.ErrorCodes, CodeItemNotFound) {
		return &job.BPMNError{Code: CodeItemNotFound, Message: nf.Error()}
	}
	var inv *invalid
	if errors.As(err, &inv) && slices.Contains(j.ErrorCodes, CodeValidationFailed) {
		return &job.BPMNError{Code: CodeValidationFailed, Message: inv.message}
	}
	return err
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The Worker lookup comes first: an unconfigured name is the actionable failure. Every
// authored value is checked before the wiki is reached, so a refused job has changed
// nothing; then the account's standing, then the operation.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	c, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("wikiwikibangbang", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("wikiwikibangbang: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
	}
	if err := checkJob(j, op); err != nil {
		return nil, err
	}
	me, err := c.Standing(ctx)
	if err != nil {
		return nil, err
	}
	value, err := c.run(ctx, j, op, me)
	if err != nil {
		return nil, thrown(j, err)
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: value}, nil
}

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Resolve turns a compiled wikiwikibangbang task into a [Job]: the authored values
// evaluated against the instance's variables, the properties built from their template
// or read up the task's scope chain. It is engine work by necessity — FEEL is compiled
// at deploy and only the engine has the scope chain. Whether the job is being delivered
// again is the caller's to set: it is a fact of the lease, not of the model.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("wikiwikibangbang: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("wikiwikibangbang: read variables: %w", err)
	}
	// processInstanceKey binds to the process instance's key, which a model writes into
	// a property to find its own item again; the element instance's is no use to it.
	piKey := elementInstanceKey
	if ei, ok, err := store.GetElementInstance(elementInstanceKey); err == nil && ok {
		piKey = ei.ProcessInstanceKey
	}
	str := func(rv compiler.RestExpr) string { return resolveValue(rv, piKey, vars) }
	connector := cp.Intern(detail.Connector)
	if detail.WikiConnector.Expr != nil {
		connector = str(detail.WikiConnector)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("wikiwikibangbang: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	j := Job{
		Connector:      connector,
		Operation:      cp.Intern(detail.WikiOp),
		Item:           str(detail.WikiItem),
		Space:          str(detail.WikiSpace),
		ItemType:       str(detail.WikiItemType),
		Parent:         str(detail.WikiParent),
		Title:          str(detail.WikiTitle),
		MatchField:     str(detail.WikiMatchField),
		Text:           str(detail.WikiText),
		Transition:     str(detail.WikiTransition),
		Comment:        str(detail.WikiComment),
		Query:          str(detail.WikiQuery),
		MaxResults:     detail.WikiMaxResults,
		ResultVariable: cp.Intern(detail.ResultVar),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}
	if j.Labels, err = resolveLabels(detail.WikiLabels, piKey, vars); err != nil {
		return Job{}, err
	}
	if j.Blocks, err = resolveBlocks(detail.WikiBlocks, piKey, vars); err != nil {
		return Job{}, err
	}
	switch {
	case detail.WikiProperties.Expr != nil:
		v, err := detail.WikiProperties.Expr.Eval(bindVars(piKey, vars, detail.WikiProperties.Expr.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("wikiwikibangbang: evaluate the properties: %w", err)
		}
		kind, _, text := expr.Classify(v)
		if kind != expr.KindJSON {
			return Job{}, fmt.Errorf("wikiwikibangbang: the properties evaluated to a %v, not an object", kind)
		}
		if j.Properties, err = decodeObject(text, "the properties"); err != nil {
			return Job{}, err
		}
	default:
		name := cp.Intern(detail.WikiPropertiesVar)
		if name == "" {
			return j, nil
		}
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("wikiwikibangbang: properties variable %q is not set at this task", name)
		}
		if v.Kind != model.VarJSON {
			return Job{}, fmt.Errorf("wikiwikibangbang: properties variable %q must be an object, not a %v", name, v.Kind)
		}
		if j.Properties, err = decodeObject(v.Text, fmt.Sprintf("properties variable %q", name)); err != nil {
			return Job{}, err
		}
	}
	return j, nil
}

// resolveLabels evaluates the labels: a literal is one comma-separated text, FEEL a list
// or such a text. Unset — no labels authored, or FEEL null — is nil, which leaves an
// item's labels as they are; an empty list clears them.
func resolveLabels(rv compiler.RestExpr, piKey uint64, vars map[string]model.VariableValue) (*[]string, error) {
	if rv.Expr == nil {
		if strings.TrimSpace(rv.Literal) == "" {
			return nil, nil
		}
		labels := catalog.SplitLabels(rv.Literal)
		return &labels, nil
	}
	v, err := rv.Expr.Eval(bindVars(piKey, vars, rv.Expr.Inputs()))
	if err != nil {
		return nil, nil
	}
	kind, _, text := expr.Classify(v)
	switch kind {
	case expr.KindNull:
		return nil, nil
	case expr.KindJSON:
		var list []any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&list); err != nil {
			return nil, fmt.Errorf("wikiwikibangbang: the labels evaluated to an object, not a list of labels")
		}
		labels := make([]string, 0, len(list))
		for _, l := range list {
			if s := strings.TrimSpace(fmt.Sprint(l)); l != nil && s != "" {
				labels = append(labels, s)
			}
		}
		return &labels, nil
	}
	labels := catalog.SplitLabels(text)
	return &labels, nil
}

// resolveBlocks evaluates the blocks: a literal is the JSON array the compiler checked,
// FEEL must evaluate to a list. FEEL null is no blocks, which the worker reports.
func resolveBlocks(rv compiler.RestExpr, piKey uint64, vars map[string]model.VariableValue) ([]any, error) {
	text := rv.Literal
	if rv.Expr != nil {
		v, err := rv.Expr.Eval(bindVars(piKey, vars, rv.Expr.Inputs()))
		if err != nil {
			return nil, nil
		}
		kind, _, t := expr.Classify(v)
		switch kind {
		case expr.KindNull:
			return nil, nil
		case expr.KindJSON:
			text = t
		default:
			return nil, fmt.Errorf("wikiwikibangbang: the blocks evaluated to a %v, not a list of blocks", kind)
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var blocks []any
	if err := dec.Decode(&blocks); err != nil {
		return nil, fmt.Errorf("wikiwikibangbang: the blocks are not a list of blocks")
	}
	return blocks, nil
}

func decodeObject(text, what string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil || out == nil {
		return nil, fmt.Errorf("wikiwikibangbang: %s is not a JSON object", what)
	}
	return out, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, as it does for every other worker.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL value against the scope's variables and
// coerces it to its string form. A FEEL null — an absent variable or a failed
// evaluation — becomes the empty string, which the operation reports where it needs a
// value.
func resolveValue(rv compiler.RestExpr, piKey uint64, vars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, vars, rv.Expr.Inputs()))
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
