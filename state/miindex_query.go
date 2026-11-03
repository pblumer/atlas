package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// MILoopCounterVariable is the variable a multi-instance iteration's 1-based
// counter is bound to (ADR-0077). It must equal engine.LoopCounterVariable; the
// state package cannot import the engine, so an engine test pins the two.
const MILoopCounterVariable = "loopCounter"

// MIHistoryState is a durable multi-instance lifecycle outcome (ADR-0517). The
// values are part of on-disk keys (cfMIRoundByState) and are append-only.
type MIHistoryState byte

const (
	MIHistoryActive     MIHistoryState = 1
	MIHistoryCompleted  MIHistoryState = 2
	MIHistoryTerminated MIHistoryState = 3
)

// String is the API spelling of a lifecycle state.
func (s MIHistoryState) String() string {
	switch s {
	case MIHistoryActive:
		return "active"
	case MIHistoryCompleted:
		return "completed"
	case MIHistoryTerminated:
		return "terminated"
	}
	return "unknown"
}

// ParseMIHistoryState is the inverse of [MIHistoryState.String].
func ParseMIHistoryState(s string) (MIHistoryState, bool) {
	for _, st := range [...]MIHistoryState{MIHistoryActive, MIHistoryCompleted, MIHistoryTerminated} {
		if st.String() == s {
			return st, true
		}
	}
	return 0, false
}

// MIHistoryEntry describes one historical MI body activation or iteration. A
// body's BodyElementKey equals ElementInstanceKey; an iteration references its
// own body's concrete activation, not merely a BPMN element ID.
//
// Every field is a fold of durable events (ADR-0517, invariants I4/I6). Fields
// that only apply to one role are zero on the other.
type MIHistoryEntry struct {
	ElementInstanceKey     uint64
	BodyElementKey         uint64
	BodyActivationPosition uint64
	ActivationPosition     uint64
	EndPosition            uint64
	ProcessDefKey          uint64
	ElementID              int32
	TokenID                uint64
	ParentTokenID          uint64
	State                  MIHistoryState

	// Iterations only.
	//
	// LoopCounter is the iteration's 1-based loopCounter as the engine bound it
	// when it seeded the iteration — the original iteration number, not the
	// order iterations happened to finish in. 0 means it was not recorded.
	LoopCounter uint64
	// IncidentsRaised counts incidents attributed to this entry: raised on its
	// element instance or on an element its scope encloses (innermost MI owner).
	// FirstIncidentPosition is the log position of the first of them.
	IncidentsRaised       uint64
	FirstIncidentPosition uint64

	// Bodies only.
	//
	// ParentIterationKey is the innermost MI iteration enclosing this body, 0 if
	// none does. Iterations, Completed and Terminated count iterations seeded and
	// ended; the active count is their difference. IterationsWithIncidents counts
	// iterations that had at least one incident; IncidentsRaised (above) is the
	// body's total, including incidents raised on the body element itself.
	// Changes counts the facts that changed any iteration's state or incident
	// attribution; a reader compares two values to learn whether the iteration
	// set moved in between.
	ParentIterationKey      uint64
	Iterations              uint64
	Completed               uint64
	Terminated              uint64
	IterationsWithIncidents uint64
	Changes                 uint64
}

// ActiveIterations is the number of seeded iterations that have not ended.
func (e MIHistoryEntry) ActiveIterations() uint64 {
	return e.Iterations - e.Completed - e.Terminated
}

// Value schema v2. The first byte is the version, the second the role, so a
// body value can never decode as an iteration or vice versa.
const (
	miHistoryValueVersion byte = 2
	miRoleBody            byte = 'b'
	miRoleRound           byte = 'r'

	miCommonValueSize = 47
	miRoundValueSize  = miCommonValueSize + 24
	miBodyValueSize   = miCommonValueSize + 8
	// miMaxValueSize is the largest MI value: an iteration row.
	miMaxValueSize = miRoundValueSize

	// miMaxKeySize is the longest index key: cfMIRoundByState.
	miMaxKeySize = 1 + 8 + 8 + 8 + 1 + 8 + 8
)

