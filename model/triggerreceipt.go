package model

import "encoding/binary"

// TriggerReceiptValue records that a directed trigger was applied (ADR-0425): which
// sender asked, under which of its own trigger ids, and the instance that answered.
//
// It exists because the senders of a trigger deliver at least once. An HR system
// that reports a leaver and times out waiting for the answer reports the leaver
// again, and a second deprovisioning is a second conversation with a target system
// Atlas does not control. The receipt is what makes the second delivery answer with
// the first instance instead of creating another.
//
// It is a set, not a high-water mark like InboundDeliveryValue (ADR-0075): a
// sender's trigger ids are its own and are not ordered, so "everything up to N was
// applied" is not a statement anybody could make about them.
//
// On IntentTriggerReceiptsPruned only Cutoff is meaningful: every receipt received
// before it is dropped. The cutoff is written into the event, so replay drops
// exactly what was dropped live (invariant I6).
type TriggerReceiptValue struct {
	Source      string
	TriggerID   string
	InstanceKey uint64
	At          int64 // when the trigger was received, Unix nanoseconds
	Cutoff      int64 // prune events only: receipts received before this go
}

func (*TriggerReceiptValue) ValueType() ValueType { return VTTriggerReceipt }

func (v *TriggerReceiptValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.InstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.At))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Cutoff))
	dst = appendString(dst, v.Source)
	return appendString(dst, v.TriggerID)
}

func (v *TriggerReceiptValue) decode(src []byte) error {
	if len(src) < 24 {
		return ErrShortBuffer
	}
	v.InstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.At = int64(binary.LittleEndian.Uint64(src[8:]))
	v.Cutoff = int64(binary.LittleEndian.Uint64(src[16:]))
	source, rest, err := readString(src[24:])
	if err != nil {
		return err
	}
	id, _, err := readString(rest)
	if err != nil {
		return err
	}
	v.Source, v.TriggerID = source, id
	return nil
}
