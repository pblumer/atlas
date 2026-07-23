package api

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// projectDeployResp reports the outcome of deploying a whole project: the BPMN
// definitions that were registered and the DMN references that were resolved and
// validated as part of the same action. Deployed is false when the bundle was
// refused (Reason says why) and nothing was registered.
type projectDeployResp struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Deployed    bool                   `json:"deployed"`
	Reason      string                 `json:"reason,omitempty"`
	Definitions []deployedProcess      `json:"definitions"`
	References  []dmnRefValidationResp `json:"references"`
}

// handleDeployProject deploys a project as a bundle (ADR-0034): it first resolves
// and validates every DMN reference (the deploy-time gate), then deploys every
// BPMN draft as a runnable definition. It is "validate all, then deploy all" — a
// draft that does not compile or a reference that does not validate refuses the
// whole bundle before anything is registered, so a broken artifact never leaves a
// half-deployed project.
//
// Honest limitations: the DMN references are validated as part of the bundle but
// not yet wired into the engine's runtime (the server does not execute DMN yet —
// the ADR-0014 follow-up); and the final BPMN deploy loop is not atomic against a
// mid-loop persist failure (same as a multi-pool deploy).
func (s *Server) handleDeployProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Phase 1 (on-loop): load the project and its artifacts.
	var (
		proj            project
		ok              bool
		getErr, loadErr error
		drafts          []draft
		refs            []dmnRef
	)
	s.do(func() {
		if proj, ok, getErr = s.projects.get(id); getErr != nil || !ok {
			return
		}
		var allDrafts []draft
		if allDrafts, loadErr = s.drafts.loadAll(); loadErr != nil {
			return
		}
		for _, d := range allDrafts {
			if d.ProjectID == id {
				drafts = append(drafts, d)
			}
		}
		var allRefs []dmnRef
		if allRefs, loadErr = s.dmnrefs.loadAll(); loadErr != nil {
			return
		}
		for _, rec := range allRefs {
			if rec.ProjectID == id {
				refs = append(refs, rec)
			}
		}
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+getErr.Error())
		return
	case !ok:
		writeError(w, http.StatusNotFound, "no project with that id")
		return
	case loadErr != nil:
		writeError(w, http.StatusInternalServerError, "list artifacts: "+loadErr.Error())
		return
	}

	// Phase 2 (off-loop): DMN preflight. Resolve + validate every reference; a
	// single failure refuses the bundle without deploying anything. For each valid
	// reference, keep its model XML and the decisions it provides, so a draft's
	// business rule tasks can be matched to a model below.
	refReports := make([]dmnRefValidationResp, 0, len(refs))
	var models []resolvedModel
	invalidRefs := 0
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		if !res.Valid {
			invalidRefs++
		} else {
			xml, err := s.dmnResolver.Resolve(r.Context(), rec.ModelRef)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
				return
			}
			models = append(models, resolvedModel{decisions: res.Decisions, xml: xml})
		}
		refReports = append(refReports, dmnRefValidationResp{
			ID: rec.ID, Name: rec.Name, ModelRef: rec.ModelRef,
			Resolved: res.Resolved, Valid: res.Valid,
			ModelName: res.ModelName, Decisions: res.Decisions, Message: res.Message,
		})
	}
	if invalidRefs > 0 {
		writeJSON(w, http.StatusConflict, projectDeployResp{
			ID: proj.ID, Name: proj.Name, Deployed: false,
			Reason:      fmt.Sprintf("%d DMN reference(s) unresolved or invalid", invalidRefs),
			Definitions: []deployedProcess{}, References: refReports,
		})
		return
	}

	// Phase 3-prep (off-loop): compile every draft and match it to the DMN model
	// that provides its business rule tasks' decisions. A draft that does not
	// compile, or references a decision no project model provides, refuses the
	// whole bundle before anything is registered.
	dmnForDraft := make([][]byte, len(drafts))
	for i, d := range drafts {
		deployables, err := compiler.ParseAll(1, 1, bytes.NewReader([]byte(d.XML)))
		if err != nil {
			writeJSON(w, http.StatusConflict, projectDeployResp{
				ID: proj.ID, Name: proj.Name, Deployed: false,
				Reason:      fmt.Sprintf("draft %q does not compile: %s", d.ProcessID, err.Error()),
				Definitions: []deployedProcess{}, References: refReports,
			})
			return
		}
		needed := draftDecisions(deployables)
		if len(needed) == 0 {
			continue
		}
		xml, ok := matchModel(models, needed)
		if !ok {
			writeJSON(w, http.StatusConflict, projectDeployResp{
				ID: proj.ID, Name: proj.Name, Deployed: false,
				Reason:      fmt.Sprintf("draft %q references decision(s) %v not provided by any DMN reference in this project", d.ProcessID, needed),
				Definitions: []deployedProcess{}, References: refReports,
			})
			return
		}
		dmnForDraft[i] = xml
	}

	// Phase 3 (on-loop): deploy each draft with its matched DMN model.
	var (
		persistErr error
		deployed   []deployedProcess
	)
	s.do(func() {
		deployedAt := time.Now().Unix()
		for i, d := range drafts {
			dps, _, pErr := s.deployModel([]byte(d.XML), dmnForDraft[i], deployedAt)
			if pErr != nil {
				persistErr = pErr
				return
			}
			deployed = append(deployed, dps...)
		}
	})
	if persistErr != nil {
		writeError(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
		return
	}
	if deployed == nil {
		deployed = []deployedProcess{}
	}
	writeJSON(w, http.StatusOK, projectDeployResp{
		ID: proj.ID, Name: proj.Name, Deployed: true,
		Definitions: deployed, References: refReports,
	})
}

// resolvedModel is one project DMN reference resolved for the bundle: its model
// XML and the decision names it provides.
type resolvedModel struct {
	decisions []string
	xml       []byte
}

// draftDecisions is the distinct set of DMN decision ids referenced by every
// process in one compiled draft.
func draftDecisions(deployables []compiler.Deployable) []string {
	seen := map[string]bool{}
	var out []string
	for i := range deployables {
		for _, id := range deployables[i].Process.BusinessRuleDecisions() {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// matchModel returns the XML of a model that provides every needed decision, or
// ok=false if none does. A draft's decisions must all live in a single model
// (the DMN registry holds one model per process) — spanning models is not
// supported yet.
func matchModel(models []resolvedModel, needed []string) ([]byte, bool) {
	for _, m := range models {
		have := make(map[string]bool, len(m.decisions))
		for _, d := range m.decisions {
			have[d] = true
		}
		covers := true
		for _, n := range needed {
			if !have[n] {
				covers = false
				break
			}
		}
		if covers {
			return m.xml, true
		}
	}
	return nil, false
}
