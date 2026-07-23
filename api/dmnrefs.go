package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
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
// decision model (ADR-0033 Phase 2). It stores only a display name and the temis
// model handle — never DMN XML — so Atlas organizes the reference without
// becoming a DMN editor. An optional projectId files it into a project and, when
// present, must name an existing one. Body: {"name","modelRef","projectId"?}.
func (s *Server) handleCreateDmnRef(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name      string `json:"name"`
		ModelRef  string `json:"modelRef"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	modelRef := strings.TrimSpace(payload.ModelRef)
	if name == "" {
		writeError(w, http.StatusBadRequest, "reference name is required")
		return
	}
	if modelRef == "" {
		writeError(w, http.StatusBadRequest, "a temis model reference is required")
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	rec := dmnRef{ID: id, Name: name, ModelRef: modelRef, ProjectID: payload.ProjectID, CreatedAt: time.Now().Unix()}
	var (
		saveErr, projErr error
		unknownProject   bool
	)
	s.do(func() {
		if rec.ProjectID != "" {
			_, ok, e := s.projects.get(rec.ProjectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
		}
		saveErr = s.dmnrefs.save(rec)
	})
	switch {
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "create dmn reference: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, toDmnRefResp(rec))
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
		recs, loadErr = s.dmnrefs.loadAll()
		for _, rec := range recs {
			if filter != "" && rec.ProjectID != filter {
				continue
			}
			list = append(list, toDmnRefResp(rec))
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMoveDmnRef reassigns a DMN reference to a different project (or to
// Ungrouped when projectId is empty). Body: {"projectId": "..."}. A non-empty
// projectId must name an existing project.
func (s *Server) handleMoveDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		found, unknownProject    bool
		getErr, projErr, saveErr error
		view                     dmnRefResp
	)
	s.do(func() {
		rec, ok, e := s.dmnrefs.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.ProjectID != "" {
			_, pok, pe := s.projects.get(payload.ProjectID)
			if pe != nil {
				projErr = pe
				return
			}
			if !pok {
				unknownProject = true
				return
			}
		}
		rec.ProjectID = payload.ProjectID
		if saveErr = s.dmnrefs.save(rec); saveErr != nil {
			return
		}
		view = toDmnRefResp(rec)
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no dmn reference with that id")
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "move dmn reference: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// handleDeleteDmnRef removes a DMN reference. Deleting an absent reference
// succeeds, so the operation is idempotent.
func (s *Server) handleDeleteDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.dmnrefs.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete dmn reference: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
