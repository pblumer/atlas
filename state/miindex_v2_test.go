package state

import (
	"bytes"
	"errors"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/model"
)

// miFixture writes MI facts the way applyToState does: a loopCounter variable
// in the iteration's scope, then the iteration's activation.
type miFixture struct {
	t  *testing.T
	s  *Store
	tx *Tx
	pi uint64
}

func newMIFixture(t *testing.T, pi uint64) *miFixture {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &miFixture{t: t, s: s, tx: s.NewTransaction(), pi: pi}
}

func (f *miFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *miFixture) commit() {
	f.t.Helper()
	f.must(f.tx.Commit())
	f.must(f.tx.Close())
	f.tx = f.s.NewTransaction()
}

func (f *miFixture) element(key, scope uint64, mi uint8) model.ElementInstanceValue {
	v := model.ElementInstanceValue{ProcessInstanceKey: f.pi, ProcessDefKey: 9, ElementId: 4, FlowScopeKey: scope, TokenID: key, MultiInstance: mi}
	f.must(f.tx.PutElementInstance(key, &v))
	return v
}

func (f *miFixture) body(key, scope, pos uint64) {
	f.t.Helper()
	v := f.element(key, scope, 1)
	f.must(f.tx.RecordMIActivation(key, &v, pos))
}

func (f *miFixture) round(key, body, pos uint64, loop int) {
	f.t.Helper()
	if loop > 0 {
		f.must(f.tx.PutVariable(&model.VariableValue{ScopeKey: key, Name: MILoopCounterVariable, Kind: model.VarNumber, Text: strconv.Itoa(loop), Index: -1}))
	}
	v := f.element(key, body, 2)
	f.must(f.tx.RecordMIActivation(key, &v, pos))
}

func (f *miFixture) end(key, scope uint64, mi uint8, pos uint64, terminated bool) {
	f.t.Helper()
	v := model.ElementInstanceValue{ProcessInstanceKey: f.pi, FlowScopeKey: scope, MultiInstance: mi}
	f.must(f.tx.RecordMIEnd(key, &v, pos, terminated))
}

func (f *miFixture) iterations(body, bodyPos uint64, q MIIterationQuery) ([]MIHistoryEntry, MIIterationPage) {
	f.t.Helper()
	var out []MIHistoryEntry
	page, err := f.s.MIIterationPage(f.pi, body, bodyPos, q, func(e MIHistoryEntry) error {
		out = append(out, e)
		return nil
	})
	f.must(err)
	return out, page
}

