package api

import (
	"encoding/json"
	"errors"
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
	seen        map[[2]string]bool
	problems    []adoptProblem
}

func (p *adoptPlan) scope(projectID, ownerID string) {
	k := [2]string{projectID, ownerID}
	if p.seen == nil {
		p.seen = map[[2]string]bool{}
	}
	if !p.seen[k] {
		p.seen[k] = true
		p.scopes = append(p.scopes, k)
	}
}

func (p *adoptPlan) refuse(kind, id, problem string) {
	p.problems = append(p.problems, adoptProblem{Kind: kind, ID: id, Problem: problem})
}

// filedElsewhere is the problem of an artifact that already belongs to an
// application: adopt is not the move route.
const filedElsewhere = "already belongs to an application; move it with its own move route instead"

// adoptKind is one kind of design-time record adopt files, addressed by a string id:
// how it is read and written, which application it names and whose personal space
// it is in while it names none. One description per kind keeps the check and the
// write the same for all of them.
type adoptKind[T any] struct {
	kind    string // the kind as a problem names it
	missing string // the problem of an id that names nothing
	route   string // where a filed one is moved instead
	get     func(id string) (T, bool, error)
	save    func(T) error
	id      func(T) string
	tag     func(T) string // the application it is filed under
	owner   func(T) string
	retag   func(T, string) T
}

// plan checks each id: it names a record, and that record belongs to no application.
func (k adoptKind[T]) plan(ids []string, projs map[string]project, p *adoptPlan) ([]T, error) {
	var out []T
	for _, id := range ids {
		rec, ok, err := k.get(id)
		switch {
		case err != nil:
			return out, err
		case !ok:
			p.refuse(k.kind, id, k.missing)
		case !unfiled(k.tag(rec), projs):
			p.refuse(k.kind, id, filedElsewhere+k.route)
		default:
			out = append(out, rec)
			p.scope(k.tag(rec), k.owner(rec))
		}
	}
	return out, nil
}

// write files each planned record under appID, re-read first: a record a concurrent
// move filed since the plan is left where it now is, and one deleted since is
// skipped. Returns the ids it moved. Must be called on the run loop.
func (k adoptKind[T]) write(recs []T, appID string, projs map[string]project) ([]string, error) {
	moved := []string{}
	for _, planned := range recs {
		cur, ok, err := k.get(k.id(planned))
		if err != nil {
			return moved, err
		}
		if !ok || !unfiled(k.tag(cur), projs) {
			continue
		}
		if err := k.save(k.retag(cur, appID)); err != nil {
			return moved, err
		}
		moved = append(moved, k.id(cur))
	}
	return moved, nil
}

func (s *Server) adoptDrafts() adoptKind[draft] {
	return adoptKind[draft]{
		kind: "draft", missing: "no draft with that process id", route: " (PATCH /api/v1/drafts/{id})",
		get: s.drafts.Get, save: s.drafts.Save,
		id: func(d draft) string { return d.ProcessID }, tag: func(d draft) string { return d.ProjectID },
		owner: func(d draft) string { return d.OwnerID },
		retag: func(d draft, app string) draft { d.ProjectID = app; return d },
	}
}

func (s *Server) adoptForms() adoptKind[form] {
	return adoptKind[form]{
		kind: "form", missing: "no form with that id", route: " (save it with another projectId)",
		get: s.forms.Get, save: s.forms.Save,
		id: func(f form) string { return f.ID }, tag: func(f form) string { return f.ProjectID },
		owner: func(f form) string { return f.OwnerID },
		retag: func(f form, app string) form { f.ProjectID = app; return f },
	}
}

func (s *Server) adoptRefs() adoptKind[dmnRef] {
	return adoptKind[dmnRef]{
		kind: "decisionReference", missing: "no decision reference with that id", route: " (PATCH /api/v1/dmnrefs/{id})",
		get: s.dmnrefs.Get, save: s.dmnrefs.Save,
		id: func(r dmnRef) string { return r.ID }, tag: func(r dmnRef) string { return r.ProjectID },
		owner: func(r dmnRef) string { return r.OwnerID },
		retag: func(r dmnRef, app string) dmnRef { r.ProjectID = app; return r },
	}
}

func (s *Server) adoptDmnDrafts() adoptKind[dmnDraft] {
	return adoptKind[dmnDraft]{
		kind: "decisionDraft", missing: "no decision draft with that id",
		get: s.dmnDrafts.Get, save: s.dmnDrafts.Save,
		id: func(d dmnDraft) string { return d.ID }, tag: func(d dmnDraft) string { return d.ProjectID },
		owner: func(d dmnDraft) string { return d.OwnerID },
		retag: func(d dmnDraft, app string) dmnDraft { d.ProjectID = app; return d },
	}
}

// adoptDecisions addresses a decision deployment by its key in decimal, the only
// form in which a key reaches it.
func (s *Server) adoptDecisions() adoptKind[persistedDecision] {
	return adoptKind[persistedDecision]{
		kind: "decisionDeployment", missing: "no decision deployment with that key",
		get: s.decisionDeploys.Get, save: s.decisionDeploys.Save,
		id:    func(d persistedDecision) string { return decisionKeyName(d.Key) },
		tag:   func(d persistedDecision) string { return d.ApplicationID },
		owner: func(d persistedDecision) string { return d.DeployedBy },
		retag: func(d persistedDecision, app string) persistedDecision { d.ApplicationID = app; return d },
	}
}

