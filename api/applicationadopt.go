package api

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
)

// Adopting what belongs to no application
// (ADR-draft-artifacts-belong-to-an-application).
//
// The artifacts written before every artifact had to belong to an application are
// listed under Not assigned. They are not filed anywhere automatically — which
// application a process belongs to is a decision about who owns it — so this is how
// they are filed: GET /api/v1/unassigned names them, and
// POST /api/v1/applications/{id}/adopt files a chosen set of them under one
// application in one request. The per-artifact move routes stay for moving between
// applications; adopt takes only what belongs to none, so it can never be used to
// empty somebody else's application.

// adoptRequest names what an adoption files, by the identity each artifact is
// addressed by elsewhere in the API.
type adoptRequest struct {
	// Processes are deployed process ids. Every version moves, and so do the other
	// pools of a collaboration, as with PATCH /api/v1/processes/{key}.
	Processes []string `json:"processes"`
	// Drafts are BPMN draft process ids.
	Drafts []string `json:"drafts"`
	// Forms are form ids.
	Forms []string `json:"forms"`
	// DecisionReferences are decision reference ids. A reference's unfiled decision
	// drafts move with it, because a decision draft belongs where its decision does.
	DecisionReferences []string `json:"decisionReferences"`
	// DecisionDrafts are decision draft ids.
	DecisionDrafts []string `json:"decisionDrafts"`
	// DecisionDeployments are decision deployment keys.
	DecisionDeployments []uint64 `json:"decisionDeployments"`
}

func (q adoptRequest) empty() bool {
	return len(q.Processes)+len(q.Drafts)+len(q.Forms)+len(q.DecisionReferences)+
		len(q.DecisionDrafts)+len(q.DecisionDeployments) == 0
}

// adoptProblem is why one named artifact cannot be adopted. Any problem refuses the
// whole request, before anything is written.
type adoptProblem struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Problem string `json:"problem"`
}

// adoptResp is what an adoption filed: per kind, the ids it moved. Processes lists
// the deployed version keys, so a caller sees that a definition's history came along.
type adoptResp struct {
	ApplicationID       string   `json:"applicationId"`
	Processes           []uint64 `json:"processes"`
	Drafts              []string `json:"drafts"`
	Forms               []string `json:"forms"`
	DecisionReferences  []string `json:"decisionReferences"`
	DecisionDrafts      []string `json:"decisionDrafts"`
	DecisionDeployments []uint64 `json:"decisionDeployments"`
}

// adoptPlan is what the first loop turn found: the records to rewrite, and every
// scope (current application or owner) the caller has to be an editor of.
type adoptPlan struct {
	versionKeys []uint64
	drafts      []draft
	forms       []form
	refs        []dmnRef
	dmnDrafts   []dmnDraft
	decisions   []persistedDecision
	scopes      [][2]string
	problems    []adoptProblem
}

func (p *adoptPlan) scope(projectID, ownerID string, seen map[[2]string]bool) {
	k := [2]string{projectID, ownerID}
	if !seen[k] {
		seen[k] = true
		p.scopes = append(p.scopes, k)
	}
}

func (p *adoptPlan) refuse(kind, id, problem string) {
	p.problems = append(p.problems, adoptProblem{Kind: kind, ID: id, Problem: problem})
}

// filedElsewhere is the problem of an artifact that already belongs to an
// application: adopt is not the move route.
const filedElsewhere = "already belongs to an application; move it with its own move route instead"

