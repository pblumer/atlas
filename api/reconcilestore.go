package api

import (
	"sort"

	"github.com/pblumer/atlas/api/sidecar"
)

// The durable journal of what Atlas and the target systems disagreed about
// (ADR-draft-reconciliation).
//
// # Transitions, not samples
//
// A reconciliation that stored every run would store "healthy, healthy, healthy"
// forever and be read by nobody. This stores **changes**: a disagreement that
// persists across ten runs is one record whose last-seen moment moves, and a
// disagreement that goes away is that same record closed. The shape is the one
// api/panorama/drift.go argues for and this is the durable cousin of it — evidence
// rather than a reading surface, so it survives a restart where Panorama's
// deliberately does not.
//
// # Why a sidecar rather than engine state
//
// The inventory is engine state because a *process* writes it: an order grants a
// right, through the log, and it has to survive the retention deletion of the
// instance that produced it. Nothing in a process writes a discrepancy. It is
// produced by a comparison somebody runs, it is never replayed, and applyToState
// has no business with it — so it belongs with the other durable records that are
// not the engine's (accounts, groups, tokens, the mirror's cursor): a sidecar,
// backed up by classification rather than by somebody remembering it.
//
// The cost of that choice, stated rather than discovered: a discrepancy is not in
// the event log, so it cannot be reconstructed from the log alone. What it records
// is a judgement about two states at one moment, and neither of those states is
// the engine's to replay.

// How an episode ended. A closure is never silent about its cause, because the
// three mean completely different things to whoever reads the journal a year
// later.
const (
	// closedGone is the disagreement resolving itself: a later run looked and did
	// not find it. Nobody here did anything — the target system changed, or Atlas
	// did. It is the ordinary and least interesting closure, and it is still worth
	// recording, because "it stopped" and "somebody stopped it" are different
	// answers to an auditor.
	closedGone = "gone"
	// closedAdopted is somebody accepting an unmanaged right into the inventory.
	closedAdopted = "adopted"
	// closedDeprovisioning is somebody starting the product's deprovisioning. The
	// episode closes when the decision is taken, not when the target system has
	// caught up: what is recorded is that a person decided, and the process's own
	// outcome is the process's to report.
	closedDeprovisioning = "deprovisioning-started"
	// closedRevoked is somebody removing a record Atlas could not substantiate.
	closedRevoked = "revoked"
)

// discrepancyRecord is one disagreement's whole history, compressed to what a
// reader actually needs.
type discrepancyRecord struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	System    string `json:"system"`
	Ref       string `json:"ref,omitempty"`
	Principal string `json:"principal"`
	ItemID    string `json:"itemId"`
	Origin    string `json:"origin,omitempty"`

	// OpenedAt is when the *current* episode began and LastSeenAt when a run last
	// confirmed it. The pair is what says "this has been true for three weeks"
	// rather than "somebody looked once".
	OpenedAt   int64 `json:"openedAt"`
	LastSeenAt int64 `json:"lastSeenAt"`

	// ClosedAt, ClosedHow and ClosedBy are set while the record is closed and
	// cleared when it reopens. ClosedBy is empty for closedGone, and that emptiness
	// is the signal: a closure with no author is one nobody decided.
	ClosedAt  int64  `json:"closedAt,omitempty"`
	ClosedHow string `json:"closedHow,omitempty"`
	ClosedBy  string `json:"closedBy,omitempty"`

	// Episodes counts how many times this disagreement has opened, and
	// PreviousClosedHow how the one before this ended.
	//
	// They exist because a flapping discrepancy is itself a finding, and a journal
	// of one record per identity would otherwise hide it perfectly: a membership
	// somebody keeps re-adding looks, in the current episode alone, exactly like
	// one that appeared for the first time this morning.
	Episodes          int    `json:"episodes"`
	PreviousClosedHow string `json:"previousClosedHow,omitempty"`

	UpdatedAt int64 `json:"updatedAt"`
}

// Open reports whether this record is a disagreement that still stands.
func (r discrepancyRecord) Open() bool { return r.ClosedAt == 0 }

// discrepancyStore is the durable home of the journal.
type discrepancyStore struct {
	*sidecar.Store[discrepancyRecord]
}

func newDiscrepancyStore(dir string) (*discrepancyStore, error) {
	s, err := sidecar.NewStore(dir, "discrepancystore",
		func(rec discrepancyRecord) string { return rec.ID })
	if err != nil {
		return nil, err
	}
	return &discrepancyStore{s}, nil
}

// open returns the disagreements that still stand, newest first.
func (s *discrepancyStore) open() ([]discrepancyRecord, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := make([]discrepancyRecord, 0, len(all))
	for _, r := range all {
		if r.Open() {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OpenedAt > out[j].OpenedAt })
	return out, nil
}

// seen folds one run's finding into the journal and reports whether anything
// changed.
//
// A finding already open moves its last-seen moment and nothing else. One that was
// closed **reopens**, counting an episode and keeping how the previous one ended.
// A finding nobody has recorded opens a first episode.
//
// The "nothing else" in the first case is deliberate. Re-describing an open
// finding on every run would let today's wording quietly replace the wording
// somebody read last week, and the fields that could change — a reference, an
// origin — changing is itself something a reader should see rather than have
// silently applied.
func seenDiscrepancy(prev discrepancyRecord, found discrepancy, now int64) (discrepancyRecord, bool) {
	switch {
	case prev.ID == "": // never recorded
		return discrepancyRecord{
			ID: found.ID, Kind: found.Kind, System: found.System, Ref: found.Ref,
			Principal: found.Principal, ItemID: found.ItemID, Origin: found.Origin,
			OpenedAt: now, LastSeenAt: now, Episodes: 1, UpdatedAt: now,
		}, true
	case prev.Open():
		if prev.LastSeenAt == now {
			return prev, false
		}
		next := prev
		next.LastSeenAt, next.UpdatedAt = now, now
		return next, true
	default: // closed, and back
		next := prev
		next.OpenedAt, next.LastSeenAt = now, now
		next.PreviousClosedHow = prev.ClosedHow
		next.ClosedAt, next.ClosedHow, next.ClosedBy = 0, "", ""
		next.Episodes++
		next.UpdatedAt = now
		return next, true
	}
}

// closeDiscrepancy ends an episode. by is empty for a closure nobody decided.
func closeDiscrepancy(rec discrepancyRecord, how, by string, now int64) discrepancyRecord {
	next := rec
	next.ClosedAt, next.ClosedHow, next.ClosedBy = now, how, by
	next.UpdatedAt = now
	return next
}
