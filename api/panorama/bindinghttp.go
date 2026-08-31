package panorama

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The HTTP surface for Atlas bindings (ADR-0189 §4). Reading resolves a model's
// bindings against what this caller may see; writing sets one key on one element
// under the same optimistic revision check every other write on this resource uses.

// HandleBindings resolves every Atlas binding a model declares.
func (s *Service) HandleBindings(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	set, err := ExtractBindings([]byte(model.XML))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read bindings: "+err.Error())
		return
	}
	var catalog Catalog
	var ran bool
	var opErr error
	s.loop.Do(func() {
		ran = true
		catalog, opErr = s.catalog(r)
	})
	// runloop.Do declines to run anything once the loop is closing, leaving the
	// catalog empty and the error nil. Resolving against an empty catalog would
	// report every binding as missing — a model that looks broken rather than one
	// that was not read. See the same guard on the landscape mesh.
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "resolve bindings: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, ResolveBindings(set, catalog))
}

type setBindingRequest struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	ElementID        string   `json:"elementId"`
	Key              string   `json:"key"`
	Values           []string `json:"values"`
}

// HandleSetBinding sets one binding key on one element, replacing whatever that key
// held. An empty values list clears it. Everything else in the document — Atlas's
// other keys, foreign properties, formatting, unsupported standard content — is
// left byte-for-byte alone.
func (s *Service) HandleSetBinding(w http.ResponseWriter, r *http.Request) {
	var payload setBindingRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	if payload.ExpectedRevision < 1 {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision must be at least 1")
		return
	}
	if strings.TrimSpace(payload.ElementID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "elementId is required")
		return
	}
	if _, known := allowedOn[payload.Key]; !known {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"unknown Atlas binding key %q; contract version %d defines %s",
			payload.Key, BindingContractVersion, strings.Join(BindingKeys(), ", ")))
		return
	}

	id := r.PathValue("id")
	now := s.now().Unix()
	actor := requestActor(r)
	var model Model
	var refusal *operationRefusal
	var opErr error
	// contractErr is the caller's mistake — a key on the wrong element type, an
	// unknown element — and must come back as a 400 naming what is wrong rather
	// than as an opaque 500 from inside the writer.
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
		updated, err := SetBinding([]byte(model.XML), payload.ElementID, payload.Key, payload.Values)
		if err != nil {
			contractErr = err
			return
		}
		// The writer only ever splices; validating the result is what proves that,
		// and it refuses to store a document it just made invalid.
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
		httpapi.Error(w, http.StatusInternalServerError, "set binding: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, summarize(model))
}
