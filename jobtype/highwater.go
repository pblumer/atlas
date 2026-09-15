package jobtype

import (
	"fmt"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
)

// The high-water mark under the dynamic job-type indices
// (ADR-draft-the-job-type-index-space-never-goes-backwards).
//
// This package has always stated the rule outright — "an index, once issued, is
// permanent: jobs already on disk carry it, so the registry never recycles one, not
// even after a record is removed by hand" — and until this file existed it did not
// enforce it. `next` was rebuilt at startup as `max(index over the entries this
// build could still use) + 1`, which is a derivation from survivors, not a memory of
// what was issued. Two things lower it:
//
//   - an entry file that is gone: removed by hand, lost to a partial restore, or
//     dropped by any future route that prunes a job type. Measured: remove the
//     highest entry, restart, and the next `Intern` hands that index to a different
//     name;
//   - an entry [Registry.remember] refuses, which needs no editing at all. An entry
//     whose *name* a later build turns into a built-in is skipped — correctly, since
//     the constant decides that name's index now — and skipped entries did not raise
//     `next` either, so a high index a model-authored type had been issued went
//     straight back into circulation. Measured: a store holding that name at 1005 let
//     the fifth new job type be issued 1005.
//
// Neither is hypothetical for the jobs already on disk. A parked job carries the
// number, not the name; hand its index to another type and a worker subscribed to
// that type is handed somebody else's work — the exact failure ADR-0007 built this
// table to prevent, arrived at from the other direction.
//
// So the counter gets a durable memory. It is raised **before** the index it covers
// is issued: a crash in between spends an index nothing claims, which is a gap and
// costs nothing, rather than leaving an index a record claims and the mark does not.
// That is the ordering ADR-0339 established for the definition key space, applied to
// the counter that names the same problem in its own doc comment.

// highWaterStem is the mark's filename inside the table's directory.
//
// It is deliberately not valid hex. Entry files are named by hex-encoding the job
// type, and the entry store ignores every stem that is not hex — so the mark can
// share the directory with the table it belongs to without a listing ever picking it
// up, which is what [sidecar.Names] exists for. `TestTheMarkIsNotReadAsAnEntry` pins
// that, because it is a coupling between two stores that would otherwise fail
// quietly.
const highWaterStem = "highest"

// highWater is the durable high-water mark of the dynamic half of the table.
//
// Only the dynamic half: the reserved indices are compile-time constants that are
// never issued and never at risk. `Highest` is the largest index this installation
// has ever handed to a model-authored job type. It only rises.
type highWater struct {
	Highest int32 `json:"highest"`
}

// newHighWaterStore opens the mark beside the entries it is about. One directory,
// because the mark is part of the table rather than a second thing to back up,
// restore and classify (ADR-0282) — the inventory still has one `jobtypes` line, and
// it still means the same thing.
func newHighWaterStore(dir string) (*sidecar.Store[highWater], error) {
	return sidecar.NewStore(dir, "job type high-water mark",
		func(highWater) string { return highWaterStem },
		sidecar.Names[highWater](
			func(string) string { return highWaterStem },
			func(stem string) bool { return stem == highWaterStem },
		))
}

// loadHighWater raises next past the stored mark.
//
// A store with no mark is every installation that predates this file, and is not an
// error: the entries themselves still raise the counter exactly as they did before,
// and the mark starts being kept from here on. That is also why the caller writes it
// back — see [Registry.rememberHighWater].
func (r *Registry) loadHighWater() error {
	rec, ok, err := r.marks.Get(highWaterStem)
	if err != nil {
		return fmt.Errorf("job types: read the index high-water mark: %w", err)
	}
	if ok && rec.Highest >= r.next {
		r.next = rec.Highest + 1
	}
	return nil
}

// rememberHighWater persists the counter the load arrived at, when that is higher
// than what is on disk.
//
// This is the upgrade path, and it is the only moment the knowledge still exists. An
// installation that has been issuing indices for a year has no mark; its entries say
// how far it got, and they say so *now*. Leave the write until the next `Intern` and
// a record removed in between takes its index with it — the whole defect, on the one
// boot that could have closed it.
//
// A write on a read path is worth stating plainly rather than hiding: it happens
// only when the mark would rise, so it is idempotent and silent on every boot after
// the first. It cannot rise above what the entries already forced `next` to for this
// boot, so it makes a high index permanent one deploy sooner than `Intern` would
// have — it does not make one reachable that was not.
func (r *Registry) rememberHighWater() error {
	highest := r.next - 1
	if highest < compiler.FirstDynamicJobTypeIndex() {
		return nil // nothing dynamic has ever been issued; there is no mark to make
	}
	rec, ok, err := r.marks.Get(highWaterStem)
	if err != nil {
		return fmt.Errorf("job types: read the index high-water mark: %w", err)
	}
	if ok && rec.Highest >= highest {
		return nil
	}
	if err := r.marks.Save(highWater{Highest: highest}); err != nil {
		return fmt.Errorf("job types: record the index high-water mark: %w", err)
	}
	return nil
}

// markSpent raises the durable mark to cover idx before it is issued, and is the
// only way an index leaves this package. Its in-memory counterpart is
// [Registry.advancePast]; this one is the half that survives a restart.
//
// The order is the design. The mark goes to disk first, so the sequence of facts on
// disk is always "this index is spent" before "here is the name that holds it". A
// crash in between leaves an index nothing claims: a gap, and indices are opaque —
// they are compared, never counted or iterated. The reverse order would leave an
// index a record claims and the mark has never heard of, which is precisely the state
// a lost record puts the table in today.
func (r *Registry) markSpent(idx int32) error {
	if err := r.marks.Save(highWater{Highest: idx}); err != nil {
		return fmt.Errorf("job types: reserve index %d: %w", idx, err)
	}
	return nil
}
