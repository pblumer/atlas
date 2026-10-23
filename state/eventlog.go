package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// The event log (ADR-0472 §3): the signals and messages that were sent, and
// where each arrived, as state.
//
// applyToState folds one row per record — a send, a reception, a start, a message kept,
// taken from the buffer or expired — keyed by the partition and the log position of the
// record that carried it, both fixed by the log, so replay writes the same rows under
// the same keys (I4). A row holds its kind, the record's timestamp, the record's source
// position and the record's value without any payload. The source position is the
// cause: a send and the receptions it caused are written while one command is
// processed, so they share it, and a reader groups them without the fold keeping
// anything between records.
//
// The rows are dropped by an explicit prune fact through a position, never by a delete
// nobody replays, and the position the log was pruned through is kept beside the rows,
// as the event feed does (feed.go).

// EventLogKind is what an event-log row records.
type EventLogKind byte

const (
	// EventLogDispatched is a send: a signal broadcast or a message published
	// (model.DispatchValue).
	EventLogDispatched EventLogKind = 1
	// EventLogSignalReceived is a signal delivered to a waiting element
	// (model.SignalSubscriptionValue).
	EventLogSignalReceived EventLogKind = 2
	// EventLogSignalStarted is an instance a signal started: its definition and start
	// element (model.SignalSubscriptionValue, no instance yet).
	EventLogSignalStarted EventLogKind = 3
	// EventLogMessageDelivered is a message delivered to a waiting element, or — with a
	// receiving instance of zero — an instance it started (model.MessageFlowValue).
	EventLogMessageDelivered EventLogKind = 4
	// EventLogMessageKept is a message nothing received, kept in the buffer for its time
	// to live (model.MessageValue without its payload).
	EventLogMessageKept EventLogKind = 5
	// EventLogMessageTaken is a kept message handed to a subscription that opened later.
	EventLogMessageTaken EventLogKind = 6
	// EventLogMessageExpired is a kept message whose time to live passed while it still
	// waited: nothing ever received it.
	EventLogMessageExpired EventLogKind = 7
)

const (
	eventLogRows  byte = 0x00
	eventLogPrune byte = 0x01
)

// eventLogHead is the fixed part of a row before its value: kind, time, cause.
const eventLogHead = 1 + 8 + 8

func eventLogRowPrefix(partition uint16) []byte {
	return appendBE32([]byte{byte(cfEventLog), eventLogRows}, uint32(partition))
}

func keyEventLogRow(partition uint16, position uint64) []byte {
	return appendBE64(eventLogRowPrefix(partition), position)
}

func keyEventLogPrunedThrough(partition uint16) []byte {
	return appendBE32([]byte{byte(cfEventLog), eventLogPrune}, uint32(partition))
}

// EventLogEntry is one row of the event log: where its record sits on the log, when it
// was recorded, the record that caused it, and the fact — exactly one of the values,
// by Kind.
type EventLogEntry struct {
	Partition uint16
	Position  uint64
	At        int64
	Cause     uint64
	Kind      EventLogKind
	Dispatch  *model.DispatchValue
	Signal    *model.SignalSubscriptionValue
	Flow      *model.MessageFlowValue
	Message   *model.MessageValue
}

// eventLogValueType is the value type a kind's value is encoded as.
func eventLogValueType(k EventLogKind) (model.ValueType, bool) {
	switch k {
	case EventLogDispatched:
		return model.VTDispatch, true
	case EventLogSignalReceived, EventLogSignalStarted:
		return model.VTSignal, true
	case EventLogMessageDelivered:
		return model.VTMessageFlow, true
	case EventLogMessageKept, EventLogMessageTaken, EventLogMessageExpired:
		return model.VTMessage, true
	}
	return 0, false
}

// PutEventLogRow writes the row for the record at position on partition, recorded at
// at and caused by the record at cause. A message's payload is never written: the
// caller hands it over without one, and the row refuses one that carries it.
func (t *Tx) PutEventLogRow(partition uint16, position uint64, at int64, cause uint64, kind EventLogKind, v model.Value) error {
	if vt, ok := eventLogValueType(kind); !ok || v.ValueType() != vt {
		return fmt.Errorf("state: an event-log row of kind %d cannot carry a %s", kind, v.ValueType())
	}
	if m, ok := v.(*model.MessageValue); ok && len(m.Variables) > 0 {
		return fmt.Errorf("state: an event-log row never carries a message's payload")
	}
	row := append(t.scratch[:0], byte(kind))
	row = binary.BigEndian.AppendUint64(row, uint64(at))
	row = binary.BigEndian.AppendUint64(row, cause)
	t.scratch = model.AppendValue(row, v)
	return t.b.Set(keyEventLogRow(partition, position), t.scratch, nil)
}

