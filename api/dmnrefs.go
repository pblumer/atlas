package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// dmnRefResp is the JSON shape of a DMN reference for the Modeler.
type dmnRefResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ModelRef  string `json:"modelRef"`
	ProjectID string `json:"projectId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

func toDmnRefResp(r dmnRef) dmnRefResp {
	return dmnRefResp{ID: r.ID, Name: r.Name, ModelRef: r.ModelRef, ProjectID: r.ProjectID, CreatedAt: r.CreatedAt}
}

// handleCreateDmnRef creates a DMN reference: a pointer to a temis-authored
// decision model (ADR-0034 Phase 2). It stores only a display name and the temis
// model handle — never DMN XML — so Atlas organizes the reference without
// becoming a DMN editor. An optional projectId files it into a project and, when
// present, must name an existing one. Body: {"name","modelRef","projectId"?}.
func (s *Server) handleCreateDmnRef(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name      string `json:"name"`
		ModelRef  string `json:"modelRef"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	modelRef := strings.TrimSpace(payload.ModelRef)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "reference name is required")
		return
	}
	if modelRef == "" {
		httpapi.Error(w, http.StatusBadRequest, "a temis model reference is required")
		return
	}
	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	rec := dmnRef{ID: id, Name: name, ModelRef: modelRef, ProjectID: payload.ProjectID, OwnerID: s.artifactOwnerOnCreate(r), CreatedAt: time.Now().Unix()}
	// Filing a reference into a project needs editor on that project (ADR-0071),
	// which also enforces that it exists (400 otherwise).
	if rec.ProjectID != "" {
		if code, msg := s.authorizeTargetProject(r, rec.ProjectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	var saveErr error
	s.do(func() { saveErr = s.dmnrefs.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create dmn reference: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, toDmnRefResp(rec))
}

// handleListDmnRefs lists DMN references, oldest first. An optional ?projectId=
// query narrows the list to one project's references.
func (s *Server) handleListDmnRefs(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []dmnRefResp{}
	var loadErr error
	s.do(func() {
		var recs []dmnRef
		if recs, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			if filter != "" && rec.ProjectID != filter {
				continue
			}
			// Inherit the project's scope (ADR-0071): hide references the caller
			// cannot view.
			if !s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
				continue
			}
			list = append(list, toDmnRefResp(rec))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleUpdateDmnRef updates a DMN reference. It can move the reference to a
// different project (or to Ungrouped when projectId is empty) and/or rename it.
// Both fields are optional and applied only when present in the body, so a move
// never clears the name and a rename never moves the reference — the Modeler's
// "edit a DMN in place" flow renames a reference (to track an in-editor decision
// rename) without disturbing which project it is filed under. Body:
// {"projectId"?: "...", "name"?: "..."}. A present projectId, when non-empty, must
// name an existing project; a present name must not be blank.
func (s *Server) handleUpdateDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// Pointer fields distinguish "absent" from "present but empty": an absent
	// field leaves that attribute untouched, while a present empty projectId is a
	// deliberate move to Ungrouped.
	var payload struct {
		ProjectID *string `json:"projectId"`
		Name      *string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var newName string
	if payload.Name != nil {
		newName = strings.TrimSpace(*payload.Name)
		if newName == "" {
			httpapi.Error(w, http.StatusBadRequest, "reference name cannot be blank")
			return
		}
	}
	// Load the reference (to learn its current scope and carry it into the save).
	var (
		rec    dmnRef
		found  bool
		getErr error
	)
	s.do(func() { rec, found, getErr = s.dmnrefs.Get(id) })
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no dmn reference with that id")
		return
	}
	// Editing (rename or move) needs editor on the reference's current scope; a
	// move into a non-empty project also needs editor on that target (ADR-0071).
	if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if payload.ProjectID != nil && *payload.ProjectID != "" {
		if code, msg := s.authorizeTargetProject(r, *payload.ProjectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	if payload.ProjectID != nil {
		rec.ProjectID = *payload.ProjectID
	}
	if payload.Name != nil {
		rec.Name = newName
	}
	var saveErr error
	s.do(func() { saveErr = s.dmnrefs.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "update dmn reference: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, toDmnRefResp(rec))
}

// handleDeleteDmnRef removes a DMN reference. Deleting an absent reference
// succeeds, so the operation is idempotent.
func (s *Server) handleDeleteDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Deletion inherits the reference's project scope (ADR-0071): editor required,
	// an invisible reference 404s, an absent one still succeeds (idempotent).
	var (
		projectID string
		ownerID   string
		found     bool
		getErr    error
	)
	s.do(func() {
		rec, ok, e := s.dmnrefs.Get(id)
		if e != nil {
			getErr = e
			return
		}
		found = ok
		projectID = rec.ProjectID
		ownerID = rec.OwnerID
	})
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	}
	if found {
		if code, msg := s.authorizeArtifact(r, projectID, ownerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	var delErr error
	s.do(func() { delErr = s.dmnrefs.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete dmn reference: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
