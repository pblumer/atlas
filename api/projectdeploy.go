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
	// single failure refuses the bundle without deploying anything.
	refReports := make([]dmnRefValidationResp, 0, len(refs))
	invalidRefs := 0
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		if !res.Valid {
			invalidRefs++
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

	// Phase 3 (on-loop): compile every draft first, then — only if all compile —
	// deploy them, so a non-compilable draft refuses the bundle before any
	// definition is registered.
	var (
		compileFail string
		compErr     error
		persistErr  error
		deployed    []deployedProcess
	)
	s.do(func() {
		for _, d := range drafts {
			if _, err := compiler.ParseAll(1, 1, bytes.NewReader([]byte(d.XML))); err != nil {
				compileFail, compErr = d.ProcessID, err
				return
			}
		}
		deployedAt := time.Now().Unix()
		for _, d := range drafts {
			dps, _, pErr := s.deployModel([]byte(d.XML), deployedAt)
			if pErr != nil {
				persistErr = pErr
				return
			}
			deployed = append(deployed, dps...)
		}
	})
	switch {
	case compErr != nil:
		writeJSON(w, http.StatusConflict, projectDeployResp{
			ID: proj.ID, Name: proj.Name, Deployed: false,
			Reason:      fmt.Sprintf("draft %q does not compile: %s", compileFail, compErr.Error()),
			Definitions: []deployedProcess{}, References: refReports,
		})
	case persistErr != nil:
		writeError(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		if deployed == nil {
			deployed = []deployedProcess{}
		}
		writeJSON(w, http.StatusOK, projectDeployResp{
			ID: proj.ID, Name: proj.Name, Deployed: true,
			Definitions: deployed, References: refReports,
		})
	}
}
