package state

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

func (f *miFixture) step(el, scope uint64, mi uint8, pos uint64, action byte) {
	f.t.Helper()
	v := model.ElementInstanceValue{ProcessInstanceKey: f.pi, ProcessDefKey: 9, ElementId: int32(el % 100), FlowScopeKey: scope, MultiInstance: mi, TokenID: el}
	f.must(f.tx.RecordOutlineStep(el, &v, int64(pos), pos, action))
}

func (f *miFixture) outline(after *OutlinePosition, highWater uint64, limit int) ([]uint64, *OutlinePosition) {
	f.t.Helper()
	var keys []uint64
	next, err := f.s.OutlinePage(f.pi, after, highWater, limit, func(s OutlineStep) error {
		keys = append(keys, s.ElementInstanceKey*10+uint64(s.Action))
		return nil
	})
	f.must(err)
	return keys, next
}

// The outline keeps an instance's own steps and its body executions, and hides
// every iteration and everything inside one, however deeply nested.
func TestOutlineHidesWhatRunsInsideAnIteration(t *testing.T) {
	const pi = 60
	f := newMIFixture(t, pi)
	f.step(1, pi, 0, 1, ReplayActivated) // start event
	f.step(1, pi, 0, 2, ReplayCompleted)
	f.step(10, pi, 1, 3, ReplayActivated)  // MI body (a subprocess)
	f.step(11, 10, 2, 4, ReplayActivated)  // iteration
	f.step(12, 11, 0, 5, ReplayActivated)  // task inside the iteration
	f.step(13, 11, 1, 6, ReplayActivated)  // nested MI body inside it
	f.step(14, 13, 2, 7, ReplayActivated)  // nested iteration
	f.step(15, 14, 0, 8, ReplayActivated)  // task inside the nested iteration
	f.step(15, 14, 0, 9, ReplayCompleted)  // its end, also hidden
	f.step(11, 10, 2, 10, ReplayCompleted) // iteration end
	f.step(10, pi, 1, 11, ReplayCompleted) // body end
	f.step(20, pi, 0, 12, ReplayActivated) // a plain subprocess
	f.step(21, 20, 0, 13, ReplayActivated) // a task inside it: visible
	f.step(30, pi, 0, 14, ReplayActivated) // end event
	f.commit()

	got, next := f.outline(nil, 0, 50)
	want := []uint64{11, 12, 101, 102, 201, 211, 301}
	if !equalU64(got, want) || next != nil {
		t.Fatalf("outline %v, want %v (next %+v)", got, want, next)
	}
	first, next := f.outline(nil, 0, 3)
	if !equalU64(first, want[:3]) || next == nil {
		t.Fatalf("first page %v next %+v", first, next)
	}
	rest, next := f.outline(next, 0, 50)
	if !equalU64(rest, want[3:]) || next != nil {
		t.Fatalf("second page %v next %+v", rest, next)
	}
	// The high-water mark hides what was recorded after the first page.
	upTo, _ := f.outline(nil, 11, 50)
	if !equalU64(upTo, want[:4]) {
		t.Fatalf("high water: %v", upTo)
	}
	if _, err := f.s.OutlinePage(pi, nil, 0, 0, func(OutlineStep) error { return nil }); err == nil {
		t.Fatal("an invalid limit was accepted")
	}
	stop := errors.New("stop")
	if _, err := f.s.OutlinePage(pi, nil, 0, 50, func(OutlineStep) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("a visitor's error was not returned: %v", err)
	}

	f.must(f.tx.PurgeInstanceHistory(pi, 9, 0))
	f.commit()
	if got, _ := f.outline(nil, 0, 50); len(got) != 0 {
		t.Fatalf("outline survived purge: %v", got)
	}
}

func (f *miFixture) iterationSteps(iteration uint64, after *OutlinePosition, highWater uint64, limit int) ([]uint64, *OutlinePosition) {
	f.t.Helper()
	var keys []uint64
	next, err := f.s.IterationStepsPage(f.pi, iteration, after, highWater, limit, func(s OutlineStep) error {
		keys = append(keys, s.ElementInstanceKey*10+uint64(s.Action))
		return nil
	})
	f.must(err)
	return keys, next
}

