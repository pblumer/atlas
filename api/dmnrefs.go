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
	rec := dmnRef{ID: id, Name: name, ModelRef: modelRef, ProjectID: payload.ProjectID, CreatedAt: time.Now().Unix()}
	var (
		saveErr, projErr error
		unknownProject   bool
	)
	s.do(func() {
		if rec.ProjectID != "" {
			_, ok, e := s.projects.Get(rec.ProjectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
		}
		saveErr = s.dmnrefs.Save(rec)
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "create dmn reference: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, toDmnRefResp(rec))
	}
}

// handleListDmnRefs lists DMN references, oldest first. An optional ?projectId=
// query narrows the list to one project's references.
func (s *Server) handleListDmnRefs(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []dmnRefResp{}
	var loadErr error
	s.do(func() {
		var recs []dmnRef
		recs, loadErr = s.dmnrefs.LoadAll()
		for _, rec := range recs {
			if filter != "" && rec.ProjectID != filter {
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
	var (
		found, unknownProject    bool
		getErr, projErr, saveErr error
		view                     dmnRefResp
	)
	s.do(func() {
		rec, ok, e := s.dmnrefs.Get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.ProjectID != nil {
			if *payload.ProjectID != "" {
				_, pok, pe := s.projects.Get(*payload.ProjectID)
				if pe != nil {
					projErr = pe
					return
				}
				if !pok {
					unknownProject = true
					return
				}
			}
			rec.ProjectID = *payload.ProjectID
		}
		if payload.Name != nil {
			rec.Name = newName
		}
		if saveErr = s.dmnrefs.Save(rec); saveErr != nil {
			return
		}
		view = toDmnRefResp(rec)
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no dmn reference with that id")
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update dmn reference: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, view)
	}
}

// handleDeleteDmnRef removes a DMN reference. Deleting an absent reference
// succeeds, so the operation is idempotent.
func (s *Server) handleDeleteDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.dmnrefs.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete dmn reference: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
