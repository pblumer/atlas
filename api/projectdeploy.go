package api

import (
	"bytes"
	"context"
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
	// Deploying a project's artifacts is a write on the project, so it needs the
	// editor role (ADR-0071). This gates the design-time action; it does not
	// isolate the resulting running instances, which stay out of scope.
	if code, msg := s.checkProjectRole(r, proj, ScopeRoleEditor); code != 0 {
		writeError(w, code, msg)
		return
	}
	// A protected system project is deployed only by the startup bootstrap
	// (ADR-0119); refuse deploying it through the API, for every caller.
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
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
	dmnForDraft := make([][][]byte, len(drafts))
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
		xmls, ok := coverModels(models, needed)
		if !ok {
			writeJSON(w, http.StatusConflict, projectDeployResp{
				ID: proj.ID, Name: proj.Name, Deployed: false,
				Reason:      fmt.Sprintf("draft %q references decision(s) %v not provided by any DMN reference in this project", d.ProcessID, needed),
				Definitions: []deployedProcess{}, References: refReports,
			})
			return
		}
		dmnForDraft[i] = xmls
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

// dmnForDeployBody resolves the DMN model a single (non-project) deploy's business
// rule tasks need, so a local decision is bundled with the process instead of
// deployed model-less. A model-less business-rule deploy is the trap that started
// this: its tasks create DMN jobs that can never evaluate, and because every deploy
// drives all pending jobs, one such job then fails every future deploy. It mirrors
// the project deploy's matching but over all DMN references passed in.
//
// Returns (xmls, "", nil) when stored models together provide every needed local
// decision (one model when they share it, several when the tasks span models);
// (nil, "", nil) when the body needs none (no business rule tasks, or only central
// connector decisions, which carry no local model); (nil, reason, nil) when a needed
// decision is in no model, so the caller refuses (409) instead of deploying a
// process that can never run; or (nil, "", err) on an infrastructure failure. It
// resolves and compiles models (I/O + CPU), so it runs OFF the run loop; the
// reference records are read on the loop and passed in.
func (s *Server) dmnForDeployBody(ctx context.Context, body []byte, refs []dmnRef) ([][]byte, string, error) {
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(body))
	if err != nil {
		return nil, "", nil // a compile error is surfaced (as a 400) by the deploy itself
	}
	needed := draftDecisions(deployables)
	if len(needed) == 0 {
		return nil, "", nil
	}
	var models []resolvedModel
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(ctx, rec.ModelRef)
		if err != nil {
			return nil, "", err
		}
		if !res.Valid {
			continue
		}
		xml, err := s.dmnResolver.Resolve(ctx, rec.ModelRef)
		if err != nil {
			return nil, "", err
		}
		models = append(models, resolvedModel{decisions: res.Decisions, xml: xml})
	}
	if xmls, ok := coverModels(models, needed); ok {
		return xmls, "", nil
	}
	return nil, fmt.Sprintf("this diagram's business rule task(s) reference decision(s) %v that no DMN model provides — create the decision (or add its reference) in Atlas, then deploy", needed), nil
}

// coverModels returns the XML of the models that together provide every needed
// decision — one model when they all live together, several when a process's
// business rule tasks reference decisions across different models (the registry
// holds a list per process, so a deployment can bundle more than one). Each needed
// decision is assigned to the first model that provides it, and the distinct chosen
// models are returned in model order for determinism. ok is false if any needed
// decision is in no model at all, in which case the deploy is refused rather than
// registering a business rule task that can never evaluate.
func coverModels(models []resolvedModel, needed []string) ([][]byte, bool) {
	provider := map[string]int{} // decision id → index of the model that provides it
	for i, m := range models {
		for _, d := range m.decisions {
			if _, ok := provider[d]; !ok {
				provider[d] = i
			}
		}
	}
	used := map[int]bool{}
	for _, n := range needed {
		i, ok := provider[n]
		if !ok {
			return nil, false
		}
		used[i] = true
	}
	var out [][]byte
	for i := range models {
		if used[i] {
			out = append(out, models[i].xml)
		}
	}
	return out, true
}
