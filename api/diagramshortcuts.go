package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// The diagrams somebody marked, and the ones they opened last (ADR-draft-diagram-shortcuts).
//
// The shop's favourites (ADR-0348) settled what a mark is, and this file takes that
// answer over unchanged for the diagrams of every Console area: a mark stores a kind and
// an id under a principal and nothing else. It is a bookmark and never an access grant.
// Whether the caller may still see the diagram is asked at read time, by the same scope
// the diagram's own list applies, so a share withdrawn or an application deleted changes
// what the list resolves and never what it stores.
//
// # What a diagram is here
//
// Four kinds, each the thing a canvas opens:
//
//   - process   — a BPMN process, by its process id: the draft when there is one the
//     caller may see, otherwise the newest deployed version
//   - decision  — a DMN decision, by its reference id or decision-draft id
//   - infomodel — an information model's class diagram
//   - panorama  — a Panorama architecture model
//
// A process is marked by its process id, not by a deployment key, because a key names
// one version and a mark names the process: deploying v2 must not leave the star on v1.
//
// # Marks and visits live in one record
//
// One file per account holds both lists. They are written on the same run loop, so the
// file never interleaves two writes, and a person's whole arrangement is backed up and
// restored as one thing.
//
// # Only what the caller may see is resolved, and only that
//
// A mark whose diagram the caller can no longer see comes back as its kind and id with
// available=false and no name: the id is the caller's own (they stored it), the name is
// the diagram's, and a mark must not be a way to keep reading a name a share has since
// withdrawn. It stays listed so it can be cleared. A visit that no longer resolves is
// left out — a recent list is a history, and a history entry nobody can open is noise.

// diagramKinds are the kinds a mark or a visit may name.
var diagramKinds = map[string]bool{
	"process": true, "decision": true, "infomodel": true, "panorama": true,
}

// recentDiagrams is how many visits one account keeps. Not a budget: the list is
// trimmed on every write rather than refused, so it cannot grow, and the number is a
// judgement about what a "recent" menu shows rather than a cost an operator tunes.
const recentDiagrams = 15

// maxDiagramID bounds an id before it is stored. The ids every kind uses are far
// shorter; this keeps a hand-written request from filing a megabyte under a star.
const maxDiagramID = 256

// diagramRef names one diagram.
type diagramRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// diagramVisit is one entry of the recent list.
type diagramVisit struct {
	diagramRef
	OpenedAt int64 `json:"openedAt"`
}

// diagramShortcuts is one person's marks and visits.
type diagramShortcuts struct {
	// Principal owns the record and is its key, as in the shop's favourites.
	Principal string `json:"principal"`
	// Favourites are kept in the order they were marked, oldest first: a list a
	// person arranges by marking reads best in the order they built it.
	Favourites []diagramRef `json:"favourites"`
	// Recent is newest first, one entry per diagram, at most recentDiagrams long.
	Recent    []diagramVisit `json:"recent"`
	UpdatedAt int64          `json:"updatedAt"`
}

type diagramShortcutStore = sidecar.Store[diagramShortcuts]

func newDiagramShortcutStore(dir string) (*diagramShortcutStore, error) {
	return sidecar.NewStore(dir, "diagramshortcutstore",
		func(rec diagramShortcuts) string { return rec.Principal })
}

// diagramShortcutsOf reads one person's record, answering an empty one where nothing
// is stored. Must run on the run loop.
func (s *Server) diagramShortcutsOf(principal string) (diagramShortcuts, error) {
	rec, ok, err := s.diagramShortcuts.Get(principal)
	if err != nil {
		return diagramShortcuts{}, err
	}
	if !ok {
		rec = diagramShortcuts{Principal: principal}
	}
	if rec.Favourites == nil {
		rec.Favourites = []diagramRef{}
	}
	if rec.Recent == nil {
		rec.Recent = []diagramVisit{}
	}
	return rec, nil
}

// whoseDiagramShortcuts answers which record this request may touch: always the
// caller's own, and there is no ?principal= — see ADR-0348 for why nobody needs to
// read another person's bookmarks.
func whoseDiagramShortcuts(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"favourites and recent diagrams belong to an account, and nobody is signed in")
		return "", false
	}
	return p.UserID, true
}

