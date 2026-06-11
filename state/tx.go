package state

import (
	"encoding/binary"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/chrampfer/model"
)

// Tx is a state transaction: a set of mutations that commit atomically. It is
// an indexed Pebble batch, so reads through it observe its own pending writes.
type Tx struct {
	b       *pebble.Batch
	scratch []byte // reused value-encode buffer; Pebble copies on Set
}

// Commit applies the transaction. It does not fsync: durability is the WAL's
// responsibility (ADR-0005), and the store is rebuildable by replay, so a state
// commit lost to a crash is simply re-derived on recovery.
func (t *Tx) Commit() error { return t.b.Commit(pebble.NoSync) }

// Close releases the transaction's resources. Safe to call after Commit.
func (t *Tx) Close() error { return t.b.Close() }

// SetLastAppliedPosition records, within this transaction, the highest log
// position folded into state. Committed atomically with the mutations so state
// and position never diverge.
func (t *Tx) SetLastAppliedPosition(pos uint64) error {
	t.scratch = appendBE64(t.scratch[:0], pos)
	return t.b.Set(keyMeta(metaLastApplied), t.scratch, nil)
}

func (t *Tx) encodeValue(v model.Value) []byte {
	t.scratch = model.AppendValue(t.scratch[:0], v)
	return t.scratch
}

// --- ElementInstance ---

// PutElementInstance writes the element instance and its elByProc index entry.
func (t *Tx) PutElementInstance(key uint64, v *model.ElementInstanceValue) error {
	if err := t.b.Set(keyElementInstance(key), t.encodeValue(v), nil); err != nil {
		return err
	}
	return t.b.Set(keyElByProc(v.ProcessInstanceKey, key), nil, nil)
}

// GetElementInstance returns the element instance, or nil if absent.
func (t *Tx) GetElementInstance(key uint64) (*model.ElementInstanceValue, error) {
	raw, ok, err := getCopy(t.b, keyElementInstance(key))
	if err != nil || !ok {
		return nil, err
	}
	v, err := model.DecodeValue(model.VTElementInstance, raw)
	if err != nil {
		return nil, err
	}
	return v.(*model.ElementInstanceValue), nil
}

// DeleteElementInstance removes the element instance and its index entry. The
// value is required to locate the elByProc entry; on recovery it comes from the
// event payload.
func (t *Tx) DeleteElementInstance(key uint64, v *model.ElementInstanceValue) error {
	if err := t.b.Delete(keyElementInstance(key), nil); err != nil {
		return err
	}
	return t.b.Delete(keyElByProc(v.ProcessInstanceKey, key), nil)
}

// --- Job ---

// PutJob writes the job and its activatable index entry.
func (t *Tx) PutJob(key uint64, v *model.JobValue) error {
	if err := t.b.Set(keyJob(key), t.encodeValue(v), nil); err != nil {
		return err
	}
	return t.b.Set(keyJobActivatable(v.JobType, key), nil, nil)
}

// GetJob returns the job, or nil if absent.
func (t *Tx) GetJob(key uint64) (*model.JobValue, error) {
	raw, ok, err := getCopy(t.b, keyJob(key))
	if err != nil || !ok {
		return nil, err
	}
	v, err := model.DecodeValue(model.VTJob, raw)
	if err != nil {
		return nil, err
	}
	return v.(*model.JobValue), nil
}

// DeleteJob removes the job and its activatable index entry.
func (t *Tx) DeleteJob(key uint64, v *model.JobValue) error {
	if err := t.b.Delete(keyJob(key), nil); err != nil {
		return err
	}
	return t.b.Delete(keyJobActivatable(v.JobType, key), nil)
}

// --- Timer ---

// PutTimer writes the timer into the due-date index, which is its primary store.
func (t *Tx) PutTimer(key uint64, v *model.TimerValue) error {
	return t.b.Set(keyTimer(v.DueDate, key), t.encodeValue(v), nil)
}

// DeleteTimer removes the timer. The value supplies the due date that locates
// its index key; on recovery it comes from the event payload.
func (t *Tx) DeleteTimer(key uint64, v *model.TimerValue) error {
	return t.b.Delete(keyTimer(v.DueDate, key), nil)
}

// --- ProcessInstance ---

// PutProcessInstance writes the process instance.
func (t *Tx) PutProcessInstance(key uint64, v *model.ProcessInstanceValue) error {
	return t.b.Set(keyProcessInstance(key), t.encodeValue(v), nil)
}

// GetProcessInstance returns the process instance, or nil if absent.
func (t *Tx) GetProcessInstance(key uint64) (*model.ProcessInstanceValue, error) {
	raw, ok, err := getCopy(t.b, keyProcessInstance(key))
	if err != nil || !ok {
		return nil, err
	}
	v, err := model.DecodeValue(model.VTProcessInstance, raw)
	if err != nil {
		return nil, err
	}
	return v.(*model.ProcessInstanceValue), nil
}

// DeleteProcessInstance removes the process instance.
func (t *Tx) DeleteProcessInstance(key uint64) error {
	return t.b.Delete(keyProcessInstance(key), nil)
}

// --- Active-children counter ---
//
// Each scope (a process instance or a subprocess instance) tracks how many
// child element instances are active. A scope completes when its counter hits
// zero. The counter is pure state — mutated only from applyToState — so it is
// rebuilt identically on recovery.

// IncrementActiveChildren adds one active child to scope.
func (t *Tx) IncrementActiveChildren(scope uint64) error {
	n, err := t.ActiveChildren(scope)
	if err != nil {
		return err
	}
	return t.setActiveChildren(scope, n+1)
}

// DecrementActiveChildren removes one active child from scope, deleting the
// counter entry when it reaches zero.
func (t *Tx) DecrementActiveChildren(scope uint64) error {
	n, err := t.ActiveChildren(scope)
	if err != nil {
		return err
	}
	if n <= 1 {
		return t.b.Delete(keyActiveChildren(scope), nil)
	}
	return t.setActiveChildren(scope, n-1)
}

// ActiveChildren returns the active-child count for scope (0 if none).
func (t *Tx) ActiveChildren(scope uint64) (int32, error) {
	raw, ok, err := getCopy(t.b, keyActiveChildren(scope))
	if err != nil || !ok {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(raw)), nil
}

func (t *Tx) setActiveChildren(scope uint64, n int32) error {
	t.scratch = appendBE32(t.scratch[:0], uint32(n))
	return t.b.Set(keyActiveChildren(scope), t.scratch, nil)
}
