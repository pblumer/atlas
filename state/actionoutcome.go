package state

import "github.com/pblumer/atlas/model"

// Action outcomes (ADR-0429 §3).
//
// One record per action asked of an order position, keyed by the order, the
// position and the command id, because the command path asks exactly one question
// of the family — has this command already ended, and how — and a position's
// outcomes are read together. The 0x00 separators keep an order or a position whose
// id prefixes another's from reading the other's commands as its own.
//
// The family is never pruned by instance retention: an outcome outlives the order
// it came from, as an entitlement does.

func actionOutcomePositionPrefix(orderID, position string) []byte {
	out := make([]byte, 0, 3+len(orderID)+len(position))
	out = append(out, byte(cfActionOutcome))
	out = append(out, orderID...)
	out = append(out, 0x00)
	out = append(out, position...)
	return append(out, 0x00)
}

func keyActionOutcome(orderID, position, commandID string) []byte {
	return append(actionOutcomePositionPrefix(orderID, position), commandID...)
}

// PutActionOutcome records how one command ended.
func (t *Tx) PutActionOutcome(v *model.ActionOutcomeValue) error {
	return t.b.Set(keyActionOutcome(v.OrderID, v.Position, v.CommandID), t.encodeValue(v), nil)
}

// ActionOutcome returns how a command already ended, and false when it has not. It
// reads through the transaction, so an outcome written earlier in the same batch
// counts: two reports of one command in one batch write one fact.
func (t *Tx) ActionOutcome(orderID, position, commandID string) (*model.ActionOutcomeValue, bool, error) {
	raw, ok, err := getCopy(t.b, keyActionOutcome(orderID, position, commandID))
	if err != nil || !ok {
		return nil, false, err
	}
	v, err := model.DecodeValue(model.VTActionOutcome, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.ActionOutcomeValue), true, nil
}

// ActionOutcome reads how one command of a position ended.
func (q queries) ActionOutcome(orderID, position, commandID string) (*model.ActionOutcomeValue, bool, error) {
	raw, ok, err := getCopy(q.r, keyActionOutcome(orderID, position, commandID))
	if err != nil || !ok {
		return nil, false, err
	}
	v, err := model.DecodeValue(model.VTActionOutcome, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.ActionOutcomeValue), true, nil
}

// ActionOutcomesOf calls fn with every outcome recorded for one position, in
// command-id order. A prefix scan bounded by how many actions were asked of one
// position, which is what a position's page shows.
func (q queries) ActionOutcomesOf(orderID, position string, fn func(v *model.ActionOutcomeValue) error) error {
	return q.scanPrefix(actionOutcomePositionPrefix(orderID, position), func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTActionOutcome, raw)
		if err != nil {
			return err
		}
		return fn(v.(*model.ActionOutcomeValue))
	})
}