func appendMICommon(dst []byte, role byte, v *MIHistoryEntry) []byte {
	dst = append(dst, miHistoryValueVersion, role)
	dst = appendBE64(dst, v.ProcessDefKey)
	dst = appendBE32(dst, uint32(v.ElementID))
	dst = appendBE64(dst, v.TokenID)
	dst = appendBE64(dst, v.ParentTokenID)
	dst = appendBE64(dst, v.ActivationPosition)
	dst = appendBE64(dst, v.EndPosition)
	return append(dst, byte(v.State))
}

func appendMIRoundValue(dst []byte, v *MIHistoryEntry) []byte {
	dst = appendMICommon(dst, miRoleRound, v)
	dst = appendBE64(dst, v.LoopCounter)
	dst = appendBE64(dst, v.IncidentsRaised)
	return appendBE64(dst, v.FirstIncidentPosition)
}

// appendMIBodyValue encodes a body's row. Its counters are not in the row:
// they live under cfMIBodyCounter as write-only merges.
func appendMIBodyValue(dst []byte, v *MIHistoryEntry) []byte {
	dst = appendMICommon(dst, miRoleBody, v)
	return appendBE64(dst, v.ParentIterationKey)
}

func decodeMICommon(raw []byte, role byte, size int) (MIHistoryEntry, error) {
	if len(raw) != size || raw[0] != miHistoryValueVersion || raw[1] != role {
		return MIHistoryEntry{}, fmt.Errorf("state: invalid MI history value (version, role or size; %d bytes)", len(raw))
	}
	v := MIHistoryEntry{
		ProcessDefKey:      binary.BigEndian.Uint64(raw[2:10]),
		ElementID:          int32(binary.BigEndian.Uint32(raw[10:14])),
		TokenID:            binary.BigEndian.Uint64(raw[14:22]),
		ParentTokenID:      binary.BigEndian.Uint64(raw[22:30]),
		ActivationPosition: binary.BigEndian.Uint64(raw[30:38]),
		EndPosition:        binary.BigEndian.Uint64(raw[38:46]),
		State:              MIHistoryState(raw[46]),
	}
	if v.State < MIHistoryActive || v.State > MIHistoryTerminated {
		return MIHistoryEntry{}, fmt.Errorf("state: invalid MI lifecycle state %d", v.State)
	}
	return v, nil
}

func decodeMIRoundValue(raw []byte) (MIHistoryEntry, error) {
	v, err := decodeMICommon(raw, miRoleRound, miRoundValueSize)
	if err != nil {
		return v, err
	}
	v.LoopCounter = binary.BigEndian.Uint64(raw[47:55])
	v.IncidentsRaised = binary.BigEndian.Uint64(raw[55:63])
	v.FirstIncidentPosition = binary.BigEndian.Uint64(raw[63:71])
	if (v.IncidentsRaised == 0) != (v.FirstIncidentPosition == 0) {
		return MIHistoryEntry{}, errors.New("state: inconsistent MI iteration incident counters")
	}
	return v, nil
}

func decodeMIBodyValue(raw []byte) (MIHistoryEntry, error) {
	v, err := decodeMICommon(raw, miRoleBody, miBodyValueSize)
	if err != nil {
		return v, err
	}
	v.ParentIterationKey = binary.BigEndian.Uint64(raw[47:55])
	return v, nil
}

