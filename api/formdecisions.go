package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/formdecision"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// What a form field whose options come from a decision may reach
// (ADR-0511).
//
// The service holds no state; these closures are its whole reach into this server. The
// registry's description of a deployment is read on the run loop, as every other reader
// of it does (DeployedDecisions, LatestDecisionKey); the evaluation runs off it, on the
// request's goroutine, as the business-rule-task worker's does — the compiled model is
// shared read-only state behind the registry's own lock, and evaluating it writes
// nothing (I3, I5).

func (s *Server) newFormDecisions() {
	s.formDecision = formdecision.New(formdecision.Config{
		Decision: func(id string) (d dmn.DeployedDecision, key uint64, ok bool) {
			s.do(func() { d, key, ok = s.dmnRegistry.LatestDeployedDecision(id) })
			return d, key, ok
		},
		Decisions: func() (out []dmn.DeployedDecision) {
			s.do(func() { out = s.dmnRegistry.LatestDeployedDecisions() })
			return out
		},
		Evaluate: func(ctx context.Context, key uint64, id string, in map[string]any) (map[string]any, error) {
			return s.dmnRegistry.Evaluate(ctx, key, id, in)
		},
		MaxBody: s.budgets().Request,
	})
}

// handleFormFieldOptions answers the options of one decision field of a stored form, for
// the values the person has chosen in the fields its inputs are bound to. Whoever may
// read the form (GET /api/v1/forms/{id}) may ask it; what the decision is evaluated
// against is read from the stored form.
func (s *Server) handleFormFieldOptions(w http.ResponseWriter, r *http.Request) {
	var (
		rec     form
		ok      bool
		loadErr error
	)
	s.do(func() { rec, ok, loadErr = s.forms.Get(r.PathValue("id")) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+loadErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no form with that id")
	default:
		s.formDecision.WriteFieldOptions(w, r, []byte(rec.Schema), r.PathValue("field"))
	}
}

// handlePublicFieldOptions is handleFormFieldOptions for the form a public start link
// renders: the visitor names the link and the field, never a form or a decision.
func (s *Server) handlePublicFieldOptions(w http.ResponseWriter, r *http.Request) {
	s.setPublicCORS(w, r)
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var (
		schema string
		found  bool
		opErr  error
	)
	s.do(func() {
		link, ok, e := s.publicLinks.Get(r.PathValue("token"))
		if e != nil || !ok {
			opErr = e
			return
		}
		f, ok, e := s.forms.Get(link.FormID)
		if e != nil || !ok {
			opErr = e
			return
		}
		schema, found = f.Schema, true
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "unknown or revoked link")
	default:
		s.formDecision.WriteFieldOptions(w, r, []byte(schema), r.PathValue("field"))
	}
}

// handlePublicWizardFieldOptions is the same for a screen of a public wizard, which the
// visitor's pass opens (publicwizard.go).
func (s *Server) handlePublicWizardFieldOptions(w http.ResponseWriter, r *http.Request) {
	piKey, ok := s.publicPassOrRefuse(w, r)
	if !ok {
		return
	}
	taskKey, err := strconv.ParseUint(r.PathValue("taskKey"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusNotFound, errNoPublicStep.Error())
		return
	}
	var schema string
	s.do(func() { schema, err = s.publicStepForm(piKey, taskKey) })
	switch {
	case err == errNoPublicStep:
		httpapi.Error(w, http.StatusNotFound, err.Error())
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read step: "+err.Error())
	default:
		s.formDecision.WriteFieldOptions(w, r, []byte(schema), r.PathValue("field"))
	}
}

// taskVariables is what a task's form was opened with: the variables visible at its
// element-instance scope, decoded as the Tasks app receives them. The submission check
// reads a bound field form-js did not submit from it — a disabled one.
func (s *Server) taskVariables(key uint64) map[string]any {
	data := map[string]any{}
	s.do(func() {
		jv, ok, err := s.store.GetJob(key)
		if err != nil || !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
			return
		}
		tr := s.enrichTask(key, jv)
		scope := tr.ElementInstanceKey
		if scope == 0 {
			scope = jv.ProcessInstanceKey
		}
		_ = state.VisibleVariables(s.personalReader(s.store), scope, func(v *model.VariableValue) error {
			data[v.Name] = nativeVar(v)
			return nil
		})
	})
	b, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return nil
	}
	return out
}

// refuseDecisionValues holds what a public form submits to the lists its decision fields
// were shown, before anything is sealed or started. It answers true when it wrote the
// refusal. A form without such a field is not looked at.
func (s *Server) refuseDecisionValues(w http.ResponseWriter, r *http.Request, schema string, body []byte) bool {
	if !strings.Contains(schema, formdecision.Member) {
		return false
	}
	vars, err := submittedVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return true
	}
	if e := s.formDecision.CheckSubmission(r.Context(), []byte(schema), vars, nil); e != nil {
		e.Write(w)
		return true
	}
	return false
}
