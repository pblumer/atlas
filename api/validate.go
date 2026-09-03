package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/compiler"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/infomodel"
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
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	problems, err := compiler.ValidateModel(bytes.NewReader(body))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "validate: "+err.Error())
		return
	}
	// Data-flow findings are appended when the caller says which application the
	// draft belongs to, because a data object's declared type resolves against *that*
	// application's information model and against nothing else
	// (ADR-0230). Without an application id the answer is
	// the compiler's alone, exactly as before: the panel on a draft filed nowhere is
	// not suddenly quieter or noisier.
	if appID := strings.TrimSpace(r.URL.Query().Get("applicationId")); appID != "" {
		problems = append(problems, s.dataFlowProblems(bytes.NewReader(body), appID)...)
	}
	// A nil slice would serialize as JSON null; the panel expects an array, so
	// normalize "no problems" to an empty list.
	if problems == nil {
		problems = []compiler.Problem{}
	}
	httpapi.JSON(w, http.StatusOK, validateResp{Version: Version, Problems: problems})
}

// dataFlowWarnings renders the information model's findings on a compiled process
// as deploy warnings (ADR-0230, slice 3).
//
// They are warnings by construction and never a refusal, for the same reason a
// connector reference that resolves to nothing is one (ADR-0158): a model is
// routinely deployed before the vocabulary it names exists, and before the activity
// that will write a datum has been drawn. The author is told at deploy rather than
// by the first token to read a null.
func dataFlowWarnings(cp *compiler.CompiledProcess, vocab *infomodel.Vocabulary) []string {
	problems := infomodel.CheckDataFlow(cp, vocab)
	if len(problems) == 0 {
		return nil
	}
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		if p.Element != "" {
			out = append(out, p.Element+": "+p.Message)
			continue
		}
		out = append(out, p.Message)
	}
	return out
}

// dataFlowProblems compiles the posted model a second time — once per pool — and
// checks each against the application's vocabulary.
//
// It compiles again rather than threading the processes out of ValidateModel
// because that function's contract is the compiler's own findings, and a dry run is
// cheap: it parses and discards, touching no engine or store state. A model that
// does not compile has no graph to check, and its compile errors are already in the
// list this appends to.
func (s *Server) dataFlowProblems(r io.Reader, applicationID string) []compiler.Problem {
	deployables, err := compiler.ParseAll(0, 1, r)
	if err != nil {
		return nil // the compiler already reported why
	}
	var vocab *infomodel.Vocabulary
	var vocabErr error
	s.do(func() { vocab, vocabErr = s.infomodel.VocabularyOnLoop(applicationID) })
	if vocabErr != nil {
		return nil // best-effort: the compiler's own findings still stand
	}
	var out []compiler.Problem
	for _, d := range deployables {
		out = append(out, infomodel.CheckDataFlow(d.Process, vocab)...)
	}
	return out
}