// miReadBodyCounters folds a body's merge counters into its entry and checks
// that they describe a possible body.
func (q queries) miReadBodyCounters(piKey, bodyKey uint64, v *MIHistoryEntry) error {
	var c [len(miBodyCounterKinds) + 1]int64
	for _, kind := range miBodyCounterKinds {
		raw, ok, err := getCopy(q.r, keyMIBodyCounter(piKey, bodyKey, kind))
		if err != nil {
			return err
		}
		if ok {
			if len(raw) != 8 {
				return fmt.Errorf("state: corrupt MI body counter (%d bytes)", len(raw))
			}
			c[kind] = decodeCounter(raw)
		}
		if c[kind] < 0 {
			return errors.New("state: negative MI body counter")
		}
	}
	v.Iterations, v.Completed, v.Terminated = uint64(c[miCountIterations]), uint64(c[miCountCompleted]), uint64(c[miCountTerminated])
	v.IncidentsRaised, v.IterationsWithIncidents = uint64(c[miCountIncidentsRaised]), uint64(c[miCountIterationsWithIncidents])
	v.Changes = uint64(c[miCountChanges])
	if v.Completed > v.Iterations || v.Terminated > v.Iterations-v.Completed ||
		v.IterationsWithIncidents > v.Iterations {
		return errors.New("state: inconsistent MI body counters")
	}
	return nil
}

// MIIncidentOutcome says how an attributed incident ended, as far as events
// record it. An incident whose element ended while the incident was open is
// cleared without an event (ADR-0061); it stays MIIncidentUnresolved here, and a
// reader tells it from an open one with [queries.IncidentOpen].
type MIIncidentOutcome byte

const (
	MIIncidentUnresolved MIIncidentOutcome = 0
	MIIncidentResolved   MIIncidentOutcome = 1
	// MIIncidentSuperseded: a later incident was raised on the same element
	// instance while this one was still recorded as open.
	MIIncidentSuperseded MIIncidentOutcome = 2
)

// MIIncidentRecord is one incident attributed to an MI body or iteration.
type MIIncidentRecord struct {
	OwnerKey           uint64
	ElementInstanceKey uint64
	RaisedPosition     uint64
	EndPosition        uint64
	Outcome            MIIncidentOutcome
}

const miIncidentRecordSize = 17

func decodeMIIncidentRecord(key, raw []byte) (MIIncidentRecord, error) {
	if len(key) != 25 || key[0] != byte(cfMIIncidentByOwner) || len(raw) != miIncidentRecordSize || raw[16] > byte(MIIncidentSuperseded) {
		return MIIncidentRecord{}, errors.New("state: corrupt MI incident record")
	}
	return MIIncidentRecord{
		OwnerKey:           binary.BigEndian.Uint64(key[9:17]),
		RaisedPosition:     binary.BigEndian.Uint64(key[17:25]),
		ElementInstanceKey: binary.BigEndian.Uint64(raw[0:8]),
		EndPosition:        binary.BigEndian.Uint64(raw[8:16]),
		Outcome:            MIIncidentOutcome(raw[16]),
	}, nil
}

// MIMaxPageLimit is the largest page any MI history query returns (ADR-0517).
const MIMaxPageLimit = 200

// miDefaultScanBudget bounds the index keys one iteration page may examine. A
// page that exhausts it ends early with a resume position, so no filter can turn
// a request into a walk over every iteration.
const miDefaultScanBudget = 10 * MIMaxPageLimit

func validateMILimit(limit int) error {
	if limit < 1 || limit > MIMaxPageLimit {
		return fmt.Errorf("state: MI history page limit %d outside [1, %d]", limit, MIMaxPageLimit)
	}
	return nil
}

func (q queries) miBodyRow(piKey, pos, bodyKey uint64) (MIHistoryEntry, error) {
	raw, ok, err := getCopy(q.r, keyMIBody(piKey, pos, bodyKey))
	if err != nil {
		return MIHistoryEntry{}, err
	}
	if !ok {
		return MIHistoryEntry{}, errors.New("state: MI body reference points to missing row")
	}
	entry, err := decodeMIBodyValue(raw)
	if err != nil {
		return entry, err
	}
	if entry.ActivationPosition != pos {
		return MIHistoryEntry{}, errors.New("state: mismatched MI body activation position")
	}
	if err := q.miReadBodyCounters(piKey, bodyKey, &entry); err != nil {
		return MIHistoryEntry{}, err
	}
	entry.ElementInstanceKey, entry.BodyElementKey = bodyKey, bodyKey
	entry.BodyActivationPosition = pos
	return entry, nil
}

