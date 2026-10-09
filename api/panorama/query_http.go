package panorama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// HandleQuerySchema serves the exact vocabulary accepted by HandleQuery.
func (m *Mesh) HandleQuerySchema(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, GraphQuerySchema())
}

// HandleQuery evaluates a read-only query against exactly the same already-
// authorized landscape projection HandleGraph serves. It never sees the full stores
// and never post-filters an unrestricted match set.
func (m *Mesh) HandleQuery(w http.ResponseWriter, r *http.Request) {
	limits := DefaultQueryLimits()
	// Parameters are separate from query text, so allow some JSON overhead without
	// allowing an unbounded request body. The query itself has its stricter bound.
	r.Body = http.MaxBytesReader(w, r.Body, int64(limits.MaxQueryBytes*4))
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var req QueryRequest
	if err := dec.Decode(&req); err != nil {
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_request", err.Error(), 0, req.Query))
		return
	}
	if err := ensureJSONEOF(dec); err != nil {
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_request", err.Error(), 0, req.Query))
		return
	}
	if len(req.Query) > limits.MaxQueryBytes {
		writeQueryError(w, http.StatusBadRequest, newQueryError("query_too_large", "query exceeds max query bytes", 0, req.Query))
		return
	}

	// derive is the security boundary from ADR-0211: collection is performed on
	// the run loop for the current request principal and has already replaced
	// caller-hidden resources before this evaluator sees a graph.
	graph, ok := m.derive(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	if limits.DeadlineMillis > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(limits.DeadlineMillis)*time.Millisecond)
		defer cancel()
	}
	result, err := executeGraphQueryContext(ctx, graph, req, limits)
	if err != nil {
		var qe *QueryError
		if errors.As(err, &qe) {
			writeQueryError(w, http.StatusBadRequest, qe)
			return
		}
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_query", err.Error(), 0, req.Query))
		return
	}
	httpapi.JSON(w, http.StatusOK, result)
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("request contains more than one JSON value")
	}
	return err
}

func writeQueryError(w http.ResponseWriter, status int, err *QueryError) {
	httpapi.JSON(w, status, map[string]any{"error": err})
}
