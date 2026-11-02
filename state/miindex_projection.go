package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// This file is the apply-side half of the MI history index (ADR-0517). Every
// function here is called from applyToState only, live and on recovery, and is
// a pure function of the event and of state earlier events wrote (I4/I6): no
// clock, no fresh key, no behaviour. Keys and values are built into the
// transaction's fixed buffers; Pebble copies them on Set, so they never outlive
// the call.

// miMaxScopeDepth bounds the walk from an element up its scope chain to the
// innermost enclosing MI iteration. BPMN nesting is never this deep in practice;
// the bound only guarantees the fold terminates on any state.
const miMaxScopeDepth = 256

func (t *Tx) miRefKey(family columnFamily, piKey, elementKey uint64) []byte {
	k := t.miKey[:17]
	k[0] = byte(family)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], elementKey)
	return k
}

func (t *Tx) miBodyRowKey(piKey, pos, bodyKey uint64) []byte {
	k := t.miKey[:25]
	k[0] = byte(cfMIBody)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], pos)
	binary.BigEndian.PutUint64(k[17:25], bodyKey)
	return k
}

// miQuadKey is the shape (family, a, b, c, d) of the iteration row, the
// body-by-parent and the incident-owner keys.
func (t *Tx) miQuadKey(family columnFamily, piKey, a, b, c uint64) []byte {
	k := t.miKey[:33]
	k[0] = byte(family)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], a)
	binary.BigEndian.PutUint64(k[17:25], b)
	binary.BigEndian.PutUint64(k[25:33], c)
	return k
}

func (t *Tx) miIterRowKey(piKey, bodyKey, bodyPos, roundKey uint64) []byte {
	return t.miQuadKey(cfMIIteration, piKey, bodyKey, bodyPos, roundKey)
}

func (t *Tx) miIncidentOwnerKey(piKey, ownerKey, raisedPos uint64) []byte {
	k := t.miKey[:25]
	k[0] = byte(cfMIIncidentByOwner)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], ownerKey)
	binary.BigEndian.PutUint64(k[17:25], raisedPos)
	return k
}

// miOrderedKey builds the (instance, body, activation, loopCounter, round) keys
// of cfMIRoundOrder and cfMIRoundIncident, and — with a state byte after the
// activation — of cfMIRoundByState.
func (t *Tx) miOrderedKey(family columnFamily, piKey, bodyKey, bodyPos uint64, st MIHistoryState, loop, roundKey uint64) []byte {
	n := 25
	k := t.miKey[:]
	k[0] = byte(family)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], bodyKey)
	binary.BigEndian.PutUint64(k[17:25], bodyPos)
	if family == cfMIRoundByState {
		k[25] = byte(st)
		n = 26
	}
	binary.BigEndian.PutUint64(k[n:n+8], loop)
	binary.BigEndian.PutUint64(k[n+8:n+16], roundKey)
	return k[:n+16]
}

// miGet copies a fixed-size MI value into the reusable read buffer. A value
// larger than any MI value is corruption, not something to allocate for.
func (t *Tx) miGet(key []byte) ([]byte, bool, error) {
	raw, closer, err := t.b.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(raw) > len(t.miRead) {
		_ = closer.Close()
		return nil, false, fmt.Errorf("state: oversized MI index value (%d bytes)", len(raw))
	}
	n := copy(t.miRead[:], raw)
	return t.miRead[:n], true, closer.Close()
}

func (t *Tx) miBodyActivationPosition(piKey, bodyKey uint64) (uint64, bool, error) {
	raw, ok, err := t.miGet(t.miRefKey(cfMIBodyByElement, piKey, bodyKey))
	if err != nil || !ok {
		return 0, false, err
	}
	if len(raw) != 8 {
		return 0, false, fmt.Errorf("state: corrupt MI body reference (%d bytes)", len(raw))
	}
	return binary.BigEndian.Uint64(raw), true, nil
}

