// Package state is Atlas's materialized state store: the queryable fold of
// the event log (ADR-0001), backed by Pebble (ADR-0003).
//
// State is never the source of truth — the WAL is. Durability therefore belongs
// to the WAL's fsync (ADR-0005), so state transactions commit without their own
// fsync (NoSync): after a crash the store may trail the log, and recovery
// replays events from [Store.LastAppliedPosition] forward to catch up. Because
// each transaction commits its mutations and the advanced position atomically,
// state and position can never disagree.
//
// Keys are organized into column-family indexes (see keys.go) so the engine's
// access patterns — "elements of this instance", "open jobs of this type",
// "timers due by now" — are prefix or range scans rather than full scans.
//
// A Store is owned by a single partition goroutine (invariant I3); it holds no
// locks of its own.
package state

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// metaLastApplied keys the highest log position folded into the store.
const metaLastApplied = "last_applied_position"

// Store wraps a Pebble database.
type Store struct {
	db *pebble.DB
	// freeBatch caches one indexed batch for reuse across transactions. The
	// store is single-writer (invariant I3), so at most one transaction is live
	// at a time and a single cached batch suffices — this keeps NewTransaction
	// from allocating a Pebble batch every batch cycle (invariant I1).
	freeBatch *pebble.Batch
}

// Open opens (creating if needed) the state store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := pebble.Open(dir, &pebble.Options{Merger: counterMerger})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	if s.freeBatch != nil {
		s.freeBatch.Close()
		s.freeBatch = nil
	}
	return s.db.Close()
}

// NewTransaction starts a transaction. Reads through it see its own pending
// writes (it is an indexed batch). Mutations become visible only on Commit. The
// underlying batch is drawn from the store's cache when available, so steady-
// state processing does not allocate one (invariant I1).
func (s *Store) NewTransaction() *Tx {
	b := s.freeBatch
	if b != nil {
		s.freeBatch = nil
		b.Reset()
	} else {
		b = s.db.NewIndexedBatch()
	}
	return &Tx{b: b, store: s}
}

// recycle returns a finished batch to the cache for reuse, or closes it if one
// is already cached.
func (s *Store) recycle(b *pebble.Batch) error {
	if s.freeBatch == nil {
		s.freeBatch = b
		return nil
	}
	return b.Close()
}

// LastAppliedPosition returns the highest log position folded into committed
// state, or 0 if none has been applied yet (genesis).
func (s *Store) LastAppliedPosition() (uint64, error) {
	raw, ok, err := getCopy(s.db, keyMeta(metaLastApplied))
	if err != nil || !ok {
		return 0, err
	}
	if len(raw) != 8 {
		return 0, fmt.Errorf("state: corrupt last-applied position (%d bytes)", len(raw))
	}
	return binary.BigEndian.Uint64(raw), nil
}

// ElementInstancesOfProcess calls fn with the key of every element instance
// belonging to the given process instance, via the elByProc index.
func (s *Store) ElementInstancesOfProcess(procKey uint64, fn func(elKey uint64) error) error {
	return s.scanPrefix(elByProcPrefix(procKey), func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// ActivatableJobs calls fn with the key of every open job of the given type,
// via the jobActivatable index — the worker-polling access pattern.
func (s *Store) ActivatableJobs(jobType int32, fn func(jobKey uint64) error) error {
	return s.scanPrefix(jobActivatablePrefix(jobType), func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// DueTimers calls fn for every timer whose due date is at or before now, in due
// order. Because the due date is the index prefix, this is a range scan from the
// start of the timer family up to now — no scheduler structure, no full scan.
func (s *Store) DueTimers(now int64, fn func(timerKey uint64, v *model.TimerValue) error) error {
	lo := []byte{byte(cfTimer)}
	hi := prefixEnd(appendOrderedInt64([]byte{byte(cfTimer)}, now))
	return s.scanRange(lo, hi, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTTimer, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.TimerValue))
	})
}

// GetJob returns the committed job for key, reporting whether it was present.
// Unlike Tx.GetJob it reads outside a transaction, for queries such as a worker
// runner pulling activatable jobs.
func (s *Store) GetJob(key uint64) (*model.JobValue, bool, error) {
	raw, ok, err := getCopy(s.db, keyJob(key))
	if err != nil || !ok {
		return nil, ok, err
	}
	v, err := model.DecodeValue(model.VTJob, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.JobValue), true, nil
}

// GetElementInstance returns the committed element instance for key, reporting
// whether it was present. Like GetJob it reads outside a transaction, for
// consumers such as the in-process DMN worker resolving the decision an
// activatable business-rule job belongs to.
func (s *Store) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	raw, ok, err := getCopy(s.db, keyElementInstance(key))
	if err != nil || !ok {
		return nil, ok, err
	}
	v, err := model.DecodeValue(model.VTElementInstance, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.ElementInstanceValue), true, nil
}

