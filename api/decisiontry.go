package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// Trying a decision from the editor (ADR-draft-trying-a-decision-before-it-runs).
//
// POST /api/v1/decisions/evaluate takes the DMN on screen and answers what it does
// with the given inputs. The model is compiled for this one call and discarded: no
// key is allocated, no record is written, the DMN registry is neither read nor
// modified, and the run loop is never involved. Trying a decision is a pure
// function of the bytes in the request, which is what lets two people try two
// different edits of the same decision at the same moment without seeing each
// other.

// decisionTryTimeout bounds one evaluation. A decision table is small and this is a
// person pressing a button, so the ceiling is generous; it exists because the logic
// is author-supplied and a request should not be able to sit on a handler forever.
const decisionTryTimeout = 10 * time.Second

// decisionTryReq is what the panel sends: the model, which of its decisions to run,
// and the sample inputs to run it over. An empty decisionId asks only what the model
// offers.
type decisionTryReq struct {
	XML        string         `json:"xml"`
	DecisionID string         `json:"decisionId"`
	Inputs     map[string]any `json:"inputs"`
}

// handleTryDecision compiles the submitted DMN and, when a decision is named,
// evaluates it — reporting the outputs and the temis trace that says which rules
// fired and why (ADR-0066).
//
// A model that does not compile, a decision the model does not provide, and an
// evaluation that errors all come back 200 with ok:false and a message. While
// somebody is authoring, a thing that does not work yet is the expected state, and
// a panel that had to tell "the server refused my request" from "my table is not
// finished" in order to render is a panel that would get it wrong. A malformed
// *request* — a body that is not JSON, an empty model — is still a 400: the
// distinction is whether the caller made a mistake or the model did.
func (s *Server) handleTryDecision(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req decisionTryReq
	if err := json.Unmarshal(body, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.XML == "" {
		httpapi.Error(w, http.StatusBadRequest, "empty model: expected the DMN XML to try in \"xml\"")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), decisionTryTimeout)
	defer cancel()
	httpapi.JSON(w, http.StatusOK, s.dmnValidator.Try(ctx, []byte(req.XML), req.DecisionID, req.Inputs))
}
