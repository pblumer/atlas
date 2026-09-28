package api

import (
	"errors"
	"net/http"

	"github.com/pblumer/atlas/dmn"

	"github.com/pblumer/atlas/api/httpapi"
)

// decisionCatalogItem is one thing a business rule task can call, offered to the
// Modeler's picker: a decision or a decision service, which model it lives in, and
// its self-described inputs and output, so the panel can list them and auto-fill
// input mappings and the result variable instead of making the author type ids and
// parameters by hand (ADR-0050).
type decisionCatalogItem struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Model    string              `json:"model"`
	ModelRef string              `json:"modelRef"`
	Inputs   []dmn.DecisionField `json:"inputs"`
	Output   dmn.DecisionField   `json:"output"`
	// Service marks the published interface over part of a model rather than one
	// decision in it, and Members names the decisions that interface is made of. A
	// task calls either with the same one string, so the picker cannot tell them
	// apart from the shape of the entry — it has to be told.
	Service bool     `json:"service,omitempty"`
	Members []string `json:"members,omitempty"`
	// Internal is the part of Members the service encapsulates rather than publishes.
	// The picker needs the two apart: calling an output decision gets the service's
	// own answer by a longer route, calling an encapsulated one reaches past the
	// interface into a working the service was meant to stay free to change.
	Internal []string `json:"internal,omitempty"`
	// Deployed says the engine can run this decision right now. It is not the same
	// question as where the entry came from: an entry with a model handle may be
	// deployed or not, and the picker has to be able to say which. Left out of the
	// JSON when false, because the panel asks "is it deployed" and absence is the
	// answer it wants.
	//
	// Worth saying in the list rather than only at Publish: a task wired to a
	// decision that is in the model and has never been deployed saves cleanly,
	// looks right, and is refused later by the deploy preflight — in a message
	// about a decision the author picked minutes ago and has no reason to suspect.
	Deployed bool `json:"deployed,omitempty"`
}

// handleListDecisions returns what the DMN references offer a business rule task
// (optionally narrowed to one application with ?projectId=), each with its inputs
// and output. The reference records are read on the run loop; resolving and
// compiling each model — I/O and CPU — runs off it, exactly like the per-reference
// validate endpoint (ADR-0034). A model that fails to resolve is skipped so one
// broken reference does not blank the whole catalog.
//
// "What the references offer" is decisions *and* decision services, because a task
// calls either with the same one string. Listing only the decisions left a service
// to arrive by the deployed route below — with no model handle, and therefore in no
// application — so the one thing an author is meant to call sat under "other",
// below every decision it is made of.
func (s *Server) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	var (
		refs     []dmnRef
		deployed []dmn.DeployedDecision
		loadErr  error
	)
	s.do(func() {
		var all []dmnRef
		if all, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		for _, rec := range all {
			if filter != "" && rec.ProjectID != filter {
				continue
			}
			// A reference the caller cannot view contributes no decisions (ADR-0071).
			// Deployed decisions (below) stay: they are engine-wide runtime state.
			if !s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
				continue
			}
			refs = append(refs, rec)
		}
		// Read in the same turn as the references: the registry's compiled models are
		// run-loop-owned state, and what the picker needs from them — which decisions
		// are runnable — is wanted for every listing, scoped or not.
		deployed = s.dmnRegistry.DeployedDecisions()
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}

	deployedID := make(map[string]bool, len(deployed))
	for _, d := range deployed {
		deployedID[d.ID] = true
	}

	out := []decisionCatalogItem{}
	seenModel := map[string]bool{} // the same model may be referenced twice; describe it once
	seenDecision := map[string]bool{}
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
			seenDecision[d.ID] = true
			out = append(out, decisionCatalogItem{
				ID:       d.ID,
				Name:     d.Name,
				Model:    modelName,
				ModelRef: rec.ModelRef,
				Inputs:   d.Inputs,
				Output:   d.Output,
				Service:  d.Service,
				Members:  d.Members,
				Internal: d.Internal,
				Deployed: deployedID[d.ID],
			})
		}
	}

	// Beyond referenced models, offer the decisions of *deployed* models — those the
	// engine can actually evaluate — so an author can pick a decision that is deployed
	// even when no DMN reference artifact exists for it (a decision deployed directly,
	// or whose reference was later removed). These carry no editable model handle
	// (ModelRef stays empty), and a decision already offered by a reference wins, so
	// the picker never lists the same decision twice. Only the unscoped catalog
	// includes them: they are engine-wide, not owned by any one project (ADR-0034), so
	// a project-scoped listing stays limited to the project's own references. Reading
	// the registry's compiled models is run-loop-owned state, so it runs on the loop.
	if filter == "" {
		for _, d := range deployed {
			if seenDecision[d.ID] {
				continue
			}
			seenDecision[d.ID] = true
			out = append(out, decisionCatalogItem{
				ID:       d.ID,
				Name:     d.Name,
				Model:    d.Model,
				ModelRef: "",
				Inputs:   d.Inputs,
				Output:   d.Output,
				// No Members: the registry describes what it can evaluate, not how the
				// model was drawn. An entry reaching the catalog this way has no model
				// handle either, so there is nothing for a member marker to point at.
				Service:  d.Service,
				Deployed: true,
			})
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
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
	s.do(func() { rec, ok, getErr = s.dmnrefs.Get(id) })
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no dmn reference with that id")
		return
	}
	if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	g, err := s.dmnValidator.Graph(r.Context(), rec.ModelRef)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, g)
}

// handleDmnModelXML returns the raw DMN model XML for a model handle (modelRef), so
// the embedded DMN editor can open an existing decision for editing (ADR-0062).
// Resolving the model bytes is concurrent-safe I/O that touches no engine state, so
// it runs off the run loop. An unresolved handle is a 404, not a 500, so a dangling
// reference reads as "model missing" rather than an infrastructure failure.
func (s *Server) handleDmnModelXML(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	xml, err := s.dmnResolver.Resolve(r.Context(), ref)
	if err != nil {
		if errors.Is(err, dmn.ErrNotFound) {
			httpapi.Error(w, http.StatusNotFound, "no DMN model matches the handle: "+ref)
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	// A stored model usually carries no diagram — an agent, temis or a hand writes
	// the logic, not the picture — and dmn-js draws nothing without one. The diagram
	// is completed here, on the way to the editor, the way a layout-less BPMN model
	// gets one on the way to bpmn-js (ADR-0325).
	_, _ = w.Write(dmn.EnsureDiagram(xml))
}
