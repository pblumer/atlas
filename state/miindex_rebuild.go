package state

import (
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// Adopting a rebuilt multi-instance history (ADR-draft-mi-history-legacy-rebuild).
//
// A store that recorded history before the MI index existed cannot prove the
// index complete from its own state. The engine proves it by replaying the
// whole retained log through the unchanged applyToState into a fresh store; this
// file swaps that store's MI families into the live one. It runs at startup,
// after recovery and before the processor accepts commands, so nothing else
// writes the live MI families while it copies.
//
// Every MI family sits in one contiguous key range, so the swap is one range
// tombstone and one ordered walk. Progress is a durable marker naming the last
// key copied: a crash mid-copy resumes after it, and the readiness markers are
// written only in the batch that completes the copy, so a partial copy is never
// served.

const metaMIRebuildProgress = "mi_history_rebuild_copy"

// miRangeLo and miRangeHi bound every MI index family (cfMIBody … cfMIDecision).
var (
	miRangeLo = []byte{byte(cfMIBody)}
	miRangeHi = []byte{byte(cfMIDecision) + 1}
)

// MIHistoryRebuildNeeded reports whether any part of the MI history index is not
// proven complete for this store.
func (s *Store) MIHistoryRebuildNeeded() (bool, error) {
	ready, err := s.MIHistoryIndexReady()
	if err != nil {
		return false, err
	}
	decisions, err := s.MIDecisionIndexReady()
	if err != nil {
		return false, err
	}
	return !ready || !decisions, nil
}

// AdoptMIHistory replaces this store's MI index families with src's and marks
// them complete. src must be a store rebuilt from the same log to exactly this
// store's applied position, with its own index proven complete. batch bounds
// the keys per durable commit. It returns how many keys it copied in this call.
func (s *Store) AdoptMIHistory(src *Store, batch int) (int, error) {
	if batch < 1 {
		return 0, fmt.Errorf("state: MI rebuild batch %d must be positive", batch)
	}
	if needed, err := src.MIHistoryRebuildNeeded(); err != nil || needed {
		if err == nil {
			err = errors.New("state: the rebuilt store's MI history is not itself complete")
		}
		return 0, err
	}
	srcPos, err := src.LastAppliedPosition()
	if err != nil {
		return 0, err
	}
	livePos, err := s.LastAppliedPosition()
	if err != nil {
		return 0, err
	}
	if srcPos != livePos {
		return 0, fmt.Errorf("state: rebuilt MI history reaches position %d, this store %d", srcPos, livePos)
	}

	after, inProgress, err := getCopy(s.db, keyMeta(metaMIRebuildProgress))
	if err != nil {
		return 0, err
	}
	if !inProgress {
		// Clear what the live store holds: rows recorded after an upgrade are a
		// partial projection, and the rebuilt one supersedes them. The marker is
		// written in the same batch, so a crash after it resumes the copy rather
		// than clearing again (which would be harmless, but is not needed).
		b := s.db.NewBatch()
		err := b.DeleteRange(miRangeLo, miRangeHi, nil)
		if err == nil {
			err = b.Set(keyMeta(metaMIRebuildProgress), nil, nil)
		}
		if err == nil {
			err = b.Commit(pebble.Sync)
		}
		_ = b.Close()
		if err != nil {
			return 0, err
		}
		after = nil
	}

	lower := miRangeLo
	if len(after) > 0 {
		lower = append(append([]byte(nil), after...), 0)
	}
	iter, err := src.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: miRangeHi})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	copied := 0
	b := s.db.NewBatch()
	defer func() { _ = b.Close() }()
	pending := 0
	var last []byte
	flush := func(final bool) error {
		var err error
		if final {
			err = b.Set(keyMeta(metaMIHistoryIndex), miHistoryIndexVersion[:], nil)
			if err == nil {
				err = b.Set(keyMeta(metaMIDecisionIndex), miDecisionIndexVersion[:], nil)
			}
			if err == nil {
				err = b.Delete(keyMeta(metaMIRebuildProgress), nil)
			}
		} else {
			err = b.Set(keyMeta(metaMIRebuildProgress), last, nil)
		}
		if err == nil {
			err = b.Commit(pebble.Sync)
		}
		_ = b.Close()
		b = s.db.NewBatch()
		pending = 0
		return err
	}
	for iter.First(); iter.Valid(); iter.Next() {
		k := iter.Key()
		if len(k) == 0 || k[0] < miRangeLo[0] || k[0] >= miRangeHi[0] {
			return copied, errors.New("state: MI rebuild walked outside the MI families")
		}
		// The source's merge counters read as their folded value; written with Set
		// into a cleared range they are that value exactly.
		err = b.Set(k, iter.Value(), nil)
		last = append(last[:0], k...)
		copied++
		pending++
		if err == nil && pending == batch {
			err = flush(false)
		}
		if err != nil {
			return copied, err
		}
	}
	if err := iter.Error(); err != nil {
		return copied, err
	}
	return copied, flush(true)
}