// What the outline hides is an iteration's own steps: each iteration holds its
// activation, what ran inside it and its end; a nested body is one of its steps,
// and the nested body's iterations hold their own.
func TestIterationStepsAreWhatRanInsideOneIteration(t *testing.T) {
	const pi = 63
	f := newMIFixture(t, pi)
	f.step(1, pi, 0, 1, ReplayActivated)
	f.step(10, pi, 1, 2, ReplayActivated)  // MI body (a subprocess)
	f.step(11, 10, 2, 3, ReplayActivated)  // iteration 1
	f.step(21, 10, 2, 4, ReplayActivated)  // iteration 2, in parallel
	f.step(12, 11, 0, 5, ReplayActivated)  // a task inside iteration 1
	f.step(22, 21, 0, 6, ReplayActivated)  // a task inside iteration 2
	f.step(13, 11, 0, 7, ReplayActivated)  // a plain subprocess inside iteration 1
	f.step(14, 13, 1, 8, ReplayActivated)  // a nested body inside that subprocess
	f.step(15, 14, 2, 9, ReplayActivated)  // its iteration
	f.step(16, 15, 0, 10, ReplayActivated) // a task inside the nested iteration
	f.step(16, 15, 0, 11, ReplayCompleted)
	f.step(15, 14, 2, 12, ReplayCompleted)
	f.step(14, 13, 1, 13, ReplayCompleted)
	f.step(12, 11, 0, 14, ReplayCompleted)
	f.step(22, 21, 0, 15, ReplayTerminated)
	f.step(11, 10, 2, 16, ReplayCompleted)
	f.step(21, 10, 2, 17, ReplayTerminated)
	f.step(10, pi, 1, 18, ReplayCompleted)
	f.commit()

	if got, _ := f.outline(nil, 0, 50); !equalU64(got, []uint64{11, 101, 102}) {
		t.Fatalf("outline %v", got)
	}
	one := []uint64{111, 121, 131, 141, 142, 122, 112}
	if got, next := f.iterationSteps(11, nil, 0, 50); !equalU64(got, one) || next != nil {
		t.Fatalf("iteration 1: %v (next %+v)", got, next)
	}
	if got, _ := f.iterationSteps(21, nil, 0, 50); !equalU64(got, []uint64{211, 221, 223, 213}) {
		t.Fatalf("iteration 2: %v", got)
	}
	if got, _ := f.iterationSteps(15, nil, 0, 50); !equalU64(got, []uint64{151, 161, 162, 152}) {
		t.Fatalf("nested iteration: %v", got)
	}
	first, next := f.iterationSteps(11, nil, 0, 3)
	rest, _ := f.iterationSteps(11, next, 0, 50)
	if !equalU64(append(first, rest...), one) || next == nil {
		t.Fatalf("paged: %v + %v", first, rest)
	}
	if upTo, _ := f.iterationSteps(11, nil, 8, 50); !equalU64(upTo, one[:4]) {
		t.Fatalf("high water: %v", upTo)
	}
	if got, _ := f.iterationSteps(10, nil, 0, 50); len(got) != 0 {
		t.Fatalf("a body is not an iteration: %v", got)
	}

	f.must(f.tx.PurgeInstanceHistory(pi, 9, 0))
	f.commit()
	if got, _ := f.iterationSteps(11, nil, 0, 50); len(got) != 0 {
		t.Fatalf("iteration steps survived purge: %v", got)
	}
}

