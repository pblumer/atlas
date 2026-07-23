package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// projectView is the JSON shape of a project for the Modeler. Artifacts is the
// number of artifacts (Phase 1: BPMN drafts) currently tagged with this project.
type projectView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Artifacts int    `json:"artifacts"`
}

// newProjectID mints a random, URL-safe project id. Projects are design-time
// organizational state, not engine facts (ADR-0024), so a server-generated
// random id is fine — it never enters the event log and is not replayed.
func newProjectID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handleCreateProject creates a named project. Body: {"name": "..."}.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	id, err := newProjectID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	now := time.Now().Unix()
	rec := project{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	var saveErr error
	s.do(func() { saveErr = s.projects.save(rec) })
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "create project: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectView{ID: id, Name: name, CreatedAt: now, UpdatedAt: now})
}

// handleListProjects lists projects (oldest first) with a live count of the
// artifacts tagged into each.
func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	list := []projectView{}
	var loadErr error
	s.do(func() {
		var projs []project
		if projs, loadErr = s.projects.loadAll(); loadErr != nil {
			return
		}
		var drafts []draft
		if drafts, loadErr = s.drafts.loadAll(); loadErr != nil {
			return
		}
		counts := map[string]int{}
		for _, d := range drafts {
			if d.ProjectID != "" {
				counts[d.ProjectID]++
			}
		}
		for _, p := range projs {
			list = append(list, projectView{
				ID:        p.ID,
				Name:      p.Name,
				CreatedAt: p.CreatedAt,
				UpdatedAt: p.UpdatedAt,
				Artifacts: counts[p.ID],
			})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list projects: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRenameProject renames a project. Body: {"name": "..."}.
func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	var (
		found           bool
		getErr, saveErr error
		countErr        error
		view            projectView
	)
	s.do(func() {
		rec, ok, e := s.projects.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		rec.Name = name
		rec.UpdatedAt = time.Now().Unix()
		if saveErr = s.projects.save(rec); saveErr != nil {
			return
		}
		found = true
		n, e := s.countDraftsInProject(id)
		if e != nil {
			countErr = e
			return
		}
		view = projectView{ID: rec.ID, Name: rec.Name, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt, Artifacts: n}
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+getErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no project with that id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "rename project: "+saveErr.Error())
	case countErr != nil:
		writeError(w, http.StatusInternalServerError, "count artifacts: "+countErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// handleDeleteProject removes a project. It is idempotent (deleting an absent
// project succeeds). Artifacts tagged with the id are intentionally left in
// place; they read as Ungrouped once the project is gone (ADR-0024).
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.projects.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete project: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// countDraftsInProject counts drafts tagged with a project id. It must be called
// on the run-loop goroutine (inside do).
func (s *Server) countDraftsInProject(id string) (int, error) {
	drafts, err := s.drafts.loadAll()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range drafts {
		if d.ProjectID == id {
			n++
		}
	}
	return n, nil
}
