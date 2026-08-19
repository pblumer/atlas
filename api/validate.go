package api

import (
	"bytes"
	"io"
	"net/http"

	"github.com/pblumer/atlas/compiler"
)

// validateResp is the dry-run validation result behind ADR-0026's Problems
// panel: every structured problem the compiler found in the submitted model
// (errors and warnings alike), plus the engine version that produced them — so
// "valid" on the panel means "valid for the engine you will deploy to", not
// valid in the abstract.
type validateResp struct {
	Version  string             `json:"version"`
	Problems []compiler.Problem `json:"problems"`
}

// handleValidate is the Problems-panel backend (ADR-0026): it compiles the posted
// BPMN model in a dry run — no key minted, no definition registered, no instance
// started — and returns every validation Problem with the engine version. Because
// it only parses and inspects an immutable CompiledProcess it never keeps, it
// touches no engine or store state and runs off the run-loop goroutine, like the
// FEEL and DMN validators. It always answers 200 with the findings (a malformed
// model is reported as a problem, not an HTTP error); only a missing body or an
// unreadable request is a 4xx, matching the deploy endpoint.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	problems, err := compiler.ValidateModel(bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "validate: "+err.Error())
		return
	}
	// A nil slice would serialize as JSON null; the panel expects an array, so
	// normalize "no problems" to an empty list.
	if problems == nil {
		problems = []compiler.Problem{}
	}
	writeJSON(w, http.StatusOK, validateResp{Version: Version, Problems: problems})
}