// planAdoptionOnLoop reads what q names and checks that each exists and belongs to
// no application. Must be called on the run loop.
func (s *Server) planAdoptionOnLoop(q adoptRequest) (adoptPlan, error) {
	var plan adoptPlan
	projs, err := s.projectsByID()
	if err != nil {
		return plan, err
	}
	s.planProcessAdoptionOnLoop(q.Processes, projs, &plan)
	if plan.drafts, err = s.adoptDrafts().plan(q.Drafts, projs, &plan); err != nil {
		return plan, err
	}
	if plan.forms, err = s.adoptForms().plan(q.Forms, projs, &plan); err != nil {
		return plan, err
	}
	if plan.refs, err = s.adoptRefs().plan(q.DecisionReferences, projs, &plan); err != nil {
		return plan, err
	}
	// A reference takes its unfiled decision drafts along: a decision draft belongs
	// where its decision does. The ones named on their own are planned after, once.
	allDmnDrafts, err := s.dmnDrafts.LoadAll()
	if err != nil {
		return plan, err
	}
	taken := map[string]bool{}
	for _, ref := range plan.refs {
		for _, dd := range allDmnDrafts {
			if dd.RefID == ref.ID && unfiled(dd.ProjectID, projs) {
				taken[dd.ID] = true
				plan.dmnDrafts = append(plan.dmnDrafts, dd)
				plan.scope(dd.ProjectID, dd.OwnerID)
			}
		}
	}
	var named []string
	for _, id := range q.DecisionDrafts {
		if !taken[id] {
			named = append(named, id)
		}
	}
	more, err := s.adoptDmnDrafts().plan(named, projs, &plan)
	if err != nil {
		return plan, err
	}
	plan.dmnDrafts = append(plan.dmnDrafts, more...)
	keys := make([]string, 0, len(q.DecisionDeployments))
	for _, k := range q.DecisionDeployments {
		keys = append(keys, decisionKeyName(k))
	}
	plan.decisions, err = s.adoptDecisions().plan(keys, projs, &plan)
	return plan, err
}

// planProcessAdoptionOnLoop plans the deployed processes: every version of the
// definition and of the other pools of its collaboration, all of which have to be
// unfiled. Must be called on the run loop.
func (s *Server) planProcessAdoptionOnLoop(processIDs []string, projs map[string]project, plan *adoptPlan) {
	keySeen := map[uint64]bool{}
	for _, pid := range processIDs {
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
		var versions []*deployment
		filed := false
		for _, k := range s.order {
			if d := s.deployments[k]; d != nil && pools[d.ProcessID] {
				versions = append(versions, d)
				filed = filed || !unfiled(d.ProjectID, projs)
			}
		}
		if filed {
			plan.refuse("process", pid, "a version of it "+filedElsewhere+" (PATCH /api/v1/processes/{key})")
			continue
		}
		for _, d := range versions {
			plan.scope(d.ProjectID, d.DeployedBy)
			if !keySeen[d.Key] {
				keySeen[d.Key] = true
				plan.versionKeys = append(plan.versionKeys, d.Key)
			}
		}
	}
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

	// First turn: what the request names, and whether each may be adopted. The
	// application's record is read in the same turn: a protected system application
	// (ADR-0122) is no destination, for any caller.
	var (
		plan      adoptPlan
		planErr   error
		protected bool
	)
	s.do(func() {
		if proj, ok, e := s.projects.Get(appID); e != nil || (ok && proj.Protected) {
			planErr, protected = e, e == nil
			return
		}
		plan, planErr = s.planAdoptionOnLoop(q)
	})
	switch {
	case planErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read artifacts: "+planErr.Error())
		return
	case protected:
		httpapi.Error(w, http.StatusForbidden, "protected system application cannot be modified")
		return
	case len(plan.problems) > 0:
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
	// because a concurrent move could have filed it since the first turn.
	var (
		resp     adoptResp
		writeErr error
	)
	s.do(func() { resp, writeErr = s.adoptOnLoop(plan, appID) })
	if writeErr != nil {
		// What was written before the failure stays written, and is filed correctly:
		// repeating the request adopts the rest, since what moved is no longer unfiled
		// and is skipped.
		httpapi.Error(w, http.StatusInternalServerError, "adopt: "+writeErr.Error()+
			" — what was adopted before the failure stays adopted; repeat the request for the rest")
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// adoptOnLoop writes what plan found under appID. Must be called on the run loop.
func (s *Server) adoptOnLoop(plan adoptPlan, appID string) (adoptResp, error) {
	resp := adoptResp{ApplicationID: appID, Processes: []uint64{}, DecisionDeployments: []uint64{}}
	projs, err := s.projectsByID()
	if err != nil {
		return resp, err
	}
	var keys []uint64
	for _, k := range plan.versionKeys {
		if d := s.deployments[k]; d != nil && unfiled(d.ProjectID, projs) {
			keys = append(keys, k)
		}
	}
	moved, loadErr, persistErr := s.refileDeploymentsOnLoop(keys, appID)
	resp.Processes = append(resp.Processes, moved...)
	if err := errors.Join(loadErr, persistErr); err != nil {
		return resp, err
	}
	sort.Slice(resp.Processes, func(i, j int) bool { return resp.Processes[i] < resp.Processes[j] })
	if resp.Drafts, err = s.adoptDrafts().write(plan.drafts, appID, projs); err != nil {
		return resp, err
	}
	if resp.Forms, err = s.adoptForms().write(plan.forms, appID, projs); err != nil {
		return resp, err
	}
	if resp.DecisionReferences, err = s.adoptRefs().write(plan.refs, appID, projs); err != nil {
		return resp, err
	}
	if resp.DecisionDrafts, err = s.adoptDmnDrafts().write(plan.dmnDrafts, appID, projs); err != nil {
		return resp, err
	}
	decided, err := s.adoptDecisions().write(plan.decisions, appID, projs)
	for _, id := range decided {
		key, _ := strconv.ParseUint(id, 10, 64)
		resp.DecisionDeployments = append(resp.DecisionDeployments, key)
	}
	return resp, err
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