// planAdoptionOnLoop reads what q names and checks that each exists and belongs to
// no application. Must be called on the run loop.
func (s *Server) planAdoptionOnLoop(q adoptRequest) (adoptPlan, error) {
	var plan adoptPlan
	projs, err := s.projectsByID()
	if err != nil {
		return plan, err
	}
	seen := map[[2]string]bool{}

	// Processes: every version of the definition and of the other pools of its
	// collaboration, all of which have to be unfiled.
	keySeen := map[uint64]bool{}
	for _, pid := range q.Processes {
		var latest *deployment
		for _, k := range s.order {
			if d := s.deployments[k]; d != nil && d.ProcessID == pid {
				latest = d
			}
		}
		if latest == nil {
			plan.refuse("process", pid, "no deployed process with that id")
			continue
		}
		pools := map[string]bool{}
		for _, sib := range s.poolSiblings(latest) {
			pools[sib.ProcessID] = true
		}
		var keys []uint64
		filed := false
		for _, k := range s.order {
			d := s.deployments[k]
			if d == nil || !pools[d.ProcessID] {
				continue
			}
			if !unfiled(d.ProjectID, projs) {
				filed = true
				break
			}
			keys = append(keys, k)
			plan.scope(d.ProjectID, d.DeployedBy, seen)
		}
		if filed {
			plan.refuse("process", pid, "a version of it "+filedElsewhere+" (PATCH /api/v1/processes/{key})")
			continue
		}
		for _, k := range keys {
			if !keySeen[k] {
				keySeen[k] = true
				plan.versionKeys = append(plan.versionKeys, k)
			}
		}
	}

	for _, id := range q.Drafts {
		d, ok, err := s.drafts.Get(id)
		switch {
		case err != nil:
			return plan, err
		case !ok:
			plan.refuse("draft", id, "no draft with that process id")
		case !unfiled(d.ProjectID, projs):
			plan.refuse("draft", id, filedElsewhere+" (PATCH /api/v1/drafts/{id})")
		default:
			plan.drafts = append(plan.drafts, d)
			plan.scope(d.ProjectID, d.OwnerID, seen)
		}
	}

	for _, id := range q.Forms {
		f, ok, err := s.forms.Get(id)
		switch {
		case err != nil:
			return plan, err
		case !ok:
			plan.refuse("form", id, "no form with that id")
		case !unfiled(f.ProjectID, projs):
			plan.refuse("form", id, filedElsewhere+" (save it with another projectId)")
		default:
			plan.forms = append(plan.forms, f)
			plan.scope(f.ProjectID, f.OwnerID, seen)
		}
	}

	allDmnDrafts, err := s.dmnDrafts.LoadAll()
	if err != nil {
		return plan, err
	}
	dmnDraftSeen := map[string]bool{}
	for _, id := range q.DecisionReferences {
		ref, ok, err := s.dmnrefs.Get(id)
		switch {
		case err != nil:
			return plan, err
		case !ok:
			plan.refuse("decisionReference", id, "no decision reference with that id")
			continue
		case !unfiled(ref.ProjectID, projs):
			plan.refuse("decisionReference", id, filedElsewhere+" (PATCH /api/v1/dmnrefs/{id})")
			continue
		}
		plan.refs = append(plan.refs, ref)
		plan.scope(ref.ProjectID, ref.OwnerID, seen)
		for _, dd := range allDmnDrafts {
			if dd.RefID == ref.ID && unfiled(dd.ProjectID, projs) && !dmnDraftSeen[dd.ID] {
				dmnDraftSeen[dd.ID] = true
				plan.dmnDrafts = append(plan.dmnDrafts, dd)
				plan.scope(dd.ProjectID, dd.OwnerID, seen)
			}
		}
	}

	for _, id := range q.DecisionDrafts {
		if dmnDraftSeen[id] {
			continue
		}
		dd, ok, err := s.dmnDrafts.Get(id)
		switch {
		case err != nil:
			return plan, err
		case !ok:
			plan.refuse("decisionDraft", id, "no decision draft with that id")
		case !unfiled(dd.ProjectID, projs):
			plan.refuse("decisionDraft", id, filedElsewhere)
		default:
			dmnDraftSeen[id] = true
			plan.dmnDrafts = append(plan.dmnDrafts, dd)
			plan.scope(dd.ProjectID, dd.OwnerID, seen)
		}
	}

	for _, key := range q.DecisionDeployments {
		rec, ok, err := s.decisionDeploys.load(key)
		id := strconv.FormatUint(key, 10)
		switch {
		case err != nil:
			return plan, err
		case !ok:
			plan.refuse("decisionDeployment", id, "no decision deployment with that key")
		case !unfiled(rec.ApplicationID, projs):
			plan.refuse("decisionDeployment", id, filedElsewhere)
		default:
			plan.decisions = append(plan.decisions, rec)
			plan.scope(rec.ApplicationID, rec.DeployedBy, seen)
		}
	}
	return plan, nil
}