func (q queries) miRoundRow(piKey, bodyKey, bodyPos, roundKey uint64) (MIHistoryEntry, error) {
	raw, ok, err := getCopy(q.r, keyMIIteration(piKey, bodyKey, bodyPos, roundKey))
	if err != nil {
		return MIHistoryEntry{}, err
	}
	if !ok {
		return MIHistoryEntry{}, errors.New("state: MI iteration reference points to missing row")
	}
	entry, err := decodeMIRoundValue(raw)
	if err != nil {
		return entry, err
	}
	entry.ElementInstanceKey, entry.BodyElementKey = roundKey, bodyKey
	entry.BodyActivationPosition = bodyPos
	return entry, nil
}

// MIBody gets one concrete body activation via the exact reverse index.
// Both *Store and *ReadView expose this query through their shared queries.
func (q queries) MIBody(piKey, bodyKey uint64) (*MIHistoryEntry, bool, error) {
	if err := q.requireMIHistoryIndex(); err != nil {
		return nil, false, err
	}
	ref, ok, err := getCopy(q.r, keyMIBodyByElement(piKey, bodyKey))
	if err != nil || !ok {
		return nil, false, err
	}
	if len(ref) != 8 {
		return nil, false, fmt.Errorf("state: corrupt MI body reference (%d bytes)", len(ref))
	}
	entry, err := q.miBodyRow(piKey, binary.BigEndian.Uint64(ref), bodyKey)
	if err != nil {
		return nil, false, err
	}
	return &entry, true, nil
}

// MIIteration finds one exact iteration without scanning any body history.
func (q queries) MIIteration(piKey, roundKey uint64) (*MIHistoryEntry, bool, error) {
	if err := q.requireMIHistoryIndex(); err != nil {
		return nil, false, err
	}
	ref, ok, err := getCopy(q.r, keyMIRoundByElement(piKey, roundKey))
	if err != nil || !ok {
		return nil, false, err
	}
	if len(ref) != 16 {
		return nil, false, fmt.Errorf("state: corrupt MI iteration reference (%d bytes)", len(ref))
	}
	entry, err := q.miRoundRow(piKey, binary.BigEndian.Uint64(ref[:8]), binary.BigEndian.Uint64(ref[8:]), roundKey)
	if err != nil {
		return nil, false, err
	}
	return &entry, true, nil
}

// MIBodyPosition is a body's place in its page order.
type MIBodyPosition struct {
	ActivationPosition uint64
	BodyKey            uint64
}

// MIBodyPageQuery selects one page of body activations.
type MIBodyPageQuery struct {
	// ParentIteration restricts the page to bodies the given MI iteration
	// encloses; 0 lists the bodies no MI iteration encloses.
	ParentIteration uint64
	// After resumes strictly after a position a previous page returned.
	After *MIBodyPosition
	// HighWater, when non-zero, hides bodies activated after that log position,
	// so a client paging a running instance never sees the set grow under it.
	HighWater uint64
	Limit     int
}

