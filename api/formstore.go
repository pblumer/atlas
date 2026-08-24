package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// form is a stored form definition: a form-js JSON schema plus the metadata the
// Tasks app and the Modeler list it by (ADR-0028). A user task binds a form by
// id (compiler: zeebe:formDefinition formId="..."); the Tasks app fetches the
// schema by that id and renders it. Schema is the raw form-js JSON, kept as a
// string so the store never has to understand the form model — rendering is a UI
// concern, storage is not.
type form struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the creator's user id, stamped on first save (ADR-0071). It
	// governs access only while the form is Ungrouped (the owner's personal space);
	// under a project, the project's scope governs. Empty (and open) on forms that
	// predate per-artifact ownership.
	OwnerID string `json:"ownerId,omitempty"`
	SavedAt int64  `json:"savedAt"`
	Schema  string `json:"schema"`
}

// formStore is a durable store for form definitions, one JSON file per form id
// under a single directory. Like the draft store (ADR-0021) it reuses the on-disk
// sidecar approach of the deployment store (ADR-0019) and is owned solely by the
// server's run-loop goroutine, so it needs no locking of its own.
type formStore = sidecar.Store[form]

// newFormStore opens (creating if needed) the forms directory. Forms list most
// recently saved first.
func newFormStore(dir string) (*formStore, error) {
	return sidecar.NewStore(dir, "formstore",
		func(rec form) string { return rec.ID },
		sidecar.Order(func(a, b form) bool { return a.SavedAt > b.SavedAt }),
	)
}
