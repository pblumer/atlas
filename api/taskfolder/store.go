package taskfolder

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/token"
)

// NewID mints a folder id. Sixteen bytes of crypto randomness, hex-encoded, is
// filename-safe — so the id is its own store key — and collision-free in
// practice.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("taskfolderstore: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Store is a durable store for task folders: one JSON file per folder id under a
// single directory, with the atomic-write discipline every design-time store
// shares. It is owned by the run loop, like the stores it sits beside, and does
// no locking of its own.
type Store struct {
	*sidecar.Store[Folder]
}

// NewStore opens (creating if needed) the task-folders directory. Folders list in
// sidebar order: the position a person dragged them into, then creation order,
// then id — so a listing is deterministic before anyone has reordered anything.
func NewStore(dir string) (*Store, error) {
	s, err := sidecar.NewStore(dir, "taskfolderstore",
		func(rec Folder) string { return rec.ID },
		sidecar.Names[Folder](func(id string) string { return id }, token.IsHex),
		sidecar.Order(func(a, b Folder) bool {
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}

// VisibleTo returns the folders a viewer may see, in sidebar order: their own,
// those shared with a group they belong to, and those shared organisation-wide.
func (s *Store) VisibleTo(userID string, groupIDs []string) ([]Folder, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []Folder{}
	for _, f := range all {
		if f.VisibleTo(userID, groupIDs) {
			out = append(out, f)
		}
	}
	return out, nil
}
