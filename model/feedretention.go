package model

import "encoding/binary"

// FeedRetentionValue is the cut the event feed's retention makes (ADR-0429 §5): the
// feed drops every row of the partition at or before Through, a log position.
//
// The sweep picks the position from the rows' own times and freezes it into the
// command, so replay prunes exactly what the live run pruned rather than re-reading
// a clock (I4/I6).
type FeedRetentionValue struct {
	Through uint64
}

func (*FeedRetentionValue) ValueType() ValueType { return VTFeedRetention }

func (v *FeedRetentionValue) encode(dst []byte) []byte {
	return binary.LittleEndian.AppendUint64(dst, v.Through)
}

func (v *FeedRetentionValue) decode(src []byte) error {
	if len(src) < 8 {
		return ErrShortBuffer
	}
	v.Through = binary.LittleEndian.Uint64(src)
	return nil
}
