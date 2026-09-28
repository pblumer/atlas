package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// depart records that a token of an instance left an element, the way applyToState
// does on a completion or a termination.
func depart(t *testing.T, s *state.Store, how state.Departure, defKey uint64, elementId int32, piKey uint64) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		return tx.RecordDeparture(how, defKey, elementId, piKey)
	})
}

// departedFrom drains one departure index for one element into the instance keys it
// yielded, in the order it yielded them.
func departedFrom(t *testing.T, s *state.Store, how state.Departure, defKey uint64, elementId int32, before uint64) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.InstancesDepartedElementDesc(how, defKey, elementId, before, func(key uint64, _ *model.ProcessInstanceValue) error {
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("InstancesDepartedElementDesc: %v", err)
	}
	return got
}

// TestInstancesDepartedElement is the point of the two indexes: "which instances
// completed this task" and "which were cancelled at it" are answered from the element,
// newest instance first, running and finished alike — and the two answers are kept
// apart, because a token that got here and one that got through are different facts.
func TestInstancesDepartedElement(t *testing.T) {
	s := openStore(t)
	const def, other = uint64(7), uint64(8)
	for _, pi := range []uint64{10, 11, 12, 13} {
		putActive(t, s, pi, def, int64(pi))
	}
	putActive(t, s, 14, other, 140)
	// A departure says nothing about whether the instance has ended since: 11 finished
	// after it completed the task, and is still one of the instances that completed it.
	finish(t, s, 11, def, 11, 1100)

	depart(t, s, state.DepartedCompleted, def, 3, 10)
	depart(t, s, state.DepartedCompleted, def, 3, 11)
	depart(t, s, state.DepartedCompleted, def, 3, 13)
	depart(t, s, state.DepartedCancelled, def, 3, 12)
	depart(t, s, state.DepartedCompleted, def, 4, 12)   // another element of the version
	depart(t, s, state.DepartedCompleted, other, 3, 14) // the same element index of another version

	if got, want := departedFrom(t, s, state.DepartedCompleted, def, 3, 0), []uint64{13, 11, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed at def 7 element 3 = %v, want %v (newest first, finished included)", got, want)
	}
	if got, want := departedFrom(t, s, state.DepartedCancelled, def, 3, 0), []uint64{12}; !reflect.DeepEqual(got, want) {
		t.Errorf("cancelled at def 7 element 3 = %v, want %v", got, want)
	}
	if got, want := departedFrom(t, s, state.DepartedCompleted, def, 4, 0), []uint64{12}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed at def 7 element 4 = %v, want %v", got, want)
	}
	if got, want := departedFrom(t, s, state.DepartedCompleted, other, 3, 0), []uint64{14}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed at def 8 element 3 = %v, want %v", got, want)
	}
	if got := departedFrom(t, s, state.DepartedCancelled, def, 4, 0); got != nil {
		t.Errorf("nothing was cancelled at element 4, got %v", got)
	}

	// `before` is the newest-first paging cursor, exclusive of the instance it names.
	if got, want := departedFrom(t, s, state.DepartedCompleted, def, 3, 13), []uint64{11, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed before 13 = %v, want %v", got, want)
	}
	if got := departedFrom(t, s, state.DepartedCompleted, def, 3, 10); got != nil {
		t.Errorf("before the oldest = %v, want nothing", got)
	}
}

// TestInstancesDepartedElementCountsAnInstanceOnce covers a loop: a token that leaves
// the same element five times is one instance that completed it, not five rows.
func TestInstancesDepartedElementCountsAnInstanceOnce(t *testing.T) {
	s := openStore(t)
	const def = uint64(7)
	putActive(t, s, 10, def, 100)
	for i := 0; i < 5; i++ {
		depart(t, s, state.DepartedCompleted, def, 3, 10)
	}
	if got, want := departedFrom(t, s, state.DepartedCompleted, def, 3, 0), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("five passes of one instance = %v, want %v", got, want)
	}
}

// TestInstancesDepartedElementStopsEarly proves a caller can page the index: the
// sentinel its fn returns stops the walk and is handed back, so a page costs the page.
func TestInstancesDepartedElementStopsEarly(t *testing.T) {
	s := openStore(t)
	const def = uint64(7)
	stop := errors.New("page full")
	for i := uint64(10); i < 20; i++ {
		putActive(t, s, i, def, int64(i))
		depart(t, s, state.DepartedCompleted, def, 3, i)
	}
	var got []uint64
	err := s.InstancesDepartedElementDesc(state.DepartedCompleted, def, 3, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		if len(got) == 2 {
			return stop
		}
		got = append(got, key)
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("walk error = %v, want the sentinel back", err)
	}
	if want := []uint64{19, 18}; !reflect.DeepEqual(got, want) {
		t.Errorf("page = %v, want %v", got, want)
	}
}

// TestInstancesDepartedElementRefusesUnknownDeparture keeps a caller's mistake from
// reading as a fact about the process: a departure that is neither completed nor
// cancelled is an error, on the way in and on the way out, not an empty list.
func TestInstancesDepartedElementRefusesUnknownDeparture(t *testing.T) {
	s := openStore(t)
	if err := s.InstancesDepartedElementDesc(state.Departure(9), 7, 3, 0, func(uint64, *model.ProcessInstanceValue) error {
		return nil
	}); err == nil {
		t.Error("reading an unknown departure succeeded")
	}
	tx := s.NewTransaction()
	defer func() { _ = tx.Close() }()
	if err := tx.RecordDeparture(state.Departure(0), 7, 3, 10); err == nil {
		t.Error("recording an unknown departure succeeded")
	}
}