// diagramRefFrom reads and checks a kind and an id.
func diagramRefFrom(kind, id string) (diagramRef, string) {
	ref := diagramRef{Kind: strings.TrimSpace(kind), ID: strings.TrimSpace(id)}
	switch {
	case !diagramKinds[ref.Kind]:
		return ref, "a diagram is a process, a decision, an infomodel or a panorama model, not " + strings.TrimSpace(kind)
	case ref.ID == "":
		return ref, "name a diagram"
	case len(ref.ID) > maxDiagramID:
		return ref, "that is not a diagram id: it is longer than any diagram's"
	}
	return ref, ""
}

// resolvedDiagram is one entry as the Console shows it.
type resolvedDiagram struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Available is false for a mark the caller can no longer open. Nothing below it
	// is set then.
	Available bool   `json:"available"`
	Name      string `json:"name,omitempty"`
	// Href is the Console route that opens the diagram.
	Href string `json:"href,omitempty"`
	// ApplicationID is the application the diagram is filed into, empty for one
	// that is not.
	ApplicationID string `json:"applicationId,omitempty"`
	// Deployed says a process resolved to its deployed version because the caller
	// has no draft of it to open.
	Deployed bool  `json:"deployed,omitempty"`
	OpenedAt int64 `json:"openedAt,omitempty"`
}

// diagramShortcutsResp is what every route here answers.
type diagramShortcutsResp struct {
	Favourites []resolvedDiagram `json:"favourites"`
	Recent     []resolvedDiagram `json:"recent"`
}

// diagramResolver answers what one diagram is for this caller. It is built once per
// request on the run loop, so a list of marks reads the projects once.
type diagramResolver struct {
	s     *Server
	r     *http.Request
	projs map[string]project
}

func (s *Server) newDiagramResolver(r *http.Request) (*diagramResolver, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return nil, err
	}
	return &diagramResolver{s: s, r: r, projs: projs}, nil
}

// inApplication reports whether the caller may view what an application holds — the
// check the information model and Panorama services make (server.go), read here from
// the preloaded projects.
func (d *diagramResolver) inApplication(appID string) bool {
	p, ok := d.projs[appID]
	if !ok {
		return false
	}
	role := p.effectiveRole(httpapi.PrincipalFrom(d.r.Context()), d.s.authEnabled)
	return scopeRank(role) >= scopeRank(ScopeRoleViewer)
}

// resolve answers one diagram, or ok=false when the caller cannot open it.
func (d *diagramResolver) resolve(ref diagramRef) (resolvedDiagram, bool, error) {
	out := resolvedDiagram{Kind: ref.Kind, ID: ref.ID}
	esc := url.PathEscape(ref.ID)
	switch ref.Kind {
	case "process":
		dr, ok, err := d.s.drafts.Get(ref.ID)
		if err != nil {
			return out, false, err
		}
		if ok && d.s.canViewArtifact(d.r, dr.ProjectID, dr.OwnerID, d.projs) {
			out.Name, out.ApplicationID = dr.Name, dr.ProjectID
			out.Href = "#/modeler/draft/" + esc
			break
		}
		dep := d.s.latestDeploymentByProcessID(ref.ID)
		if dep == nil || !d.s.canViewArtifact(d.r, dep.ProjectID, "", d.projs) {
			return out, false, nil
		}
		out.Name, out.ApplicationID, out.Deployed = dep.Name, dep.ProjectID, true
		out.Href = "#/modeler/d/" + strconv.FormatUint(dep.Key, 10)
	case "decision":
		ref2, ok, err := d.s.dmnrefs.Get(ref.ID)
		if err != nil {
			return out, false, err
		}
		if ok && d.s.canViewArtifact(d.r, ref2.ProjectID, ref2.OwnerID, d.projs) {
			out.Name, out.ApplicationID = ref2.Name, ref2.ProjectID
			out.Href = "#/modeler/dmn/" + esc
			break
		}
		dd, ok, err := d.s.dmnDrafts.Get(ref.ID)
		if err != nil {
			return out, false, err
		}
		if !ok || !d.s.canViewArtifact(d.r, dd.ProjectID, dd.OwnerID, d.projs) {
			return out, false, nil
		}
		out.Name, out.ApplicationID = dd.Name, dd.ProjectID
		out.Href = "#/modeler/dmn/d/" + esc
	case "infomodel":
		m, ok, err := d.s.infomodelModels.Get(ref.ID)
		if err != nil {
			return out, false, err
		}
		if !ok || !d.inApplication(m.ApplicationID) {
			return out, false, nil
		}
		out.Name, out.ApplicationID = m.Name, m.ApplicationID
		out.Href = "#/data/m/" + esc
	case "panorama":
		m, ok, err := d.s.panoramaModels.Get(ref.ID)
		if err != nil {
			return out, false, err
		}
		if !ok || !d.inApplication(m.ApplicationID) {
			return out, false, nil
		}
		out.Name, out.ApplicationID = m.Name, m.ApplicationID
		out.Href = "#/panorama/models/" + esc
	default:
		return out, false, nil
	}
	if out.Name == "" {
		out.Name = ref.ID
	}
	out.Available = true
	return out, true, nil
}

