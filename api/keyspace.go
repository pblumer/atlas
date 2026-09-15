package api

import (
	"fmt"

	"github.com/pblumer/atlas/api/sidecar"
)

// The floor under the definition key space
// (ADR-0339).
//
// Every process definition and every decision deployment is issued a key from one
// monotonic counter, and that key is the identity a great deal of durable state is
// filed under: completed instances (`piDoneByDef`), the finished count
// (`defDone`), per-element visit and termination aggregates (`elVisAgg`,
// `elTermAgg`), last activity, message-flow history — and, outside the engine, a
// release manifest's members and a decision deployment's pins.
//
// Until this file existed the counter was rebuilt at startup as `max(key over
// surviving records) + 1`. That is correct only while nothing deletes a record.
// Once something does — `DELETE /api/v1/processes/{key}` since ADR-0019, and the
// decision delete since ADR-0336 — removing the *highest*-keyed record and
// restarting hands that key straight back to the next deploy, and the new
// definition inherits every one of those rows. Measured: a freshly deployed
// process that had never run reported one finished instance and a visit on an
// element it had never reached.
//
// So the counter gets a durable floor. It is raised **before** the keys it covers
// are used, never after: a crash between raising it and writing the record costs a
// key (a gap, which is harmless) rather than losing one (a reuse, which is not).
//
// The principle is not new to Atlas. The job-type table states it in as many
// words — "an index, once issued, is permanent: jobs already on disk carry it, so
// the registry never recycles one" — and derives its counter the same way, from the
// surviving entries. That holds there because no route deletes a job type. It
// stopped holding here the day a route deleted a definition.

// keySpaceMarkID is the single record the store holds. The key space is one
// sequence, so there is one mark; naming it rather than deriving a name keeps the
// file recognisable in a data directory somebody is looking through.
const keySpaceMarkID = "definition-keys"

// keySpaceMark is the durable high-water mark of the definition key space.
type keySpaceMark struct {
	ID string `json:"id"`
	// Highest is the highest definition key this installation has ever issued. It
	// only rises, and a key at or below it is never handed out again — whatever
	// happened to the record that carried it.
	Highest uint64 `json:"highest"`
}

// keySpaceStore is the durable floor's one-record store. It is a sidecar like every
// other design-time store (ADR-0019) for the atomic write and the fsync, and it is
// classified `runtime` in the inventory (ADR-0282) for the same reason the job-type
// table is: it is not a model, it is what already-stored data means.
type keySpaceStore struct {
	*sidecar.Store[keySpaceMark]
}

func newKeySpaceStore(dir string) (*keySpaceStore, error) {
	s, err := sidecar.NewStore(dir, "key space", func(m keySpaceMark) string { return m.ID })
	if err != nil {
		return nil, err
	}
	return &keySpaceStore{s}, nil
}

// loadKeyFloor reads the stored floor, returning 0 when there is none — which is
// every installation that predates this file, and is safe: the records themselves
// still raise the counter, exactly as they did before, and the floor starts being
// kept from the next deploy onward.
func (s *Server) loadKeyFloor() error {
	rec, ok, err := s.keySpace.Get(keySpaceMarkID)
	if err != nil {
		return fmt.Errorf("api: read the definition key floor: %w", err)
	}
	if ok && rec.Highest >= s.nextKey {
		s.nextKey = rec.Highest + 1
	}
	return nil
}

// reserveKeys raises the durable floor to cover the n keys about to be issued and
// returns the first of them. Every definition key comes from here.
//
// The order is the whole point. The floor is persisted *first*, so the sequence of
// facts on disk is always "these keys are spent" before "here is what one of them
// holds". A crash in between leaves a key nothing claims — a gap in the numbering,
// which costs nothing because keys are opaque and only their order matters. The
// reverse order would leave a key that a record claimed and the floor did not,
// which is the reuse this exists to prevent.
//
// It runs on the run loop, like every other write to the counter (I3).
func (s *Server) reserveKeys(n int) (uint64, error) {
	if n <= 0 {
		return s.nextKey, nil
	}
	first := s.nextKey
	highest := first + uint64(n) - 1
	if err := s.keySpace.Save(keySpaceMark{ID: keySpaceMarkID, Highest: highest}); err != nil {
		return 0, fmt.Errorf("api: reserve %d definition key(s): %w", n, err)
	}
	s.nextKey = highest + 1
	return first, nil
}
