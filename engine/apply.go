package engine

import "github.com/pblumer/chrampfer/model"

// applyToState mutates state from a single event record. It is the one place
// state changes from a record, and it runs identically live (in the processor)
// and on recovery (replaying the log) — invariant I4. It must stay deterministic
// and side-effect-free: no time reads, no key generation, no I/O beyond the
// transaction. Timestamps and keys are read from the record, never produced here.
func applyToState(tx *stateTx, h model.RecordHeader, v *inflightValue) error {
	switch h.ValueType {
	case model.VTProcessInstance:
		switch h.Intent {
		case model.IntentActivated:
			return tx.PutProcessInstance(h.Key, &v.process)
		case model.IntentCompleted, model.IntentTerminated:
			return tx.DeleteProcessInstance(h.Key)
		}

	case model.VTElementInstance:
		switch h.Intent {
		case model.IntentActivated:
			if err := tx.PutElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			return tx.IncrementActiveChildren(v.element.FlowScopeKey)
		case model.IntentCompleted, model.IntentTerminated:
			if err := tx.DeleteElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			return tx.DecrementActiveChildren(v.element.FlowScopeKey)
		}

	case model.VTJob:
		switch h.Intent {
		case model.IntentJobCreated:
			return tx.PutJob(h.Key, &v.job)
		case model.IntentJobCompleted, model.IntentJobFailed:
			return tx.DeleteJob(h.Key, &v.job)
		}
	}
	return nil
}
