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
// outline, unless the element is inside a multi-instance iteration; then it
// records it in that iteration's own steps (cfIterationStep) instead. It is
// called beside RecordElementReplay with the same facts.
//
// An iteration owns itself. An element whose flow scope is hidden belongs to the
// iteration that scope belongs to, as cfOutlineHidden remembers it; a nested
// body inside an iteration is one of that iteration's steps, and each of its
// own iterations owns itself in turn. A hidden row written before iterations
// had steps carries no owner: its element stays hidden and records no steps,
// because the store that holds it does not claim complete iteration steps.
func (t *Tx) RecordOutlineStep(elKey uint64, v *model.ElementInstanceValue, ts int64, pos uint64, action byte) error {
	pi := v.ProcessInstanceKey
	hidden := v.MultiInstance == 2
	var owner uint64
	if hidden {
		owner = elKey
	} else if v.FlowScopeKey != 0 && v.FlowScopeKey != pi {
		raw, closer, err := t.b.Get(t.miRefKey(cfOutlineHidden, pi, v.FlowScopeKey))
		switch {
		case err == nil:
			hidden = true
			if len(raw) == 8 {
				owner = binary.BigEndian.Uint64(raw)
			}
			if err := closer.Close(); err != nil {
				return err
			}
		case !errors.Is(err, pebble.ErrNotFound):
			return err
		}
	}
	if !hidden {
		return t.b.Set(t.outlineKey(pi, ts, pos), t.outlineValue(elKey, v, action), nil)
	}
	var err error
	if action == ReplayActivated {
		// Whatever this element scopes is hidden with it, in the same iteration;
		// under an older row without an owner it stays hidden without one.
		var val [8]byte
		binary.BigEndian.PutUint64(val[:], owner)
		ownerBytes := val[:]
		if owner == 0 {
			ownerBytes = nil
		}
		err = t.b.Set(t.miRefKey(cfOutlineHidden, pi, elKey), ownerBytes, nil)
	}
	if err == nil && owner != 0 {
		err = t.b.Set(t.iterationStepKey(pi, owner, ts, pos), t.outlineValue(elKey, v, action), nil)
	}
	return err
}

func (t *Tx) outlineKey(pi uint64, ts int64, pos uint64) []byte {
	k := t.miKey[:25]
	k[0] = byte(cfOutline)
	binary.BigEndian.PutUint64(k[1:9], pi)
	binary.BigEndian.PutUint64(k[9:17], uint64(ts)^(1<<63))
	binary.BigEndian.PutUint64(k[17:25], pos)
	return k
}

func (t *Tx) iterationStepKey(pi, iteration uint64, ts int64, pos uint64) []byte {
	k := t.miKey[:33]
	k[0] = byte(cfIterationStep)
	binary.BigEndian.PutUint64(k[1:9], pi)
	binary.BigEndian.PutUint64(k[9:17], iteration)
	binary.BigEndian.PutUint64(k[17:25], uint64(ts)^(1<<63))
	binary.BigEndian.PutUint64(k[25:33], pos)
	return k
}

func (t *Tx) outlineValue(elKey uint64, v *model.ElementInstanceValue, action byte) []byte {
	val := t.miValue[:outlineValueSize]
	binary.BigEndian.PutUint32(val[0:4], uint32(v.ElementId))
	binary.BigEndian.PutUint64(val[4:12], elKey)
	binary.BigEndian.PutUint64(val[12:20], v.TokenID)
	binary.BigEndian.PutUint64(val[20:28], v.ParentTokenID)
	binary.BigEndian.PutUint64(val[28:36], v.ProcessDefKey)
	val[36], val[37], val[38] = action, v.MultiInstance, v.BpmnElementType
	return val
}

// decodeStepRow decodes an outline or iteration step row whose key is the
// family byte, prefixLen-1 bytes of owner keys, the ordered time and the position.
func decodeStepRow(k, raw []byte, family columnFamily, prefixLen int) (OutlineStep, error) {
	if len(k) != prefixLen+16 || k[0] != byte(family) || len(raw) != outlineValueSize ||
		raw[36] < ReplayActivated || raw[36] > ReplayTerminated {
		return OutlineStep{}, errors.New("state: corrupt step row")
	}
	return OutlineStep{
		At:                 int64(binary.BigEndian.Uint64(k[prefixLen:prefixLen+8]) ^ (1 << 63)),
		Position:           binary.BigEndian.Uint64(k[prefixLen+8 : prefixLen+16]),
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
	return q.stepPage(appendBE64([]byte{byte(cfOutline)}, piKey), cfOutline, after, highWater, limit, fn)
}

// IterationStepsPage visits up to limit steps inside one multi-instance
// iteration — the iteration's own activation and end, and every element that ran
// inside it outside a nested iteration — in the order they happened, like
// OutlinePage. A nested body is one step; its iterations have their own steps.
func (q queries) IterationStepsPage(piKey, iterationKey uint64, after *OutlinePosition, highWater uint64, limit int, fn func(OutlineStep) error) (*OutlinePosition, error) {
	if err := validateMILimit(limit); err != nil {
		return nil, err
	}
	ready, err := q.IterationStepsIndexReady()
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, ErrIterationStepsUnavailable
	}
	return q.stepPage(appendBE64(appendBE64([]byte{byte(cfIterationStep)}, piKey), iterationKey), cfIterationStep, after, highWater, limit, fn)
}

func (q queries) stepPage(prefix []byte, family columnFamily, after *OutlinePosition, highWater uint64, limit int, fn func(OutlineStep) error) (*OutlinePosition, error) {
	lower := prefix
	if after != nil {
		lower = append(appendBE64(appendOrderedInt64(append([]byte(nil), prefix...), after.At), after.Position), 0)
	}
	iter, err := q.r.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	var last OutlinePosition
	for iter.First(); iter.Valid(); iter.Next() {
		step, err := decodeStepRow(iter.Key(), iter.Value(), family, len(prefix))
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
		return nil, fmt.Errorf("state: read steps: %w", err)
	}
	return nil, nil
}

// IterationOwners visits every element instance of one process instance that ran
// inside a multi-instance iteration, with the element-instance key of the iteration
// that owns it — the same fact the outline uses to hide it (cfOutlineHidden), read
// back so a full replay can nest such a step under its iteration. The engine gives an
// embedded subprocess's inner start event a fresh token with no lineage, so token
// lineage alone cannot say which iteration of a multi-instance subprocess an inner
// step belongs to; the flow scope can, and the outline already folded it into this
// owner. An iteration owns itself and is not visited (its body groups it by token
// lineage); a nested body is owned by the iteration it runs in, and each of its own
// iterations by itself. Rows written before iterations had owners carry none and are
// skipped. A store whose iteration-step index is not proven complete visits nothing,
// so a reader never presents a partial nesting as the structure.
func (q queries) IterationOwners(piKey uint64, fn func(elementKey, iterationKey uint64) error) error {
	ready, err := q.IterationStepsIndexReady()
	if err != nil || !ready {
		return err
	}
	prefix := appendBE64([]byte{byte(cfOutlineHidden)}, piKey)
	return q.scanPrefix(prefix, func(k, v []byte) error {
		if len(k) != len(prefix)+8 {
			return errors.New("state: corrupt outline-hidden row")
		}
		if len(v) != 8 {
			return nil // hidden before iterations had owners
		}
		elKey := binary.BigEndian.Uint64(k[len(prefix):])
		owner := binary.BigEndian.Uint64(v)
		if owner == 0 || owner == elKey {
			return nil
		}
		return fn(elKey, owner)
	})
}
