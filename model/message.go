package model

import "encoding/binary"

// MessageValue is a published message the engine keeps (ADR-0370): one that
// correlated nothing when it was published with a time to live, and waits in the
// buffer for the subscription that opens for it later.
//
// It rides VTMessage, the value type reserved for an incoming message since the
// first record shape; until the buffer, only the publish command used it, and no
// event did.
//
// ExpiresAt is computed once, from the processor's clock and the publish's time to
// live, and written into the event, so replay keeps the message exactly as long as
// the live run did (invariant I6). MessageId is the sender's own id, "" when the
// sender gave none; while the message lives, a second publish of the same name, key
// and id is the same message and is dropped.
//
// On IntentMessageBuffered the record carries the payload. On IntentMessageIdRecorded,
// IntentMessageConsumed and IntentMessageExpired it does not: those name the entry to
// keep or drop, and the payload already reached its instance or never will.
type MessageValue struct {
	MessageName    string
	CorrelationKey string
	MessageId      string
	ExpiresAt      int64 // Unix nanoseconds
	Variables      []VariableValue
}

func (*MessageValue) ValueType() ValueType { return VTMessage }

func (v *MessageValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.ExpiresAt))
	dst = appendString(dst, v.MessageName)
	dst = appendString(dst, v.CorrelationKey)
	dst = appendString(dst, v.MessageId)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(v.Variables)))
	for i := range v.Variables {
		// Each variable is length-prefixed: VariableValue.decode reads its appended
		// fields from whatever follows the text, so it has to be handed exactly its own.
		at := len(dst)
		dst = binary.LittleEndian.AppendUint32(dst, 0)
		dst = v.Variables[i].encode(dst)
		binary.LittleEndian.PutUint32(dst[at:], uint32(len(dst)-at-4))
	}
	return dst
}

func (v *MessageValue) decode(src []byte) error {
	if len(src) < 8 {
		return ErrShortBuffer
	}
	v.ExpiresAt = int64(binary.LittleEndian.Uint64(src))
	name, rest, err := readString(src[8:])
	if err != nil {
		return err
	}
	key, rest, err := readString(rest)
	if err != nil {
		return err
	}
	id, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.MessageName, v.CorrelationKey, v.MessageId = name, key, id
	if len(rest) < 4 {
		return ErrShortBuffer
	}
	n := binary.LittleEndian.Uint32(rest)
	rest = rest[4:]
	v.Variables = nil
	if n > 0 {
		// Each entry is at least its own length prefix, so a count the bytes cannot
		// hold is refused before anything is allocated for it.
		if uint64(n)*4 > uint64(len(rest)) {
			return ErrShortBuffer
		}
		v.Variables = make([]VariableValue, n)
	}
	for i := range v.Variables {
		if len(rest) < 4 {
			return ErrShortBuffer
		}
		size := binary.LittleEndian.Uint32(rest)
		rest = rest[4:]
		if uint64(size) > uint64(len(rest)) {
			return ErrShortBuffer
		}
		if err := v.Variables[i].decode(rest[:size]); err != nil {
			return err
		}
		rest = rest[size:]
	}
	return nil
}

// WithoutPayload returns the message with its variables dropped: the shape every
// record but the buffering one carries.
func (v MessageValue) WithoutPayload() MessageValue {
	v.Variables = nil
	return v
}