func (t *Tx) miRoundReference(piKey, roundKey uint64) (uint64, uint64, bool, error) {
	raw, ok, err := t.miGet(t.miRefKey(cfMIRoundByElement, piKey, roundKey))
	if err != nil || !ok {
		return 0, 0, false, err
	}
	if len(raw) != 16 {
		return 0, 0, false, fmt.Errorf("state: corrupt MI round reference (%d bytes)", len(raw))
	}
	return binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:]), true, nil
}

func (t *Tx) miLoadBody(piKey, pos, bodyKey uint64) (MIHistoryEntry, error) {
	raw, ok, err := t.miGet(t.miBodyRowKey(piKey, pos, bodyKey))
	if err != nil {
		return MIHistoryEntry{}, err
	}
	if !ok {
		return MIHistoryEntry{}, errors.New("state: MI body reference points to missing row")
	}
	return decodeMIBodyValue(raw)
}

// miCount adds delta to one of a body's counters: a write-only merge.
func (t *Tx) miCount(piKey, bodyKey uint64, kind miBodyCounterKind, delta int64) error {
	k := t.miKey[:18]
	k[0] = byte(cfMIBodyCounter)
	binary.BigEndian.PutUint64(k[1:9], piKey)
	binary.BigEndian.PutUint64(k[9:17], bodyKey)
	k[17] = byte(kind)
	return t.b.Merge(k, appendCounter(t.miRef[:0], delta), nil)
}

func (t *Tx) miStoreBody(piKey, bodyKey uint64, v *MIHistoryEntry) error {
	value := appendMIBodyValue(t.miValue[:0], v)
	return t.b.Set(t.miBodyRowKey(piKey, v.ActivationPosition, bodyKey), value, nil)
}

func (t *Tx) miLoadRound(piKey, bodyKey, bodyPos, roundKey uint64) (MIHistoryEntry, error) {
	raw, ok, err := t.miGet(t.miIterRowKey(piKey, bodyKey, bodyPos, roundKey))
	if err != nil {
		return MIHistoryEntry{}, err
	}
	if !ok {
		return MIHistoryEntry{}, errors.New("state: MI iteration reference points to missing row")
	}
	return decodeMIRoundValue(raw)
}

func (t *Tx) miStoreRound(piKey, bodyKey, bodyPos, roundKey uint64, v *MIHistoryEntry) error {
	value := appendMIRoundValue(t.miValue[:0], v)
	return t.b.Set(t.miIterRowKey(piKey, bodyKey, bodyPos, roundKey), value, nil)
}

// miLoopCounter reads the loopCounter the engine bound into an iteration's own
// scope. The engine writes that variable event in the same batch that schedules
// the iteration's activation, so it is in state before the Activated event is
// applied; reading it is a read of an earlier durable fact (I6). 0 = absent.
func (t *Tx) miLoopCounter(roundKey uint64) (uint64, error) {
	k := t.miVar[:]
	k[0] = byte(cfVariable)
	binary.BigEndian.PutUint64(k[1:9], roundKey)
	copy(k[9:], MILoopCounterVariable)
	raw, closer, err := t.b.Get(k)
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, ok := model.VariableNumberUint(raw)
	if !ok {
		n = 0 // not a positive integer: not recorded, never guessed
	}
	return n, closer.Close()
}

