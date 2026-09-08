package formgen

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// HandleCapability answers what the editor needs before it offers to generate anything:
// whether there is an AI Worker to ask, and which ones this principal may name.
//
// It exists so the affordance can be absent rather than broken. A button that only ever
// produces "no AI Worker is configured" teaches an author that the feature does not
// work, which is a worse outcome than not having seen it.
func (s *Service) HandleCapability(w http.ResponseWriter, r *http.Request) {
	workers, err := s.workers(r)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the configured Workers: "+err.Error())
		return
	}
	if workers == nil {
		workers = []Worker{}
	}
	httpapi.JSON(w, http.StatusOK, Capability{Available: len(workers) > 0, Workers: workers})
}

// HandleGenerate writes one form and returns it unsaved.
//
// Nothing is stored: the response is a proposal the author opens in the editor, reads,
// and saves themselves through the ordinary save path — with the ordinary id check, the
// ordinary scope check, and their own name on it. That is ADR-0032's stance about
// generated diagrams, applied to forms for the same reason: the compiler gate and the
// author's own eye are what make a generated artifact safe to deploy, and skipping
// either to save a click would be trading the whole argument for the click.
func (s *Service) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.Limits.Generated))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, status, err := s.Generate(r, req)
	if err != nil {
		httpapi.Error(w, status, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}
