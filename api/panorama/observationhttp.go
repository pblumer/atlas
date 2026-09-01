package panorama

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// FactsResolver supplies the runtime facts an observation document is projected
// from, already filtered for this caller. Like [CatalogResolver] it runs on the
// API run loop and must not call Loop.Do recursively.
type FactsResolver func(r *http.Request) (Facts, error)

// HandleObservations projects a model's Atlas bindings onto what this server
// currently sees (ADR-0189 §6).
//
// It is a read of the model plus a read of the engine, and it writes nothing: the
// stored document is not touched, and no observation is persisted. That is the
// rule the record states as "the declarative XML is never mutated by polling", and
// the reason this is a separate route rather than a field on the model — a caller
// who wants the drawing must be able to have it without paying for a scan of every
// live instance.
func (s *Service) HandleObservations(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	set, err := ExtractBindings([]byte(model.XML))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read bindings: "+err.Error())
		return
	}
	if s.facts == nil {
		// No source was wired at all. Answering with a document of unbound
		// observations would look like a landscape where nothing is running.
		httpapi.Error(w, http.StatusNotImplemented, "this server observes nothing")
		return
	}

	var (
		facts Facts
		ran   bool
		opErr error
	)
	s.loop.Do(func() { ran = true; facts, opErr = s.facts(r) })
	// The loop declines to run anything once it is closing, which would leave every
	// fact absent and every element reported as unobserved — a model that looks
	// dead rather than one that was not read. Same guard as bindings and the mesh.
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "collect observations: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, Observe(set, facts, s.now().Unix()))
}