// ActiveProcessInstanceCount returns how many process instances are live.
func (s *Store) ActiveProcessInstanceCount() (int, error) {
	return s.countPrefix([]byte{byte(cfProcessInstance)})
}

// ActiveProcessInstances calls fn with the key and value of every live process
// instance, via the process-instance column family — the operator "list running
// instances" access pattern.
func (s *Store) ActiveProcessInstances(fn func(key uint64, v *model.ProcessInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfProcessInstance)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ProcessInstanceValue))
	})
}

// CompletedProcessInstances calls fn with the key and value of every process
// instance that has reached a terminal state, via the history column family —
// the operator "list finished instances" access pattern (ADR-0017). Each value
// carries its terminal State and CompletedAt.
func (s *Store) CompletedProcessInstances(fn func(key uint64, v *model.ProcessInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfProcessInstanceHistory)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ProcessInstanceValue))
	})
}

// ActiveElementInstanceCount returns how many element instances are live.
func (s *Store) ActiveElementInstanceCount() (int, error) {
	return s.countPrefix([]byte{byte(cfElementInstance)})
}

// ActiveElementInstances calls fn with the key and value of every live element
// instance. Each carries the BPMN element (as a compiled-graph index) it sits on,
// which the live diagram overlay maps back to a diagram element.
func (s *Store) ActiveElementInstances(fn func(key uint64, v *model.ElementInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfElementInstance)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTElementInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ElementInstanceValue))
	})
}

// ElementVisitHistory folds the token-visit counters for a process definition,
// calling fn with each visited element index and how many tokens have passed
// through it. With instanceFilter == 0 it aggregates every instance of the
// definition (the heatmap the live overlay draws in gray); with a non-zero
// instanceFilter it reports only that one instance's visits. Because the key
// ends with the element index and the same element sits under a distinct
// instance-key prefix per instance, a definition-wide scan can report the same
// element index once per instance — the caller sums the counts. Pebble folds
// the merge deltas for each key, so raw carries the current total for that key.
func (s *Store) ElementVisitHistory(procDefKey, instanceFilter uint64, fn func(elementId int32, count int64) error) error {
	prefix := elementVisitDefPrefix(procDefKey)
	if instanceFilter != 0 {
		prefix = elementVisitInstancePrefix(procDefKey, instanceFilter)
	}
	return s.scanPrefix(prefix, func(k, raw []byte) error {
		return fn(elementIdFromVisitKey(k), decodeCounter(raw))
	})
}

// MessageFlowHistory folds the retained message flows a definition received,
// calling fn with each flow's event timestamp, log position, and payload in the
// order they occurred (the replay timeline). Because the key sorts by timestamp
// then position, a definition-wide scan yields a monotonic sequence. The caller
// resolves the receiver element index to a diagram id via the compiled process.
func (s *Store) MessageFlowHistory(receiverDefKey uint64, fn func(ts int64, pos uint64, v *model.MessageFlowValue) error) error {
	return s.scanPrefix(messageFlowDefPrefix(receiverDefKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTMessageFlow, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromFlowKey(k), positionFromFlowKey(k), v.(*model.MessageFlowValue))
	})
}

// VariablesOfScope calls fn with every variable owned by the given scope, via
// the variable column family. Used to build a FEEL evaluation scope and to
// surface an instance's variables to operators.
func (s *Store) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	return s.scanPrefix(variablePrefix(scope), func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTVariable, raw)
		if err != nil {
			return err
		}
		return fn(v.(*model.VariableValue))
	})
}

func (s *Store) countPrefix(prefix []byte) (int, error) {
	count := 0
	err := s.scanPrefix(prefix, func(_, _ []byte) error {
		count++
		return nil
	})
	return count, err
}

func (s *Store) scanPrefix(prefix []byte, fn func(k, v []byte) error) error {
	return s.scanRange(prefix, prefixEnd(prefix), fn)
}

func (s *Store) scanRange(lo, hi []byte, fn func(k, v []byte) error) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lo, UpperBound: hi})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := fn(iter.Key(), iter.Value()); err != nil {
			return err
		}
	}
	return iter.Error()
}

// reader is the read surface shared by *pebble.DB and an indexed *pebble.Batch.
type reader interface {
	Get(key []byte) ([]byte, io.Closer, error)
}

// getCopy fetches key and returns an owned copy of the value, reporting whether
// it was present. Pebble's returned slice is only valid until its closer runs,
// so the value is copied out.
func getCopy(r reader, key []byte) ([]byte, bool, error) {
	v, closer, err := r.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := append([]byte(nil), v...)
	return out, true, closer.Close()
}