// A hidden row written before iteration steps existed names no iteration: what
// runs below it stays out of the outline and records no steps, and the store
// does not claim complete iteration steps until it is rebuilt.
func TestIterationStepsOfOlderHistoryAreUnavailable(t *testing.T) {
	const pi = 64
	f := newMIFixture(t, pi)
	f.must(f.s.db.Set(f.tx.miRefKey(cfOutlineHidden, pi, 50), nil, pebble.Sync))
	f.step(51, 50, 0, 1, ReplayActivated)
	f.step(52, 51, 0, 2, ReplayActivated)
	f.commit()
	if got, _ := f.outline(nil, 0, 50); len(got) != 0 {
		t.Fatalf("an element under an older hidden row reached the outline: %v", got)
	}
	var n int
	iter, err := f.s.db.NewIter(&pebble.IterOptions{LowerBound: []byte{byte(cfIterationStep)}, UpperBound: []byte{byte(cfIterationStep) + 1}})
	f.must(err)
	for iter.First(); iter.Valid(); iter.Next() {
		n++
	}
	f.must(iter.Close())
	if n != 0 {
		t.Fatalf("%d steps recorded without an owning iteration", n)
	}

	f.must(f.s.db.Delete(keyMeta(metaIterationStepsIndex), pebble.Sync))
	if _, err := f.s.IterationStepsPage(pi, 50, nil, 0, 10, func(OutlineStep) error { return nil }); !errors.Is(err, ErrIterationStepsUnavailable) {
		t.Fatalf("steps of an unproven store: %v", err)
	}
	if needed, err := f.s.MIHistoryRebuildNeeded(); err != nil || !needed {
		t.Fatalf("a store without iteration steps does not ask for a rebuild: %v %v", needed, err)
	}
	if _, err := f.s.IterationStepsPage(pi, 50, nil, 0, 0, func(OutlineStep) error { return nil }); err == nil {
		t.Fatal("an invalid limit was accepted")
	}
	f.must(f.s.db.Set(keyMeta(metaIterationStepsIndex), []byte{9}, pebble.Sync))
	if _, err := f.s.IterationStepsIndexReady(); err == nil {
		t.Fatal("a corrupt iteration steps marker was accepted")
	}
	if _, err := f.s.MIHistoryRebuildNeeded(); err == nil {
		t.Fatal("a corrupt iteration steps marker did not stop the rebuild check")
	}
	if _, err := f.s.IterationStepsPage(pi, 50, nil, 0, 10, func(OutlineStep) error { return nil }); err == nil {
		t.Fatal("steps behind a corrupt marker were read")
	}
}

func TestOutlineRefusesWhatItCannotProve(t *testing.T) {
	f := newMIFixture(t, 61)
	f.step(1, 61, 0, 1, ReplayActivated)
	f.commit()
	f.must(f.s.db.Set(keyOutline(61, 1, 1), []byte{1}, pebble.Sync))
	if _, err := f.s.OutlinePage(61, nil, 0, 10, func(OutlineStep) error { return nil }); err == nil {
		t.Fatal("a corrupt outline row was accepted")
	}
	f.must(f.s.db.Delete(keyMeta(metaOutlineIndex), pebble.Sync))
	if _, err := f.s.OutlinePage(61, nil, 0, 10, func(OutlineStep) error { return nil }); !errors.Is(err, ErrOutlineIndexUnavailable) {
		t.Fatalf("outline of an unproven store: %v", err)
	}
	if needed, err := f.s.MIHistoryRebuildNeeded(); err != nil || !needed {
		t.Fatalf("a store without its outline does not ask for a rebuild: %v %v", needed, err)
	}
	f.must(f.s.db.Set(keyMeta(metaOutlineIndex), []byte{9}, pebble.Sync))
	if _, err := f.s.OutlineIndexReady(); err == nil {
		t.Fatal("a corrupt outline marker was accepted")
	}
	if _, err := f.s.OutlinePage(61, nil, 0, 10, func(OutlineStep) error { return nil }); err == nil {
		t.Fatal("an outline behind a corrupt marker was read")
	}
	if _, err := f.s.MIHistoryRebuildNeeded(); err == nil {
		t.Fatal("a corrupt outline marker did not stop the rebuild check")
	}
}

// A step directly under the process instance is recorded without a read.
func TestOutlineStepOfARootElementReadsNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts under -race measure the detector's instrumentation")
	}
	f := newMIFixture(t, 62)
	v := model.ElementInstanceValue{ProcessInstanceKey: 62, FlowScopeKey: 62, ElementId: 1}
	pos := uint64(0)
	allocs := testing.AllocsPerRun(200, func() {
		pos++
		if err := f.tx.RecordOutlineStep(pos, &v, int64(pos), pos, ReplayActivated); err != nil {
			panic(err)
		}
	})
	if allocs > 0.5 {
		t.Fatalf("a root step allocated %.2f times", allocs)
	}
}

// A corrupt readiness marker keeps the store from opening: it can neither be
// trusted as complete nor silently reset.
func TestACorruptOutlineOrStepsMarkerRefusesToOpen(t *testing.T) {
	for _, marker := range []string{metaOutlineIndex, metaIterationStepsIndex} {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.db.Set(keyMeta(marker), []byte{9}, pebble.Sync); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(dir); err == nil {
			_ = s.Close()
			t.Fatalf("a store with a corrupt %s marker opened", marker)
		}
	}
}
