package googlecontacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/googlecontacts/catalog"
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

// Job is a Google Contacts task with everything already evaluated. There is nowhere
// here to put a private key or a refresh token, which is what makes "the engine holds
// no Google credential for this kind" a property of the type.
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
	// process catches. A refusal under one of them is thrown as a BPMN error; anything
	// else is a failure (ADR-0461). They travel with the job because a worker has no
	// compiled process to ask.
	ErrorCodes []string `json:"errorCodes,omitempty"`
}

// CodeContactNotFound is the BPMN error an operation on one contact ends with when the
// contact does not exist — deleted by a person since the process stored its id — and
// the task's process catches the code. Uncaught it stays a failure, with its retries
// and then an incident.
//
// delete-contact does not throw it: a contact that is already gone is what the task
// asked for, and a retried delete must not fail because its first round succeeded.
const CodeContactNotFound = "GOOGLECONTACTS_CONTACT_NOT_FOUND"

// Codes lists every code an operation of this worker can throw.
var Codes = []string{CodeContactNotFound}

// notFound is a refusal under [CodeContactNotFound]. Its message names the operation,
// not the contact: the id is often a value the process keeps enciphered.
type notFound struct{ op string }

func (e *notFound) Error() string {
	return "googlecontacts: " + e.op + ": the contact does not exist in this address book"
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

// Resolve turns a compiled Google Contacts task into a [Job]: the authored values
// evaluated against the instance's variables, and the contact object built from its
// template or read up the task's scope chain. It is engine work by necessity — FEEL is
// compiled at deploy and only the engine has the scope chain.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("googlecontacts: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("googlecontacts: read variables: %w", err)
	}
	connector := cp.Intern(detail.Connector)
	if detail.GoogleContactsConnector.Expr != nil {
		connector = resolveValue(detail.GoogleContactsConnector, elementInstanceKey, vars)
		if strings.TrimSpace(connector) == "" {
			return Job{}, fmt.Errorf("googlecontacts: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	j := Job{
		Connector:      connector,
		Operation:      cp.Intern(detail.GoogleContactsOp),
		ContactID:      resolveValue(detail.GoogleContactsContactID, elementInstanceKey, vars),
		Query:          resolveValue(detail.GoogleContactsQuery, elementInstanceKey, vars),
		Field:          resolveValue(detail.GoogleContactsField, elementInstanceKey, vars),
		Group:          resolveValue(detail.GoogleContactsGroup, elementInstanceKey, vars),
		MaxResults:     detail.GoogleContactsMaxResults,
		ResultVariable: cp.Intern(detail.ResultVar),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}
	switch {
	case detail.GoogleContactsContact.Expr != nil:
		v, err := detail.GoogleContactsContact.Expr.Eval(bindVars(elementInstanceKey, vars, detail.GoogleContactsContact.Expr.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("googlecontacts: evaluate the contact: %w", err)
		}
		kind, _, text := expr.Classify(v)
		if kind != expr.KindJSON {
			return Job{}, fmt.Errorf("googlecontacts: the contact evaluated to a %v, not an object", kind)
		}
		if j.Contact, err = decodeObject(text, "the contact"); err != nil {
			return Job{}, err
		}
	default:
		name := cp.Intern(detail.GoogleContactsContactVar)
		if name == "" {
			return j, nil
		}
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("googlecontacts: contact variable %q is not set at this task", name)
		}
		if v.Kind != model.VarJSON {
			return Job{}, fmt.Errorf("googlecontacts: contact variable %q must be an object, not a %v", name, v.Kind)
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
		return nil, fmt.Errorf("googlecontacts: %s is not a JSON object", what)
	}
	return out, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The Worker lookup comes first: an unconfigured name is the actionable failure. Every
// authored value is checked before Google is reached, so a refused job has changed
// nothing.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	c, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("googlecontacts", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("googlecontacts: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
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
	var rn string
	if op.NeedsContactID {
		var err error
		if rn, err = catalog.ContactResourceName(j.ContactID); err != nil {
			return nil, err
		}
	}
	if op.NeedsQuery && strings.TrimSpace(j.Query) == "" {
		return nil, fmt.Errorf("googlecontacts: %s: the query evaluated to nothing; set the variable it reads before this task", j.Operation)
	}
	if op.NeedsGroup && strings.TrimSpace(j.Group) == "" {
		return nil, fmt.Errorf("googlecontacts: %s: the label evaluated to nothing; set the variable it reads before this task", j.Operation)
	}
	if op.NeedsContact {
		if j.Contact == nil {
			return nil, fmt.Errorf("googlecontacts: %s: the task carries no contact", j.Operation)
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
	b := &book{c: c}
	switch j.Operation {
	case "get-contact":
		p, err := c.get(ctx, rn, readFields)
		if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		return b.project(ctx, p)
	case "find-contacts":
		return b.find(ctx, j, limit)
	case "search-contacts":
		people, err := c.search(ctx, strings.TrimSpace(j.Query), limit, readFields)
		if err != nil {
			return nil, err
		}
		return b.projectAll(ctx, people)
	case "list-contacts":
		people, err := b.scan(ctx, j.Group, limit)
		if err != nil {
			return nil, err
		}
		return b.projectAll(ctx, people)
	case "create-contact":
		return b.create(ctx, j)
	case "update-contact":
		return b.update(ctx, j, rn)
	case "delete-contact":
		err := c.remove(ctx, rn)
		if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
			return nil, nil
		}
		return nil, err
	case "add-to-group", "remove-from-group":
		return nil, b.label(ctx, j, rn)
	case "search-directory":
		people, err := c.searchDirectory(ctx, strings.TrimSpace(j.Query), limit, directoryFields)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(people))
		for _, p := range people {
			out = append(out, project(p, nil))
		}
		return out, nil
	}
	return nil, fmt.Errorf("googlecontacts: operation %q is in the table but not implemented", j.Operation)
}

// book is one job's view of an address book: the client, and the labels it has read
// once for this job.
type book struct {
	c      *Client
	all    []group
	loaded bool
}

func (b *book) groups(ctx context.Context) ([]group, error) {
	if b.loaded {
		return b.all, nil
	}
	all, err := b.c.groups(ctx)
	if err != nil {
		return nil, err
	}
	b.all, b.loaded = all, true
	return all, nil
}

// labels reads the address book's labels for a projection — only when a contact being
// projected is a member of some group at all, so a read of a contact without labels
// costs one call, not two.
func (b *book) labels(ctx context.Context, people []person) (labels, error) {
	member := false
	for _, p := range people {
		if len(entries(p, "memberships")) > 0 {
			member = true
			break
		}
	}
	if !member {
		return nil, nil
	}
	all, err := b.groups(ctx)
	if err != nil {
		return nil, err
	}
	out := labels{}
	for _, g := range all {
		if g.GroupType == userGroup {
			out[g.ResourceName] = g.Name
		}
	}
	return out, nil
}

func (b *book) project(ctx context.Context, p person) (any, error) {
	lbl, err := b.labels(ctx, []person{p})
	if err != nil {
		return nil, err
	}
	return project(p, lbl), nil
}

func (b *book) projectAll(ctx context.Context, people []person) ([]any, error) {
	lbl, err := b.labels(ctx, people)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(people))
	for _, p := range people {
		out = append(out, project(p, lbl))
	}
	return out, nil
}

// resolveGroup turns a label as a model names it into its resource name: the resource
// name itself, or the name a person gave it — exactly, or else ignoring case when that
// is unambiguous. An unknown name is refused rather than created: a misspelt label
// would otherwise become a second label nobody looks at.
func (b *book) resolveGroup(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if catalog.IsGroupResourceName(name) {
		return name, nil
	}
	all, err := b.groups(ctx)
	if err != nil {
		return "", err
	}
	var exact, folded []group
	for _, g := range all {
		switch {
		case g.Name == name || g.FormattedName == name:
			exact = append(exact, g)
		case strings.EqualFold(g.Name, name) || strings.EqualFold(g.FormattedName, name):
			folded = append(folded, g)
		}
	}
	match := exact
	if len(match) == 0 {
		match = folded
	}
	switch len(match) {
	case 1:
		return match[0].ResourceName, nil
	case 0:
		var known []string
		for _, g := range all {
			if g.GroupType == userGroup {
				known = append(known, g.Name)
			}
		}
		sort.Strings(known)
		if len(known) > 10 {
			known = append(known[:10], "…")
		}
		return "", fmt.Errorf("googlecontacts: this address book has no label %q (its labels: %s); create it in Google Contacts first", name, strings.Join(known, ", "))
	}
	return "", fmt.Errorf("googlecontacts: %d labels are called %q; name it by its resource name (contactGroups/…)", len(match), name)
}

// scanLimit bounds how many contacts a listing or a find reads. Google keeps at most
// 25 000 contacts in one account; a book that answers with more is not one this
// Worker should be walking to the end of.
const scanLimit = 25000

// scan reads the address book, or one label's members, failing above max: a short
// list is a wrong answer a process would act on, and it is never cut to fit.
func (b *book) scan(ctx context.Context, groupName string, max int) ([]person, error) {
	if strings.TrimSpace(groupName) != "" {
		people, count, err := b.members(ctx, groupName, max)
		if err != nil {
			return nil, err
		}
		if people == nil && count > max {
			return nil, tooMany(count, max)
		}
		return people, nil
	}
	var people []person
	err := b.c.connections(ctx, readFields, func(p person) bool {
		people = append(people, p)
		return len(people) <= max
	})
	if err != nil {
		return nil, err
	}
	if len(people) > max {
		return nil, tooMany(0, max)
	}
	return people, nil
}

// members reads one label's members, at most max of them. A label with more answers
// no people and its member count, for the caller to word the refusal its way.
func (b *book) members(ctx context.Context, groupName string, limit int) ([]person, int, error) {
	gid, err := b.resolveGroup(ctx, groupName)
	if err != nil {
		return nil, 0, err
	}
	names, count, err := b.c.members(ctx, gid, limit+1)
	if err != nil {
		return nil, 0, err
	}
	if count > limit || len(names) > limit {
		return nil, max(count, len(names)), nil
	}
	people := []person{}
	for start := 0; start < len(names); start += batchGetMax {
		end := min(start+batchGetMax, len(names))
		page, err := b.c.batchGet(ctx, names[start:end], readFields)
		if err != nil {
			return nil, 0, err
		}
		people = append(people, page...)
	}
	return people, len(names), nil
}

func tooMany(count, max int) error {
	if count > 0 {
		return fmt.Errorf("googlecontacts: the listing holds %d contacts, more than the maxResults cap of %d; narrow it to a label or raise maxResults (truncating would be a wrong answer, not a partial one)", count, max)
	}
	return fmt.Errorf("googlecontacts: the listing holds more than the maxResults cap of %d contacts; narrow it to a label or raise maxResults (truncating would be a wrong answer, not a partial one)", max)
}

// find reads the address book (or one label's members) and keeps the contacts whose
// e-mail address or phone number equals the query — or, with a field, whose custom
// field of that name does.
//
// It reads rather than searches on purpose. The People API's search answers from a
// cache that lags behind changes and matches prefixes, so "is this customer known?"
// asked of it can answer "no" for a contact created a minute ago, and the process
// then creates a second one. A read of the book is exact and current, at the cost of
// reading it.
func (b *book) find(ctx context.Context, j Job, max int) (any, error) {
	match := matcher(strings.TrimSpace(j.Query), strings.TrimSpace(j.Field))
	var hits []person
	if strings.TrimSpace(j.Group) != "" {
		people, count, err := b.members(ctx, j.Group, scanLimit)
		if err != nil {
			return nil, err
		}
		if people == nil && count > scanLimit {
			return nil, fmt.Errorf("googlecontacts: find-contacts: the label has %d members, more than the %d a find reads", count, scanLimit)
		}
		for _, p := range people {
			if match(p) {
				hits = append(hits, p)
			}
		}
	} else {
		seen := 0
		err := b.c.connections(ctx, readFields, func(p person) bool {
			seen++
			if match(p) {
				hits = append(hits, p)
			}
			return seen <= scanLimit
		})
		if err != nil {
			return nil, err
		}
		if seen > scanLimit {
			return nil, fmt.Errorf("googlecontacts: find-contacts read more than %d contacts without reaching the end of the address book; narrow it to a label", scanLimit)
		}
	}
	if len(hits) > max {
		return nil, fmt.Errorf("googlecontacts: find-contacts matched %d contacts, more than the maxResults cap of %d; a lookup that matches that many is not finding one", len(hits), max)
	}
	return b.projectAll(ctx, hits)
}

// matcher builds the test find applies. Without a field, the query is compared with
// every e-mail address, ignoring case, and with every phone number by its digits — the
// number as stored, or Google's international form of it, so "+41 79 123 45 67"
// finds "+41791234567". With a field it is compared with that custom field's value,
// ignoring case and surrounding space.
func matcher(query, field string) func(person) bool {
	if field != "" {
		return func(p person) bool {
			for _, u := range entries(p, "userDefined") {
				if strings.EqualFold(strings.TrimSpace(str(u, "key")), field) &&
					strings.EqualFold(strings.TrimSpace(str(u, "value")), query) {
					return true
				}
			}
			return false
		}
	}
	digits := phoneDigits(query)
	return func(p person) bool {
		for _, e := range entries(p, "emailAddresses") {
			if strings.EqualFold(strings.TrimSpace(str(e, "value")), query) {
				return true
			}
		}
		if digits == "" {
			return false
		}
		for _, e := range entries(p, "phoneNumbers") {
			if phoneDigits(str(e, "value")) == digits || phoneDigits(str(e, "canonicalForm")) == digits {
				return true
			}
		}
		return false
	}
}

// phoneDigits is a phone number reduced to its digits, or "" for a value that is not
// a phone number at all — anything with a letter in it, or fewer than five digits, so
// an e-mail address or a short code never matches a number by accident.
func phoneDigits(s string) string {
	var d strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			d.WriteRune(r)
		case strings.ContainsRune("+ ()-./", r):
		default:
			return ""
		}
	}
	if d.Len() < 5 {
		return ""
	}
	return d.String()
}

