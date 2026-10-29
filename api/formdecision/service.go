// Package formdecision gives a form field its options from a deployed DMN decision: a
// select, radio group, checklist or tag list whose choices are the list a decision
// answers with — typically a boxed list (DMN §10.3.2.7) that the people who own the list
// maintain in the decision editor, versioned and published like any other decision
// (ADR-0511). The decision's inputs are bound to other
// fields of the form, or fixed, so a choice in one field decides what the next one
// offers: a group, then its members; a country, then its regions.
//
// The decision is evaluated when the form is shown and whenever a field its inputs are
// bound to changes, never stored with it: a change to the list is what the next person
// to open the form sees. Nothing here is durable. An evaluation is a read of a compiled
// model the registry already holds; it reaches no log, no state store and no processor,
// and the only thing that becomes a fact is the value the person submits — which is
// checked against the list once more before the task completes.
//
// **What a request may say** is the values of the fields the stored form binds, and
// nothing else: which decision, which input each value is, and which inputs are fixed
// are read from the form, and a request naming anything more is refused. A value bound
// from a field that offers fixed choices — its own static options, or the list of a
// decision field before it — must be one of those choices, so a field cannot be used to
// ask a decision about values the form never offers. The form editor's route evaluates
// a decision with inputs of the author's choosing, and is a modeler's, who may already
// try any model (POST /api/v1/decisions/evaluate).
//
// **This area owns no state**, like formlookup: the registry it reads is the server's,
// and the server's closures observe the single writer (I3).
package formdecision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/dmn"
)

// Config is the server's half of this area.
type Config struct {
	// Decision resolves a decision id to its newest decision deployment — what it is,
	// and the key it is evaluated under (dmn.Registry.LatestDeployedDecision).
	Decision func(id string) (dmn.DeployedDecision, uint64, bool)
	// Decisions lists every decision that has a decision deployment.
	Decisions func() []dmn.DeployedDecision
	// Evaluate evaluates the decision from the deployment under key against inputs.
	Evaluate func(ctx context.Context, key uint64, id string, in map[string]any) (map[string]any, error)
	// MaxBody bounds a request's body: the server's Request budget.
	MaxBody int64
}

// Service serves a form's decision-backed options. Build it with [New].
type Service struct {
	cfg Config
}

// New builds the service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

// Codes are the machine-readable codes a refusal answers with, beside its message.
var Codes = []string{
	"not_deployed", "unbound_input", "evaluation_failed", "not_a_list",
	"not_found", "invalid_query", "invalid_value", "invalid_configuration",
}

// Error is a refusal: a status, a code a client can branch on, and a message a person
// can read.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Write answers the refusal in the API's error envelope, with its code.
func (e *Error) Write(w http.ResponseWriter) {
	httpapi.JSON(w, e.Status, map[string]any{"error": e.Message, "code": e.Code})
}

func refuse(status int, code, format string, a ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
}

// Options evaluates a decision's newest deployment against inputs and returns its list
// as options, and how many of its entries are no option a form can show. Every input the
// decision declares has to be given — a null is given, an absent one is not.
func (s *Service) Options(ctx context.Context, id string, inputs map[string]any) ([]Option, int, *Error) {
	d, key, ok := s.cfg.Decision(id)
	if !ok {
		return nil, 0, refuse(http.StatusNotFound, "not_deployed",
			"no decision %q is deployed on this server: publish it as a decision of its own — a decision bundled only with a process is not offered to forms", id)
	}
	var missing []string
	for _, in := range d.Inputs {
		if _, ok := inputs[in.Name]; !ok {
			missing = append(missing, in.Name)
		}
	}
	if len(missing) > 0 {
		return nil, 0, refuse(http.StatusUnprocessableEntity, "unbound_input",
			"the decision %q takes %s, which the field gives no value: bind each input to a field of the form or fix its value",
			id, strings.Join(quoteAll(missing), ", "))
	}
	out, err := s.cfg.Evaluate(ctx, key, id, plain(inputs))
	if err != nil {
		return nil, 0, refuse(http.StatusUnprocessableEntity, "evaluation_failed",
			"the decision %q could not be evaluated: %v", id, err)
	}
	opts, skipped, err := OptionsOf(resultOf(d, out))
	if err != nil {
		return nil, 0, refuse(http.StatusUnprocessableEntity, "not_a_list",
			"the decision %q cannot give a field its options: %v", id, err)
	}
	return opts, skipped, nil
}