// miEnclosingRound walks from a scope up the live scope chain to the innermost
// MI iteration, returning 0 when none encloses it. The scopes are live while an
// element inside them is activated, so the walk reads state, not history.
func (t *Tx) miEnclosingRound(piKey, scope uint64) (uint64, error) {
	var el model.ElementInstanceValue
	for depth := 0; scope != 0 && depth < miMaxScopeDepth; depth++ {
		_, _, ok, err := t.miRoundReference(piKey, scope)
		if err != nil || ok {
			return scope, err
		}
		found, err := t.GetElementInstanceInto(scope, &el)
		if err != nil || !found || el.ProcessInstanceKey != piKey {
			return 0, err
		}
		scope = el.FlowScopeKey
	}
	return 0, nil
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
	pi := v.ProcessInstanceKey
	entry := MIHistoryEntry{
		ActivationPosition: pos, ProcessDefKey: v.ProcessDefKey,
		ElementID: v.ElementId, TokenID: v.TokenID,
		ParentTokenID: v.ParentTokenID, State: MIHistoryActive,
	}
	if v.MultiInstance == 1 {
		parent, err := t.miEnclosingRound(pi, v.FlowScopeKey)
		if err != nil {
			return err
		}
		entry.ParentIterationKey = parent
		// Batch writes only fail on a closed batch; the chain stops at the first.
		err = t.miStoreBody(pi, elementKey, &entry)
		binary.BigEndian.PutUint64(t.miRef[:8], pos)
		if err == nil {
			err = t.b.Set(t.miRefKey(cfMIBodyByElement, pi, elementKey), t.miRef[:8], nil)
		}
		if err == nil {
			err = t.b.Set(t.miQuadKey(cfMIBodyByParent, pi, parent, pos, elementKey), nil, nil)
		}
		return err
	}
	bodyKey := v.FlowScopeKey
	bodyPos, ok, err := t.miBodyActivationPosition(pi, bodyKey)
	if err != nil || !ok {
		// Pre-index histories can still be live after a rolling upgrade. Their
		// body activation event is absent from this projection; never attach
		// the round to an invented activation.
		return err
	}
	loop, err := t.miLoopCounter(elementKey)
	if err != nil {
		return err
	}
	entry.LoopCounter = loop
	err = t.miStoreRound(pi, bodyKey, bodyPos, elementKey, &entry)
	binary.BigEndian.PutUint64(t.miRef[:8], bodyKey)
	binary.BigEndian.PutUint64(t.miRef[8:16], bodyPos)
	if err == nil {
		err = t.b.Set(t.miRefKey(cfMIRoundByElement, pi, elementKey), t.miRef[:16], nil)
	}
	if err == nil {
		err = t.b.Set(t.miOrderedKey(cfMIRoundOrder, pi, bodyKey, bodyPos, 0, loop, elementKey), nil, nil)
	}
	if err == nil {
		err = t.b.Set(t.miOrderedKey(cfMIRoundByState, pi, bodyKey, bodyPos, MIHistoryActive, loop, elementKey), nil, nil)
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, miCountIterations, 1)
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, miCountChanges, 1)
	}
	return err
}

// RecordMIEnd applies the final lifecycle fact to a historical projection.
// Terminal records stay indexed after the live element is removed; they are
// retained until the process-instance history is explicitly purged.
func (t *Tx) RecordMIEnd(elementKey uint64, v *model.ElementInstanceValue, pos uint64, terminated bool) error {
	if v.MultiInstance != 1 && v.MultiInstance != 2 {
		return nil
	}
	pi := v.ProcessInstanceKey
	st := MIHistoryCompleted
	if terminated {
		st = MIHistoryTerminated
	}
	if v.MultiInstance == 1 {
		bodyPos, ok, err := t.miBodyActivationPosition(pi, elementKey)
		if err != nil || !ok {
			return err // a legacy activation with no projection
		}
		body, err := t.miLoadBody(pi, bodyPos, elementKey)
		if err != nil {
			return err
		}
		body.EndPosition, body.State = pos, st
		return t.miStoreBody(pi, elementKey, &body)
	}
	bodyKey, bodyPos, ok, err := t.miRoundReference(pi, elementKey)
	if err != nil || !ok {
		return err
	}
	round, err := t.miLoadRound(pi, bodyKey, bodyPos, elementKey)
	if err != nil {
		return err
	}
	if round.State != MIHistoryActive {
		// An element instance ends once. Should a second end fact ever be folded,
		// the first one stands: failing here would stop the partition over a
		// read projection, and overwriting would double-count the body.
		return nil
	}
	kind := miCountCompleted
	if terminated {
		kind = miCountTerminated
	}
	err = t.b.Delete(t.miOrderedKey(cfMIRoundByState, pi, bodyKey, bodyPos, round.State, round.LoopCounter, elementKey), nil)
	if err == nil {
		err = t.b.Set(t.miOrderedKey(cfMIRoundByState, pi, bodyKey, bodyPos, st, round.LoopCounter, elementKey), nil, nil)
	}
	round.EndPosition, round.State = pos, st
	if err == nil {
		err = t.miStoreRound(pi, bodyKey, bodyPos, elementKey, &round)
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, kind, 1)
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, miCountChanges, 1)
	}
	return err
}

