package engine

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// MIRebuildResult reports what RebuildMIHistory did.
type MIRebuildResult struct {
	// Rebuilt is true when the MI history index was replaced and marked complete.
	Rebuilt bool
	// Reason says why nothing was done, or why the index stays unavailable.
	Reason   string
	Replayed int // events applied into the shadow store
	Copied   int // MI index keys copied into the live store
	Seconds  float64
}

// miRebuildCommitEvery bounds one shadow-store transaction, so a long log is not
// one huge batch and an interrupted rebuild resumes from its last commit.
const miRebuildCommitEvery = 50_000

// miRebuildCopyBatch bounds the keys per durable commit of the swap.
const miRebuildCopyBatch = 10_000

// RebuildMIHistory proves the multi-instance history index of a store that
// recorded history before the index existed (ADR-draft-mi-history-legacy-rebuild).
//
// It runs at startup, after Recover and before the processor accepts commands.
// The retained log is replayed from genesis through the one applyToState into a
// fresh shadow store under scratchDir — the same fold recovery uses, so the
// shadow store's MI index is exactly what a store built live would hold — and
// its MI families are then swapped into the live store, which marks them
// complete. Nothing is inferred from the live store's own state.
//
// It does nothing when the index is already complete, and leaves it unavailable
// — returning the reason, not an error — when the log cannot prove it: a log
// that no longer starts at position 1 (compacted, ADR-0131) or that ends before
// the store's applied position. An interrupted run resumes: the shadow store
// keeps its applied position, the swap keeps a copy marker. Only a failure to
// read or write is an error, and even then the index simply stays unavailable.
func (p *Processor) RebuildMIHistory(scratchDir string) (MIRebuildResult, error) {
	started := time.Now()
	var res MIRebuildResult
	needed, err := p.store.MIHistoryRebuildNeeded()
	if err != nil || !needed {
		res.Reason = "the multi-instance history index is complete"
		return res, err
	}
	target, err := p.store.LastAppliedPosition()
	if err != nil {
		return res, err
	}
	if target == 0 {
		res.Reason = "the store holds history but no applied log position, so no replay can be proven to cover it"
		return res, nil
	}
	earliest, ok, err := p.log.EarliestPosition(recordPosition)
	if err != nil {
		return res, err
	}
	if !ok || earliest != 1 {
		res.Reason = fmt.Sprintf("the log starts at position %d, not 1: it was compacted (ADR-0131), so the history before the cut cannot be replayed", earliest)
		return res, nil
	}

	shadow, err := state.Open(scratchDir)
	if err != nil {
		return res, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = shadow.Close()
		}
	}()
	from, err := shadow.LastAppliedPosition()
	if err != nil {
		return res, err
	}
	if from > target {
		return res, fmt.Errorf("engine: MI rebuild shadow store at %d is ahead of the live store at %d", from, target)
	}
	if from < target {
		n, err := replayInto(p, shadow, from, target)
		res.Replayed = n
		if err != nil {
			return res, err
		}
		reached, err := shadow.LastAppliedPosition()
		if err != nil {
			return res, err
		}
		if reached != target {
			res.Reason = fmt.Sprintf("the log ends at position %d but the store has applied %d", reached, target)
			return res, nil
		}
	}
	if res.Copied, err = p.store.AdoptMIHistory(shadow, miRebuildCopyBatch); err != nil {
		return res, err
	}
	closed = true
	if err := shadow.Close(); err != nil {
		return res, err
	}
	if err := os.RemoveAll(scratchDir); err != nil {
		return res, err
	}
	res.Rebuilt = true
	res.Seconds = time.Since(started).Seconds()
	return res, nil
}

// replayInto folds the log's events after `from` up to `target` into dst through
// applyToState, committing every miRebuildCommitEvery events with the applied
// position, exactly as recovery commits once.
func replayInto(p *Processor, dst *state.Store, from, target uint64) (int, error) {
	applied := 0
	tx := dst.NewTransaction()
	pendingPos := uint64(0)
	pending := 0
	commit := func() error {
		if pending == 0 {
			return nil
		}
		if err := tx.SetLastAppliedPosition(pendingPos); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := tx.Close(); err != nil {
			return err
		}
		tx = dst.NewTransaction()
		pending = 0
		return nil
	}
	errDone := errors.New("replay reached its target")
	err := p.log.ReplayFrom(from, recordPosition, func(data []byte) error {
		rec, err := model.ReadRecord(data)
		if err != nil {
			return err
		}
		h := rec.Header
		if h.RecordType != model.RecordEvent || h.Position <= from {
			return nil
		}
		if h.Position > target {
			return errDone
		}
		iv := inflightFromRecord(rec)
		if err := applyToState(tx, h, &iv); err != nil {
			return err
		}
		applied++
		pending++
		pendingPos = h.Position
		if pending == miRebuildCommitEvery {
			return commit()
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDone) {
		_ = tx.Close()
		return applied, err
	}
	if err := commit(); err != nil {
		_ = tx.Close()
		return applied, err
	}
	return applied, tx.Close()
}
