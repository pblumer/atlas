package infomodel

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The import endpoint: a class diagram somebody else drew, brought in as a model of
// this application's own.
//
// It is one route with a preview flag rather than two, because the preview and the
// import must agree: a person reads the account of what an import will do, and then
// does it. Two code paths that could drift is exactly how a preview stops being one.

// maxImportBytes caps an imported document. It is larger than the cap on a model
// document because an XMI export carries the whole UML metamodel around the classes
// — profile applications, tool extensions, diagram references — and a document Atlas
// reads ten classes out of is routinely megabytes of it.
const maxImportBytes = 24 << 20

type importRequest struct {
	ApplicationID string `json:"applicationId"`
	// Name overrides the name the document carries. A UML tool names its model after
	// the file it lives in as often as after the business, so this is offered.
	Name          string `json:"name"`
	Documentation string `json:"documentation"`
	// Format is "json" or "xmi"; unstated, it is detected from the document.
	Format string `json:"format"`
	// Document is the whole source document as text. It rides inside a JSON body
	// rather than as a multipart upload so that the browser, the MCP tools and curl
	// all reach it the same way.
	Document string `json:"document"`
	// DryRun reads the document and reports, and stores nothing.
	DryRun bool `json:"dryRun"`
}

// ImportResponse is what an import answers with — and the notes are the substance of
// it, not a footnote: an import from a foreign notation is a lossy operation, and the
// list of what it lost is the part a modeler has to read.
type ImportResponse struct {
	Format     string           `json:"format"`
	Notes      []ImportNote     `json:"notes"`
	Validation ValidationResult `json:"validation"`
	// Preview is the model the document would become, present on a dry run only.
	Preview *Model `json:"preview,omitempty"`
	// Model is the stored model, present when one was created.
	Model *Summary `json:"model,omitempty"`
}

// HandleImport reads a UML class diagram into a new information model.
//
// ADR-0230 left this out — "XMI is an export, not an interchange" — and
// ADR-0232 settles the other half: a model is routinely drawn in a
// UML tool before anybody opens Atlas, and retyping one by hand loses a business key
// quietly. What makes it safe is that the import is not trusted: it goes through the
// same subset the canvas writes through, everything outside that subset is dropped
// with a sentence naming the element, and a document that would produce a model the
// validator refuses is refused itself.
func (s *Service) HandleImport(w http.ResponseWriter, r *http.Request) {
	var payload importRequest
	if !decodeJSONLimit(w, r, &payload, maxImportBytes) {
		return
	}
	payload.ApplicationID = strings.TrimSpace(payload.ApplicationID)
	if payload.ApplicationID == "" {
		httpapi.Error(w, http.StatusBadRequest, "applicationId is required")
		return
	}
	if strings.TrimSpace(payload.Document) == "" {
		httpapi.Error(w, http.StatusBadRequest, "document is required: send the model as JSON, or as UML XMI")
		return
	}

	imported, err := ParseImport(payload.Format, []byte(payload.Document))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	model := imported.Model
	model.ApplicationID = payload.ApplicationID
	if name := strings.TrimSpace(payload.Name); name != "" {
		model.Name = name
	}
	if model.Name == "" {
		model.Name = "Imported model"
	}
	if doc := strings.TrimSpace(payload.Documentation); doc != "" {
		model.Documentation = doc
	}

	var (
		refusal *operationRefusal
		opErr   error
		invalid *ValidationResult
		stored  *Summary
	)
	s.loop.Do(func() {
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		// A dry run is checked as a write: it reads nothing of this application's, but
		// offering a preview to somebody who could never store it is an invitation to
		// do work that will be refused.
		if refusal = writeRefusal(access, true); refusal != nil {
			return
		}
		if payload.DryRun {
			if err := s.assignIDs(&model); err != nil {
				opErr = err
			}
			return
		}

		id, err := s.newID()
		if err != nil {
			opErr = err
			return
		}
		model.ID = id
		if _, exists, err := s.store.Get(model.ID); err != nil {
			opErr = err
			return
		} else if exists {
			opErr = errIDCollision
			return
		}
		if err := s.assignIDs(&model); err != nil {
			opErr = err
			return
		}
		defaultStoreModes(&model)
		// The sanitizer is meant to make this unreachable. It is checked anyway,
		// because the store's guarantee — every model on disk is one the subset
		// accepts — must not rest on one function having thought of everything.
		if res := Validate(model); !res.Valid {
			invalid = &res
			return
		}
		now := s.now().Unix()
		actor := requestActor(r)
		model.Revision = 1
		model.CreatedAt, model.CreatedBy = now, actor
		model.UpdatedAt, model.UpdatedBy = now, actor
		if opErr = s.store.Save(model); opErr != nil {
			return
		}
		summary := summarize(model)
		stored = &summary
	})

	switch {
	case refusal != nil:
		httpapi.Error(w, refusal.status, refusal.message)
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import information model: "+opErr.Error())
	case invalid != nil:
		httpapi.JSON(w, http.StatusBadRequest, map[string]any{
			"error":    "the imported model is not valid",
			"findings": invalid.Findings,
			"notes":    imported.Notes,
		})
	case payload.DryRun:
		httpapi.JSON(w, http.StatusOK, ImportResponse{
			Format: imported.Format, Notes: imported.Notes,
			Validation: Validate(model), Preview: &model,
		})
	default:
		httpapi.JSON(w, http.StatusCreated, ImportResponse{
			Format: imported.Format, Notes: imported.Notes,
			Validation: Validate(model), Model: stored,
		})
	}
}
