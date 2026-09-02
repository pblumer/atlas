package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// An artifact's id is its identity *and* its store key: a draft is filed under its
// process id, a form under the id a user task binds to. Renaming one is therefore a
// move, and two artifacts can never share an id — which is why a save that would land
// on an occupied id is refused (handleSaveDraft / handleSaveForm,
// ADR-draft-artifact-id-renames).
//
// A refusal at Save is the backstop, not the whole answer: the author has by then
// typed a new id, tabbed away, and built a mental model in which it took. These probes
// let the Modeler say so while the id is being typed — the field goes red the moment
// it collides — so the collision is a correction, not a failed save.
//
// The probe answers only what the refusal would already reveal: whether the id is
// free. The name of what holds it is added only when the caller may see that artifact
// (ADR-0071), so an id occupied inside a project they have no part in reads as taken
// and nothing more.
type idAvailability struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	// UsedBy names the artifact holding the id, and ProjectID the application it sits
	// in — both present only when the caller can see it, so a hidden artifact reports
	// a bare collision.
	UsedBy    string `json:"usedBy,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
}

// handleDraftIDAvailability reports whether a process id is free to save a draft
// under. It is the live check behind the Modeler's Process ID field.
func (s *Server) handleDraftIDAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp := idAvailability{ID: id, Available: true}
	var loadErr error
	s.do(func() {
		rec, ok, err := s.drafts.Get(id)
		if err != nil {
			loadErr = err
			return
		}
		if !ok {
			return
		}
		resp.Available = false
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		if s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
			resp.UsedBy = rec.Name
			resp.ProjectID = rec.ProjectID
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read draft: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// handleFormIDAvailability reports whether a form id is free. It is the live check
// behind the form editor's ID field — the one a user task's formId will bind to.
func (s *Server) handleFormIDAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp := idAvailability{ID: id, Available: true}
	var loadErr error
	s.do(func() {
		rec, ok, err := s.forms.Get(id)
		if err != nil {
			loadErr = err
			return
		}
		if !ok {
			return
		}
		resp.Available = false
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		if s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
			resp.UsedBy = rec.Name
			resp.ProjectID = rec.ProjectID
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}
