package startfavourites

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Store is the durable home of the start favourites: one JSON file per user id, with
// the atomic-write discipline every design-time store shares. It is owned by the run
// loop and does no locking of its own.
type Store struct {
	*sidecar.Store[Record]
}

// NewStore opens (creating if needed) the favourites directory. The key is the user
// id under the default filename scheme, whose hex encoding makes any id a directory
// hands out a safe filename and keeps a key from addressing a file outside the store.
func NewStore(dir string) (*Store, error) {
	s, err := sidecar.NewStore(dir, "startfavouritesstore",
		func(rec Record) string { return rec.UserID },
	)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}