// miIncidentOwner finds the innermost MI iteration or body an incident's
// element belongs to: the element itself, or a scope enclosing it. The element
// is live when its incident is raised or resolved, and so is its scope chain.
func (t *Tx) miIncidentOwner(piKey, elKey uint64) (owner, bodyKey, bodyPos uint64, isRound, ok bool, err error) {
	var el model.ElementInstanceValue
	scope := elKey
	for depth := 0; scope != 0 && depth < miMaxScopeDepth; depth++ {
		b, p, found, err := t.miRoundReference(piKey, scope)
		if err != nil || found {
			return scope, b, p, true, found, err
		}
		p, found, err = t.miBodyActivationPosition(piKey, scope)
		if err != nil || found {
			return scope, scope, p, false, found, err
		}
		found, err = t.GetElementInstanceInto(scope, &el)
		if err != nil || !found || el.ProcessInstanceKey != piKey {
			return 0, 0, 0, false, false, err
		}
		scope = el.FlowScopeKey
	}
	return 0, 0, 0, false, false, nil
}

func (t *Tx) miSetIncidentRecord(piKey, ownerKey, raisedPos, elKey, endPos uint64, outcome MIIncidentOutcome) error {
	rec := t.miRef[:miIncidentRecordSize]
	binary.BigEndian.PutUint64(rec[0:8], elKey)
	binary.BigEndian.PutUint64(rec[8:16], endPos)
	rec[16] = byte(outcome)
	return t.b.Set(t.miIncidentOwnerKey(piKey, ownerKey, raisedPos), rec, nil)
}

// miOpenIncident reads the (owner, raised position) of the incident on an
// element that is recorded as open in the MI index.
func (t *Tx) miOpenIncident(piKey, elKey uint64) (uint64, uint64, bool, error) {
	raw, ok, err := t.miGet(t.miRefKey(cfMIIncidentOpen, piKey, elKey))
	if err != nil || !ok {
		return 0, 0, false, err
	}
	if len(raw) != 16 {
		return 0, 0, false, fmt.Errorf("state: corrupt MI open-incident reference (%d bytes)", len(raw))
	}
	return binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:]), true, nil
}

