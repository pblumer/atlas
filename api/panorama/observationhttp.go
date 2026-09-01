package panorama

import (
	"errors"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// ErrShuttingDown is what a [FactsResolver] returns when it could not take its
// run-loop turn because the server is closing. It is a sentinel rather than a
// message because the handler has to answer 503 rather than 500 for it: a document
// built from facts nobody gathered would report every element as unobserved, and
// "the server is going away" is a different thing to tell a caller than "something
// broke".
var ErrShuttingDown = errors.New("panorama: server is shutting down")

// FactsResolver supplies the runtime facts an observation document is projected
// from, already filtered for this caller.
//
// Unlike [CatalogResolver] it is called *off* the run loop, and takes its own loop
// turns for the parts that need one. That inversion is deliberate: gathering these
// facts includes asking peer servers (ADR-0189 §6), and holding the single writer
// for the duration of a network call is the one thing every other request on this
// server is waiting for it not to do (invariant I3). A resolver whose on-loop half
// could not run returns [ErrShuttingDown].
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

	facts, err := s.facts(r)
	switch {
	case errors.Is(err, ErrShuttingDown):
		// The resolver could not take its loop turn. Every fact would be absent and
		// every element reported as unobserved — a model that looks dead rather than
		// one that was not read.
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "collect observations: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, Observe(set, facts, s.now().Unix()))
}
