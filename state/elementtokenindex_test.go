package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// putToken lands a token on an element the way applyToState does, so the piByEl
// index is written by the path the engine writes it with.
func putToken(t *testing.T, s *state.Store, elKey, piKey, defKey uint64, elementId int32) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutElementInstance(elKey, &model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      defKey,
			ElementId:          elementId,
			FlowScopeKey:       piKey,
		})
	})
}

// dropToken takes a token off an element, as a completion or a termination does.
func dropToken(t *testing.T, s *state.Store, elKey, piKey, defKey uint64, elementId int32) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		return tx.DeleteElementInstance(elKey, &model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      defKey,
			ElementId:          elementId,
			FlowScopeKey:       piKey,
		})
	})
}

// onElement drains the instances holding a token on one element into the keys the
// index yielded, in the order it yielded them.
func onElement(t *testing.T, s *state.Store, defKey uint64, elementId int32, before uint64) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.InstancesOnElementDesc(defKey, elementId, before, func(key uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey != defKey {
			t.Errorf("instance %d has def %d, want %d", key, v.ProcessDefKey, defKey)
		}
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("InstancesOnElementDesc: %v", err)
	}
	return got
}

// TestInstancesOnElement is the point of the index: "which instances are sitting on
// this task right now" is answered from the element, newest instance first, without
// reading the instances that are not.
func TestInstancesOnElement(t *testing.T) {
	s := openStore(t)
	const def, other = uint64(7), uint64(8)
	for _, pi := range []uint64{10, 11, 12} {
		putActive(t, s, pi, def, int64(pi))
	}
	putActive(t, s, 13, other, 130)

	putToken(t, s, 100, 10, def, 3)
	putToken(t, s, 101, 11, def, 4) // a different element of the same definition
	putToken(t, s, 102, 12, def, 3)
	putToken(t, s, 103, 13, other, 3) // element 3 of another definition

	if got, want := onElement(t, s, def, 3, 0), []uint64{12, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 element 3 = %v, want %v (newest first)", got, want)
	}
	if got, want := onElement(t, s, def, 4, 0), []uint64{11}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 element 4 = %v, want %v", got, want)
	}
	if got, want := onElement(t, s, other, 3, 0), []uint64{13}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 8 element 3 = %v, want %v", got, want)
	}
	if got := onElement(t, s, def, 99, 0); got != nil {
		t.Errorf("an element no token is on = %v, want nothing", got)
	}

	// `before` is the newest-first paging cursor, exclusive of the instance it names.
	if got, want := onElement(t, s, def, 3, 12), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 element 3 before 12 = %v, want %v", got, want)
	}
	if got := onElement(t, s, def, 3, 10); got != nil {
		t.Errorf("before the oldest = %v, want nothing", got)
	}

	// A token that moves on leaves the index — otherwise the filter would keep
	// offering instances that are no longer there.
	dropToken(t, s, 102, 12, def, 3)
	if got, want := onElement(t, s, def, 3, 0), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("after the token left: %v, want %v", got, want)
	}
}

// TestInstancesOnElementCountsAnInstanceOnce covers the loop and multi-instance
// case: several tokens of one instance on one element are one answer, because the
// question is about instances. The instance leaves the answer only when its last
// token does.
func TestInstancesOnElementCountsAnInstanceOnce(t *testing.T) {
	s := openStore(t)
	const def = uint64(7)
	putActive(t, s, 10, def, 100)
	putActive(t, s, 11, def, 110)
	putToken(t, s, 100, 10, def, 3)
	putToken(t, s, 101, 10, def, 3) // a second iteration on the same element
	putToken(t, s, 102, 10, def, 3)
	putToken(t, s, 103, 11, def, 3)

	if got, want := onElement(t, s, def, 3, 0), []uint64{11, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("three tokens in one instance = %v, want %v", got, want)
	}
	dropToken(t, s, 101, 10, def, 3)
	if got, want := onElement(t, s, def, 3, 0), []uint64{11, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("with one iteration gone = %v, want %v (two tokens remain)", got, want)
	}
	dropToken(t, s, 100, 10, def, 3)
	dropToken(t, s, 102, 10, def, 3)
	if got, want := onElement(t, s, def, 3, 0), []uint64{11}; !reflect.DeepEqual(got, want) {
		t.Errorf("with every iteration gone = %v, want %v", got, want)
	}
}

// TestInstancesOnElementStopsEarly proves a caller can page the index: the sentinel
// its fn returns stops the walk and is handed back, so a page costs the page.
func TestInstancesOnElementStopsEarly(t *testing.T) {
	s := openStore(t)
	const def = uint64(7)
	stop := errors.New("page full")
	for i := uint64(10); i < 20; i++ {
		putActive(t, s, i, def, int64(i))
		putToken(t, s, 100+i, i, def, 3)
	}
	var got []uint64
	err := s.InstancesOnElementDesc(def, 3, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
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

// TestInstancesOnElementFollowsMigration covers the one path that moves a live
// token between (definition, element) pairs: after a migration the instance is
// found under the target version's element and no longer under the source's
// (ADR-0162). An entry left behind would make the filter offer an instance that is
// not there, on a version it is no longer running.
func TestInstancesOnElementFollowsMigration(t *testing.T) {
	s := openStore(t)
	const from, to = uint64(7), uint64(8)
	putActive(t, s, 10, from, 100)
	putToken(t, s, 100, 10, from, 3)

	commit(t, s, func(tx *state.Tx) error {
		return tx.MigrateInstance(&model.ProcessMigrationValue{
			ProcessInstanceKey: 10,
			FromProcessDefKey:  from,
			ToProcessDefKey:    to,
			Mapping:            []model.ElementMapping{{From: 3, To: 5}},
		})
	})

	if got := onElement(t, s, from, 3, 0); got != nil {
		t.Errorf("source version still holds %v, want nothing", got)
	}
	if got, want := onElement(t, s, to, 5, 0), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("target version element 5 = %v, want %v", got, want)
	}
}
