package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// dmnRef is a DMN artifact: a *reference* to a decision model authored in temis,
// not a copy of it (ADR-0034 Phase 2, ADR-0014). Atlas organizes and (later)
// deploys the reference; it does not author DMN — so this record holds only a
// display name and the temis model handle to resolve, never DMN XML. Keeping DMN
// authoring in temis is what preserves the "no DMN authoring surface" non-goal.
type dmnRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ModelRef  string `json:"modelRef"` // the temis model handle this points at
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the creator's user id, stamped on create (ADR-0071). It governs
	// access only while the reference is Ungrouped (the owner's personal space);
	// under a project, the project's scope governs. Empty (and open) on references
	// that predate per-artifact ownership.
	OwnerID   string `json:"ownerId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// dmnRefStore is a durable store for DMN references, one JSON file per id under a
// single directory. It reuses the on-disk sidecar approach of the deployment,
// draft, and project stores (ADR-0019/0021/0024) and, like them, is owned solely
// by the server's run-loop goroutine, so it needs no locking of its own.

// dmnRefStore is a durable store for dmnRef records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type dmnRefStore = sidecar.Store[dmnRef]

// newDmnRefStore opens (creating if needed) the dmnref directory.
func newDmnRefStore(dir string) (*dmnRefStore, error) {
	return sidecar.NewStore(dir, "dmnrefstore",
		func(rec dmnRef) string { return rec.ID },
		sidecar.Order(func(a, b dmnRef) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
