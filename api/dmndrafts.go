package api

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The HTTP surface of the decision draft (ADR-0321): save, list,
// read back, discard. It is the decision editor's Save, and nothing else reads it —
// in particular the application publish does not, because a publish ships the model
// a reference resolves, never a draft somebody happens to have open.
//
// A draft is not validated. That is the point of it: an unfinished decision table is
// exactly what needs somewhere to live, and refusing to store one because it does
// not compile would put us back where we started. Validation happens where it
// matters, when the draft is written to the model.

// dmnDraftResp is a decision draft without its XML — the listing shape.
type dmnDraftResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RefID     string `json:"refId,omitempty"`
	ModelRef  string `json:"modelRef,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	SavedAt   int64  `json:"savedAt"`
}

func toDmnDraftResp(d dmnDraft) dmnDraftResp {
	return dmnDraftResp{ID: d.ID, Name: d.Name, RefID: d.RefID, ModelRef: d.ModelRef, ProjectID: d.ProjectID, SavedAt: d.SavedAt}
}

// dmnModelName reads the name a DMN model gives itself — the <definitions name>,
// which is the file, and which is what every other path here already calls a model
// by: the upload, the import, the model listing and the documentation record all
// take it. The artifact a draft holds is the file, not a decision inside it, and a
// file may hold several; naming it after whichever decision happens to come first
// makes the listing disagree with the editor's own header and renames the artifact
// when somebody reorders the model.
//
// A decision's name, then its id, remain as fallbacks, because a model being drafted
// may not have named itself yet and something is better to show than nothing.
//
// It is a plain attribute read, not a compile: a draft is stored whether or not it
// is a valid model, so nothing here may depend on it being one.
func dmnModelName(body []byte) string {
	var d struct {
		Name      string `xml:"name,attr"`
		Decisions []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"decision"`
	}
	if err := xml.Unmarshal(body, &d); err != nil {
		return ""
	}
	if n := strings.TrimSpace(d.Name); n != "" {
		return n
	}
	for _, dec := range d.Decisions {
		if n := strings.TrimSpace(dec.Name); n != "" {
			return n
		}
		if n := strings.TrimSpace(dec.ID); n != "" {
			return n
		}
	}
	return ""
}