// create creates a contact. Its labels are set in the same call as its fields, so the
// contact exists with them or not at all; a create followed by a label change would
// leave, after a failure between the two, a contact the retry then creates again.
//
// The contact also goes into myContacts, which is what makes it a contact a person sees
// under "Contacts" — naming a label on create replaces the default membership instead
// of adding to it.
func (b *book) create(ctx context.Context, j Job) (any, error) {
	p := person{}
	if _, err := apply(p, j.Contact); err != nil {
		return nil, err
	}
	if raw, ok := j.Contact[catalog.KeyGroups].([]any); ok && len(raw) > 0 {
		members := []any{membership("contactGroups/myContacts")}
		seen := map[string]bool{"contactGroups/myContacts": true}
		for _, g := range raw {
			name, _ := g.(string)
			gid, err := b.resolveGroup(ctx, name)
			if err != nil {
				return nil, err
			}
			if !seen[gid] {
				seen[gid] = true
				members = append(members, membership(gid))
			}
		}
		p["memberships"] = members
	}
	created, err := b.c.create(ctx, p, readFields)
	if err != nil {
		return nil, err
	}
	return b.project(ctx, created)
}

func membership(gid string) map[string]any {
	return map[string]any{"contactGroupMembership": map[string]any{"contactGroupResourceName": gid}}
}

