package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
)

// Changing a held position (ADR-0428).
//
// A per-position lifecycle carries the right in one instance for as long as it is
// held, and a change — a larger mailbox, a second monitor cable, a new cost centre —
// is a message that instance waits for. This is the route that sends it: to the
// instance the line recorded, by key, and with an answer. A change has no fallback
// the way a return does: there is nothing to change on a right whose instance is
// gone, and starting one to change it would run a second strand for one right.
//
// The line does not change status: it is held before a change and held after it.
// What the change did is the instance's record, which is the point of the form.
//
// Since ADR-0429 this route is the action act (orderaction.go) for the action keyed
// `change`: the same checks, the same trigger id, and the answer it always gave.

// changeReq is the body of a change.
type changeReq struct {
	// ChangeID makes a retry answer with the first delivery instead of changing the
	// right twice. Required, as the trigger route's triggerId is.
	ChangeID string `json:"changeId"`
	// Reason is what the orderer says the change is for; it reaches the process.
	Reason string `json:"reason"`
	// Variables are what the change needs — the new size, the new cost centre.
	Variables map[string]any `json:"variables"`
}

// changeResp names the instance that took the change.
type changeResp struct {
	InstanceKey uint64 `json:"instanceKey"`
	Process     string `json:"process"`
}

func (s *Server) handleChangeLine(w http.ResponseWriter, r *http.Request) {
	var req changeReq
	if !readActionBody(w, r, s.budgets().ModelUpload, &req) {
		return
	}
	if strings.TrimSpace(req.ChangeID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "changeId is required: it is what makes a retry "+
			"answer with the first change instead of changing the right twice")
		return
	}
	// The action act under the key ADR-0425's operation map gave the change, with the
	// same trigger id it always had, so a retry across the two routes is one change.
	out, status, msg := s.actOnLine(httpapi.PrincipalFrom(r.Context()), r.PathValue("id"),
		r.PathValue("item"), catalog.ActionChange, actionReq{
			CommandID: req.ChangeID, Reason: req.Reason, Variables: req.Variables,
		})
	if status != http.StatusOK {
		httpapi.Error(w, status, msg)
		return
	}
	httpapi.JSON(w, http.StatusOK, changeResp{InstanceKey: out.InstanceKey, Process: out.Process})
}
