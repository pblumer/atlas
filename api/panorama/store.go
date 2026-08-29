package panorama

import (
	"fmt"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/token"
)

// Store persists Panorama models as atomic, fsynced design-time sidecars. It is
// owned by the API run loop and performs no locking of its own (ADR-0189/I3).
type Store struct {
	*sidecar.Store[Model]
}

// NewStore opens the Panorama model directory. Models list newest first, with a
// stable id tie-breaker. IDs name their files directly and therefore must have
// the opaque hexadecimal shape produced by token.New.
func NewStore(dir string) (*Store, error) {
	store, err := sidecar.NewStore(dir, "panoramastore",
		func(model Model) string { return model.ID },
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

// ForApplication returns one application's models newest first.
func (s *Store) ForApplication(applicationID string) ([]Model, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("panoramastore: list application: %w", err)
	}
	out := make([]Model, 0)
	for _, model := range all {
		if model.ApplicationID == applicationID {
			out = append(out, model)
		}
	}
	return out, nil
}
