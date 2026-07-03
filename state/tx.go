package state

import (
	"errors"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// readInto decodes the value at key into dst without allocating: it decodes
// directly from Pebble's returned bytes before releasing them. Reports whether
// the key was present.
func (t *Tx) readInto(key []byte, dst model.Value) (bool, error) {
	raw, closer, err := t.b.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	derr := model.DecodeValueInto(dst, raw)
	closer.Close()
	if derr != nil {
		return false, derr
	}
	return true, nil
}

// Tx is a state transaction: a set of mutations that commit atomically. It is
// an indexed Pebble batch, so reads through it observe its own pending writes.
type Tx struct {
	b       *pebble.Batch
	store   *Store
	scratch []byte // reused value-encode buffer; Pebble copies on Set
}

// Commit applies the transaction. It does not fsync: durability is the WAL's
// responsibility (ADR-0005), and the store is rebuildable by replay, so a state
// commit lost to a crash is simply re-derived on recovery.
func (t *Tx) Commit() error { return t.b.Commit(pebble.NoSync) }

// Close releases the transaction, returning its batch to the store for reuse.
// Safe to call after Commit. The Tx must not be used afterward.
func (t *Tx) Close() error {
	b := t.b
	t.b = nil
	return t.store.recycle(b)
}

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

// GetElementInstanceInto decodes the element instance into dst without
// allocating, reporting whether it was present.
func (t *Tx) GetElementInstanceInto(key uint64, dst *model.ElementInstanceValue) (bool, error) {
	return t.readInto(keyElementInstance(key), dst)
}

// GetElementInstance returns the element instance, or nil if absent.
func (t *Tx) GetElementInstance(key uint64) (*model.ElementInstanceValue, error) {
	var v model.ElementInstanceValue
	ok, err := t.GetElementInstanceInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
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

// GetJobInto decodes the job into dst without allocating, reporting whether it
// was present.
func (t *Tx) GetJobInto(key uint64, dst *model.JobValue) (bool, error) {
	return t.readInto(keyJob(key), dst)
}

// GetJob returns the job, or nil if absent.
func (t *Tx) GetJob(key uint64) (*model.JobValue, error) {
	var v model.JobValue
	ok, err := t.GetJobInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// DeleteJob removes the job and its activatable index entry.
func (t *Tx) DeleteJob(key uint64, v *model.JobValue) error {
	if err := t.b.Delete(keyJob(key), nil); err != nil {
		return err
	}
	return t.b.Delete(keyJobActivatable(v.JobType, key), nil)
}

// --- UserTask ---
//
// A user task lives in cfUserTask and in exactly one queue index at a time: the
// candidate-group queue (cfUserTaskGroup) while unclaimed, moving to the
// assignee index (cfUserTaskAssign) when claimed. All three mutations run from
// applyToState so the indexes are rebuilt identically on recovery (invariant I4).

// PutUserTaskCreated writes a newly created task and offers it to its candidate
// group's claimable queue.
func (t *Tx) PutUserTaskCreated(key uint64, v *model.UserTaskValue) error {
	if err := t.b.Set(keyUserTask(key), t.encodeValue(v), nil); err != nil {
		return err
	}
	return t.b.Set(keyUserTaskGroup(v.CandidateGroup, key), nil, nil)
}

// ApplyUserTaskClaimed records a claim: the task moves out of its group's
// claimable queue and into the assignee's index, and its stored value (now
// carrying the assignee and claimed state) is rewritten. v is the post-claim
// value, so its CandidateGroup still locates the queue entry to remove.
func (t *Tx) ApplyUserTaskClaimed(key uint64, v *model.UserTaskValue) error {
	if err := t.b.Set(keyUserTask(key), t.encodeValue(v), nil); err != nil {
		return err
	}
	if err := t.b.Delete(keyUserTaskGroup(v.CandidateGroup, key), nil); err != nil {
		return err
	}
	return t.b.Set(keyUserTaskAssign(v.Assignee, key), nil, nil)
}

// GetUserTaskInto decodes the task into dst without allocating, reporting whether
// it was present.
func (t *Tx) GetUserTaskInto(key uint64, dst *model.UserTaskValue) (bool, error) {
	return t.readInto(keyUserTask(key), dst)
}

// GetUserTask returns the task, or nil if absent.
func (t *Tx) GetUserTask(key uint64) (*model.UserTaskValue, error) {
	var v model.UserTaskValue
	ok, err := t.GetUserTaskInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// DeleteUserTask removes the task and whichever queue entry it currently holds —
// the assignee index if claimed, otherwise the candidate-group queue. The value
// supplies the state and keys; on recovery it comes from the event payload.
func (t *Tx) DeleteUserTask(key uint64, v *model.UserTaskValue) error {
	if err := t.b.Delete(keyUserTask(key), nil); err != nil {
		return err
	}
	if v.State == model.UserTaskClaimed {
		return t.b.Delete(keyUserTaskAssign(v.Assignee, key), nil)
	}
	return t.b.Delete(keyUserTaskGroup(v.CandidateGroup, key), nil)
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

// GetProcessInstanceInto decodes the process instance into dst without
// allocating, reporting whether it was present.
func (t *Tx) GetProcessInstanceInto(key uint64, dst *model.ProcessInstanceValue) (bool, error) {
	return t.readInto(keyProcessInstance(key), dst)
}

// GetProcessInstance returns the process instance, or nil if absent.
func (t *Tx) GetProcessInstance(key uint64) (*model.ProcessInstanceValue, error) {
	var v model.ProcessInstanceValue
	ok, err := t.GetProcessInstanceInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
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

// IncrementActiveChildren adds one active child to scope. It is a write-only
// merge (no read), so it does not allocate on the hot path (invariant I1).
func (t *Tx) IncrementActiveChildren(scope uint64) error {
	return t.mergeActiveChildren(scope, 1)
}

// DecrementActiveChildren removes one active child from scope. A scope that
// returns to zero leaves a zero-valued counter entry rather than being deleted;
// completion checks treat absent and zero alike.
func (t *Tx) DecrementActiveChildren(scope uint64) error {
	return t.mergeActiveChildren(scope, -1)
}

func (t *Tx) mergeActiveChildren(scope uint64, delta int64) error {
	t.scratch = appendCounter(t.scratch[:0], delta)
	return t.b.Merge(keyActiveChildren(scope), t.scratch, nil)
}

// ActiveChildren returns the active-child count for scope (0 if none). This read
// folds the merged deltas, so it is used only where the current count is needed
// (e.g. detecting a finished scope), not on every increment.
func (t *Tx) ActiveChildren(scope uint64) (int32, error) {
	raw, ok, err := getCopy(t.b, keyActiveChildren(scope))
	if err != nil || !ok {
		return 0, err
	}
	return int32(decodeCounter(raw)), nil
}
