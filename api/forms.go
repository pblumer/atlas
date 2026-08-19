package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// maxFormBytes caps a stored form schema. form-js schemas are small JSON
// documents; this is a generous ceiling that still refuses a runaway upload.
const maxFormBytes = 1 << 20 // 1 MiB

// formMeta is a form's listing metadata — everything but the schema, so a list
// stays small (the Tasks app and Modeler fetch the schema per-form when needed).
type formMeta struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	SavedAt   int64  `json:"savedAt"`
}

func metaOf(f form) formMeta {
	return formMeta{ID: f.ID, Name: f.Name, ProjectID: f.ProjectID, SavedAt: f.SavedAt}
}

// isJSONObject reports whether raw is a syntactically valid JSON object. A
// form-js schema is always an object ({type, components, ...}); this rejects a
// bare string/number/array/null that happens to be valid JSON.
func isJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(raw)
}

// handleSaveForm creates or overwrites a form (ADR-0028). Body:
// {"id": "...", "name": "...", "schema": {...}, "projectId": "..."}. The id is
// required and the schema must be a JSON object (a form-js schema); saving the
// same id overwrites, so this doubles as update. The store is schema-agnostic —
// rendering the form is a UI concern — so it only checks the schema is valid
// JSON, not that it is a well-formed form-js document.
func (s *Server) handleSaveForm(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFormBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		ProjectID string          `json:"projectId"`
		Schema    json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := strings.TrimSpace(payload.ID)
	if id == "" {
		httpapi.Error(w, http.StatusBadRequest, "form id is required")
		return
	}
	if !isJSONObject(payload.Schema) {
		httpapi.Error(w, http.StatusBadRequest, "schema must be a JSON form-js document (an object)")
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = id
	}
	rec := form{
		ID:        id,
		Name:      name,
		ProjectID: strings.TrimSpace(payload.ProjectID),
		SavedAt:   time.Now().Unix(),
		Schema:    string(payload.Schema),
	}
	var (
		saveErr   error
		protected bool
	)
	s.do(func() {
		// A form filed under the protected system project is platform-managed
		// (ADR-0122): refuse overwriting it, for every caller.
		if existing, ok, e := s.forms.Get(id); e == nil && ok && existing.ProjectID == systemProjectID {
			protected = true
			return
		}
		saveErr = s.forms.Save(rec)
	})
	switch {
	case protected:
		httpapi.Error(w, http.StatusForbidden, "protected system form cannot be modified")
		return
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save form: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, metaOf(rec))
}

// handleListForms lists stored forms (metadata only), most recently saved first.
// An optional ?projectId= narrows the list to one project's forms.
func (s *Server) handleListForms(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []formMeta{}
	var loadErr error
	s.do(func() {
		var recs []form
		recs, loadErr = s.forms.LoadAll()
		for _, f := range recs {
			if filter != "" && f.ProjectID != filter {
				continue
			}
			list = append(list, metaOf(f))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list forms: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleGetForm returns one form including its schema — the Tasks app fetches it
// to render a task's bound form. 404 if no form has that id.
func (s *Server) handleGetForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec     form
		ok      bool
		loadErr error
	)
	s.do(func() { rec, ok, loadErr = s.forms.Get(id) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+loadErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no form with that id")
	default:
		// Schema is stored as a raw JSON string; emit it as JSON, not a
		// re-escaped string, so the client gets the form-js document directly.
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"id":        rec.ID,
			"name":      rec.Name,
			"projectId": rec.ProjectID,
			"savedAt":   rec.SavedAt,
			"schema":    json.RawMessage(rec.Schema),
		})
	}
}

// handleDeleteForm removes a form by id. Deleting a form a user task still
// references is allowed — the binding then resolves to nothing and the Tasks app
// shows the task without a form — so this does not scan deployments.
func (s *Server) handleDeleteForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		delErr    error
		protected bool
	)
	s.do(func() {
		if existing, ok, e := s.forms.Get(id); e == nil && ok && existing.ProjectID == systemProjectID {
			protected = true
			return
		}
		delErr = s.forms.Delete(id)
	})
	switch {
	case protected:
		httpapi.Error(w, http.StatusForbidden, "protected system form cannot be deleted")
		return
	case delErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete form: "+delErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"id": id})
}
