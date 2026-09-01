package panorama

import (
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// Creating elements and relationships from the canvas (ADR-0189 §2, P2b).
//
// Two routes rather than one, because they are two different acts: adding a box
// and joining two boxes fail in different ways and refuse for different reasons,
// and a single endpoint taking "some content" would have to unpick which.
//
// Neither takes a document. Same rule as the layout writer: the canvas sends what
// it did, and the server writes it. That keeps §2's round-trip guarantee on the
// server, and it keeps each route able to do exactly one thing whatever is posted.

// addElementRequest is one new element and where it goes.
type addElementRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Type             string `json:"type"`
	Name             string `json:"name"`
	ViewID           string `json:"viewId"`
	X                int    `json:"x"`
	Y                int    `json:"y"`
	W                int    `json:"w"`
	H                int    `json:"h"`
}

// addRelationshipRequest is one new relationship and the view it is drawn on.
type addRelationshipRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Type             string `json:"type"`
	Source           string `json:"source"`
	Target           string `json:"target"`
	ViewID           string `json:"viewId"`
}

// created is what both routes answer with: the updated model, and the identifier
// the canvas needs to find what it just made.
type created struct {
	Summary
	// CreatedID is the *view* identifier for an element — the shape, which is what
	// the canvas selects — and the relationship identifier for a relationship.
	CreatedID string `json:"createdId"`
}

// HandleAddElement creates an element and places it on a view.
func (s *Service) HandleAddElement(w http.ResponseWriter, r *http.Request) {
	var payload addElementRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	s.writeContent(w, r, payload.ExpectedRevision, "add element",
		func(document []byte) ([]byte, string, error) {
			return AddElement(document, NewElement{
				Type: payload.Type, Name: payload.Name, ViewID: payload.ViewID,
				X: payload.X, Y: payload.Y, W: payload.W, H: payload.H,
			})
		})
}

// HandleAddRelationship creates a relationship and draws it on a view.
//
// The subset refuses what ArchiMate does not permit, in the matrix's own words, so
// a caller that went around the canvas gets the same answer the canvas would have
// given — and learns the rule rather than only that it was refused.
func (s *Service) HandleAddRelationship(w http.ResponseWriter, r *http.Request) {
	var payload addRelationshipRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	s.writeContent(w, r, payload.ExpectedRevision, "add relationship",
		func(document []byte) ([]byte, string, error) {
			return AddRelationship(document, NewRelationship{
				Type: payload.Type, Source: payload.Source,
				Target: payload.Target, ViewID: payload.ViewID,
			})
		})
}

// writeContent is the half both routes share: the access check, the revision
// check, the write, and the validation of what the write produced.
//
// It is one function because every one of those steps has to be identical between
// them. Two copies of a revision check is how one of them ends up not having one.
func (s *Service) writeContent(w http.ResponseWriter, r *http.Request,
	expectedRevision int64, what string, write func([]byte) ([]byte, string, error)) {
	if expectedRevision < 1 {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision must be at least 1")
		return
	}

	id := r.PathValue("id")
	now := s.now().Unix()
	actor := requestActor(r)
	var model Model
	var createdID string
	var refusal *operationRefusal
	var opErr error
	// contractErr is the caller's mistake — an element type outside the subset, a
	// relationship the notation forbids, a view that is not there — and must come
	// back as a 400 naming what is wrong rather than as an opaque 500.
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
		if model.Revision != expectedRevision {
			refusal = &operationRefusal{
				status: http.StatusConflict,
				message: fmt.Sprintf("revision conflict: expected %d, current revision is %d",
					expectedRevision, model.Revision),
			}
			return
		}
		updated, made, err := write([]byte(model.XML))
		if err != nil {
			contractErr = err
			return
		}
		// The writer splices; validating the result is what proves it, and it refuses
		// to store a document it has just made invalid. This matters more here than
		// for a move: an insert can produce a dangling reference in a way a changed
		// number cannot.
		if validation := Validate(updated); !validation.Valid {
			contractErr = fmt.Errorf("the edited document would not validate: %s", validation.Problems[0].Message)
			return
		}
		createdID = made
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
		httpapi.Error(w, http.StatusInternalServerError, what+": "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, created{Summary: summarize(model), CreatedID: createdID})
}
