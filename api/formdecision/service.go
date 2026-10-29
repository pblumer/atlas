// Package formdecision gives a form field its options from a deployed DMN decision: a
// select, radio group, checklist or tag list whose choices are the list a decision
// without inputs answers with — typically a boxed list (DMN §10.3.2.7) that the people
// who own the list maintain in the decision editor, versioned and published like any
// other decision (ADR-draft-form-options-from-a-decision).
//
// The decision is evaluated when the form is shown, never stored with it: a change to
// the list is what the next person to open the form sees. Nothing here is durable. An
// evaluation is a read of a compiled model the registry already holds; it reaches no
// log, no state store and no processor, and the only thing that becomes a fact is the
// value the person submits — which is checked against the list once more before the
// task completes.
//
// **What a request may name** is a decision, and only for its options. Who may read
// them is who may already read the decision's whole model through
// GET /api/v1/decision-deployments/{key}/xml — any signed-in caller — and a decision
// that takes inputs is refused, so the route cannot be used to evaluate a decision
// against values of the caller's choosing. A visitor through a public link names
// nothing: the server puts the options into the form it serves them.
//
// **This area owns no state**, like formlookup: the registry it reads is the server's,
// and the server's closures observe the single writer (I3).
package formdecision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	// Evaluate evaluates the decision from the deployment under key, without inputs.
	Evaluate func(ctx context.Context, key uint64, id string) (map[string]any, error)
}

// Service serves a form's decision-backed options. Build it with [New].
type Service struct {
	cfg Config
}

// New builds the service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

// Codes are the machine-readable codes a refusal answers with, beside its message.
var Codes = []string{"not_deployed", "takes_inputs", "evaluation_failed", "not_a_list", "invalid_value", "invalid_configuration"}

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

// Options evaluates a decision's newest deployment and returns its list as options, and
// how many of its entries are no option a form can show.
func (s *Service) Options(ctx context.Context, id string) ([]Option, int, *Error) {
	d, key, ok := s.cfg.Decision(id)
	if !ok {
		return nil, 0, &Error{Status: http.StatusNotFound, Code: "not_deployed", Message: fmt.Sprintf(
			"no decision %q is deployed on this server: publish it as a decision of its own — a decision bundled only with a process is not offered to forms", id)}
	}
	if len(d.Inputs) > 0 {
		names := make([]string, 0, len(d.Inputs))
		for _, in := range d.Inputs {
			names = append(names, in.Name)
		}
		return nil, 0, &Error{Status: http.StatusUnprocessableEntity, Code: "takes_inputs", Message: fmt.Sprintf(
			"the decision %q takes inputs (%s); a form's options come from a decision without inputs", id, strings.Join(names, ", "))}
	}
	out, err := s.cfg.Evaluate(ctx, key, id)
	if err != nil {
		return nil, 0, &Error{Status: http.StatusUnprocessableEntity, Code: "evaluation_failed", Message: fmt.Sprintf(
			"the decision %q could not be evaluated: %v", id, err)}
	}
	opts, skipped, err := OptionsOf(resultOf(d, out))
	if err != nil {
		return nil, 0, &Error{Status: http.StatusUnprocessableEntity, Code: "not_a_list", Message: fmt.Sprintf(
			"the decision %q cannot give a field its options: %v", id, err)}
	}
	return opts, skipped, nil
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

// listedDecision is one decision the form editor offers as an options source.
type listedDecision struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Service bool   `json:"service,omitempty"`
}

// HandleList is GET /api/v1/form-data/decisions: the decisions a field's options can come
// from — every decision with a decision deployment that takes no inputs.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	out := []listedDecision{}
	for _, d := range s.cfg.Decisions() {
		if len(d.Inputs) > 0 {
			continue
		}
		out = append(out, listedDecision{ID: d.ID, Name: d.Name, Model: d.Model, Service: d.Service})
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"decisions": out})
}

// HandleOptions is GET /api/v1/form-data/decisions/{decisionId}/options: the options a
// field naming this decision shows now.
func (s *Service) HandleOptions(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("decisionId"))
	opts, skipped, e := s.Options(r.Context(), id)
	if e != nil {
		e.Write(w)
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"decisionId": id, "options": opts, "skipped": skipped})
}

// Resolve writes each decision field's options into the form as the field's own
// `values` and drops its atlasDecision block, so the form renders as a plain form-js
// form wherever it is shown. It is for the pages nobody signs in to — a public start
// link, a public wizard step — which have nobody to ask the options route as.
//
// A field whose decision cannot answer now is left as it was: the viewer then says that
// its options could not be loaded, which an empty list would not. A form without a
// decision field, or one the save check would refuse, comes back unchanged.
func (s *Service) Resolve(ctx context.Context, schema []byte) []byte {
	if !bytes.Contains(schema, []byte(Member)) {
		return schema
	}
	if _, err := Parse(schema); err != nil {
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
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if block, ok := t[Member].(map[string]any); ok {
				id, _ := block["decisionId"].(string)
				id = strings.TrimSpace(id)
				opts, done := answers[id]
				if !done && !failed[id] {
					var e *Error
					if opts, _, e = s.Options(ctx, id); e != nil {
						failed[id] = true
					} else {
						answers[id] = opts
					}
				}
				if !failed[id] {
					t["values"] = opts
					delete(t, Member)
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
	out, err := json.Marshal(doc)
	if err != nil {
		return schema
	}
	return out
}

// CheckSubmission holds what a completion submits to each decision field's list, as the
// decision answers now, and returns the first refusal. A form without a decision field
// is not checked at all.
//
// It checks that a value given is one the list offers, not that one is given: a
// required field is form-js's to insist on, as it is for every other field. Like the
// directory field's check (ADR-0473) it runs before the completion, not with it: the
// list may change between this check and the next step that reads the value.
func (s *Service) CheckSubmission(ctx context.Context, schema []byte, vars map[string]any) *Error {
	form, err := Parse(schema)
	if err != nil {
		return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_configuration",
			Message: "the task's form cannot be checked: " + err.Error()}
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
		for _, v := range valuesAt(vars, f.Path) {
			if s, ok := v.(string); ok && s == "" {
				continue
			}
			opts, ok := answers[f.DecisionID]
			if !ok {
				var e *Error
				if opts, _, e = s.Options(ctx, f.DecisionID); e != nil {
					// The task is there; its form names a list that cannot be read. That
					// is the form's configuration, whatever the options route would answer.
					return &Error{Status: http.StatusUnprocessableEntity, Code: e.Code, Message: fmt.Sprintf(
						"%s cannot be checked: %s", name, e.Message)}
				}
				answers[f.DecisionID] = opts
			}
			chosen := []any{v}
			if f.Multi {
				list, ok := v.([]any)
				if !ok {
					return &Error{Status: http.StatusBadRequest, Code: "invalid_value", Message: fmt.Sprintf(
						"%s takes a list of the options the decision %q offers", name, f.DecisionID)}
				}
				chosen = list
			}
			for _, c := range chosen {
				if !offers(opts, c) {
					return &Error{Status: http.StatusBadRequest, Code: "invalid_value", Message: fmt.Sprintf(
						"%s: %s is not one of the options the decision %q offers now", name, quote(c), f.DecisionID)}
				}
			}
		}
	}
	return nil
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
