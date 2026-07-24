package api

import (
	"net/http"

	"github.com/pblumer/atlas/dmn"
)

// decisionCatalogItem is one decision offered to the Modeler's business-rule-task
// picker: which model it lives in and its self-described inputs and output, so the
// panel can list decisions and auto-fill input mappings and the result variable
// instead of making the author type ids and parameters by hand (ADR-0050).
type decisionCatalogItem struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Model    string              `json:"model"`
	ModelRef string              `json:"modelRef"`
	Inputs   []dmn.DecisionField `json:"inputs"`
	Output   dmn.DecisionField   `json:"output"`
}

// handleListDecisions returns the decisions available from the DMN references
// (optionally narrowed to one project with ?projectId=), each with its inputs and
// output. The reference records are read on the run loop; resolving and compiling
// each model — I/O and CPU — runs off it, exactly like the per-reference validate
// endpoint (ADR-0034). A model that fails to resolve is skipped so one broken
// reference does not blank the whole catalog.
func (s *Server) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	var (
		refs    []dmnRef
		loadErr error
	)
	s.do(func() {
		var all []dmnRef
		all, loadErr = s.dmnrefs.loadAll()
		for _, rec := range all {
			if filter != "" && rec.ProjectID != filter {
				continue
			}
			refs = append(refs, rec)
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}

	out := []decisionCatalogItem{}
	seenModel := map[string]bool{} // the same model may be referenced twice; describe it once
	for _, rec := range refs {
		if seenModel[rec.ModelRef] {
			continue
		}
		seenModel[rec.ModelRef] = true
		modelName, decisions, err := s.dmnValidator.Describe(r.Context(), rec.ModelRef)
		if err != nil {
			continue // infra failure on one model; the validate endpoint reports it per-ref
		}
		for _, d := range decisions {
			out = append(out, decisionCatalogItem{
				ID:       d.ID,
				Name:     d.Name,
				Model:    modelName,
				ModelRef: rec.ModelRef,
				Inputs:   d.Inputs,
				Output:   d.Output,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDmnRefGraph returns one DMN reference's decision requirements graph for the
// read-only viewer: the reference record is read on the run loop, then the model is
// resolved and compiled off it (like the validate endpoint). An unresolved or
// invalid model is a normal 200 carrying a message and no nodes, so the viewer can
// explain the state.
func (s *Server) handleDmnRefGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec    dmnRef
		ok     bool
		getErr error
	)
	s.do(func() { rec, ok, getErr = s.dmnrefs.get(id) })
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	case !ok:
		writeError(w, http.StatusNotFound, "no dmn reference with that id")
		return
	}
	g, err := s.dmnValidator.Graph(r.Context(), rec.ModelRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}
