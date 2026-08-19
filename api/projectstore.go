package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// project is a named container that groups artifacts so related work lives
// together in the Modeler (ADR-0034). It holds only organizational metadata:
// membership is a projectId tag carried on each artifact (a draft, in Phase 1),
// not a list stored here, so a project and its artifacts stay loosely coupled and
// deleting a project leaves its artifacts intact (they fall back to "Ungrouped").
//
// The access fields (OwnerID, Visibility, Members) are the ADR-0071 sharing
// scope: they turn the project into a private-or-shared access boundary enforced
// by the HTTP handlers against the request Principal (ADR-0044). They are
// optional and omitempty so a pre-scopes project — which carries none of them —
// keeps deserializing and reads as an ownerless, legacy project (see
// effectiveRole), needing no migration.
type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Key is the portable application key (ADR-0134): a stable slug that identifies
	// the application *across servers*, so a repository cloned into a server that
	// has never seen it lands in the right place. The ID above stays what it always
	// was — random and local — so nothing migrates. Empty on every application that
	// predates ADR-0134; one is derived and saved the first time something needs it
	// (applicationKeyFor).
	Key        string          `json:"key,omitempty"`
	OwnerID    string          `json:"ownerId,omitempty"`
	Visibility string          `json:"visibility,omitempty"`
	Members    []projectMember `json:"members,omitempty"`
	// Protected marks a platform-managed system project (ADR-0122): its content is
	// bootstrap-deployed and it must not be renamed, deleted, reshared, or written
	// into through the design-time API — by any caller, admins included. Additive
	// and omitempty, so every pre-0119 record deserializes as an ordinary
	// (Protected=false) project with no migration.
	Protected bool  `json:"protected,omitempty"`
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// projectStore is a durable store for projects, one JSON file per project id
// under a single directory. It reuses the on-disk sidecar approach of the
// deployment and draft stores (ADR-0019/0021) and, like them, is owned solely by
// the server's run-loop goroutine, so it needs no locking of its own.
type projectStore = sidecar.Store[project]

// newProjectStore opens (creating if needed) the projects directory. Projects list
// oldest first (creation order), so the Modeler's order does not reshuffle on a
// rename; the id breaks ties between projects created within the same second, so
// the order is deterministic.
func newProjectStore(dir string) (*projectStore, error) {
	return sidecar.NewStore(dir, "projectstore",
		func(rec project) string { return rec.ID },
		sidecar.Order(func(a, b project) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
