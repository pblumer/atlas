package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// The instance outline (ADR-0517, bounded overview) is the instance's step
// history without what happens inside a multi-instance iteration: a body
// execution appears as its own activation and end, its iterations and every
// element inside them do not. It is what lets Operations show "Start → a loop of
// 50,000 iterations → End" from a few rows, where the full replay history holds a
// row per element of every iteration.
//
// Like every MI family it is written only by applyToState (I4): at each element
// activation and end, the fold decides from the event and from earlier state
// whether the element sits inside an iteration. An iteration is; an element
// whose flow scope is hidden is; everything hidden is remembered in
// cfOutlineHidden so its children are hidden in turn. An element directly under
// the process instance needs no read at all.

// OutlineStep is one row of an instance's outline.
type OutlineStep struct {
	At                 int64
	Position           uint64
	ElementID          int32
	ElementInstanceKey uint64
	ProcessDefKey      uint64 // the version that ran the element, as its event says
	TokenID            uint64
	ParentTokenID      uint64
	Action             byte // ReplayActivated, ReplayCompleted, ReplayTerminated
	MultiInstance      uint8
	BpmnElementType    uint8
}

const outlineValueSize = 4 + 8 + 8 + 8 + 8 + 1 + 1 + 1

func keyOutline(piKey uint64, ts int64, pos uint64) []byte {
	return appendBE64(appendOrderedInt64(appendBE64([]byte{byte(cfOutline)}, piKey), ts), pos)
}

// RecordOutlineStep records one element activation or end in the instance's
// outline, unless the element is inside a multi-instance iteration. It is
// called beside RecordElementReplay with the same facts.
func (t *Tx) RecordOutlineStep(elKey uint64, v *model.ElementInstanceValue, ts int64, pos uint64, action byte) error {
	pi := v.ProcessInstanceKey
	hidden := v.MultiInstance == 2
	if !hidden && v.FlowScopeKey != 0 && v.FlowScopeKey != pi {
		_, closer, err := t.b.Get(t.miRefKey(cfOutlineHidden, pi, v.FlowScopeKey))
		switch {
		case err == nil:
			hidden = true
			if err := closer.Close(); err != nil {
				return err
			}
		case !errors.Is(err, pebble.ErrNotFound):
			return err
		}
	}
	if hidden {
		if action != ReplayActivated {
			return nil
		}
		// Whatever this element scopes is hidden with it.
		return t.b.Set(t.miRefKey(cfOutlineHidden, pi, elKey), nil, nil)
	}
	k := t.miKey[:25]
	k[0] = byte(cfOutline)
	binary.BigEndian.PutUint64(k[1:9], pi)
	binary.BigEndian.PutUint64(k[9:17], uint64(ts)^(1<<63))
	binary.BigEndian.PutUint64(k[17:25], pos)
	val := t.miValue[:outlineValueSize]
	binary.BigEndian.PutUint32(val[0:4], uint32(v.ElementId))
	binary.BigEndian.PutUint64(val[4:12], elKey)
	binary.BigEndian.PutUint64(val[12:20], v.TokenID)
	binary.BigEndian.PutUint64(val[20:28], v.ParentTokenID)
	binary.BigEndian.PutUint64(val[28:36], v.ProcessDefKey)
	val[36], val[37], val[38] = action, v.MultiInstance, v.BpmnElementType
	return t.b.Set(k, val, nil)
}

func decodeOutlineStep(k, raw []byte) (OutlineStep, error) {
	if len(k) != 25 || k[0] != byte(cfOutline) || len(raw) != outlineValueSize ||
		raw[36] < ReplayActivated || raw[36] > ReplayTerminated {
		return OutlineStep{}, errors.New("state: corrupt instance outline row")
	}
	return OutlineStep{
		At:                 int64(binary.BigEndian.Uint64(k[9:17]) ^ (1 << 63)),
		Position:           binary.BigEndian.Uint64(k[17:25]),
		ElementID:          int32(binary.BigEndian.Uint32(raw[0:4])),
		ElementInstanceKey: binary.BigEndian.Uint64(raw[4:12]),
		TokenID:            binary.BigEndian.Uint64(raw[12:20]),
		ParentTokenID:      binary.BigEndian.Uint64(raw[20:28]),
		ProcessDefKey:      binary.BigEndian.Uint64(raw[28:36]),
		Action:             raw[36],
		MultiInstance:      raw[37],
		BpmnElementType:    raw[38],
	}, nil
}

// OutlinePosition is a step's place in its page order.
type OutlinePosition struct {
	At       int64
	Position uint64
}

// OutlinePage visits up to limit outline steps of one instance in the order
// they happened, after the given position when one is given, hiding steps past
// highWater when it is non-zero. It returns the position to resume after when
// more follow.
func (q queries) OutlinePage(piKey uint64, after *OutlinePosition, highWater uint64, limit int, fn func(OutlineStep) error) (*OutlinePosition, error) {
	if err := validateMILimit(limit); err != nil {
		return nil, err
	}
	ready, err := q.OutlineIndexReady()
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, ErrOutlineIndexUnavailable
	}
	prefix := appendBE64([]byte{byte(cfOutline)}, piKey)
	lower := prefix
	if after != nil {
		lower = append(keyOutline(piKey, after.At, after.Position), 0)
	}
	iter, err := q.r.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	var last OutlinePosition
	for iter.First(); iter.Valid(); iter.Next() {
		step, err := decodeOutlineStep(iter.Key(), iter.Value())
		if err != nil {
			return nil, err
		}
		if highWater != 0 && step.Position > highWater {
			// Event timestamps do not decrease in log order, so (time, position)
			// order is log order: nothing after this row is at or below highWater.
			break
		}
		if count == limit {
			return &last, nil
		}
		if err := fn(step); err != nil {
			return nil, err
		}
		count++
		last = OutlinePosition{At: step.At, Position: step.Position}
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("state: read instance outline: %w", err)
	}
	return nil, nil
}