// MIBodyPage visits up to Limit body activations in activation order and
// returns the position to resume after when more follow (nil when none do).
// The walk is a bounded seek over cfMIBodyByParent plus one point read per row.
func (q queries) MIBodyPage(piKey uint64, query MIBodyPageQuery, fn func(MIHistoryEntry) error) (*MIBodyPosition, error) {
	if err := validateMILimit(query.Limit); err != nil {
		return nil, err
	}
	if err := q.requireMIHistoryIndex(); err != nil {
		return nil, err
	}
	prefix := miBodyByParentPrefix(piKey, query.ParentIteration)
	lower := prefix
	if query.After != nil {
		lower = append(appendBE64(appendBE64(append([]byte(nil), prefix...), query.After.ActivationPosition), query.After.BodyKey), 0)
	}
	iter, err := q.r.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	var last MIBodyPosition
	for iter.First(); iter.Valid(); iter.Next() {
		k := iter.Key()
		if len(k) != len(prefix)+16 {
			return nil, fmt.Errorf("state: corrupt MI body parent key (%d bytes)", len(k))
		}
		pos, bodyKey := binary.BigEndian.Uint64(k[len(prefix):]), binary.BigEndian.Uint64(k[len(prefix)+8:])
		if query.HighWater != 0 && pos > query.HighWater {
			return nil, iter.Error() // ordered by position: nothing later qualifies
		}
		if count == query.Limit {
			return &last, nil
		}
		entry, err := q.miBodyRow(piKey, pos, bodyKey)
		if err != nil {
			return nil, err
		}
		if entry.ParentIterationKey != query.ParentIteration {
			return nil, errors.New("state: MI body parent index disagrees with body row")
		}
		if err := fn(entry); err != nil {
			return nil, err
		}
		count++
		last = MIBodyPosition{ActivationPosition: pos, BodyKey: bodyKey}
	}
	return nil, iter.Error()
}

// MIIterationPosition is an iteration's place in its page order.
type MIIterationPosition struct {
	LoopCounter uint64
	RoundKey    uint64
}

// MIIterationQuery selects one page of one body activation's iterations. The
// order is always (loopCounter, element-instance key).
type MIIterationQuery struct {
	// State, when non-zero, restricts the page to iterations currently in that
	// lifecycle state (served by cfMIRoundByState).
	State MIHistoryState
	// IncidentsOnly restricts the page to iterations that have had at least one
	// incident (served by cfMIRoundIncident). Combined with State, the state is
	// checked per row, within the scan budget.
	IncidentsOnly bool
	// LoopCounter, when non-zero, restricts the page to that iteration number.
	LoopCounter uint64
	// From, when non-zero and After is nil, starts the page at that iteration
	// number: a seek, not a skip.
	From  uint64
	After *MIIterationPosition
	// HighWater, when non-zero, hides iterations activated (or, with
	// IncidentsOnly, first attributed an incident) after that log position.
	HighWater uint64
	Limit     int
	// ScanBudget bounds the index keys examined; 0 means the default.
	ScanBudget int
}

// MIIterationPage reports where a page ended.
type MIIterationPage struct {
	// Next is the position to resume after, nil when the body has no more
	// matching iterations. A page cut short by its scan budget returns fewer
	// rows than its limit — possibly none — and a Next.
	Next *MIIterationPosition
	// Scanned is how many index keys the page examined.
	Scanned int
}

