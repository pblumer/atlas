package api

import (
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/dmn"
)

// handleDmnLayout re-flows a DMN model's decision requirements graph, discarding
// whatever diagram it carries and laying the whole graph out afresh
// (ADR-0325). It backs the decision editor's
// Auto-layout action — the counterpart of POST /api/v1/layout for a diagram — and
// it is the one thing that moves a diagram somebody placed, because it is the
// author asking for exactly that.
//
// A pure transform: nothing is compiled, stored, or deployed, and only the
// coordinates change. The model comes back unchanged if it cannot be laid out,
// which is the same best-effort contract the BPMN route has.
func (s *Server) handleDmnLayout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected DMN XML")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(dmn.RegenerateDiagram(body))
}