// updateAttempts bounds how often an update re-reads a contact that changed between its
// read and its write. Google refuses such a write (FAILED_PRECONDITION) and asks the
// client to read again and merge; a contact that changes three times in that window is
// being edited by something that will not stop, and the job fails rather than spin.
const updateAttempts = 3

// update reads the contact's current version, applies the model's changes to it and
// writes it back under that version's etag.
func (b *book) update(ctx context.Context, j Job, rn string) (any, error) {
	fields := fieldsFor(j.Contact)
	if len(fields) == 0 {
		// Every value was null: nothing to change, and the answer is the contact as it is.
		p, err := b.c.get(ctx, rn, readFields)
		if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		return b.project(ctx, p)
	}
	read := strings.Join(fields, ",") + ",metadata"
	var lastErr error
	for attempt := 0; attempt < updateAttempts; attempt++ {
		current, err := b.c.get(ctx, rn, read)
		if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		body := person{}
		if etag, ok := current["etag"]; ok {
			body["etag"] = etag
		}
		if meta, ok := current["metadata"].(map[string]any); ok {
			if sources, ok := meta["sources"]; ok {
				body["metadata"] = map[string]any{"sources": sources}
			}
		}
		for _, f := range fields {
			if v, ok := current[f]; ok {
				body[f] = v
			}
		}
		if _, err := apply(body, j.Contact); err != nil {
			return nil, err
		}
		for _, f := range fields {
			if _, ok := body[f]; !ok {
				body[f] = []any{}
			}
		}
		updated, err := b.c.update(ctx, rn, body, fields, readFields)
		if isGoogle(err, http.StatusBadRequest, "FAILED_PRECONDITION") {
			lastErr = err
			continue
		}
		if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		return b.project(ctx, updated)
	}
	return nil, fmt.Errorf("googlecontacts: update-contact: the contact changed between reading and writing it %d times in a row: %w", updateAttempts, lastErr)
}

// label adds a contact to a label or removes it from one. Both are safe to repeat: a
// member added again, or a non-member removed, is no change.
func (b *book) label(ctx context.Context, j Job, rn string) error {
	gid, err := b.resolveGroup(ctx, j.Group)
	if err != nil {
		return err
	}
	var add, remove []string
	if j.Operation == "add-to-group" {
		add = []string{rn}
	} else {
		remove = []string{rn}
	}
	res, err := b.c.modify(ctx, gid, add, remove)
	if err != nil {
		return err
	}
	if slices.Contains(res.NotFound, rn) {
		return &notFound{op: j.Operation}
	}
	if slices.Contains(res.CannotRemove, rn) {
		return fmt.Errorf("googlecontacts: remove-from-group: Google keeps every contact in at least one group, and this is the contact's last; add it to another label first, or delete the contact")
	}
	return nil
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