// answer resolves a whole record for the caller. Must run on the run loop.
func (d *diagramResolver) answer(rec diagramShortcuts) (diagramShortcutsResp, error) {
	resp := diagramShortcutsResp{
		Favourites: make([]resolvedDiagram, 0, len(rec.Favourites)),
		Recent:     make([]resolvedDiagram, 0, len(rec.Recent)),
	}
	for _, ref := range rec.Favourites {
		got, ok, err := d.resolve(ref)
		if err != nil {
			return resp, err
		}
		if !ok {
			got = resolvedDiagram{Kind: ref.Kind, ID: ref.ID}
		}
		resp.Favourites = append(resp.Favourites, got)
	}
	for _, v := range rec.Recent {
		got, ok, err := d.resolve(v.diagramRef)
		if err != nil {
			return resp, err
		}
		if !ok {
			continue
		}
		got.OpenedAt = v.OpenedAt
		resp.Recent = append(resp.Recent, got)
	}
	return resp, nil
}

// handleListDiagramShortcuts answers the caller's marks and visits.
func (s *Server) handleListDiagramShortcuts(w http.ResponseWriter, r *http.Request) {
	principal, ok := whoseDiagramShortcuts(w, r)
	if !ok {
		return
	}
	var (
		resp diagramShortcutsResp
		ran  bool
		err  error
	)
	s.do(func() {
		ran = true
		var rec diagramShortcuts
		if rec, err = s.diagramShortcutsOf(principal); err != nil {
			return
		}
		var d *diagramResolver
		if d, err = s.newDiagramResolver(r); err != nil {
			return
		}
		resp, err = d.answer(rec)
	})
	s.writeDiagramShortcuts(w, ran, err, resp)
}

// handleMarkDiagram marks one diagram, and handleUnmarkDiagram clears the mark.
//
// One diagram per call, for the reason the shop's star gives: a replace-the-list write
// would drop whatever another open tab marked in between.
func (s *Server) handleMarkDiagram(w http.ResponseWriter, r *http.Request) {
	s.changeDiagramMark(w, r, true)
}

func (s *Server) handleUnmarkDiagram(w http.ResponseWriter, r *http.Request) {
	s.changeDiagramMark(w, r, false)
}

func (s *Server) changeDiagramMark(w http.ResponseWriter, r *http.Request, mark bool) {
	principal, ok := whoseDiagramShortcuts(w, r)
	if !ok {
		return
	}
	ref, bad := diagramRefFrom(r.PathValue("kind"), r.PathValue("id"))
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, "favourites: "+bad)
		return
	}
	var (
		resp    diagramShortcutsResp
		ran     bool
		full    bool
		missing bool
		err     error
	)
	s.do(func() {
		ran = true
		var rec diagramShortcuts
		if rec, err = s.diagramShortcutsOf(principal); err != nil {
			return
		}
		var d *diagramResolver
		if d, err = s.newDiagramResolver(r); err != nil {
			return
		}
		if mark {
			// A new mark must name a diagram the caller can open: a star on nothing,
			// or on something hidden, would be a list entry nobody could ever use. An
			// existing mark is never checked again — that is the read's job — and a
			// clear is never checked at all, so a mark that stopped resolving can
			// always be taken away.
			if _, ok, rerr := d.resolve(ref); rerr != nil {
				err = rerr
				return
			} else if !ok && !hasDiagramRef(rec.Favourites, ref) {
				missing = true
				return
			}
		}
		next, changed := withDiagramMark(rec.Favourites, ref, mark)
		if changed {
			if mark && len(next) > int(s.budgets().DiagramFavourites) {
				full = true
				return
			}
			rec.Favourites, rec.UpdatedAt = next, s.now()
			if err = s.diagramShortcuts.Save(rec); err != nil {
				return
			}
		}
		resp, err = d.answer(rec)
	})
	switch {
	case missing:
		// 404 whether the diagram is gone or hidden, as its own route answers.
		httpapi.Error(w, http.StatusNotFound, "favourites: no such diagram")
	case full:
		httpapi.Error(w, http.StatusUnprocessableEntity,
			"that is more favourite diagrams than one account may keep. A list nobody can "+
				"scan is not a shortcut, so clear one before marking another")
	default:
		s.writeDiagramShortcuts(w, ran, err, resp)
	}
}

