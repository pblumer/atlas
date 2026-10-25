package carddav

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/carddav/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Job is a CardDAV task with everything already evaluated. There is nowhere here to
// put a password, which is what makes "the engine holds no CardDAV credential" a
// property of the type.
type Job struct {
	// Connector names the address book the *worker* is configured for.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	ContactID string `json:"contactId,omitempty"`
	Query     string `json:"query,omitempty"`
	Field     string `json:"field,omitempty"`
	Group     string `json:"group,omitempty"`
	// Contact is the evaluated contact object of a write.
	Contact map[string]any `json:"contact,omitempty"`
	// MaxResults bounds a listing; the compiler has applied its default. Zero on every
	// other operation.
	MaxResults     int32  `json:"maxResults,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the task's
	// process catches (ADR-0461); they travel because a worker has no compiled process.
	ErrorCodes []string `json:"errorCodes,omitempty"`
	// CreateKey is what makes create-contact safe to repeat: derived by the engine from
	// this installation's node id and the job key ([CreateKey]), the same on every
	// delivery of the job and different for every other job. The contact's UID and
	// resource name are made from it. Set on create-contact only.
	CreateKey string `json:"createKey,omitempty"`
}

// CodeContactNotFound is the BPMN error an operation on one contact ends with when the
// contact does not exist — deleted by a person since the process stored its id — and
// the task's process catches the code. Uncaught it stays a failure, with its retries
// and then an incident. delete-contact does not throw it: a contact already gone is
// what the task asked for.
const CodeContactNotFound = "CARDDAV_CONTACT_NOT_FOUND"

// Codes lists every code an operation of this worker can throw.
var Codes = []string{CodeContactNotFound}

// notFound is a refusal under [CodeContactNotFound]. Its message names the operation,
// not the contact.
type notFound struct{ op string }

func (e *notFound) Error() string {
	return "carddav: " + e.op + ": the contact does not exist in this address book"
}

// thrown is err as the job ends with it: a missing contact becomes the BPMN error the
// process has a path for, when it has one; anything else stays a failure.
func thrown(j Job, err error) error {
	var nf *notFound
	if errors.As(err, &nf) && slices.Contains(j.ErrorCodes, CodeContactNotFound) {
		return &job.BPMNError{Code: CodeContactNotFound, Message: nf.Error()}
	}
	return err
}

// CreateKey derives a create-contact job's key from the installation's node id and the
// job key. The node id is what tells two installations' job keys apart — a test server
// and a production server writing into one address book would otherwise derive the same
// name for different contacts, and the second would take the first's for its own.
func CreateKey(nodeID string, jobKey uint64) string {
	sum := sha256.Sum256([]byte("atlas carddav create\x00" + nodeID + "\x00" + strconv.FormatUint(jobKey, 10)))
	return hex.EncodeToString(sum[:16])
}

// CreateUID is the UID a created contact gets, and its resource name before ".vcf": the
// create key in a UUID's form (RFC 9562 version 8), upper-case as Apple writes UIDs. A
// key that is not one — a job built by hand — gets a random UID, which makes that create
// as repeatable as Google's and no more.
func CreateUID(key string) string {
	b, err := hex.DecodeString(key)
	if err != nil || len(b) < 16 {
		b = make([]byte, 16)
		_, _ = rand.Read(b)
	}
	b = b[:16]
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	h := strings.ToUpper(hex.EncodeToString(b))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Resolve turns a compiled CardDAV task into a [Job]: the authored values evaluated
// against the instance's variables, and the contact object built from its template or
// read up the task's scope chain. It is engine work by necessity — FEEL is compiled at
// deploy and only the engine has the scope chain. jobKey and nodeID make the create key.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey, jobKey uint64, nodeID string) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("carddav: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("carddav: read variables: %w", err)
	}
	connector := cp.Intern(detail.Connector)
	if detail.CardDAVConnector.Expr != nil {
		connector = resolveValue(detail.CardDAVConnector, elementInstanceKey, vars)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("carddav: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	j := Job{
		Connector:      connector,
		Operation:      cp.Intern(detail.CardDAVOp),
		ContactID:      resolveValue(detail.CardDAVContactID, elementInstanceKey, vars),
		Query:          resolveValue(detail.CardDAVQuery, elementInstanceKey, vars),
		Field:          resolveValue(detail.CardDAVField, elementInstanceKey, vars),
		Group:          resolveValue(detail.CardDAVGroup, elementInstanceKey, vars),
		MaxResults:     detail.CardDAVMaxResults,
		ResultVariable: cp.Intern(detail.ResultVar),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}
	if op, ok := catalog.Lookup(j.Operation); ok && op.Create {
		j.CreateKey = CreateKey(nodeID, jobKey)
	}
	switch {
	case detail.CardDAVContact.Expr != nil:
		v, err := detail.CardDAVContact.Expr.Eval(bindVars(elementInstanceKey, vars, detail.CardDAVContact.Expr.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("carddav: evaluate the contact: %w", err)
		}
		kind, _, text := expr.Classify(v)
		if kind != expr.KindJSON {
			return Job{}, fmt.Errorf("carddav: the contact evaluated to a %v, not an object", kind)
		}
		if j.Contact, err = decodeObject(text, "the contact"); err != nil {
			return Job{}, err
		}
	default:
		name := cp.Intern(detail.CardDAVContactVar)
		if name == "" {
			return j, nil
		}
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("carddav: contact variable %q is not set at this task", name)
		}
		if v.Kind != model.VarJSON {
			return Job{}, fmt.Errorf("carddav: contact variable %q must be an object, not a %v", name, v.Kind)
		}
		if j.Contact, err = decodeObject(v.Text, fmt.Sprintf("contact variable %q", name)); err != nil {
			return Job{}, err
		}
	}
	return j, nil
}

func decodeObject(text, what string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil || out == nil {
		return nil, fmt.Errorf("carddav: %s is not a JSON object", what)
	}
	return out, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The Worker lookup comes first: an unconfigured name is the actionable failure. Every
// authored value is checked before the server is reached, so a refused job has changed
// nothing.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	c, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("carddav", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("carddav: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
	}
	value, err := run(ctx, c, j, op)
	if err != nil {
		return nil, thrown(j, err)
	}
	if op.Result == catalog.ResultNone || j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: value}, nil
}

func run(ctx context.Context, c *Client, j Job, op catalog.Op) (any, error) {
	id := catalog.ContactID(j.ContactID)
	if op.NeedsContactID {
		if err := catalog.CheckContactID(id); err != nil {
			return nil, err
		}
	}
	if op.NeedsQuery && strings.TrimSpace(j.Query) == "" {
		return nil, fmt.Errorf("carddav: %s: the query evaluated to nothing; set the variable it reads before this task", j.Operation)
	}
	if op.NeedsGroup && strings.TrimSpace(j.Group) == "" {
		return nil, fmt.Errorf("carddav: %s: the group evaluated to nothing; set the variable it reads before this task", j.Operation)
	}
	if op.NeedsContact {
		if j.Contact == nil {
			return nil, fmt.Errorf("carddav: %s: the task carries no contact", j.Operation)
		}
		if err := catalog.CheckValues(j.Operation, j.Contact); err != nil {
			return nil, err
		}
	}
	// The compiler has applied the default and the ceiling; a job that arrives without
	// them — from a hand-built payload — gets the default rather than an unbounded read.
	limit := int(j.MaxResults)
	if op.MaxResults.Max > 0 && (limit <= 0 || limit > int(op.MaxResults.Max)) {
		limit = int(op.MaxResults.Default)
	}
	coll, err := c.addressBook(ctx)
	if err != nil {
		return nil, err
	}
	b := &book{c: c, coll: coll}
	switch j.Operation {
	case "get-contact":
		u, err := memberURL(coll, id)
		if err != nil {
			return nil, err
		}
		card, _, err := c.getCard(ctx, "GET contact", u)
		if isStatus(err, 404, 410) {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		return b.project(ctx, id, card)
	case "find-contacts":
		return b.find(ctx, j, limit)
	case "search-contacts":
		return b.search(ctx, strings.TrimSpace(j.Query), limit)
	case "list-contacts":
		return b.list(ctx, j.Group, limit)
	case "create-contact":
		return b.create(ctx, j)
	case "update-contact":
		return b.update(ctx, j, id)
	case "delete-contact":
		u, err := memberURL(coll, id)
		if err != nil {
			return nil, err
		}
		if err := c.deleteCard(ctx, u); err != nil && !isStatus(err, 404, 410) {
			return nil, err
		}
		return nil, nil
	case "add-to-group", "remove-from-group":
		return nil, b.label(ctx, j, id)
	}
	return nil, fmt.Errorf("carddav: operation %q is in the table but not implemented", j.Operation)
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, as it does for every other worker.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL value against the scope's variables and
// coerces it to its string form. A FEEL null — an absent variable or a failed
// evaluation — becomes the empty string, which the operation reports.
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
