package infomodel

import (
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/token"
)

// Store persists information models as atomic, fsynced design-time sidecars. It is
// owned by the API run loop and does no locking of its own (I3).
type Store struct {
	*sidecar.Store[Model]
}

// NewStore opens the information-model directory. Models list newest first with a
// stable id tie-breaker, so a listing does not reshuffle between two reads that
// happened in the same second. IDs name their files directly and therefore must
// have the opaque hexadecimal shape token.New produces.
func NewStore(dir string) (*Store, error) {
	store, err := sidecar.NewStore(dir, "infomodelstore",
		func(m Model) string { return m.ID },
		sidecar.Names[Model](func(id string) string { return id }, token.IsHex),
		sidecar.Order(func(a, b Model) bool {
			if a.UpdatedAt != b.UpdatedAt {
				return a.UpdatedAt > b.UpdatedAt
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Store{store}, nil
}
