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
// worker reference that resolves to nothing is one (ADR-0158): a model is
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

// deployWarningsOnLoop is the deploy-time preflight, in one place so every deploy
// path runs the same one (ADR-0287).
//
// It used to be four lines inside handleDeploy, which meant it ran for a single-model
// deploy and for nothing else: publishing an application (ADR-0128) and importing a
// release (ADR-0129) reach deployModel directly, and both deployed models whose worker
// references resolved to nothing without saying a word. Publish is the route most
// applications reach production through, so the one warning ADR-0158 exists to give —
// "this names a worker nobody configured" — was missing exactly where it was most
// needed, and the first token to park was again the first anybody heard of it.
//
// Reads the deployment registry, the worker store and the live registries, so it runs
// on the run-loop goroutine (invariant I3), inside the same closure as the deploy it
// describes. The model's own bytes are not needed here: foreignAtlasNamespaceWarnings
// reads them and runs off the loop, where its caller keeps it.
//
// applicationID is the application the deploy filed under; it decides which
// information model a data object's declared type resolves against (ADR-0230). A
// vocabulary that cannot be read costs the data-flow half and keeps the worker half,
// because a best-effort warning must not be able to suppress another one.
func (s *Server) deployWarningsOnLoop(deployed []deployedProcess, applicationID string) []string {
	vocab, vocabErr := s.infomodel.VocabularyOnLoop(applicationID)
	var out []string
	for _, d := range deployed {
		dep, ok := s.deployments[d.Key]
		if !ok || dep.cp == nil {
			continue
		}
		out = append(out, s.connectorWarnings(dep.cp)...)
		if vocabErr == nil {
			out = append(out, dataFlowWarnings(dep.cp, vocab)...)
		}
	}
	return out
}

// dedupeWarnings drops the exact repeats a bundle produces and keeps the order the
// warnings were found in. A namespace mistake is bound once per document, so several
// drafts carrying the same one say the same sentence; printing it per draft tells the
// reader nothing the first one did not. Only identical strings collapse — a warning
// that names its element is already distinct per draft and survives.
func dedupeWarnings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, w := range in {
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}