// plain turns values decoded with UseNumber into the plain JSON shapes a decision's
// inputs take — numbers as float64 — as the business rule task's inputs are
// (dmn/worker.go feelToInput).
func plain(in map[string]any) map[string]any {
	b, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return in
	}
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// resultOf picks the decision's own result out of what an evaluation returns: under
// the name of its output, or the one value there is.
func resultOf(d dmn.DeployedDecision, out map[string]any) any {
	for _, name := range []string{d.Output.Name, d.Name, d.ID} {
		if v, ok := out[name]; ok && name != "" {
			return v
		}
	}
	if len(out) == 1 {
		for _, v := range out {
			return v
		}
	}
	return out
}

// empty reports whether a field holds nothing chosen yet.
func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}

// fieldOptions evaluates one decision field of a form against the values of the fields
// its inputs are bound to. A field bound to one that holds nothing yet offers nothing,
// without asking the decision. A bound value that comes from a field with fixed choices
// must be one of them; that is refused as invalid_query, naming the field.
func (s *Service) fieldOptions(ctx context.Context, form *Form, fl Field, values map[string]any, depth int) ([]Option, int, *Error) {
	if depth > len(form.Fields) {
		// The save refuses cycles; a form reaching here was not saved through it.
		return nil, 0, refuse(http.StatusUnprocessableEntity, "invalid_configuration",
			"the options of %q depend on themselves", fl.Key)
	}
	inputs := map[string]any{}
	for _, b := range fl.Inputs {
		if b.Fixed() {
			inputs[b.Name] = b.Value
			continue
		}
		v := values[b.Field]
		if empty(v) {
			return []Option{}, 0, nil
		}
		if e := s.offeredBy(ctx, form, b.Field, v, values, depth); e != nil {
			return nil, 0, e
		}
		inputs[b.Name] = v
	}
	return s.Options(ctx, fl.DecisionID, inputs)
}

// offeredBy holds a bound value to the choices of the field it comes from, where that
// field fixes them: its own static options, or — for a field whose options come from a
// decision — the list that decision answers for the values before it. A field a person
// types into fixes nothing, and its value is the person's.
func (s *Service) offeredBy(ctx context.Context, form *Form, key string, v any, values map[string]any, depth int) *Error {
	var choices []Option
	switch k := form.keys[key]; {
	case k.Decision:
		src, _ := form.Field(key)
		opts, _, e := s.fieldOptions(ctx, form, src, values, depth+1)
		if e != nil {
			return e
		}
		choices = opts
	case k.HasStatic:
		choices = k.Static
	default:
		return nil
	}
	list, isList := v.([]any)
	if !isList {
		list = []any{v}
	}
	for _, x := range list {
		if !offers(choices, x) {
			return refuse(http.StatusBadRequest, "invalid_query",
				"%s is not one of the choices the field %q offers", quote(x), key)
		}
	}
	return nil
}

