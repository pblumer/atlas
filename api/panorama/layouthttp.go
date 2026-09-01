package panorama

import (
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// setLayoutRequest is what the canvas sends after somebody has moved boxes on a
// view: the shapes that moved and where they are now.
//
// It is not the document. The canvas parsed one to draw it and could serialise it
// back, but a browser's XMLSerializer normalises — attribute order, whitespace,
// namespace prefixes, the comment somebody left — and ADR-0189 §2 requires that
// none of that changes. So the canvas sends what it actually knows and the server
// splices (see layoutwrite.go). It is also the smaller trust surface: this route
// can move a shape and can do nothing else, whatever is posted to it.
type setLayoutRequest struct {
	ExpectedRevision int64          `json:"expectedRevision"`
	Changes          []LayoutChange `json:"changes"`
}

// HandleSetLayout writes the new positions of shapes on a view (ADR-0189 §2, P2a).
//
// It takes the same rights as any other write to the model, and the same revision
// check: two people arranging one view is exactly the case where a lost update
// would be invisible — the loser's boxes simply drift back, with nothing to say
// why.
func (s *Service) HandleSetLayout(w http.ResponseWriter, r *http.Request) {
	var payload setLayoutRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	if payload.ExpectedRevision < 1 {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision must be at least 1")
		return
	}
	if len(payload.Changes) == 0 {
		// Nothing moved. Refused rather than accepted as a no-op, because a save that
		// bumps the revision without changing anything makes everybody else's open
		// editor conflict for no reason.
		httpapi.Error(w, http.StatusBadRequest, "changes is required and must not be empty")
		return
	}

	id := r.PathValue("id")
	now := s.now().Unix()
	actor := requestActor(r)
	var model Model
	var refusal *operationRefusal
	var opErr error
	// contractErr is the caller's mistake — a shape the document does not have, a
	// size that is not a size — and must come back as a 400 naming what is wrong
	// rather than as an opaque 500 from inside the writer.
	var contractErr error

	s.loop.Do(func() {
		var exists bool
		model, exists, opErr = s.store.Get(id)
		if opErr != nil {
			return
		}
		if !exists {
			refusal = &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
			return
		}
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if refusal = writeRefusal(access, false); refusal != nil {
			return
		}
		if model.Revision != payload.ExpectedRevision {
			refusal = &operationRefusal{
				status: http.StatusConflict,
				message: fmt.Sprintf("revision conflict: expected %d, current revision is %d",
					payload.ExpectedRevision, model.Revision),
			}
			return
		}
		updated, err := SetLayout([]byte(model.XML), payload.Changes)
		if err != nil {
			contractErr = err
			return
		}
		// The writer only ever splices four numbers; validating the result is what
		// proves that, and it refuses to store a document it just made invalid.
		if validation := Validate(updated); !validation.Valid {
			contractErr = fmt.Errorf("the edited document would not validate: %s", validation.Problems[0].Message)
			return
		}
		model.XML = string(updated)
		model.Revision++
		model.UpdatedAt = now
		model.UpdatedBy = actor
		opErr = s.store.Save(model)
	})

	if contractErr != nil {
		httpapi.Error(w, http.StatusBadRequest, contractErr.Error())
		return
	}
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "set layout: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, summarize(model))
}
