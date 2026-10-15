package userprefs

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Store is the durable home of the display preferences: one JSON file per user
// id, with the atomic-write discipline every design-time store shares. It is
// owned by the run loop, like the stores it sits beside, and does no locking of
// its own.
type Store struct {
	*sidecar.Store[Prefs]
}

// NewStore opens (creating if needed) the preferences directory.
//
// The key is the user id, and the filename scheme is the default: user ids come
// from whichever directory signs people in — an email address, an OIDC subject, a
// UUID — and hex-encoding is what makes any of those a safe, unique, deterministic
// filename. The encoding also guards the key on the way in, so an id carrying a
// slash cannot address a file outside the store.
func NewStore(dir string) (*Store, error) {
	s, err := sidecar.NewStore(dir, "userprefsstore",
		func(rec Prefs) string { return rec.UserID },
	)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}
