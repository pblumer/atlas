package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Where a directory synchronisation resumes from, and how far it has got
// (ADR-0332).
//
// This is the whole of the mirror's own state. Everything else it decides is
// derived from the accounts and groups it wrote — which is deliberate: a mirror
// that kept a second model of the directory beside the user store would have two
// things to keep in step, and the one nobody looks at is the one that goes wrong.

// directorySyncStateID is the key of the single record. The store is a sidecar like
// every other durable design-time store rather than a file of its own, because one
// record today is one record with a shape somebody will extend — a second tenant, a
// second directory — and a store takes that without a migration.
const directorySyncStateID = "entra"

// directorySyncState is where the next run starts.
type directorySyncState struct {
	ID string `json:"id"`

	// Revision counts applied runs. It is what a reporting run pins itself to, so a
	// message delivered twice cannot move the cursor twice: the job protocol delivers
	// at least once, and the second delivery of a batch that was already written
	// carries a revision that is no longer current (ADR-0007).
	//
	// It is a counter and not a timestamp on purpose. Two runs a millisecond apart are
	// indistinguishable by clock and perfectly distinguishable by count, and the engine
	// already refuses to let time decide anything it can decide by ordering.
	Revision int64 `json:"revision"`

	// UsersDeltaLink and GroupsDeltaLink are Graph's @odata.deltaLink for each
	// collection: the cursor a run resumes from. Empty means the next run enumerates
	// the whole collection — which is expensive and completely safe, and that
	// asymmetry is what lets this be one small record with no backup ceremony of its
	// own. Losing it costs one full read; getting it wrong loses changes.
	//
	// A cursor is operating state, not a credential: it addresses a change set and
	// opens nothing without the tenant credential the worker holds. It is still kept
	// out of every audit line and error message, because a URL that names a tenant in
	// a log that is shipped somewhere else is a disclosure nobody chose.
	UsersDeltaLink  string `json:"usersDeltaLink,omitempty"`
	GroupsDeltaLink string `json:"groupsDeltaLink,omitempty"`

	// AppliedAt is when a run last wrote anything, and zero when none ever has. It is
	// the field a report reads to say "this installation has never applied a
	// synchronisation" in the run that would otherwise look ordinary — the sentence
	// that has to be there when a mirror has been reporting for a month because
	// somebody forgot to arm it.
	AppliedAt int64 `json:"appliedAt,omitempty"`

	UpdatedAt int64 `json:"updatedAt"`
}

// directorySyncStore is the durable home of that record: the same sidecar shape as
// every other store here, so a backup carries it by classification rather than by
// somebody remembering it (storeregistry.go).
type directorySyncStore struct {
	*sidecar.Store[directorySyncState]
}

// newDirectorySyncStore opens (creating if needed) the directory-sync directory.
func newDirectorySyncStore(dir string) (*directorySyncStore, error) {
	s, err := sidecar.NewStore(dir, "directorysyncstore",
		func(rec directorySyncState) string { return rec.ID })
	if err != nil {
		return nil, err
	}
	return &directorySyncStore{s}, nil
}

// current returns the stored state, or the zero state when nothing has run yet.
//
// The zero state is a real answer and not a missing one: revision 0, no cursors, and
// a first run that enumerates the whole tenant. So the absence of the record is
// never reported as an error to a caller who would have to invent the same thing.
func (s *directorySyncStore) current() (directorySyncState, error) {
	rec, ok, err := s.Get(directorySyncStateID)
	if err != nil {
		return directorySyncState{}, err
	}
	if !ok {
		return directorySyncState{ID: directorySyncStateID}, nil
	}
	return rec, nil
}
