package panorama

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// ContextResolver asks whichever historical stores are wired about one element's
// bound values, and returns one result per (source, value) pair it considered.
//
// Like [FactsResolver] it is called *off* the run loop and takes its own turns for
// the parts that need one: every adapter here talks to a store outside this
// process, and holding the single writer across that is the one thing every other
// request is waiting for it not to do (invariant I3).
//
// It returns a result for every pair it considered, including the ones it could not
// answer. A resolver that returned only its successes would make "no store is
// wired" and "the store holds nothing" the same answer, which is exactly the
// conflation the six states exist to prevent.
type ContextResolver func(r *http.Request, queries []ContextQuery) ([]ContextResult, error)

// WithContextResolver wires the historical-context adapters onto a service.
//
// It is a separate call rather than another parameter of [New] because it is
// genuinely optional: a server with no log store and no metrics store is a
// complete Atlas, and its Panorama answers every other question exactly as before.
func (s *Service) WithContextResolver(resolve ContextResolver) *Service {
	s.context = resolve
	return s
}

// HandleContext answers "has it been like this" for one element (ADR-0189 P5b).
//
// It writes nothing and stores nothing — the stores it reads are external, owned by
// somebody else, and retained on their own terms. That is the whole point: ADR-0189
// rejected copying remote metrics and logs into a Panorama database by name, so
// this asks the question every time rather than keeping the answer.
func (s *Service) HandleContext(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	if s.context == nil {
		// No adapter is compiled in at all. Answering with a document of
		// not-configured rows would be a claim this build cannot support: it does not
		// know whether a store is wired, because nothing here can look.
		httpapi.Error(w, http.StatusNotImplemented, "this server reads no historical context")
		return
	}

	// The element is required rather than defaulted. A model-wide context answer
	// would multiply one panel's question by the whole landscape's bindings, against
	// somebody else's cluster; making the caller name the element is what bounds it.
	element := strings.TrimSpace(r.URL.Query().Get("element"))
	if element == "" {
		httpapi.Error(w, http.StatusBadRequest, "name the element to read context for: ?element=<id>")
		return
	}
	window, ok := NewContextWindow(r.URL.Query().Get("window"), s.now().Unix())
	if !ok {
		httpapi.Error(w, http.StatusBadRequest,
			"window must be one of "+strings.Join(Windows(), ", "))
		return
	}

	set, err := ExtractBindings([]byte(model.XML))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read bindings: "+err.Error())
		return
	}
	queries := QueriesFor(set, element, window)
	if len(queries) == 0 {
		// The element binds nothing, so there is nothing to ask about — and that is a
		// complete answer rather than an error. The document still carries its limits,
		// because "this element binds nothing" and "no store could answer" are
		// different findings and the reader has to be able to tell them apart.
		httpapi.JSON(w, http.StatusOK, AssembleContext(element, window, nil, s.now().Unix()))
		return
	}

	results, err := s.context(r, queries)
	switch {
	case errors.Is(err, ErrShuttingDown):
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read context: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, AssembleContext(element, window, results, s.now().Unix()))
}