// MIIterationPage visits up to Limit iterations of one body activation. It
// seeks to the requested position in the index serving the filter and examines
// at most ScanBudget keys; its cost is independent of how many iterations
// precede the requested one or how many the body has in total.
func (q queries) MIIterationPage(piKey, bodyKey, bodyPos uint64, query MIIterationQuery, fn func(MIHistoryEntry) error) (MIIterationPage, error) {
	if err := validateMILimit(query.Limit); err != nil {
		return MIIterationPage{}, err
	}
	if query.State != 0 && (query.State < MIHistoryActive || query.State > MIHistoryTerminated) {
		return MIIterationPage{}, fmt.Errorf("state: invalid MI state filter %d", query.State)
	}
	if err := q.requireMIHistoryIndex(); err != nil {
		return MIIterationPage{}, err
	}
	budget := query.ScanBudget
	if budget <= 0 {
		budget = miDefaultScanBudget
	}
	var prefix []byte
	switch {
	case query.IncidentsOnly:
		prefix = miRoundIncidentPrefix(piKey, bodyKey, bodyPos)
	case query.State != 0:
		prefix = miRoundStatePrefix(piKey, bodyKey, bodyPos, query.State)
	default:
		prefix = miRoundOrderPrefix(piKey, bodyKey, bodyPos)
	}
	tailAt := len(prefix)
	scope := prefix
	if query.LoopCounter != 0 {
		scope = appendBE64(append([]byte(nil), prefix...), query.LoopCounter)
	}
	lower := scope
	switch {
	case query.After != nil:
		lower = append(appendBE64(appendBE64(append([]byte(nil), prefix...), query.After.LoopCounter), query.After.RoundKey), 0)
	case query.From != 0 && query.LoopCounter == 0:
		lower = appendBE64(append([]byte(nil), prefix...), query.From)
	}
	iter, err := q.r.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: prefixEnd(scope)})
	if err != nil {
		return MIIterationPage{}, err
	}
	defer iter.Close()
	var page MIIterationPage
	count := 0
	var last MIIterationPosition
	for iter.First(); iter.Valid(); iter.Next() {
		k := iter.Key()
		loop, roundKey, ok := miOrderedTail(k, tailAt)
		if !ok {
			return MIIterationPage{}, fmt.Errorf("state: corrupt MI iteration order key (%d bytes)", len(k))
		}
		if count == query.Limit {
			page.Next = &last
			return page, nil
		}
		if page.Scanned == budget {
			page.Next = &last
			return page, nil
		}
		page.Scanned++
		last = MIIterationPosition{LoopCounter: loop, RoundKey: roundKey}
		if query.IncidentsOnly {
			v := iter.Value()
			if len(v) != 8 {
				return MIIterationPage{}, errors.New("state: corrupt MI incident index value")
			}
			if query.HighWater != 0 && binary.BigEndian.Uint64(v) > query.HighWater {
				continue
			}
		}
		entry, err := q.miRoundRow(piKey, bodyKey, bodyPos, roundKey)
		if err != nil {
			return MIIterationPage{}, err
		}
		if entry.LoopCounter != loop {
			return MIIterationPage{}, errors.New("state: MI iteration order index disagrees with iteration row")
		}
		if query.HighWater != 0 && entry.ActivationPosition > query.HighWater {
			continue
		}
		if query.State != 0 && entry.State != query.State {
			if !query.IncidentsOnly {
				return MIIterationPage{}, errors.New("state: MI state index disagrees with iteration row")
			}
			continue
		}
		if err := fn(entry); err != nil {
			return MIIterationPage{}, err
		}
		count++
	}
	return page, iter.Error()
}

// MIIncidents visits up to limit incident records attributed to one body or
// iteration, oldest first, and reports whether more exist.
func (q queries) MIIncidents(piKey, ownerKey uint64, limit int, fn func(MIIncidentRecord) error) (bool, error) {
	if err := validateMILimit(limit); err != nil {
		return false, err
	}
	if err := q.requireMIHistoryIndex(); err != nil {
		return false, err
	}
	count := 0
	err := q.scanPrefix(miIncidentOwnerPrefix(piKey, ownerKey), func(k, raw []byte) error {
		if count == limit {
			return errMIPageComplete
		}
		rec, err := decodeMIIncidentRecord(k, raw)
		if err != nil {
			return err
		}
		count++
		return fn(rec)
	})
	if errors.Is(err, errMIPageComplete) {
		return true, nil
	}
	return false, err
}

// IncidentOpen reports whether an incident is currently recorded on the given
// element instance: the authoritative current-incident table, one point read.
func (q queries) IncidentOpen(elKey uint64) (bool, error) {
	_, ok, err := getCopy(q.r, keyIncident(elKey))
	return ok, err
}

var errMIPageComplete = errors.New("mi page complete")

