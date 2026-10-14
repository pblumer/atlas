package feelgen

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// HandleCapability answers whether there is an AI Worker to ask, and which ones this
// principal may name. The assistant asks first so that its chat can be absent rather
// than broken: without an AI Worker the editor, the test pane, the history and the
// favourites still work, and only the conversation is missing.
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

// HandleGenerate answers one message of the conversation with a checked proposal.
//
// Nothing is stored: the expression goes to the assistant's editor, where the author
// runs it, changes it, copies it or applies it to a field — and that field's own save
// path, with its own checks, is what puts it into a model.
func (s *Service) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Generated))
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