// RecordMIIncidentRaised attributes an IncidentCreated event to the innermost
// MI iteration or body whose scope holds the incident's element, and counts it
// there and on the body. Incidents outside any MI execution are not indexed.
func (t *Tx) RecordMIIncidentRaised(inc *model.IncidentValue, pos uint64) error {
	pi, elKey := inc.ProcessInstanceKey, inc.ElementInstanceKey
	owner, bodyKey, bodyPos, isRound, ok, err := t.miIncidentOwner(pi, elKey)
	if err != nil || !ok {
		return err
	}
	prevOwner, prevRaised, open, err := t.miOpenIncident(pi, elKey)
	if err != nil {
		return err
	}
	var round MIHistoryEntry
	if isRound {
		if round, err = t.miLoadRound(pi, bodyKey, bodyPos, owner); err != nil {
			return err
		}
	}
	// From here on only batch writes follow; the chain stops at the first that
	// fails, which only a closed batch does.
	if open {
		err = t.miSetIncidentRecord(pi, prevOwner, prevRaised, elKey, pos, MIIncidentSuperseded)
	}
	if err == nil {
		err = t.miSetIncidentRecord(pi, owner, pos, elKey, 0, MIIncidentUnresolved)
	}
	binary.BigEndian.PutUint64(t.miRef[:8], owner)
	binary.BigEndian.PutUint64(t.miRef[8:16], pos)
	if err == nil {
		err = t.b.Set(t.miRefKey(cfMIIncidentOpen, pi, elKey), t.miRef[:16], nil)
	}
	firstForRound := isRound && round.FirstIncidentPosition == 0
	if isRound {
		round.IncidentsRaised++
		if firstForRound {
			round.FirstIncidentPosition = pos
			binary.BigEndian.PutUint64(t.miRef[:8], pos)
			if err == nil {
				err = t.b.Set(t.miOrderedKey(cfMIRoundIncident, pi, bodyKey, bodyPos, 0, round.LoopCounter, owner), t.miRef[:8], nil)
			}
		}
		if err == nil {
			err = t.miStoreRound(pi, bodyKey, bodyPos, owner, &round)
		}
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, miCountIncidentsRaised, 1)
	}
	if err == nil && firstForRound {
		err = t.miCount(pi, bodyKey, miCountIterationsWithIncidents, 1)
	}
	if err == nil {
		err = t.miCount(pi, bodyKey, miCountChanges, 1)
	}
	return err
}

// RecordMIIncidentResolved closes the attribution an IncidentResolved event
// names. An incident raised before the index existed has no attribution and
// is left alone.
func (t *Tx) RecordMIIncidentResolved(inc *model.IncidentValue, pos uint64) error {
	pi, elKey := inc.ProcessInstanceKey, inc.ElementInstanceKey
	owner, raised, open, err := t.miOpenIncident(pi, elKey)
	if err != nil || !open {
		return err
	}
	err = t.miSetIncidentRecord(pi, owner, raised, elKey, pos, MIIncidentResolved)
	if err == nil {
		err = t.b.Delete(t.miRefKey(cfMIIncidentOpen, pi, elKey), nil)
	}
	return err
}

// RecordMIDecision attributes a DecisionEvaluated event to the innermost MI
// iteration (or body) whose live scope chain holds the evaluating element, by
// recording the evaluation's own key — the decision history row it names is
// written beside it by RecordDecisionEvaluation. One iteration's decisions are
// then a prefix read instead of a walk over every evaluation of the instance.
func (t *Tx) RecordMIDecision(v *model.DecisionEvaluationValue, ts int64, pos uint64) error {
	owner, _, _, _, ok, err := t.miIncidentOwner(v.ProcessInstanceKey, v.ElementInstanceKey)
	if err != nil || !ok {
		return err
	}
	return t.b.Set(t.miQuadKey(cfMIDecision, v.ProcessInstanceKey, owner, uint64(ts)^(1<<63), pos), nil, nil)
}

// miIndexFamilies is every per-instance family of the MI history index.
var miIndexFamilies = [...]columnFamily{
	cfMIBody, cfMIIteration, cfMIRoundByElement, cfMIBodyByElement,
	cfMIRoundOrder, cfMIRoundByState, cfMIRoundIncident,
	cfMIIncidentByOwner, cfMIIncidentOpen, cfMIBodyByParent, cfMIBodyCounter,
	cfMIDecision,
}

// purgeMIIndexHistory drops complete per-instance MI index ranges without a
// scan or individual deletes over potentially 50,000 rounds. Other instances'
// indexes and the immutable WAL are not affected.
func (t *Tx) purgeMIIndexHistory(piKey uint64) error {
	var err error
	for _, family := range miIndexFamilies {
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
		if err == nil {
			err = t.b.DeleteRange(lo[:], end, nil)
		}
	}
	return err
}