func loops(rows []MIHistoryEntry) []uint64 {
	out := make([]uint64, len(rows))
	for i, r := range rows {
		out[i] = r.LoopCounter
	}
	return out
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMIIndexV2RecordsLoopCounterAndBodyCounters(t *testing.T) {
	f := newMIFixture(t, 7)
	f.body(100, 7, 10)
	f.round(101, 100, 11, 1)
	f.round(102, 100, 12, 2)
	f.round(103, 100, 13, 3)
	f.round(104, 100, 14, 0) // no loopCounter recorded: unknown, never guessed
	f.commit()
	f.end(101, 100, 2, 20, false)
	f.end(102, 100, 2, 21, true)
	f.end(102, 100, 2, 22, false) // a second end fact must not double count
	f.commit()

	b, ok, err := f.s.MIBody(7, 100)
	if err != nil || !ok {
		t.Fatalf("body: %v %v", ok, err)
	}
	if b.Iterations != 4 || b.Completed != 1 || b.Terminated != 1 || b.ActiveIterations() != 2 || b.Changes != 6 {
		t.Fatalf("body counters: %+v", b)
	}
	r, ok, err := f.s.MIIteration(7, 103)
	if err != nil || !ok || r.LoopCounter != 3 || r.State != MIHistoryActive {
		t.Fatalf("iteration 3: %+v %v %v", r, ok, err)
	}
	r, _, _ = f.s.MIIteration(7, 102)
	if r.State != MIHistoryTerminated || r.EndPosition != 21 {
		t.Fatalf("first end fact did not stand: %+v", r)
	}
	r, _, _ = f.s.MIIteration(7, 104)
	if r.LoopCounter != 0 {
		t.Fatalf("absent loopCounter was guessed: %+v", r)
	}
	rows, page := f.iterations(100, 10, MIIterationQuery{Limit: 10})
	if !equalU64(loops(rows), []uint64{0, 1, 2, 3}) || page.Next != nil {
		t.Fatalf("ordered page: %v next=%+v", loops(rows), page.Next)
	}
	for st, want := range map[MIHistoryState][]uint64{
		MIHistoryActive: {0, 3}, MIHistoryCompleted: {1}, MIHistoryTerminated: {2},
	} {
		rows, _ := f.iterations(100, 10, MIIterationQuery{Limit: 10, State: st})
		if !equalU64(loops(rows), want) {
			t.Fatalf("state %s: %v, want %v", st, loops(rows), want)
		}
	}
}

func TestMIIndexV2PagesSeekAndResumeWithoutDuplicates(t *testing.T) {
	f := newMIFixture(t, 8)
	f.body(1000, 8, 10)
	const n = 1000
	for i := 1; i <= n; i++ {
		f.round(1000+uint64(i), 1000, 10+uint64(i), i)
	}
	f.commit()

	seen := map[uint64]bool{}
	var after *MIIterationPosition
	pages := 0
	for {
		rows, page := f.iterations(1000, 10, MIIterationQuery{Limit: 100, After: after})
		pages++
		for _, r := range rows {
			if seen[r.LoopCounter] {
				t.Fatalf("iteration %d returned twice", r.LoopCounter)
			}
			seen[r.LoopCounter] = true
		}
		if page.Scanned > 100 {
			t.Fatalf("page examined %d keys for 100 rows", page.Scanned)
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	if len(seen) != n || pages != 10 {
		t.Fatalf("paged %d iterations in %d pages", len(seen), pages)
	}

	// A jump is a seek: the cost does not depend on the iterations before it.
	rows, page := f.iterations(1000, 10, MIIterationQuery{Limit: 3, From: 742})
	if !equalU64(loops(rows), []uint64{742, 743, 744}) || page.Scanned != 3 {
		t.Fatalf("jump: %v scanned=%d", loops(rows), page.Scanned)
	}
	rows, _ = f.iterations(1000, 10, MIIterationQuery{Limit: 5, LoopCounter: 742})
	if !equalU64(loops(rows), []uint64{742}) {
		t.Fatalf("exact iteration: %v", loops(rows))
	}
	rows, page = f.iterations(1000, 10, MIIterationQuery{Limit: 5, LoopCounter: n + 1})
	if len(rows) != 0 || page.Next != nil {
		t.Fatalf("missing iteration: %v %+v", rows, page)
	}
	// The high-water mark hides iterations activated later than the first page.
	rows, page = f.iterations(1000, 10, MIIterationQuery{Limit: 200, From: 995, HighWater: 10 + 997})
	if !equalU64(loops(rows), []uint64{995, 996, 997}) || page.Next != nil {
		t.Fatalf("high water: %v next=%+v", loops(rows), page.Next)
	}
}

func TestMIIndexV2ScanBudgetBoundsFilteredWork(t *testing.T) {
	f := newMIFixture(t, 9)
	f.body(1, 9, 10)
	for i := 1; i <= 50; i++ {
		f.round(1+uint64(i), 1, 10+uint64(i), i)
	}
	f.commit()
	inc := model.IncidentValue{ProcessInstanceKey: 9}
	for i := 1; i <= 50; i++ {
		inc.ElementInstanceKey = 1 + uint64(i)
		f.must(f.tx.RecordMIIncidentRaised(&inc, 100+uint64(i)))
		if i != 50 {
			f.end(1+uint64(i), 1, 2, 200+uint64(i), false)
		}
	}
	f.commit()
	// Incident iterations that are still active: only #50. The state is checked
	// per row, so a small budget yields a short page and a resume position.
	rows, page := f.iterations(1, 10, MIIterationQuery{Limit: 10, IncidentsOnly: true, State: MIHistoryActive, ScanBudget: 20})
	if len(rows) != 0 || page.Next == nil || page.Scanned != 20 || page.Next.LoopCounter != 20 {
		t.Fatalf("budgeted page: rows=%v page=%+v", loops(rows), page)
	}
	rows, page = f.iterations(1, 10, MIIterationQuery{Limit: 10, IncidentsOnly: true, State: MIHistoryActive, ScanBudget: 100, After: page.Next})
	if !equalU64(loops(rows), []uint64{50}) || page.Next != nil {
		t.Fatalf("resumed page: rows=%v page=%+v", loops(rows), page)
	}
}

func TestMIIndexV2AttributesIncidentsToInnermostOwner(t *testing.T) {
	f := newMIFixture(t, 11)
	f.body(10, 11, 1)
	f.round(11, 10, 2, 1)
	f.round(12, 10, 3, 2)
	// Iteration 12 is a subprocess; its task 13 raises the incident.
	f.element(13, 12, 0)
	// A nested MI body inside iteration 11, with one iteration 21.
	f.body(20, 11, 4)
	f.round(21, 20, 5, 1)
	f.commit()

	nested, _, _ := f.s.MIBody(11, 20)
	if nested.ParentIterationKey != 11 {
		t.Fatalf("nested body parent: %+v", nested)
	}
	var top []uint64
	if _, err := f.s.MIBodyPage(11, MIBodyPageQuery{Limit: 10}, func(e MIHistoryEntry) error {
		top = append(top, e.ElementInstanceKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !equalU64(top, []uint64{10}) {
		t.Fatalf("top-level bodies: %v", top)
	}

	raise := func(el, pos uint64) {
		f.must(f.tx.RecordMIIncidentRaised(&model.IncidentValue{ProcessInstanceKey: 11, ElementInstanceKey: el}, pos))
	}
	resolve := func(el, pos uint64) {
		f.must(f.tx.RecordMIIncidentResolved(&model.IncidentValue{ProcessInstanceKey: 11, ElementInstanceKey: el}, pos))
	}
	raise(13, 30)   // inside iteration 12
	resolve(13, 31) // resolved
	raise(13, 32)   // raised again
	raise(13, 33)   // overwritten while open
	raise(21, 34)   // nested iteration: owned by the nested body, not iteration 11
	raise(10, 35)   // on the body itself
	raise(999, 36)  // not an element of any MI execution: not indexed
	resolve(998, 37)
	f.commit()

	r12, _, _ := f.s.MIIteration(11, 12)
	if r12.IncidentsRaised != 3 || r12.FirstIncidentPosition != 30 {
		t.Fatalf("iteration 12 incidents: %+v", r12)
	}
	r11, _, _ := f.s.MIIteration(11, 11)
	if r11.IncidentsRaised != 0 {
		t.Fatalf("outer iteration took a nested incident: %+v", r11)
	}
	body, _, _ := f.s.MIBody(11, 10)
	if body.IncidentsRaised != 4 || body.IterationsWithIncidents != 1 || body.Changes != 6 {
		t.Fatalf("body incident counters: %+v", body)
	}
	nested, _, _ = f.s.MIBody(11, 20)
	if nested.IncidentsRaised != 1 || nested.IterationsWithIncidents != 1 {
		t.Fatalf("nested body incident counters: %+v", nested)
	}
	var recs []MIIncidentRecord
	more, err := f.s.MIIncidents(11, 12, 10, func(r MIIncidentRecord) error {
		recs = append(recs, r)
		return nil
	})
	if err != nil || more || len(recs) != 3 {
		t.Fatalf("incident records: %+v more=%v err=%v", recs, more, err)
	}
	want := []MIIncidentRecord{
		{OwnerKey: 12, ElementInstanceKey: 13, RaisedPosition: 30, EndPosition: 31, Outcome: MIIncidentResolved},
		{OwnerKey: 12, ElementInstanceKey: 13, RaisedPosition: 32, EndPosition: 33, Outcome: MIIncidentSuperseded},
		{OwnerKey: 12, ElementInstanceKey: 13, RaisedPosition: 33, Outcome: MIIncidentUnresolved},
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Fatalf("record %d: %+v, want %+v", i, recs[i], want[i])
		}
	}
	more, err = f.s.MIIncidents(11, 12, 2, func(MIIncidentRecord) error { return nil })
	if err != nil || !more {
		t.Fatalf("bounded incident page: more=%v err=%v", more, err)
	}
	rows, _ := f.iterations(10, 1, MIIterationQuery{Limit: 10, IncidentsOnly: true})
	if !equalU64(loops(rows), []uint64{2}) {
		t.Fatalf("incident iterations: %v", loops(rows))
	}
	rows, _ = f.iterations(10, 1, MIIterationQuery{Limit: 10, IncidentsOnly: true, HighWater: 29})
	if len(rows) != 0 {
		t.Fatalf("incident after the high-water mark was visible: %v", loops(rows))
	}
	if open, err := f.s.IncidentOpen(13); err != nil || open {
		t.Fatalf("incident table: open=%v err=%v", open, err)
	}
}

func TestMIIndexV2PurgeRemovesEveryFamily(t *testing.T) {
	f := newMIFixture(t, 12)
	f.body(10, 12, 1)
	f.round(11, 10, 2, 1)
	f.must(f.tx.RecordMIIncidentRaised(&model.IncidentValue{ProcessInstanceKey: 12, ElementInstanceKey: 11}, 3))
	f.commit()
	f.must(f.tx.PurgeInstanceHistory(12, 9, 0))
	f.commit()
	for _, family := range miIndexFamilies {
		prefix := appendBE64([]byte{byte(family)}, 12)
		if err := f.s.scanPrefix(prefix, func(k, _ []byte) error {
			return errors.New("left behind " + strconv.Itoa(int(family)))
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// The apply path builds keys into a reusable buffer; the read path uses the
// allocating helpers. The two layouts must be byte-identical.
func TestMIIndexKeyLayoutsAgree(t *testing.T) {
	tx := &Tx{}
	cp := func(b []byte) []byte { return append([]byte(nil), b...) }
	pairs := []struct {
		name      string
		apply, rd []byte
	}{
		{"body", cp(tx.miBodyRowKey(1, 2, 3)), keyMIBody(1, 2, 3)},
		{"iteration", cp(tx.miIterRowKey(1, 2, 3, 4)), keyMIIteration(1, 2, 3, 4)},
		{"round ref", cp(tx.miRefKey(cfMIRoundByElement, 1, 2)), keyMIRoundByElement(1, 2)},
		{"body ref", cp(tx.miRefKey(cfMIBodyByElement, 1, 2)), keyMIBodyByElement(1, 2)},
		{"open incident", cp(tx.miRefKey(cfMIIncidentOpen, 1, 2)), keyMIIncidentOpen(1, 2)},
		{"body parent", cp(tx.miQuadKey(cfMIBodyByParent, 1, 2, 3, 4)), keyMIBodyByParent(1, 2, 3, 4)},
		{"order", cp(tx.miOrderedKey(cfMIRoundOrder, 1, 2, 3, 0, 5, 6)), appendBE64(appendBE64(miRoundOrderPrefix(1, 2, 3), 5), 6)},
		{"state", cp(tx.miOrderedKey(cfMIRoundByState, 1, 2, 3, MIHistoryTerminated, 5, 6)), appendBE64(appendBE64(miRoundStatePrefix(1, 2, 3, MIHistoryTerminated), 5), 6)},
		{"incident", cp(tx.miOrderedKey(cfMIRoundIncident, 1, 2, 3, 0, 5, 6)), appendBE64(appendBE64(miRoundIncidentPrefix(1, 2, 3), 5), 6)},
		{"incident owner", cp(tx.miIncidentOwnerKey(1, 2, 3)), appendBE64(miIncidentOwnerPrefix(1, 2), 3)},
	}
	for _, p := range pairs {
		if !bytes.Equal(p.apply, p.rd) {
			t.Errorf("%s: apply %x, read %x", p.name, p.apply, p.rd)
		}
	}
	tx.miVar[0] = 0
	k := tx.miVar[:]
	k[0] = byte(cfVariable)
	copy(k[1:9], appendBE64(nil, 42))
	copy(k[9:], MILoopCounterVariable)
	if !bytes.Equal(k, keyVariable(42, MILoopCounterVariable)) {
		t.Fatalf("loopCounter key %x differs from variable key %x", k, keyVariable(42, MILoopCounterVariable))
	}
}

// Recording an iteration must not add allocations of its own: every key and
// value is built in the transaction's fixed buffers (invariant I1). What does
// allocate is Pebble's point read through an indexed batch (two allocations per
// Get that falls through to the database), which every read in applyToState
// pays. The fold reads two facts per activation (body reference, loopCounter)
// and two per end (iteration reference, iteration row); the body's counters are
// write-only merges. The bound is exactly those four reads.
func TestMIIndexV2IterationFoldAllocatesOnlyInPointReads(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts under -race measure the detector's instrumentation; the plain test run asserts them")
	}
	f := newMIFixture(t, 13)
	f.body(10, 13, 1)
	f.commit()
	perGet := testing.AllocsPerRun(100, func() {
		if _, _, err := f.tx.miBodyActivationPosition(13, 10); err != nil {
			panic(err)
		}
	})
	round := model.ElementInstanceValue{ProcessInstanceKey: 13, FlowScopeKey: 10, MultiInstance: 2}
	key := uint64(100)
	allocs := testing.AllocsPerRun(200, func() {
		key++
		if err := f.tx.RecordMIActivation(key, &round, key); err != nil {
			panic(err)
		}
		if err := f.tx.RecordMIEnd(key, &round, key+1, false); err != nil {
			panic(err)
		}
	})
	if limit := 4*perGet + 0.5; allocs > limit {
		t.Fatalf("iteration fold allocated %.2f times per activation+end; four point reads account for %.2f", allocs, 4*perGet)
	}
}

func TestMIIndexV2RejectsImpossibleBodyCounters(t *testing.T) {
	f := newMIFixture(t, 14)
	f.body(10, 14, 1)
	f.round(11, 10, 2, 1)
	f.commit()
	for _, tt := range []struct {
		name  string
		kind  miBodyCounterKind
		delta int64
	}{
		{"more completed than seeded", miCountCompleted, 2},
		{"negative", miCountTerminated, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f.must(f.tx.miCount(14, 10, tt.kind, tt.delta))
			f.commit()
			if _, _, err := f.s.MIBody(14, 10); err == nil {
				t.Fatal("impossible counters were accepted")
			}
			f.must(f.tx.miCount(14, 10, tt.kind, -tt.delta))
			f.commit()
			if _, _, err := f.s.MIBody(14, 10); err != nil {
				t.Fatalf("restored counters rejected: %v", err)
			}
		})
	}
	f.must(f.s.db.Set(keyMIBodyCounter(14, 10, miCountIterations), []byte{1}, nil))
	if _, _, err := f.s.MIBody(14, 10); err == nil {
		t.Fatal("a corrupt counter was accepted")
	}
}

func TestMIHistoryStateNamesRoundTrip(t *testing.T) {
	for _, st := range []MIHistoryState{MIHistoryActive, MIHistoryCompleted, MIHistoryTerminated} {
		got, ok := ParseMIHistoryState(st.String())
		if !ok || got != st {
			t.Fatalf("%s did not round-trip: %v %v", st, got, ok)
		}
	}
	if _, ok := ParseMIHistoryState("failed"); ok {
		t.Fatal("an unknown state name parsed")
	}
	if MIHistoryState(9).String() != "unknown" {
		t.Fatal("an invalid state has a name")
	}
}

// Every corrupt MI index entry is an error, never a plausible answer.
func TestMIIndexV2CorruptEntriesFailReads(t *testing.T) {
	f := newMIFixture(t, 15)
	f.body(10, 15, 1)
	f.round(11, 10, 2, 1)
	f.must(f.tx.RecordMIIncidentRaised(&model.IncidentValue{ProcessInstanceKey: 15, ElementInstanceKey: 11}, 3))
	f.commit()
	pageAll := func() error {
		_, err := f.s.MIIterationPage(15, 10, 1, MIIterationQuery{Limit: 10}, func(MIHistoryEntry) error { return nil })
		return err
	}
	pageInc := func() error {
		_, err := f.s.MIIterationPage(15, 10, 1, MIIterationQuery{Limit: 10, IncidentsOnly: true}, func(MIHistoryEntry) error { return nil })
		return err
	}
	pageState := func() error {
		_, err := f.s.MIIterationPage(15, 10, 1, MIIterationQuery{Limit: 10, State: MIHistoryCompleted}, func(MIHistoryEntry) error { return nil })
		return err
	}
	bodies := func() error {
		_, err := f.s.MIBodyPage(15, MIBodyPageQuery{Limit: 10}, func(MIHistoryEntry) error { return nil })
		return err
	}
	incidents := func() error {
		_, err := f.s.MIIncidents(15, 11, 10, func(MIIncidentRecord) error { return nil })
		return err
	}
	tests := []struct {
		name  string
		key   []byte
		value []byte
		read  func() error
	}{
		{"short order key", miRoundOrderPrefix(15, 10, 1), nil, pageAll},
		{"order key without row", appendBE64(appendBE64(miRoundOrderPrefix(15, 10, 1), 2), 99), nil, pageAll},
		{"order key with wrong loopCounter", appendBE64(appendBE64(miRoundOrderPrefix(15, 10, 1), 7), 11), nil, pageAll},
		{"state index disagrees with row", appendBE64(appendBE64(miRoundStatePrefix(15, 10, 1, MIHistoryCompleted), 1), 11), nil, pageState},
		{"incident index value", appendBE64(appendBE64(miRoundIncidentPrefix(15, 10, 1), 1), 11), []byte{1}, pageInc},
		{"body parent key", append(miBodyByParentPrefix(15, 0), 1), nil, bodies},
		{"body parent without row", keyMIBodyByParent(15, 0, 50, 51), nil, bodies},
		{"body parent disagrees with row", keyMIBodyByParent(15, 77, 1, 10), nil, func() error {
			_, err := f.s.MIBodyPage(15, MIBodyPageQuery{ParentIteration: 77, Limit: 10}, func(MIHistoryEntry) error { return nil })
			return err
		}},
		{"incident record", appendBE64(miIncidentOwnerPrefix(15, 11), 3), []byte{1, 2}, incidents},
		{"iteration row", keyMIIteration(15, 10, 1, 11), []byte{2, 'r'}, pageAll},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, had, err := getCopy(f.s.db, tt.key)
			f.must(err)
			f.must(f.s.db.Set(tt.key, tt.value, nil))
			if err := tt.read(); err == nil {
				t.Fatal("corrupt MI index entry was accepted")
			}
			if had {
				f.must(f.s.db.Set(tt.key, original, nil))
			} else {
				f.must(f.s.db.Delete(tt.key, nil))
			}
			if err := tt.read(); err != nil {
				t.Fatalf("restored index rejected: %v", err)
			}
		})
	}
	if _, err := f.s.MIIterationPage(15, 10, 1, MIIterationQuery{Limit: 1, State: 9}, func(MIHistoryEntry) error { return nil }); err == nil {
		t.Fatal("an invalid state filter was accepted")
	}
	if _, err := f.s.MIIncidents(15, 11, 0, func(MIIncidentRecord) error { return nil }); err == nil {
		t.Fatal("an invalid incident page limit was accepted")
	}
	stop := errors.New("stop")
	if _, err := f.s.MIIterationPage(15, 10, 1, MIIterationQuery{Limit: 1}, func(MIHistoryEntry) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("callback error lost: %v", err)
	}
	if _, err := f.s.MIBodyPage(15, MIBodyPageQuery{Limit: 1}, func(MIHistoryEntry) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("callback error lost: %v", err)
	}
	if _, err := f.s.MIIncidents(15, 11, 1, func(MIIncidentRecord) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("callback error lost: %v", err)
	}
}

// The fold refuses to build on a corrupt projection instead of compounding it.
func TestMIIndexV2FoldFailsOnCorruptProjection(t *testing.T) {
	f := newMIFixture(t, 16)
	f.body(10, 16, 1)
	f.round(11, 10, 2, 1)
	f.commit()
	round := model.ElementInstanceValue{ProcessInstanceKey: 16, FlowScopeKey: 10, MultiInstance: 2}
	body := model.ElementInstanceValue{ProcessInstanceKey: 16, MultiInstance: 1}
	inc := model.IncidentValue{ProcessInstanceKey: 16, ElementInstanceKey: 11}
	tests := []struct {
		name  string
		key   []byte
		value []byte
		apply func() error
	}{
		{"body reference", keyMIBodyByElement(16, 10), []byte{1}, func() error { return f.tx.RecordMIActivation(12, &round, 5) }},
		{"round reference", keyMIRoundByElement(16, 11), []byte{1}, func() error { return f.tx.RecordMIEnd(11, &round, 5, false) }},
		{"round row", keyMIIteration(16, 10, 1, 11), []byte{1}, func() error { return f.tx.RecordMIEnd(11, &round, 5, false) }},
		{"round row missing", keyMIIteration(16, 10, 1, 11), nil, func() error { return f.tx.RecordMIIncidentRaised(&inc, 5) }},
		{"body row", keyMIBody(16, 1, 10), []byte{1}, func() error { return f.tx.RecordMIEnd(10, &body, 5, false) }},
		{"body row missing", keyMIBody(16, 1, 10), nil, func() error { return f.tx.RecordMIEnd(10, &body, 5, false) }},
		{"oversized value", keyMIRoundByElement(16, 11), make([]byte, 200), func() error { return f.tx.RecordMIEnd(11, &round, 5, false) }},
		{"open incident reference", keyMIIncidentOpen(16, 11), []byte{1}, func() error { return f.tx.RecordMIIncidentResolved(&inc, 5) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.tx = f.s.NewTransaction()
			if tt.value == nil {
				f.must(f.tx.b.Delete(tt.key, nil))
			} else {
				f.must(f.tx.b.Set(tt.key, tt.value, nil))
			}
			if err := tt.apply(); err == nil {
				t.Fatal("fold accepted a corrupt projection")
			}
			_ = f.tx.Close() // discard: nothing of the corruption is committed
		})
	}
	f.tx = f.s.NewTransaction()
}

// A loopCounter the projection cannot read as a positive integer is recorded as
// unknown (0): the fold never fails over it and never guesses a number.
func TestMIIndexV2MalformedLoopCounterIsUnknown(t *testing.T) {
	f := newMIFixture(t, 17)
	f.body(10, 17, 1)
	f.must(f.tx.PutVariable(&model.VariableValue{ScopeKey: 11, Name: MILoopCounterVariable, Kind: model.VarString, Text: "7", Index: -1}))
	round := f.element(11, 10, 2)
	f.must(f.tx.RecordMIActivation(11, &round, 2))
	f.commit()
	r, ok, err := f.s.MIIteration(17, 11)
	if err != nil || !ok || r.LoopCounter != 0 {
		t.Fatalf("malformed loopCounter: %+v %v %v", r, ok, err)
	}
}
