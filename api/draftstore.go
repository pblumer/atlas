package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// draft is a saved-but-not-deployed diagram: the raw BPMN XML plus the metadata
// the Modeler lists it by. Unlike a deployment it is not compiled, so an
// incomplete or not-yet-executable model (e.g. a lone start event) can still be
// saved and reopened. It is keyed by process id, so re-saving the same diagram
// overwrites the previous draft rather than piling up versions.
type draft struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	// ProjectID is the optional project this draft belongs to (ADR-0034). Empty
	// (the value for every draft that predates projects) means "Ungrouped"; so
	// does an id that no longer names an existing project.
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the id of the user who created this draft, stamped on first save
	// (ADR-0071). It governs access only while the draft is Ungrouped, where it is
	// the owner's personal space; when the draft is filed into a project, that
	// project's scope governs instead and this lies dormant. Empty on every draft
	// that predates per-artifact ownership, which keeps such a draft open — no
	// migration.
	OwnerID string `json:"ownerId,omitempty"`
	SavedAt int64  `json:"savedAt"`
	XML     string `json:"xml"`
}

// draftStore is a durable store for diagram drafts, one JSON file per process id
// under a single directory. It reuses the on-disk sidecar approach of the
// deployment store (ADR-0019) and, like it, is owned solely by the server's
// run-loop goroutine, so it needs no locking of its own.
type draftStore = sidecar.Store[draft]

// newDraftStore opens (creating if needed) the drafts directory. Drafts list most
// recently saved first — the order the Modeler shows them in. A draft is keyed by
// process id, so re-saving the same diagram overwrites it rather than piling up
// versions.
func newDraftStore(dir string) (*draftStore, error) {
	return sidecar.NewStore(dir, "draftstore",
		func(rec draft) string { return rec.ProcessID },
		sidecar.Order(func(a, b draft) bool { return a.SavedAt > b.SavedAt }),
	)
}
