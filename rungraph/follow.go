package rungraph

import (
	"fmt"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/wal"
)

// Log is the part of [wal.Tailer] a follower uses: durable records forward from a
// cursor, with the option to stop. It is an interface so the follower can be
// driven by a log in a test without a WAL directory, and so nothing in this
// package holds the writing [wal.Log] (invariant I3).
type Log interface {
	Read(from wal.Cursor, fn func(data []byte) (stop bool, err error)) (wal.Cursor, error)
}

// Drift is how far a projection has fallen behind the log since it was seeded,
// measured in **node-set changes** rather than in records. A busy installation
// writes far more variables, jobs and timers than element instances, so a record
// count would overstate staleness by orders of magnitude and call for rebuilds
// nothing needed.
//
// Position is the projection's own position — what it is true as of. Durable is
// how far durability reached when the drift was last measured. The two together
// are the interval the counts describe.
type Drift struct {
	Position uint64 // the seed's position: what the projection reflects
	Durable  uint64 // the durable watermark the measurement stopped at
	Arrived  uint64 // element instances activated since the seed
	Departed uint64 // element instances completed or terminated since the seed
	Gap      bool   // records between the seed and the log's start are gone
}

// Changes is the drift as one number: every node the projection does not have,
// plus every node it has and reality does not. Both directions count, because
// either makes the picture wrong.
func (d Drift) Changes() uint64 { return d.Arrived + d.Departed }

// RebuildDue reports whether a projection of nodes nodes should be rebuilt rather
// than kept, given a threshold expressed as a fraction of its size.
//
// The threshold is relative on purpose: a thousand changes are nothing to a
// hundred-million-node projection and everything to one of two thousand, so an
// absolute number would be wrong at one end of the range or the other — the same
// reason §9's budget is stated in bytes rather than in nodes. Two cases are
// answered before the arithmetic: a gap cannot be caught up from at all, and any
// change at all against an empty projection makes it wrong, where a fraction of
// zero is not a number.
func (d Drift) RebuildDue(nodes int, fraction float64) bool {
	if d.Gap {
		return true
	}
	changes := d.Changes()
	if changes == 0 {
		return false
	}
	if nodes <= 0 {
		return true
	}
	return float64(changes) >= fraction*float64(nodes)
}

// Follower measures a projection's drift from the durable log. It is the second
// half of ADR-0404 §4 — *"kept current from the tailer"* — and what "kept
// current" can mean here is decided by the structure rather than by preference.
// An increment can add and cannot remove:
//
//   - a completing element instance is *deleted* from state (engine/apply.go, on
//     IntentCompleted and IntentTerminated), so in any steady-state installation
//     the node set shrinks as fast as it grows;
//   - the CSR is packed and undirected, so one new edge has to be inserted into
//     *both* endpoints' adjacency lists, one of which is in the middle of a
//     two-gigabyte array;
//   - union-find merges incrementally by design and cannot un-merge, so a removed
//     edge cannot be undone without the component pass running again.
//
// So the follower does not mutate the graph. It counts how far the node set has
// drifted since the seed and lets that number decide when a rebuild is due
// ([Drift.RebuildDue]) — which keeps the projection exactly as of its position,
// the one property every consumer needs (§5) and the one an in-place mutation
// would destroy.
//
// A Follower is not safe for concurrent use: drive it from one goroutine, off the
// processing loop, one [Follower.Follow] at a time.
type Follower struct {
	log  Log
	seed uint64

	cur      wal.Cursor // where the next read resumes; an optimisation only, see Follow
	at       uint64     // highest position counted so far; the seed until one is
	arrived  uint64
	departed uint64
	gap      bool
}

// NewFollower returns a Follower measuring drift from a projection seeded at the
// given log position.
func NewFollower(log Log, seed uint64) *Follower {
	return &Follower{log: log, seed: seed, at: seed}
}

// Follower returns a Follower measuring g's drift from log. The seed is g's own
// position, which is why a graph has to state one (see [Graph.Position]).
func (g *Graph) Follower(log Log) *Follower { return NewFollower(log, g.Position()) }

// Follow reads the log up to durable — the caller's statement of how far
// durability reaches, the state store's LastAppliedPosition — and returns the
// drift accumulated since the seed. It is cumulative: the number is the distance
// from the seed, not from the previous call, because that is the number a rebuild
// threshold is about.
//
// Two properties are load-bearing and cost nothing:
//
// **It starts at the seed's position without seeking.** A [wal.Cursor] cannot be
// constructed — its fields are unexported and there is no seek — so the first read
// starts at whatever cursor the log gives out and every record at or below the
// position already counted is skipped. That is the same re-derivation the
// OpenSearch exporter does from its high-water mark, and it costs a header decode
// per record rather than an application.
//
// **It is therefore idempotent under re-delivery.** A cursor is valid within one
// process run and a restart resumes from genesis by design (ADR-0114), so a
// follower is going to be handed records it has already seen. Skipping by position
// means it cannot double-count them, and a failed read — whose cursor must not be
// adopted — simply re-reads.
//
// A record whose position is more than one past what has been counted means the
// records in between are gone: positions are a dense monotonic sequence (one
// counter, incremented once per event in engine/context.go), so compaction
// (ADR-0131) is the only thing that removes them and nothing can supply them
// again. Follow reports that as [Drift.Gap] and keeps counting what it can see: a
// caller deciding what to do wants to know how much it is missing even when the
// answer is "rebuild".
//
// On an error nothing is advanced — neither the cursor nor the counts — so the
// next call re-reads the interval rather than skipping records it never saw.
func (f *Follower) Follow(durable uint64) (Drift, error) {
	at, arrived, departed, gap := f.at, f.arrived, f.departed, f.gap
	next, err := f.log.Read(f.cur, func(data []byte) (bool, error) {
		h, err := model.ReadHeader(data)
		if err != nil {
			return false, fmt.Errorf("rungraph: read record header: %w", err)
		}
		pos := h.Position
		if pos > durable {
			return true, nil // beyond what a reader could see: stop, not consumed
		}
		if pos <= at {
			return false, nil // already counted, or in the seed
		}
		if pos > at+1 {
			gap = true
		}
		at = pos
		if h.RecordType != model.RecordEvent || h.ValueType != model.VTElementInstance {
			return false, nil // no node either way (invariant I6: only facts count)
		}
		switch h.Intent {
		case model.IntentActivated:
			arrived++
		case model.IntentCompleted, model.IntentTerminated:
			departed++
		}
		return false, nil
	})
	if err != nil {
		return Drift{}, err
	}
	f.cur, f.at, f.arrived, f.departed, f.gap = next, at, arrived, departed, gap
	return Drift{
		Position: f.seed,
		Durable:  durable,
		Arrived:  f.arrived,
		Departed: f.departed,
		Gap:      f.gap,
	}, nil
}