// FieldOptions answers the options of one decision field of a stored form, for the
// values of the fields its inputs are bound to. values may name those fields and the
// ones they depend on in turn, and nothing else.
func (s *Service) FieldOptions(ctx context.Context, schema []byte, key string, values map[string]any) ([]Option, int, *Error) {
	form, err := Parse(schema)
	if err != nil {
		return nil, 0, refuse(http.StatusUnprocessableEntity, "invalid_configuration",
			"the form's options cannot be read: %v", err)
	}
	fl, ok := form.Field(key)
	if !ok {
		return nil, 0, refuse(http.StatusNotFound, "not_found",
			"the form has no field %q whose options come from a decision", key)
	}
	allowed := form.closure(fl)
	for k, v := range values {
		if !contains(allowed, k) {
			return nil, 0, refuse(http.StatusBadRequest, "invalid_query",
				"%q is not a field the options of %q depend on", k, key)
		}
		if b, err := json.Marshal(v); err != nil || len(b) > maxValueBytes {
			return nil, 0, refuse(http.StatusBadRequest, "invalid_query",
				"the value of %q is larger than %d bytes", k, maxValueBytes)
		}
	}
	return s.fieldOptions(ctx, form, fl, values, 0)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// fieldOptionsRequest is the whole of what a request for a field's options may say.
type fieldOptionsRequest struct {
	Values map[string]any `json:"values"`
}

// WriteFieldOptions answers a request for the options of one decision field of a stored
// form the server has already authorized the caller for: the console's, a public link's
// or a public wizard step's.
func (s *Service) WriteFieldOptions(w http.ResponseWriter, r *http.Request, schema []byte, key string) {
	var req fieldOptionsRequest
	if e := decodeRequest(w, r, s.cfg.MaxBody, &req); e != nil {
		e.Write(w)
		return
	}
	opts, skipped, e := s.FieldOptions(r.Context(), schema, key, req.Values)
	if e != nil {
		e.Write(w)
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"field": key, "options": opts, "skipped": skipped})
}

// decodeRequest reads a request's body into into, bounded by maxBody — the values of a
// form's fields, or an author's inputs, never a document. An empty body says nothing.
func decodeRequest(w http.ResponseWriter, r *http.Request, maxBody int64, into any) *Error {
	if maxBody <= 0 {
		maxBody = 1 << 16
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case errors.As(err, &tooLarge):
			return refuse(http.StatusRequestEntityTooLarge, "invalid_query", "the request is larger than %d bytes", maxBody)
		}
		return refuse(http.StatusBadRequest, "invalid_query", "invalid JSON body: %v", err)
	}
	return nil
}

// listedDecision is one decision the form editor offers as an options source, with the
// inputs a field has to give it.
type listedDecision struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Model   string              `json:"model,omitempty"`
	Service bool                `json:"service,omitempty"`
	Inputs  []dmn.DecisionField `json:"inputs"`
}

// HandleList is GET /api/v1/form-data/decisions: the decisions a field's options can come
// from — every decision with a decision deployment — and the inputs each takes.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	out := []listedDecision{}
	for _, d := range s.cfg.Decisions() {
		inputs := d.Inputs
		if inputs == nil {
			inputs = []dmn.DecisionField{}
		}
		out = append(out, listedDecision{ID: d.ID, Name: d.Name, Model: d.Model, Service: d.Service, Inputs: inputs})
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"decisions": out})
}

// evaluateRequest is what the form editor sends: the inputs as the author has them.
type evaluateRequest struct {
	Inputs map[string]any `json:"inputs"`
}

// HandleEvaluate is POST /api/v1/form-data/decisions/{decisionId}/options: the options a
// decision answers for inputs the author gives — the form editor's previews, for a form
// that may not be saved yet.
func (s *Service) HandleEvaluate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("decisionId"))
	var req evaluateRequest
	if e := decodeRequest(w, r, s.cfg.MaxBody, &req); e != nil {
		e.Write(w)
		return
	}
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}
	opts, skipped, e := s.Options(r.Context(), id, req.Inputs)
	if e != nil {
		e.Write(w)
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"decisionId": id, "options": opts, "skipped": skipped})
}

// Resolve writes each decision field's options into the form as the field's own
// `values` and drops its atlasDecision block, so the form renders as a plain form-js
// form wherever it is shown. It is for the pages nobody signs in to — a public start
// link, a public wizard step — where each such field would otherwise be one more request.
//
// Only a field whose inputs are all fixed is written in: one bound to another field
// depends on what the visitor chooses there, and asks the page's route as the visitor
// chooses. A field whose decision cannot answer now is left as it was too: the viewer
// then says that its options could not be loaded, which an empty list would not. A form
// without a decision field, or one the save check would refuse, comes back unchanged.
func (s *Service) Resolve(ctx context.Context, schema []byte) []byte {
	if !bytes.Contains(schema, []byte(Member)) {
		return schema
	}
	form, err := Parse(schema)
	if err != nil {
		return schema
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return schema
	}
	answers := map[string][]Option{}
	failed := map[string]bool{}
	changed := false
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			key, _ := t["key"].(string)
			if fl, ok := form.Field(strings.TrimSpace(key)); ok && t[Member] != nil && !fl.BoundFields() {
				opts, done := answers[fl.Key]
				if !done && !failed[fl.Key] {
					var e *Error
					if opts, _, e = s.fieldOptions(ctx, form, fl, nil, 0); e != nil {
						failed[fl.Key] = true
					} else {
						answers[fl.Key] = opts
					}
				}
				if !failed[fl.Key] {
					t["values"] = opts
					delete(t, Member)
					changed = true
				}
			}
			walk(t["components"])
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(doc)
	if !changed {
		return schema
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return schema
	}
	return out
}