// handleSaveDmnDraft stores decision work in progress. Body:
//
//	{"id"?, "refId"?, "modelRef"?, "projectId"?, "xml"}
//
// The id is the draft's key. An editing session on a decision that is already in
// the model passes the reference's id, so the draft sits under the decision it
// belongs to and re-saving overwrites it rather than piling up copies. A decision
// that is not in the model yet has no reference to be keyed by, so an absent id
// mints one (prefixed, so the two key spaces cannot be confused) and the response
// carries it — the editor then addresses the draft by it.
//
// Unlike a BPMN draft the id is not read out of the document: a DMN model's
// decision names are not the artifact's identity (a model may hold several), the
// reference is. So this takes JSON rather than raw XML.
func (s *Server) handleSaveDmnDraft(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ID        string `json:"id"`
		RefID     string `json:"refId"`
		ModelRef  string `json:"modelRef"`
		ProjectID string `json:"projectId"`
		XML       string `json:"xml"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	modelXML := strings.TrimSpace(payload.XML)
	if modelXML == "" {
		httpapi.Error(w, http.StatusBadRequest, "empty decision draft: expected DMN XML in \"xml\"")
		return
	}
	id := strings.TrimSpace(payload.ID)
	refID := strings.TrimSpace(payload.RefID)
	// A draft on a decision that exists is keyed by that decision, so the two agree
	// by construction; a caller that sends only one of them gets the other.
	if id == "" && refID != "" {
		id = refID
	}
	if refID == "" && id != "" && !strings.HasPrefix(id, dmnDraftIDPrefix) {
		refID = id
	}

	// What this draft continues, if anything: its project and its creator carry
	// across a re-save rather than being restamped (ADR-0071).
	var (
		existing dmnDraft
		existed  bool
		getErr   error
	)
	if id != "" {
		s.do(func() { existing, existed, getErr = s.dmnDrafts.Get(id) })
		if getErr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read decision draft: "+getErr.Error())
			return
		}
	}
	if existed {
		if code, msg := s.authorizeArtifact(r, existing.ProjectID, existing.OwnerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	// A draft on a decision that is in the model belongs where that decision belongs,
	// and needs editor on it (ADR-0071) — the server decides both rather than taking
	// the client's word, so a draft cannot be filed under somebody else's decision or
	// dragged out of its application by a save.
	var (
		ref      dmnRef
		refFound bool
	)
	if refID != "" {
		s.do(func() { ref, refFound, getErr = s.dmnrefs.Get(refID) })
		if getErr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
			return
		}
		if refFound {
			if code, msg := s.authorizeArtifact(r, ref.ProjectID, ref.OwnerID, ScopeRoleEditor); code != 0 {
				httpapi.Error(w, code, msg)
				return
			}
		}
	}
	// Otherwise: a new draft is filed where the body says (empty is Ungrouped), and an
	// existing one keeps its application unless the body names a different one.
	projectID := existing.ProjectID
	switch {
	case refFound:
		projectID = ref.ProjectID
	case !existed || payload.ProjectID != "":
		projectID = payload.ProjectID
	}
	if projectID != "" && !refFound && (!existed || projectID != existing.ProjectID) {
		if code, msg := s.authorizeTargetProject(r, projectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	if id == "" {
		minted, err := newID()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
			return
		}
		id = dmnDraftIDPrefix + minted
	}
	ownerID := existing.OwnerID
	if !existed {
		ownerID = s.artifactOwnerOnCreate(r)
	}
	modelRef := strings.TrimSpace(payload.ModelRef)
	if refFound {
		modelRef = ref.ModelRef
	} else if modelRef == "" {
		modelRef = existing.ModelRef
	}
	rec := dmnDraft{
		ID: id, Name: dmnModelName([]byte(modelXML)), RefID: refID, ModelRef: modelRef,
		ProjectID: projectID, OwnerID: ownerID, SavedAt: time.Now().Unix(), XML: modelXML,
	}
	var (
		saveErr, projErr error
		protectedProject bool
	)
	s.do(func() {
		// Backstop the scope check for protected system projects (ADR-0122), which
		// effectiveRole grants admins and owners on: writing into one is refused for
		// all. Inside the writer's turn, so it cannot be outrun by the write.
		if rec.ProjectID != "" {
			proj, ok, e := s.projects.Get(rec.ProjectID)
			if e != nil {
				projErr = e
				return
			}
			if ok && proj.Protected {
				protectedProject = true
				return
			}
		}
		saveErr = s.dmnDrafts.Save(rec)
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save decision draft: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, toDmnDraftResp(rec))
	}
}

// handleListDmnDrafts lists decision drafts, most recently saved first. An optional
// ?projectId= narrows the list to one application's (ADR-0034).
func (s *Server) handleListDmnDrafts(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []dmnDraftResp{}
	var loadErr error
	s.do(func() {
		var recs []dmnDraft
		if recs, loadErr = s.dmnDrafts.LoadAll(); loadErr != nil {
			return
		}
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		for _, d := range recs {
			if filter != "" && d.ProjectID != filter {
				continue
			}
			// Membership inherits from the application (ADR-0071): a draft in one
			// the caller cannot view is not listed.
			if !s.canViewArtifact(r, d.ProjectID, d.OwnerID, projs) {
				continue
			}
			list = append(list, toDmnDraftResp(d))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list decision drafts: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleDmnDraftXML returns a decision draft's DMN XML, so the editor reopens the
// work rather than the model it has not been written to.
func (s *Server) handleDmnDraftXML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec     dmnDraft
		ok      bool
		readErr error
	)
	s.do(func() { rec, ok, readErr = s.dmnDrafts.Get(id) })
	switch {
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read decision draft: "+readErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no decision draft with that id")
	default:
		if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleViewer); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(rec.XML))
	}
}

// handleDeleteDmnDraft discards a decision draft. It is what the editor calls once
// the work has been written to the model — a draft exists only while it differs
// from it — and what "Discard draft" calls. Deleting an absent draft succeeds, so
// the operation is idempotent.
func (s *Server) handleDeleteDmnDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		projectID string
		ownerID   string
		found     bool
		getErr    error
	)
	s.do(func() {
		rec, ok, e := s.dmnDrafts.Get(id)
		if e != nil {
			getErr = e
			return
		}
		found = ok
		projectID = rec.ProjectID
		ownerID = rec.OwnerID
	})
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read decision draft: "+getErr.Error())
		return
	}
	if found {
		if code, msg := s.authorizeArtifact(r, projectID, ownerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	var delErr error
	s.do(func() { delErr = s.dmnDrafts.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete decision draft: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
