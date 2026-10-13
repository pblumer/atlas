package state

import (
	"encoding/binary"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// The message buffer (ADR-0370).
//
// Three families hold it. The buffer itself mirrors the subscriptions it serves:
// keyed by the same length-prefixed (name, correlation key) pair, so "is a message
// waiting for this subscription?" is the one prefix scan "is a subscription waiting
// for this message?" already is, and the message key that follows orders the
// messages of one pair oldest first. The expiry index leads with the expiry, so the
// sweep finds what is due with a range scan up to now, the shape of the due-timer and
// history-expiry indexes. The id index answers whether a sender's message id was
// already taken while that message lives.
//
// A message's expiry entry outlives its delivery when it carries an id: the id must be
// remembered until the message would have expired, and the sweep is what forgets it.

func bufferedMessagePrefix(name, correlationKey string) []byte {
	b := appendLenString([]byte{byte(cfBufferedMessage)}, name)
	return appendLenString(b, correlationKey)
}

func keyBufferedMessage(name, correlationKey string, msgKey uint64) []byte {
	return appendBE64(bufferedMessagePrefix(name, correlationKey), msgKey)
}

func keyMessageExpiry(expiresAt int64, msgKey uint64) []byte {
	return appendBE64(appendOrderedInt64([]byte{byte(cfMessageExpiry)}, expiresAt), msgKey)
}

// keyMessageId keys a sender's message id within its message name and correlation key,
// the triple ADR-0370 names: a retry repeats all three, and two senders that happen to
// use the same id for different messages do not collide.
func keyMessageId(name, correlationKey, messageId string) []byte {
	b := appendLenString([]byte{byte(cfMessageId)}, name)
	b = appendLenString(b, correlationKey)
	return append(b, messageId...)
}

// PutBufferedMessage keeps a message for a later subscriber: the message with its
// payload, its expiry entry and, when it carries one, its id.
func (t *Tx) PutBufferedMessage(msgKey uint64, v *model.MessageValue) error {
	if err := t.b.Set(keyBufferedMessage(v.MessageName, v.CorrelationKey, msgKey), t.encodeValue(v), nil); err != nil {
		return err
	}
	return t.RecordMessageId(msgKey, v)
}

// RecordMessageId remembers a message until its expiry without keeping its payload:
// the expiry entry, and the id when there is one. A message delivered at once that
// carries an id is recorded this way, so a repeat of it is recognised.
func (t *Tx) RecordMessageId(msgKey uint64, v *model.MessageValue) error {
	bare := v.WithoutPayload()
	if err := t.b.Set(keyMessageExpiry(v.ExpiresAt, msgKey), t.encodeValue(&bare), nil); err != nil {
		return err
	}
	if v.MessageId == "" {
		return nil
	}
	t.scratch = binary.BigEndian.AppendUint64(t.scratch[:0], msgKey)
	return t.b.Set(keyMessageId(v.MessageName, v.CorrelationKey, v.MessageId), t.scratch, nil)
}

// ConsumeBufferedMessage removes a delivered message from the buffer. Its expiry entry
// stays while its id has to be remembered, and goes with it when there is none.
func (t *Tx) ConsumeBufferedMessage(msgKey uint64, v *model.MessageValue) error {
	if err := t.b.Delete(keyBufferedMessage(v.MessageName, v.CorrelationKey, msgKey), nil); err != nil {
		return err
	}
	if v.MessageId != "" {
		return nil
	}
	return t.b.Delete(keyMessageExpiry(v.ExpiresAt, msgKey), nil)
}

// ExpireMessage drops everything kept for a message whose time to live has passed:
// the message, if it still waited, its expiry entry and its id.
func (t *Tx) ExpireMessage(msgKey uint64, v *model.MessageValue) error {
	if err := t.b.Delete(keyBufferedMessage(v.MessageName, v.CorrelationKey, msgKey), nil); err != nil {
		return err
	}
	if err := t.b.Delete(keyMessageExpiry(v.ExpiresAt, msgKey), nil); err != nil {
		return err
	}
	if v.MessageId == "" {
		return nil
	}
	return t.b.Delete(keyMessageId(v.MessageName, v.CorrelationKey, v.MessageId), nil)
}

// MessageIdTaken reports whether a message with this name, key and id is still
// remembered. It reads through the batch, so two publishes of one id in a single batch
// keep one.
func (t *Tx) MessageIdTaken(name, correlationKey, messageId string) (bool, error) {
	_, ok, err := getCopy(t.b, keyMessageId(name, correlationKey, messageId))
	return ok, err
}

// MessageExpiryPending reports whether a message's expiry entry is still there, so the
// sweep's command drops a message once even when two sweeps offered it.
func (t *Tx) MessageExpiryPending(msgKey uint64, expiresAt int64) (bool, error) {
	_, ok, err := getCopy(t.b, keyMessageExpiry(expiresAt, msgKey))
	return ok, err
}

// OldestBufferedMessage returns the oldest message waiting for a (name, correlation
// key) pair that has not expired by now. An expired message the sweep has not reached
// yet is passed over: it is no longer anybody's to receive. It reads through the
// batch, so a message buffered earlier in the same batch is found.
func (t *Tx) OldestBufferedMessage(name, correlationKey string, now int64) (uint64, model.MessageValue, bool, error) {
	prefix := bufferedMessagePrefix(name, correlationKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return 0, model.MessageValue{}, false, err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var v model.MessageValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return 0, model.MessageValue{}, false, err
		}
		if v.ExpiresAt <= now {
			continue
		}
		return trailingKey(iter.Key()), v, true, nil
	}
	return 0, model.MessageValue{}, false, iter.Error()
}

// BufferedMessages calls fn for every message waiting in the buffer, in (name, key,
// age) order. It is for tests and for an operator reading what waits; nothing on the
// command path walks the whole buffer.
func (q queries) BufferedMessages(fn func(msgKey uint64, v *model.MessageValue) error) error {
	return q.scanPrefix([]byte{byte(cfBufferedMessage)}, func(k, raw []byte) error {
		var v model.MessageValue
		if err := model.DecodeValueInto(&v, raw); err != nil {
			return err
		}
		return fn(trailingKey(k), &v)
	})
}

// DueMessageExpiries calls fn for every message whose time to live has passed by now,
// in expiry order, with the message as its expiry entry keeps it (no payload). It is a
// range scan bounded by now, so an idle buffer costs one empty scan per sweep.
func (q queries) DueMessageExpiries(now int64, fn func(msgKey uint64, v *model.MessageValue) error) error {
	lo := []byte{byte(cfMessageExpiry)}
	hi := prefixEnd(appendOrderedInt64([]byte{byte(cfMessageExpiry)}, now))
	return q.scanRange(lo, hi, func(k, raw []byte) error {
		var v model.MessageValue
		if err := model.DecodeValueInto(&v, raw); err != nil {
			return err
		}
		return fn(trailingKey(k), &v)
	})
}
