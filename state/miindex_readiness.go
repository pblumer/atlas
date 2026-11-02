package state

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// ErrMIHistoryIndexUnavailable means the retained multi-instance history has
// not been proven complete for this version of the index (ADR-0517). Callers
// must not turn this into an empty result: a legacy store needs an audited
// historical backfill before its MI discovery endpoints may be enabled.
var ErrMIHistoryIndexUnavailable = errors.New("state: multi-instance history index unavailable until verified backfill")

// metaMIHistoryIndex names the readiness marker of the current index schema.
// Schema v1 (PR #1487-#1490) carried no loopCounter, counters, state or
// incident families; a store marked only for v1 is therefore a legacy store for
// v2 and stays unavailable until a verified rebuild, exactly like a store that
// predates the index altogether.
const metaMIHistoryIndex = "mi_history_index_v2"

var miHistoryIndexVersion = [...]byte{2}

// metaMIDecisionIndex marks the decision attribution family (cfMIDecision) as
// complete. It is separate from the v2 marker on purpose: a store that recorded
// MI history before decisions were attributed keeps its iteration listing, and
// only the per-iteration decision list reports itself unavailable.
const metaMIDecisionIndex = "mi_history_decisions_v1"

var miDecisionIndexVersion = [...]byte{1}

// metaOutlineIndex marks the instance outline (cfOutline) complete: every step
// outside an MI iteration of every retained instance is in it. Separate from
// the MI markers for the reason the decision marker is.
const metaOutlineIndex = "instance_outline_v1"

var outlineIndexVersion = [...]byte{1}

// ErrOutlineIndexUnavailable means the instance outline is not proven complete
// for this store; a reader must not present a partial outline as the history.
var ErrOutlineIndexUnavailable = errors.New("state: instance outline unavailable for history recorded before it existed")

// OutlineIndexReady reports whether the instance outline covers the whole
// retained history of this store.
func (q queries) OutlineIndexReady() (bool, error) {
	value, ok, err := getCopy(q.r, keyMeta(metaOutlineIndex))
	if err != nil || !ok {
		return false, err
	}
	if !bytes.Equal(value, outlineIndexVersion[:]) {
		return false, fmt.Errorf("state: invalid instance outline version marker %x", value)
	}
	return true, nil
}

// ErrMIDecisionIndexUnavailable means the decision attribution of the MI history
// is not proven complete for this store; a reader must not present an empty
// decision list for an iteration as "no decisions".
var ErrMIDecisionIndexUnavailable = errors.New("state: multi-instance decision attribution unavailable for history recorded before it existed")

// MIDecisionIndexReady reports whether decisions are attributed to MI iterations
// for the whole retained history of this store.
func (q queries) MIDecisionIndexReady() (bool, error) {
	value, ok, err := getCopy(q.r, keyMeta(metaMIDecisionIndex))
	if err != nil || !ok {
		return false, err
	}
	if !bytes.Equal(value, miDecisionIndexVersion[:]) {
		return false, fmt.Errorf("state: invalid MI decision index version marker %x", value)
	}
	return true, nil
}

// MIHistoryIndexReady reports whether this store or read snapshot has a
// verified, versioned MI projection. It intentionally performs one metadata
// point read rather than walking the history or guessing from the presence of
// some newer MI entries.
func (q queries) MIHistoryIndexReady() (bool, error) {
	value, ok, err := getCopy(q.r, keyMeta(metaMIHistoryIndex))
	if err != nil || !ok {
		return false, err
	}
	if !bytes.Equal(value, miHistoryIndexVersion[:]) {
		return false, fmt.Errorf("state: invalid MI history index version marker %x", value)
	}
	return true, nil
}

func (q queries) requireMIHistoryIndex() error {
	ready, err := q.MIHistoryIndexReady()
	if err != nil {
		return err
	}
	if !ready {
		return ErrMIHistoryIndexUnavailable
	}
	return nil
}

// initializeMIHistoryIndexReadiness is an O(1)-sized startup probe, not a
// backfill. A brand-new store, including a fresh WAL-replay destination, can
// truthfully declare the index complete before any events have been applied:
// every subsequent event passes through the same deterministic applyToState.
//
// A legacy store may already contain historic MI rounds that the new index
// did not record. Neither partial index keys nor new live events prove that
// old history was reconstructed, so the marker stays absent. The later
// migration must explicitly verify its durable source before marking ready.
//
// The probe uses at most one Seek per family and never scans historical rows.
func (s *Store) initializeMIHistoryIndexReadiness() error {
	ready, err := s.MIHistoryIndexReady()
	if err != nil {
		return err
	}
	decisions, err := s.MIDecisionIndexReady()
	if err != nil {
		return err
	}
	outline, err := s.OutlineIndexReady()
	if err != nil || (ready && decisions && outline) {
		return err
	}
	position, err := s.LastAppliedPosition()
	if err != nil {
		return err
	}
	if position != 0 {
		return nil
	}

	// A legacy store may lack its last-applied position (e.g. an old
	// checkpoint or a store assembled before position tracking). Guard all
	// retained lifecycle and MI index families, not just active instances.
	families := append([]columnFamily{
		cfProcessInstance,
		cfProcessInstanceHistory,
		cfElementInstance,
		cfElementStep,
		cfElementReplay,
	}, miIndexFamilies[:]...)
	for _, family := range families {
		prefix := []byte{byte(family)}
		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: prefix,
			UpperBound: prefixEnd(prefix),
		})
		if err != nil {
			return err
		}
		found := iter.First()
		err = iter.Error()
		if closeErr := iter.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}

	// A durable marker written at initialisation distinguishes a genuinely
	// empty instance history from a partially upgraded running instance.
	// It does not alter the event stream or recovery's apply order. Each
	// marker is written only for the part of the index it names.
	b := s.db.NewBatch()
	defer b.Close()
	if !ready {
		if err := b.Set(keyMeta(metaMIHistoryIndex), miHistoryIndexVersion[:], nil); err != nil {
			return err
		}
	}
	if !decisions {
		if err := b.Set(keyMeta(metaMIDecisionIndex), miDecisionIndexVersion[:], nil); err != nil {
			return err
		}
	}
	if !outline {
		if err := b.Set(keyMeta(metaOutlineIndex), outlineIndexVersion[:], nil); err != nil {
			return err
		}
	}
	return b.Commit(pebble.Sync)
}
