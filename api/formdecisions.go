package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/formdecision"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/dmn"
)

// What a form field whose options come from a decision may reach
// (ADR-draft-form-options-from-a-decision).
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
		Evaluate: func(ctx context.Context, key uint64, id string) (map[string]any, error) {
			return s.dmnRegistry.Evaluate(ctx, key, id, map[string]any{})
		},
	})
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
	if e := s.formDecision.CheckSubmission(r.Context(), []byte(schema), vars); e != nil {
		e.Write(w)
		return true
	}
	return false
}