// PruneEventLog drops every row of partition at or before through, and remembers
// through so a reader whose cursor is older is told what it missed. A cut below the
// one already made is a no-op: the rows below it are gone already.
func (t *Tx) PruneEventLog(partition uint16, through uint64) error {
	raw, closer, err := t.b.Get(keyEventLogPrunedThrough(partition))
	switch {
	case err == nil:
		done := uint64(0)
		if len(raw) >= 8 {
			done = binary.BigEndian.Uint64(raw)
		}
		closer.Close()
		if through <= done {
			return nil
		}
	case !errors.Is(err, pebble.ErrNotFound):
		return err
	}
	if err := t.b.DeleteRange(eventLogRowPrefix(partition), keyEventLogRow(partition, through+1), nil); err != nil {
		return err
	}
	return t.b.Set(keyEventLogPrunedThrough(partition), binary.BigEndian.AppendUint64(nil, through), nil)
}

// BufferedMessageWaits reports whether the buffered message msgKey still waits: an
// expiry folds an event-log row only for a message nothing ever received.
func (t *Tx) BufferedMessageWaits(msgKey uint64, v *model.MessageValue) (bool, error) {
	_, present, err := getCopy(t.b, keyBufferedMessage(v.MessageName, v.CorrelationKey, msgKey))
	return present, err
}

func decodeEventLogRow(partition uint16, key, raw []byte) (EventLogEntry, error) {
	if len(key) < 8 || len(raw) < eventLogHead {
		return EventLogEntry{}, fmt.Errorf("state: an event-log row of %d bytes is cut short", len(raw))
	}
	e := EventLogEntry{
		Partition: partition,
		Position:  binary.BigEndian.Uint64(key[len(key)-8:]),
		Kind:      EventLogKind(raw[0]),
		At:        int64(binary.BigEndian.Uint64(raw[1:9])),
		Cause:     binary.BigEndian.Uint64(raw[9:17]),
	}
	vt, ok := eventLogValueType(e.Kind)
	if !ok {
		return EventLogEntry{}, fmt.Errorf("state: an event-log row of unknown kind %d", e.Kind)
	}
	v, err := model.DecodeValue(vt, raw[eventLogHead:])
	if err != nil {
		return EventLogEntry{}, err
	}
	switch val := v.(type) {
	case *model.DispatchValue:
		e.Dispatch = val
	case *model.SignalSubscriptionValue:
		e.Signal = val
	case *model.MessageFlowValue:
		e.Flow = val
	case *model.MessageValue:
		e.Message = val
	}
	return e, nil
}

// EventLogAfter calls fn with the rows of partition after position after, oldest
// first, until the rows run out or fn returns an error — a caller stops a page early
// with a sentinel of its own.
func (q queries) EventLogAfter(partition uint16, after uint64, fn func(EventLogEntry) error) error {
	if after == ^uint64(0) {
		return nil
	}
	return q.scanRange(keyEventLogRow(partition, after+1), prefixEnd(eventLogRowPrefix(partition)), func(k, v []byte) error {
		e, err := decodeEventLogRow(partition, k, v)
		if err != nil {
			return err
		}
		return fn(e)
	})
}

// EventLogBefore calls fn with the rows of partition before position before, newest
// first; a before of zero reads from the newest row. It stops like EventLogAfter.
func (q queries) EventLogBefore(partition uint16, before uint64, fn func(EventLogEntry) error) error {
	hi := prefixEnd(eventLogRowPrefix(partition))
	if before != 0 {
		hi = keyEventLogRow(partition, before)
	}
	return q.scanRangeDesc(eventLogRowPrefix(partition), hi, func(k, v []byte) error {
		e, err := decodeEventLogRow(partition, k, v)
		if err != nil {
			return err
		}
		return fn(e)
	})
}

// EventLogPrunedThrough is the position partition's event log was pruned through, or
// 0 when it never was.
func (q queries) EventLogPrunedThrough(partition uint16) (uint64, error) {
	raw, ok, err := getCopy(q.r, keyEventLogPrunedThrough(partition))
	if err != nil || !ok || len(raw) < 8 {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw), nil
}

// EventLogThroughBefore is the position of the last row of partition recorded before
// cutoff, in position order, and whether there is one: what the retention sweep prunes
// through. Like the feed's, it stops at the first row recorded at or after cutoff, so
// the window is a lower bound, never a cut through the middle of the log.
func (q queries) EventLogThroughBefore(partition uint16, cutoff int64) (uint64, bool, error) {
	var (
		through uint64
		found   bool
	)
	err := q.scanPrefix(eventLogRowPrefix(partition), func(k, v []byte) error {
		if len(v) < 9 || int64(binary.BigEndian.Uint64(v[1:9])) >= cutoff {
			return errEventLogScanDone
		}
		through, found = binary.BigEndian.Uint64(k[len(k)-8:]), true
		return nil
	})
	if err != nil && !errors.Is(err, errEventLogScanDone) {
		return 0, false, err
	}
	return through, found, nil
}

var errEventLogScanDone = errors.New("state: event-log scan done")
