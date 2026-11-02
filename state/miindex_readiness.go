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

const metaMIHistoryIndexV1 = "mi_history_index_v1"

var miHistoryIndexVersion = [...]byte{1}

// MIHistoryIndexReady reports whether this store or read snapshot has a
// verified, versioned MI projection. It intentionally performs one metadata
// point read rather than walking the history or guessing from the presence of
// some newer MI entries.
func (q queries) MIHistoryIndexReady() (bool, error) {
	value, ok, err := getCopy(q.r, keyMeta(metaMIHistoryIndexV1))
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
	if err != nil || ready {
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
	for _, family := range [...]columnFamily{
		cfProcessInstance,
		cfProcessInstanceHistory,
		cfElementInstance,
		cfElementStep,
		cfElementReplay,
		cfMIBody,
		cfMIIteration,
		cfMIRoundByElement,
		cfMIBodyByElement,
	} {
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
	// It does not alter the event stream or recovery's apply order.
	return s.db.Set(keyMeta(metaMIHistoryIndexV1), miHistoryIndexVersion[:], pebble.Sync)
}