// MIDecisions visits up to limit decision evaluations attributed to one MI
// iteration (or body), in evaluation order, and reports whether more exist.
// Each is a point read of the instance's decision history row the attribution
// names; nothing else of the instance's decisions is read.
func (q queries) MIDecisions(piKey, ownerKey uint64, limit int, fn func(ts int64, pos uint64, v *model.DecisionEvaluationValue) error) (bool, error) {
	if err := validateMILimit(limit); err != nil {
		return false, err
	}
	if err := q.requireMIHistoryIndex(); err != nil {
		return false, err
	}
	ready, err := q.MIDecisionIndexReady()
	if err != nil {
		return false, err
	}
	if !ready {
		return false, ErrMIDecisionIndexUnavailable
	}
	prefix := appendBE64(appendBE64([]byte{byte(cfMIDecision)}, piKey), ownerKey)
	count := 0
	err = q.scanPrefix(prefix, func(k, _ []byte) error {
		if count == limit {
			return errMIPageComplete
		}
		if len(k) != len(prefix)+16 {
			return fmt.Errorf("state: corrupt MI decision key (%d bytes)", len(k))
		}
		ts := int64(binary.BigEndian.Uint64(k[len(prefix):]) ^ (1 << 63))
		pos := binary.BigEndian.Uint64(k[len(prefix)+8:])
		raw, ok, err := getCopy(q.r, keyDecisionEvaluation(piKey, ts, pos))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("state: MI decision reference points to a missing evaluation")
		}
		v, err := model.DecodeValue(model.VTDecisionEvaluation, raw)
		if err != nil {
			return err
		}
		count++
		return fn(ts, pos, v.(*model.DecisionEvaluationValue))
	})
	if errors.Is(err, errMIPageComplete) {
		return true, nil
	}
	return false, err
}

// VariableSnapshotsUpTo visits, in change order, the retained variable changes of
// one scope recorded at or before log position at, up to limit of them, and
// reports whether more such changes exist beyond the limit. Snapshot keys are
// ordered by event time, and event times do not decrease in log order, so the
// first change past at ends the read: nothing after it is at or before at.
// Folding the visited changes by name gives the scope's values as they stood at
// that position (ADR-0048).
func (q queries) VariableSnapshotsUpTo(scopeKey, at uint64, limit int, fn func(ts int64, pos uint64, v *model.VariableValue) error) (bool, error) {
	if err := validateMILimit(limit); err != nil {
		return false, err
	}
	count, more := 0, false
	err := q.scanPrefix(variableSnapshotScopePrefix(scopeKey), func(k, raw []byte) error {
		if positionFromVarSnapKey(k) > at {
			return errMIPageComplete
		}
		if count == limit {
			more = true
			return errMIPageComplete
		}
		v, err := model.DecodeValue(model.VTVariable, raw)
		if err != nil {
			return err
		}
		count++
		return fn(timestampFromVarSnapKey(k), positionFromVarSnapKey(k), v.(*model.VariableValue))
	})
	if errors.Is(err, errMIPageComplete) {
		err = nil
	}
	return more, err
}

// VariableSnapshotPage visits up to limit retained variable changes of one scope
// in change order (ADR-0048) and reports whether more exist. A multi-instance
// iteration's own scope is a scope, so this is how one iteration's inputs and
// local outputs are read without the instance's other variables.
func (q queries) VariableSnapshotPage(scopeKey uint64, limit int, fn func(ts int64, pos uint64, v *model.VariableValue) error) (bool, error) {
	if err := validateMILimit(limit); err != nil {
		return false, err
	}
	count := 0
	err := q.scanPrefix(variableSnapshotScopePrefix(scopeKey), func(k, raw []byte) error {
		if count == limit {
			return errMIPageComplete
		}
		v, err := model.DecodeValue(model.VTVariable, raw)
		if err != nil {
			return err
		}
		count++
		return fn(timestampFromVarSnapKey(k), positionFromVarSnapKey(k), v.(*model.VariableValue))
	})
	if errors.Is(err, errMIPageComplete) {
		return true, nil
	}
	return false, err
}
