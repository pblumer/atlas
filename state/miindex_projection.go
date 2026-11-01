package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// miReverseKey reuses Tx.miKey for point references. It must never be retained
// beyond this synchronous Pebble operation; Pebble copies keys on mutation.
func (t *Tx) miReverseKey(family columnFamily, piKey, elementKey uint64) []byte {
	k := t.miKey[:17]
	k[0] = byte(family)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], elementKey)
	return k
}

func (t *Tx) miBodyKey(piKey, pos, bodyKey uint64) []byte {
	k := t.miKey[:25]
	k[0] = byte(cfMIBody)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], pos)
	binary.BigEndian.PutUint64(k[17:25], bodyKey)
	return k
}

func (t *Tx) miIterationKey(piKey, bodyKey, bodyPos, roundKey uint64) []byte {
	k := t.miKey[:33]
	k[0] = byte(cfMIIteration)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], bodyKey)
	binary.BigEndian.PutUint64(k[17:25], bodyPos)
	binary.BigEndian.PutUint64(k[25:33], roundKey)
	return k
}

func (t *Tx) miBodyActivationPosition(piKey, bodyKey uint64) (uint64, bool, error) {
	raw, closer, err := t.b.Get(t.miReverseKey(cfMIBodyByElement, piKey, bodyKey))
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(raw) != 8 {
		_ = closer.Close()
		return 0, false, fmt.Errorf("state: corrupt MI body reference (%d bytes)", len(raw))
	}
	pos := binary.BigEndian.Uint64(raw)
	return pos, true, closer.Close()
}

func (t *Tx) miRoundReference(piKey, roundKey uint64) (uint64, uint64, bool, error) {
	raw, closer, err := t.b.Get(t.miReverseKey(cfMIRoundByElement, piKey, roundKey))
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if len(raw) != 16 {
		_ = closer.Close()
		return 0, 0, false, fmt.Errorf("state: corrupt MI round reference (%d bytes)", len(raw))
	}
	bodyKey, pos := binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])
	return bodyKey, pos, true, closer.Close()
}

func (t *Tx) miStoreValue(key []byte, v MIHistoryEntry) error {
	t.scratch = appendMIHistoryValue(t.scratch[:0], v)
	return t.b.Set(key, t.scratch, nil)
}

// RecordMIActivation materializes body/round lineage from the durable
// Activated event. The parent body activation's log position is found through
// an O(1) indexed-batch lookup: it is a persisted prior fact, including when
// this activation and its body were written in the same processor batch.
// No log scan, clock, fresh key, or behaviour execution occurs in this fold.
func (t *Tx) RecordMIActivation(elementKey uint64, v *model.ElementInstanceValue, pos uint64) error {
	if v.MultiInstance != 1 && v.MultiInstance != 2 {
		return nil
	}
	entry := MIHistoryEntry{
		ActivationPosition: pos, ProcessDefKey: v.ProcessDefKey,
		ElementID: v.ElementId, TokenID: v.TokenID,
		ParentTokenID: v.ParentTokenID, State: MIHistoryActive,
	}
	if v.MultiInstance == 1 {
		if err := t.miStoreValue(t.miBodyKey(v.ProcessInstanceKey, pos, elementKey), entry); err != nil {
			return err
		}
		t.scratch = appendBE64(t.scratch[:0], pos)
		return t.b.Set(t.miReverseKey(cfMIBodyByElement, v.ProcessInstanceKey, elementKey), t.scratch, nil)
	}
	bodyKey := v.FlowScopeKey
	bodyPos, ok, err := t.miBodyActivationPosition(v.ProcessInstanceKey, bodyKey)
	if err != nil || !ok {
		// Pre-index histories can still be live after a rolling upgrade. Their
		// body activation event is absent from this projection; never attach
		// the round to an invented activation. Backfill is a separate OHS2 slice.
		return err
	}
	if err := t.miStoreValue(t.miIterationKey(v.ProcessInstanceKey, bodyKey, bodyPos, elementKey), entry); err != nil {
		return err
	}
	t.scratch = appendBE64(t.scratch[:0], bodyKey)
	t.scratch = appendBE64(t.scratch, bodyPos)
	return t.b.Set(t.miReverseKey(cfMIRoundByElement, v.ProcessInstanceKey, elementKey), t.scratch, nil)
}

func (t *Tx) miFinishValue(key []byte, endPosition uint64, state MIHistoryState) error {
	raw, closer, err := t.b.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil // a legacy activation with no projection
	}
	if err != nil {
		return err
	}
	entry, err := decodeMIHistoryValue(raw)
	if cerr := closer.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	entry.EndPosition = endPosition
	entry.State = state
	return t.miStoreValue(key, entry)
}

// RecordMIEnd applies the final lifecycle fact to a historical projection.
// Terminal records stay indexed after the live element is removed; they are
// retained until the process-instance history is explicitly purged.
func (t *Tx) RecordMIEnd(elementKey uint64, v *model.ElementInstanceValue, pos uint64, terminated bool) error {
	if v.MultiInstance != 1 && v.MultiInstance != 2 {
		return nil
	}
	state := MIHistoryCompleted
	if terminated {
		state = MIHistoryTerminated
	}
	if v.MultiInstance == 1 {
		bodyPos, ok, err := t.miBodyActivationPosition(v.ProcessInstanceKey, elementKey)
		if err != nil || !ok {
			return err
		}
		return t.miFinishValue(t.miBodyKey(v.ProcessInstanceKey, bodyPos, elementKey), pos, state)
	}
	bodyKey, bodyPos, ok, err := t.miRoundReference(v.ProcessInstanceKey, elementKey)
	if err != nil || !ok {
		return err
	}
	return t.miFinishValue(t.miIterationKey(v.ProcessInstanceKey, bodyKey, bodyPos, elementKey), pos, state)
}

// purgeMIIndexHistory drops complete per-instance MI index ranges without a
// scan or individual deletes over potentially 50,000 rounds. Other instances'
// indexes and the immutable WAL are not affected.
func (t *Tx) purgeMIIndexHistory(piKey uint64) error {
	for _, family := range [...]columnFamily{
		cfMIBody, cfMIIteration, cfMIRoundByElement, cfMIBodyByElement,
	} {
		var lo, hi [9]byte
		lo[0], hi[0] = byte(family), byte(family)
		binary.BigEndian.PutUint64(lo[1:], piKey)
		var end []byte
		if piKey == ^uint64(0) {
			hi[0] = byte(family) + 1
			end = hi[:1]
		} else {
			binary.BigEndian.PutUint64(hi[1:], piKey+1)
			end = hi[:]
		}
		if err := t.b.DeleteRange(lo[:], end, nil); err != nil {
			return err
		}
	}
	return nil
}