// CheckSubmission holds what a completion submits to each decision field's list, as the
// decision answers now for what the same submission chose in the fields its inputs are
// bound to, and returns the first refusal. Inside a dynamic list each row is held to the
// list its own row's choices give. A form without a decision field is not checked at
// all.
//
// A bound field form-js did not submit — a disabled one — is read from known, which is
// what the form was opened with (the instance's variables for a task), and failing that
// from its default value. known is called at most once, and only when needed; it may be
// nil.
//
// It checks that a value given is one the list offers, not that one is given: a
// required field is form-js's to insist on, as it is for every other field. Like the
// directory field's check (ADR-0473) it runs before the completion, not with it: the
// list may change between this check and the next step that reads the value.
func (s *Service) CheckSubmission(ctx context.Context, schema []byte, vars map[string]any, known func() map[string]any) *Error {
	form, err := Parse(schema)
	if err != nil {
		return refuse(http.StatusUnprocessableEntity, "invalid_configuration",
			"the task's form cannot be checked: %v", err)
	}
	var knownVars map[string]any
	knownRead := false
	readKnown := func() map[string]any {
		if !knownRead && known != nil {
			knownVars = known()
		}
		knownRead = true
		return knownVars
	}
	answers := map[string][]Option{}
	for _, f := range form.Fields {
		if f.Disabled {
			continue
		}
		name := f.Label
		if strings.TrimSpace(name) == "" {
			name = f.Key
		}
		for _, occ := range occurrences(vars, f.Path) {
			if empty(occ.Value) {
				continue
			}
			values := map[string]any{}
			for _, key := range form.closure(f) {
				k := form.keys[key]
				rows := occ.Rows[:min(eachCount(k.Path), len(occ.Rows))]
				v, ok := valueAt(vars, k.Path, rows)
				if !ok {
					v, ok = valueAt(readKnown(), k.Path, rows)
				}
				if !ok && k.HasDefault {
					v = k.Default
				}
				values[key] = v
			}
			cacheKey := f.Key + "\x00" + canonical(values)
			opts, ok := answers[cacheKey]
			if !ok {
				var e *Error
				if opts, _, e = s.fieldOptions(ctx, form, f, values, 0); e != nil {
					if e.Code == "invalid_query" {
						return refuse(http.StatusBadRequest, "invalid_value", "%s cannot be checked: %s", name, e.Message)
					}
					// The task is there; its form names a list that cannot be read. That
					// is the form's configuration, whatever the options route would answer.
					return refuse(http.StatusUnprocessableEntity, e.Code, "%s cannot be checked: %s", name, e.Message)
				}
				answers[cacheKey] = opts
			}
			chosen := []any{occ.Value}
			if f.Multi {
				list, ok := occ.Value.([]any)
				if !ok {
					return refuse(http.StatusBadRequest, "invalid_value",
						"%s takes a list of the options the decision %q offers", name, f.DecisionID)
				}
				chosen = list
			}
			for _, c := range chosen {
				if !offers(opts, c) {
					return refuse(http.StatusBadRequest, "invalid_value",
						"%s: %s is not one of the options the decision %q offers now", name, quote(c), f.DecisionID)
				}
			}
		}
	}
	return nil
}

// canonical is a map's JSON with its keys in order, for keying what was evaluated.
func canonical(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v, _ := json.Marshal(m[k])
		b.WriteString(k)
		b.WriteByte('=')
		b.Write(v)
		b.WriteByte(';')
	}
	return b.String()
}

func quote(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
