package state

import (
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// Trigger receipts (ADR-0425).
//
// A directed trigger is delivered at least once by its sender, so the engine keeps
// one receipt per (sender, trigger id) it has applied: a second delivery reads it and
// answers with the instance the first one created. The family is keyed by the pair
// because that is the only question asked of it on the command path; pruning by age
// walks the whole family, which is bounded by the retention and runs rarely.

// keyTriggerReceipt keys one receipt. The 0x00 separator keeps a source whose name
// prefixes another's from reading the other's trigger ids as its own.
func keyTriggerReceipt(source, triggerID string) []byte {
	out := make([]byte, 0, 2+len(source)+len(triggerID))
	out = append(out, byte(cfTriggerReceipt))
	out = append(out, source...)
	out = append(out, 0x00)
	return append(out, triggerID...)
}

// PutTriggerReceipt records that a trigger was applied and which instance answered.
func (t *Tx) PutTriggerReceipt(source, triggerID string, instanceKey uint64, at int64) error {
	t.scratch = binary.LittleEndian.AppendUint64(t.scratch[:0], instanceKey)
	t.scratch = binary.LittleEndian.AppendUint64(t.scratch, uint64(at))
	return t.b.Set(keyTriggerReceipt(source, triggerID), t.scratch, nil)
}

// TriggerReceipt returns the instance that answered a trigger this sender already
// delivered, and false when the trigger is new. It reads through the transaction, so
// a receipt written earlier in the same batch counts: two deliveries of one trigger
// in a single batch start one instance.
func (t *Tx) TriggerReceipt(source, triggerID string) (uint64, bool, error) {
	raw, ok, err := getCopy(t.b, keyTriggerReceipt(source, triggerID))
	if err != nil || !ok {
		return 0, false, err
	}
	if len(raw) < 16 {
		return 0, false, fmt.Errorf("state: trigger receipt for %q/%q is %d bytes, want 16", source, triggerID, len(raw))
	}
	return binary.LittleEndian.Uint64(raw), true, nil
}

// TriggerReceipt reads the instance that answered a trigger this sender delivered,
// outside a transaction — for a reader such as a job handler that needs to know a
// trigger was applied in a batch already committed.
func (q queries) TriggerReceipt(source, triggerID string) (uint64, bool, error) {
	raw, ok, err := getCopy(q.r, keyTriggerReceipt(source, triggerID))
	if err != nil || !ok {
		return 0, false, err
	}
	if len(raw) < 16 {
		return 0, false, fmt.Errorf("state: trigger receipt for %q/%q is %d bytes, want 16", source, triggerID, len(raw))
	}
	return binary.LittleEndian.Uint64(raw), true, nil
}

// PruneTriggerReceipts drops every receipt received before cutoff. The cutoff comes
// from the event, and the receipts from state this same pipeline built, so replay
// drops exactly what was dropped live (invariants I4, I6).
func (t *Tx) PruneTriggerReceipts(cutoff int64) error {
	prefix := []byte{byte(cfTriggerReceipt)}
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	var stale [][]byte
	for iter.First(); iter.Valid(); iter.Next() {
		v := iter.Value()
		if len(v) >= 16 && int64(binary.LittleEndian.Uint64(v[8:])) < cutoff {
			stale = append(stale, append([]byte(nil), iter.Key()...))
		}
	}
	if err := iter.Error(); err != nil {
		iter.Close()
		return err
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, k := range stale {
		if err := t.b.Delete(k, nil); err != nil {
			return err
		}
	}
	return nil
}

// TriggerReceiptCount returns how many receipts are held. It is for tests and for
// an operator asking whether pruning keeps up; nothing on the command path reads it.
func (q queries) TriggerReceiptCount() (int, error) {
	n := 0
	err := q.scanPrefix([]byte{byte(cfTriggerReceipt)}, func(_, _ []byte) error {
		n++
		return nil
	})
	return n, err
}

// HasTriggerReceiptBefore reports whether any receipt was received before cutoff,
// so a prune is written to the log only when it would drop something.
func (q queries) HasTriggerReceiptBefore(cutoff int64) (bool, error) {
	found := false
	errStop := fmt.Errorf("stop")
	err := q.scanPrefix([]byte{byte(cfTriggerReceipt)}, func(_, v []byte) error {
		if len(v) >= 16 && int64(binary.LittleEndian.Uint64(v[8:])) < cutoff {
			found = true
			return errStop
		}
		return nil
	})
	if err == errStop {
		err = nil
	}
	return found, err
}
