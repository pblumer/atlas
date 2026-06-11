package engine

import "github.com/pblumer/chrampfer/model"

// applyToState mutates state from a single event record. It is the one place
// state changes from a record, and it runs identically live (in the processor)
// and on recovery (replaying the log) — invariant I4. It must stay deterministic
// and side-effect-free: no time reads, no key generation, no I/O beyond the
// transaction. Timestamps and keys are read from the record, never produced here.
func applyToState(tx *stateTx, h model.RecordHeader, v model.Value) error {
	switch h.ValueType {
	case model.VTProcessInstance:
		pv := v.(*model.ProcessInstanceValue)
		switch h.Intent {
		case model.IntentActivated:
			return tx.PutProcessInstance(h.Key, pv)
		case model.IntentCompleted, model.IntentTerminated:
			return tx.DeleteProcessInstance(h.Key)
		}

	case model.VTElementInstance:
		ei := v.(*model.ElementInstanceValue)
		switch h.Intent {
		case model.IntentActivated:
			if err := tx.PutElementInstance(h.Key, ei); err != nil {
				return err
			}
			return tx.IncrementActiveChildren(ei.FlowScopeKey)
		case model.IntentCompleted, model.IntentTerminated:
			if err := tx.DeleteElementInstance(h.Key, ei); err != nil {
				return err
			}
			return tx.DecrementActiveChildren(ei.FlowScopeKey)
		}

	case model.VTJob:
		jv := v.(*model.JobValue)
		switch h.Intent {
		case model.IntentJobCreated:
			return tx.PutJob(h.Key, jv)
		case model.IntentJobCompleted, model.IntentJobFailed:
			return tx.DeleteJob(h.Key, jv)
		}
	}
	return nil
}
