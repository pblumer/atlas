package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/pblumer/atlas/compiler"
)

// ruleCompile is the stable rule slug the /validate endpoint stamps on a compile
// failure the compiler reports as an opaque error rather than a structured
// [compiler.Problem] — the stage-1–4 faults (malformed XML, a bad FEEL
// expression, an unknown flow reference, an unsupported element) that abort the
// pipeline before graph-wide validation can run and anchor a finding to an
// element. Grouping them under one slug lets the Problems panel render them
// alongside the structured findings; giving each its own element anchor is a
// follow-up that needs the compiler to collect rather than fail-fast on those
// stages.
const ruleCompile = "compile"

// validateResp is the POST /api/v1/validate result (ADR-0026): the structured
// problem list from a dry-run compile plus the engine version that produced it, so
// the Modeler's Problems panel can render findings and attribute "valid" to a
// specific Atlas capability level. Valid is true iff no problem is an error — a
// model with only warnings is deployable. Problems is never null (an empty list
// means a clean model), and each carries the element/severity/rule/message shape
// the panel links to the canvas.
type validateResp struct {
	Valid    bool               `json:"valid"`
	Version  string             `json:"version"`
	Problems []compiler.Problem `json:"problems"`
}

// handleValidate is the ADR-0026 dry-run validate endpoint: it runs the real
// parse → resolve → validate compiler pipeline over a submitted BPMN body and
// returns the structured problems, WITHOUT registering a definition, minting a
// version, or starting an instance. The compiler is the one source of validation
// truth (no rules are re-implemented here), so the panel's findings never drift
// from what a deploy would reject.
//
// It touches no engine or store state — a dry compile is pure CPU — so it runs
// off the run-loop goroutine, cheap enough for the panel to call debounced on
// edit. An invalid model is a 200 with valid=false and the findings in the body:
// the request succeeded, it is the *model* that has problems. Only a malformed
// request (an empty or over-large body) is a 4xx.
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

	resp := validateResp{Version: Version, Problems: []compiler.Problem{}}

	// A dry-run compile. Key/version are placeholders — nothing is registered, so
	// they never escape this call. ParseAll compiles every pool of a collaboration.
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(body))
	if err != nil {
		// A graph-wide fault surfaces as a *ValidationError carrying the full,
		// already-structured problem list (warnings included); every other compile
		// error is an opaque stage-1–4 fault, reported as one generic problem.
		var ve *compiler.ValidationError
		if errors.As(err, &ve) {
			resp.Problems = append(resp.Problems, ve.Problems...)
		} else {
			resp.Problems = append(resp.Problems, compiler.Problem{
				Severity: compiler.SeverityError,
				Rule:     ruleCompile,
				Message:  err.Error(),
			})
		}
		resp.Valid = false
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// The model compiled with no error-severity problems: gather each pool's
	// warnings (dead code, a split missing its default) so the panel still shows
	// them. Re-running Validate on the built process is exactly what the compile
	// gate did internally, minus the fatal-on-error step.
	for i := range deployables {
		resp.Problems = append(resp.Problems, compiler.Validate(deployables[i].Process)...)
	}
	resp.Valid = !compiler.HasErrors(resp.Problems)
	writeJSON(w, http.StatusOK, resp)
}
