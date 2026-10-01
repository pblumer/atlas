package model

import (
	"encoding/binary"
	"strings"
)

// ActionOutcomeValue is how one action asked of an order position ended
// (ADR-0429 §3): completed, rejected or failed, under the event type it is
// published as beyond Atlas.
//
// It is keyed by the order, the position and the command id, because that is what
// makes a report idempotent: the process that carries out an action reports at
// least once, and a second identical report must not be a second fact. A report of
// a different outcome for the same command is refused before it is written, so the
// family never holds two endings of one command.
//
// Like an entitlement it outlives the order it came from, and the same open question
// about erasure applies to it (ADR-0346): it names the principal by id and nothing
// else, and Result is bounded and holds scalars an action declares — a rule for the
// product's author, because the log cannot enforce it (ADR-0314).
//
// At is read by the caller at command time and frozen into the event, so replay
// reproduces it rather than re-reading a clock (I4/I6).
type ActionOutcomeValue struct {
	InstanceKey uint64 // the instance that carried the action out, when one did
	At          int64  // when the outcome was recorded, Unix nanoseconds

	OrderID   string
	Position  string // the order position (ADR-0384): the item id, or item#variant
	CommandID string
	Source    string // who reported it: the order itself, the REST route, a send task
	Action    string // the action's key
	Effect    string // provision | deprovision | change | service
	Outcome   string // completed | rejected | failed
	EventType string // what it is published as: declared, or <message>.<outcome>
	Principal string // whose right it is, by id
	ItemID    string
	VariantID string
	Result    string // a JSON object of scalars, bounded by whoever reports it
}

func (*ActionOutcomeValue) ValueType() ValueType { return VTActionOutcome }

func (v *ActionOutcomeValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.InstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.At))
	for _, s := range []string{v.OrderID, v.Position, v.CommandID, v.Source, v.Action, v.Effect,
		v.Outcome, v.EventType, v.Principal, v.ItemID, v.VariantID, v.Result} {
		dst = appendString(dst, s)
	}
	return dst
}

func (v *ActionOutcomeValue) decode(src []byte) error {
	if len(src) < 16 {
		return ErrShortBuffer
	}
	v.InstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.At = int64(binary.LittleEndian.Uint64(src[8:]))
	rest := src[16:]
	// The key and the ending are required: a truncated record that decoded to "some
	// command of some position ended somehow" would be published by the feed.
	required := []*string{&v.OrderID, &v.Position, &v.CommandID, &v.Source, &v.Action, &v.Effect, &v.Outcome}
	for _, into := range required {
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	// The rest are append-compatible, as the other values in this package are: a
	// record written before a field simply ends, and the field decodes empty.
	for _, into := range []*string{&v.EventType, &v.Principal, &v.ItemID, &v.VariantID, &v.Result} {
		if len(rest) == 0 {
			return nil
		}
		s, next, err := readString(rest)
		if err != nil {
			return err
		}
		*into, rest = s, next
	}
	return nil
}

// Valid reports whether this outcome can be written down: it names the command it
// ends and how it ended.
func (v *ActionOutcomeValue) Valid() bool {
	for _, s := range []string{v.OrderID, v.Position, v.CommandID, v.Action, v.Outcome} {
		if strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}
