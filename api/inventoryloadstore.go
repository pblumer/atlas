package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Whether this installation has taken its inventory, and of what
// (ADR-0333).
//
// One record per target system, and it exists to answer exactly one question the
// inventory itself cannot: *has a load ever been applied for this system?* An empty
// result from "loaded and found nothing" and one from "nobody ever ran it" look
// identical in the entitlement family, and those two situations call for opposite
// actions. Everything else a load needs is derived from the accounts, the products
// and the inventory — a second model of the target system kept beside them would be
// one more thing to keep in step, and the one nobody looks at is the one that goes
// wrong.
//
// There is deliberately no cursor here, and the absence is a design decision rather
// than an omission. The directory mirror keeps one because a delta read is
// destructive on repeat: advancing past a change set that was never written loses
// it. A load carries absolute facts — this person holds this right — so a batch
// delivered twice decides "already recorded as legacy" the second time and writes
// nothing. It is idempotent by construction, which is why it needs no revision to
// pin itself to and why pages of one system's export need no ordering between them.

// inventoryLoadState is what one target system's loads have amounted to.
type inventoryLoadState struct {
	// ID is the system's name as the message spells it, which is also what
	// [catalog.TargetRef.System] is matched against.
	ID string `json:"id"`

	// AppliedAt is when a load last wrote anything for this system, zero when none
	// ever has. Runs counts the loads that wrote, and Granted the records they
	// wrote — together they say "three runs, 847 rights" rather than leaving an
	// operator to infer it from an inventory they cannot subtract.
	AppliedAt int64 `json:"appliedAt,omitempty"`
	Runs      int64 `json:"runs,omitempty"`
	Granted   int64 `json:"granted,omitempty"`

	// ReportedAt is when a load last *reported* for this system without writing. It
	// is what makes "this has been previewing for a week and nobody armed it"
	// visible, which is the failure mode of every dry run that is safe enough to
	// leave on.
	ReportedAt int64 `json:"reportedAt,omitempty"`

	UpdatedAt int64 `json:"updatedAt"`
}

// inventoryLoadStore is the durable home of those records: the same sidecar shape
// as every other store here, so a backup carries it by classification rather than
// by somebody remembering it (storeregistry.go).
type inventoryLoadStore struct {
	*sidecar.Store[inventoryLoadState]
}

// newInventoryLoadStore opens (creating if needed) the inventory-load directory.
func newInventoryLoadStore(dir string) (*inventoryLoadStore, error) {
	s, err := sidecar.NewStore(dir, "inventoryloadstore",
		func(rec inventoryLoadState) string { return rec.ID })
	if err != nil {
		return nil, err
	}
	return &inventoryLoadStore{s}, nil
}

// current returns one system's record, or the zero record when it has never been
// loaded. The zero record is a real answer and not a missing one — never applied,
// never reported — so its absence is not reported as an error to a caller who would
// have to invent the same thing.
func (s *inventoryLoadStore) current(system string) (inventoryLoadState, error) {
	rec, ok, err := s.Get(system)
	if err != nil {
		return inventoryLoadState{}, err
	}
	if !ok {
		return inventoryLoadState{ID: system}, nil
	}
	return rec, nil
}

// advanceInventoryState records what a run amounted to.
//
// A reporting run moves ReportedAt and nothing else. That is what lets a report say
// "this system has never had a load applied" on the tenth preview as clearly as on
// the first, instead of the record slowly coming to look like one that has been
// doing something.
func advanceInventoryState(state inventoryLoadState, system string, granted int, applied bool, now int64) inventoryLoadState {
	next := state
	next.ID = system
	next.UpdatedAt = now
	if !applied {
		next.ReportedAt = now
		return next
	}
	next.AppliedAt = now
	next.Runs++
	next.Granted += int64(granted)
	return next
}
