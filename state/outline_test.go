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

	f.must(f.tx.PurgeInstanceHistory(pi, 9, 0))
	f.commit()
	if got, _ := f.outline(nil, 0, 50); len(got) != 0 {
		t.Fatalf("outline survived purge: %v", got)
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