// handleRecordDiagramVisit notes that the caller opened a diagram. The Console calls
// it when a canvas opens; it answers 204 and nothing else, because nothing waits on it.
//
// A deployed process is opened by its deployment key, which the Console has and the
// process id it does not, so a visit may name {"kind":"process","deploymentKey":N}
// and the server files it under that deployment's process id.
func (s *Server) handleRecordDiagramVisit(w http.ResponseWriter, r *http.Request) {
	principal, ok := whoseDiagramShortcuts(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind          string `json:"kind"`
		ID            string `json:"id"`
		DeploymentKey uint64 `json:"deploymentKey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.budgets().Request)).Decode(&body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "recent diagrams: "+err.Error())
		return
	}
	byKey := body.Kind == "process" && body.ID == "" && body.DeploymentKey != 0
	ref, bad := diagramRefFrom(body.Kind, body.ID)
	if bad != "" && !byKey {
		httpapi.Error(w, http.StatusBadRequest, "recent diagrams: "+bad)
		return
	}
	var (
		ran     bool
		missing bool
		err     error
	)
	s.do(func() {
		ran = true
		if byKey {
			dep, ok := s.deployments[body.DeploymentKey]
			if !ok {
				missing = true
				return
			}
			ref.ID = dep.ProcessID
		}
		var d *diagramResolver
		if d, err = s.newDiagramResolver(r); err != nil {
			return
		}
		// Only a diagram the caller can open is a visit: a mistyped URL is not
		// history, and a hidden diagram must not enter the list by its id.
		if _, ok, rerr := d.resolve(ref); rerr != nil || !ok {
			err, missing = rerr, rerr == nil
			return
		}
		var rec diagramShortcuts
		if rec, err = s.diagramShortcutsOf(principal); err != nil {
			return
		}
		now := s.now()
		rec.Recent = withDiagramVisit(rec.Recent, ref, now)
		rec.UpdatedAt = now
		err = s.diagramShortcuts.Save(rec)
	})
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable, "recent diagrams: this server is shutting down")
	case missing:
		httpapi.Error(w, http.StatusNotFound, "recent diagrams: no such diagram")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "recent diagrams: "+err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) writeDiagramShortcuts(w http.ResponseWriter, ran bool, err error, resp diagramShortcutsResp) {
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable, "favourites: this server is shutting down")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "favourites: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}

func hasDiagramRef(have []diagramRef, ref diagramRef) bool {
	for _, h := range have {
		if h == ref {
			return true
		}
	}
	return false
}

// withDiagramMark returns the marks with ref added at the end or taken out, and
// whether that changed anything. Marking what is marked and clearing what is not are
// the state asked for, not errors, and write nothing.
func withDiagramMark(have []diagramRef, ref diagramRef, mark bool) ([]diagramRef, bool) {
	if hasDiagramRef(have, ref) == mark {
		return have, false
	}
	if mark {
		return append(append(make([]diagramRef, 0, len(have)+1), have...), ref), true
	}
	out := make([]diagramRef, 0, len(have))
	for _, h := range have {
		if h != ref {
			out = append(out, h)
		}
	}
	return out, true
}

// withDiagramVisit puts ref at the front of the visits, dropping its earlier entry
// and whatever falls off the end.
func withDiagramVisit(have []diagramVisit, ref diagramRef, at int64) []diagramVisit {
	out := make([]diagramVisit, 0, recentDiagrams)
	out = append(out, diagramVisit{diagramRef: ref, OpenedAt: at})
	for _, v := range have {
		if len(out) == recentDiagrams {
			break
		}
		if v.diagramRef != ref {
			out = append(out, v)
		}
	}
	return out
}