// handleAdoptArtifacts is POST /api/v1/applications/{id}/adopt: it files the named
// artifacts, each of which belongs to no application, under this one. It needs
// editor on the application and on each artifact's current scope (its owner's
// personal space, ADR-0071), as a move does. It is all or nothing as far as the
// checks go: one artifact that cannot be adopted refuses the request with 409 and
// writes nothing.
func (s *Server) handleAdoptArtifacts(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Definition))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var q adoptRequest
	if err := json.Unmarshal(body, &q); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if q.empty() {
		httpapi.Error(w, http.StatusBadRequest, "name at least one artifact to adopt")
		return
	}
	if code, msg := s.authorizeTargetProject(r, appID, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var (
		protected bool
		readErr   error
	)
	s.do(func() {
		if proj, ok, e := s.projects.Get(appID); e != nil {
			readErr = e
		} else if ok && proj.Protected {
			protected = true
		}
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read application: "+readErr.Error())
		return
	}
	if protected {
		httpapi.Error(w, http.StatusForbidden, "protected system application cannot be modified")
		return
	}

	// First turn: what the request names, and whether each may be adopted.
	var (
		plan    adoptPlan
		planErr error
	)
	s.do(func() { plan, planErr = s.planAdoptionOnLoop(q) })
	if planErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read artifacts: "+planErr.Error())
		return
	}
	if len(plan.problems) > 0 {
		httpapi.JSON(w, http.StatusConflict, map[string]any{
			"error":    "nothing was adopted: some of the named artifacts cannot be",
			"problems": plan.problems,
		})
		return
	}
	// Editor on every scope it takes something out of, outside the loop: authorizing
	// reads the application store through a do() of its own (I3).
	for _, sc := range plan.scopes {
		if code, msg := s.authorizeArtifact(r, sc[0], sc[1], ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}

	// Second turn: the writes, each re-checked against what the store holds now,
	// because a concurrent move could have filed it since the first turn. Such an
	// artifact is left where it is rather than taken from its new application.
	resp := adoptResp{ApplicationID: appID, Processes: []uint64{}, Drafts: []string{}, Forms: []string{},
		DecisionReferences: []string{}, DecisionDrafts: []string{}, DecisionDeployments: []uint64{}}
	var loadErr, writeErr error
	s.do(func() {
		projs, err := s.projectsByID()
		if err != nil {
			loadErr = err
			return
		}
		var keys []uint64
		for _, k := range plan.versionKeys {
			if d := s.deployments[k]; d != nil && unfiled(d.ProjectID, projs) {
				keys = append(keys, k)
			}
		}
		if resp.Processes, loadErr, writeErr = s.refileDeploymentsOnLoop(keys, appID); loadErr != nil || writeErr != nil {
			return
		}
		for _, d := range plan.drafts {
			cur, ok, err := s.drafts.Get(d.ProcessID)
			if err != nil {
				loadErr = err
				return
			}
			if !ok || !unfiled(cur.ProjectID, projs) {
				continue
			}
			cur.ProjectID = appID
			if writeErr = s.drafts.Save(cur); writeErr != nil {
				return
			}
			resp.Drafts = append(resp.Drafts, cur.ProcessID)
		}
		for _, f := range plan.forms {
			cur, ok, err := s.forms.Get(f.ID)
			if err != nil {
				loadErr = err
				return
			}
			if !ok || !unfiled(cur.ProjectID, projs) {
				continue
			}
			cur.ProjectID = appID
			if writeErr = s.forms.Save(cur); writeErr != nil {
				return
			}
			resp.Forms = append(resp.Forms, cur.ID)
		}
		for _, ref := range plan.refs {
			cur, ok, err := s.dmnrefs.Get(ref.ID)
			if err != nil {
				loadErr = err
				return
			}
			if !ok || !unfiled(cur.ProjectID, projs) {
				continue
			}
			cur.ProjectID = appID
			if writeErr = s.dmnrefs.Save(cur); writeErr != nil {
				return
			}
			resp.DecisionReferences = append(resp.DecisionReferences, cur.ID)
		}
		for _, dd := range plan.dmnDrafts {
			cur, ok, err := s.dmnDrafts.Get(dd.ID)
			if err != nil {
				loadErr = err
				return
			}
			if !ok || !unfiled(cur.ProjectID, projs) {
				continue
			}
			cur.ProjectID = appID
			if writeErr = s.dmnDrafts.Save(cur); writeErr != nil {
				return
			}
			resp.DecisionDrafts = append(resp.DecisionDrafts, cur.ID)
		}
		for _, rec := range plan.decisions {
			cur, ok, err := s.decisionDeploys.load(rec.Key)
			if err != nil {
				loadErr = err
				return
			}
			if !ok || !unfiled(cur.ApplicationID, projs) {
				continue
			}
			cur.ApplicationID = appID
			if writeErr = s.decisionDeploys.Save(cur); writeErr != nil {
				return
			}
			resp.DecisionDeployments = append(resp.DecisionDeployments, cur.Key)
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read artifacts: "+loadErr.Error())
	case writeErr != nil:
		// What was written before the failure stays written, and is filed correctly:
		// repeating the request adopts the rest, since what moved is no longer unfiled
		// and is skipped.
		httpapi.Error(w, http.StatusInternalServerError, "adopt: "+writeErr.Error()+
			" — what was adopted before the failure stays adopted; repeat the request for the rest")
	default:
		sort.Slice(resp.Processes, func(i, j int) bool { return resp.Processes[i] < resp.Processes[j] })
		httpapi.JSON(w, http.StatusOK, resp)
	}
}

// unassignedResp is what GET /api/v1/unassigned lists: everything the caller can see
// that belongs to no application, by kind.
type unassignedResp struct {
	Processes           []unassignedProcess    `json:"processes"`
	Drafts              []unassignedArtifact   `json:"drafts"`
	Forms               []unassignedArtifact   `json:"forms"`
	DecisionReferences  []unassignedArtifact   `json:"decisionReferences"`
	DecisionDrafts      []unassignedArtifact   `json:"decisionDrafts"`
	DecisionDeployments []unassignedDecisionDp `json:"decisionDeployments"`
}

type unassignedProcess struct {
	ProcessID string   `json:"processId"`
	Name      string   `json:"name,omitempty"`
	Versions  []uint64 `json:"versions"`
	// FiledVersions is how many of its versions do belong to an application, which
	// adopt refuses: those are moved as a whole with PATCH /api/v1/processes/{key}.
	FiledVersions int `json:"filedVersions,omitempty"`
}

type unassignedArtifact struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type unassignedDecisionDp struct {
	Key       uint64   `json:"key"`
	Decisions []string `json:"decisions"`
}

// handleListUnassigned is GET /api/v1/unassigned: what belongs to no application
// and the caller may see — the inventory a clean-up adopts from.
func (s *Server) handleListUnassigned(w http.ResponseWriter, r *http.Request) {
	out := unassignedResp{Processes: []unassignedProcess{}, Drafts: []unassignedArtifact{},
		Forms: []unassignedArtifact{}, DecisionReferences: []unassignedArtifact{},
		DecisionDrafts: []unassignedArtifact{}, DecisionDeployments: []unassignedDecisionDp{}}
	var loadErr error
	s.do(func() {
		projs, err := s.projectsByID()
		if err != nil {
			loadErr = err
			return
		}
		visible := func(projectID, ownerID string) bool {
			return unfiled(projectID, projs) && s.canViewArtifact(r, projectID, ownerID, projs)
		}
		byPID := map[string]int{}
		filed := map[string]int{}
		for _, k := range s.order {
			d := s.deployments[k]
			if d == nil {
				continue
			}
			if !unfiled(d.ProjectID, projs) {
				filed[d.ProcessID]++
				continue
			}
			if !s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs) {
				continue
			}
			i, ok := byPID[d.ProcessID]
			if !ok {
				i = len(out.Processes)
				byPID[d.ProcessID] = i
				out.Processes = append(out.Processes, unassignedProcess{ProcessID: d.ProcessID})
			}
			out.Processes[i].Versions = append(out.Processes[i].Versions, k)
			out.Processes[i].Name = d.Name
		}
		for i := range out.Processes {
			out.Processes[i].FiledVersions = filed[out.Processes[i].ProcessID]
		}
		var drafts []draft
		if drafts, loadErr = s.drafts.LoadAll(); loadErr != nil {
			return
		}
		for _, d := range drafts {
			if visible(d.ProjectID, d.OwnerID) {
				out.Drafts = append(out.Drafts, unassignedArtifact{ID: d.ProcessID, Name: d.Name})
			}
		}
		var forms []form
		if forms, loadErr = s.forms.LoadAll(); loadErr != nil {
			return
		}
		for _, f := range forms {
			if visible(f.ProjectID, f.OwnerID) {
				out.Forms = append(out.Forms, unassignedArtifact{ID: f.ID, Name: f.Name})
			}
		}
		var refs []dmnRef
		if refs, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		for _, ref := range refs {
			if visible(ref.ProjectID, ref.OwnerID) {
				out.DecisionReferences = append(out.DecisionReferences, unassignedArtifact{ID: ref.ID, Name: ref.Name})
			}
		}
		var dds []dmnDraft
		if dds, loadErr = s.dmnDrafts.LoadAll(); loadErr != nil {
			return
		}
		for _, dd := range dds {
			if visible(dd.ProjectID, dd.OwnerID) {
				out.DecisionDrafts = append(out.DecisionDrafts, unassignedArtifact{ID: dd.ID, Name: dd.Name})
			}
		}
		var decs []persistedDecision
		if decs, loadErr = s.decisionDeploys.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range decs {
			if !visible(rec.ApplicationID, rec.DeployedBy) {
				continue
			}
			row := unassignedDecisionDp{Key: rec.Key, Decisions: []string{}}
			for _, d := range rec.Decisions {
				row.Decisions = append(row.Decisions, d.ID)
			}
			out.DecisionDeployments = append(out.DecisionDeployments, row)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list unassigned artifacts: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
