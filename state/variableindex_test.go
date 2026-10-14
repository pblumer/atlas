package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// putVar writes one variable through the ordinary transaction path, so the value
// index is maintained the way the engine maintains it.
func putIndexedVar(t *testing.T, s *state.Store, scope uint64, name, text string, indexed bool) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutVariable(&model.VariableValue{
			ScopeKey: scope, Name: name, Kind: model.VarString, Text: text, Indexed: indexed,
		})
	})
}

// found collects the instances the index reports for a query.
func found(t *testing.T, s *state.Store, name, value string, prefix bool) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.InstancesByVariable(name, value, prefix, func(piKey uint64) error {
		got = append(got, piKey)
		return nil
	}); err != nil {
		t.Fatalf("InstancesByVariable(%q, %q, prefix=%v): %v", name, value, prefix, err)
	}
	return got
}

// The point of the index: a business value names its instance in a seek, not a walk.
// Entries are ordered by value, so the instances holding one value come back together
// and in instance order.
func TestInstancesByVariableExact(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	putIndexedVar(t, s, 11, "identityId", "MT-1999", true)
	putIndexedVar(t, s, 12, "identityId", "MT-1998", true) // two instances, one value
	putIndexedVar(t, s, 13, "nachname", "MT-1998", true)   // same value, another name

	if got, want := found(t, s, "identityId", "MT-1998", false), []uint64{10, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("identityId=MT-1998 = %v, want %v", got, want)
	}
	if got, want := found(t, s, "identityId", "MT-1999", false), []uint64{11}; !reflect.DeepEqual(got, want) {
		t.Errorf("identityId=MT-1999 = %v, want %v", got, want)
	}
	if got := found(t, s, "identityId", "MT-2000", false); got != nil {
		t.Errorf("a value nothing holds = %v, want nothing", got)
	}
	if got := found(t, s, "unbekannt", "MT-1998", false); got != nil {
		t.Errorf("an undeclared name = %v, want nothing", got)
	}
	// An exact match is exact: a value that merely starts with the query is not it.
	if got := found(t, s, "identityId", "MT-19", false); got != nil {
		t.Errorf("exact search for a prefix = %v, want nothing", got)
	}
}

// Prefix is the other question an ordered index can answer, and the reason the
// exact match needs its own terminator: without one the two would be the same query.
func TestInstancesByVariablePrefix(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	putIndexedVar(t, s, 11, "identityId", "MT-1999", true)
	putIndexedVar(t, s, 12, "identityId", "XY-1000", true)

	if got, want := found(t, s, "identityId", "MT-", true), []uint64{10, 11}; !reflect.DeepEqual(got, want) {
		t.Errorf("identityId prefix MT- = %v, want %v", got, want)
	}
	if got, want := found(t, s, "identityId", "MT-1998", true), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("a prefix that is a whole value = %v, want %v", got, want)
	}
	if got := found(t, s, "identityId", "ZZ", true); got != nil {
		t.Errorf("a prefix nothing starts with = %v, want nothing", got)
	}
}

// Only what the process declared is indexed. An unmarked write is invisible to the
// index — which is what makes an undeclared process cost nothing.
func TestUnmarkedVariableIsNotIndexed(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", false)
	if got := found(t, s, "identityId", "MT-1998", false); got != nil {
		t.Errorf("an unmarked write = %v, want nothing in the index", got)
	}
}

// A variable is a current value, not a log: overwriting it must move the entry, or
// the index would answer with a value the instance no longer holds.
func TestOverwrittenVariableMovesItsEntry(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	putIndexedVar(t, s, 10, "identityId", "MT-2000", true)

	if got := found(t, s, "identityId", "MT-1998", false); got != nil {
		t.Errorf("the old value still names the instance: %v", got)
	}
	if got, want := found(t, s, "identityId", "MT-2000", false), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("the new value = %v, want %v", got, want)
	}
}

// A name that stops being declared (a new version drops it) stops being indexed, and
// the entry it already had has to go with it.
func TestUnmarkingAVariableDropsItsEntry(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", false)

	if got := found(t, s, "identityId", "MT-1998", false); got != nil {
		t.Errorf("an unmarked rewrite left its entry standing: %v", got)
	}
}

// Deleting the variable deletes its entry.
func TestDeletedVariableDropsItsEntry(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteVariable(10, "identityId") })

	if got := found(t, s, "identityId", "MT-1998", false); got != nil {
		t.Errorf("a deleted variable still names its instance: %v", got)
	}
	// Idempotent: deleting an absent variable is a no-op, as every fold step must be.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteVariable(10, "identityId") })
}

// History retention hard-deletes a finished instance (ADR-0146); its index entries
// go with it, or the index would name an instance that no longer exists.
func TestPurgeDropsIndexEntries(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutProcessInstanceHistory(10, &model.ProcessInstanceValue{
			ProcessDefKey: 7, State: model.PICompleted, CompletedAt: 700,
		})
	})
	putIndexedVar(t, s, 10, "identityId", "MT-1998", true)
	putIndexedVar(t, s, 10, "item", "4711", true)
	putIndexedVar(t, s, 11, "identityId", "MT-1999", true) // another instance, untouched

	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(10, 7, 0) })

	if got := found(t, s, "identityId", "MT-1998", false); got != nil {
		t.Errorf("a purged instance still names itself: %v", got)
	}
	if got := found(t, s, "item", "4711", false); got != nil {
		t.Errorf("a purged instance's second entry survived: %v", got)
	}
	if got, want := found(t, s, "identityId", "MT-1999", false), []uint64{11}; !reflect.DeepEqual(got, want) {
		t.Errorf("the purge took another instance's entry: %v, want %v", got, want)
	}
}

// A declared name whose write cannot be indexed — a structured value, or one past the
// length bound — drops whatever entry it had and adds none. The index must never
// claim a value the instance does not hold, and a truncated key would do exactly
// that: it would answer an exact query with a row whose value merely starts the same.
func TestUnindexableValueOfADeclaredNameLeavesNoEntry(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "payload", "MT-1998", true)
	if got := found(t, s, "payload", "MT-1998", false); len(got) != 1 {
		t.Fatalf("setup: %v, want the instance indexed", got)
	}

	long := make([]byte, model.MaxIndexedValueBytes+1)
	for i := range long {
		long[i] = 'x'
	}
	for _, tc := range []struct {
		name string
		v    model.VariableValue
	}{
		{"json", model.VariableValue{ScopeKey: 10, Name: "payload", Kind: model.VarJSON, Text: `{"a":1}`, Indexed: true}},
		{"too-long", model.VariableValue{ScopeKey: 10, Name: "payload", Kind: model.VarString, Text: string(long), Indexed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putIndexedVar(t, s, 10, "payload", "MT-1998", true) // back to an indexed value
			v := tc.v
			commit(t, s, func(tx *state.Tx) error { return tx.PutVariable(&v) })

			if got := found(t, s, "payload", "MT-1998", false); got != nil {
				t.Errorf("the previous value still names the instance: %v", got)
			}
			// And the variable itself is stored regardless — only the index declines it.
			var back model.VariableValue
			if err := s.VariablesOfScope(10, func(got *model.VariableValue) error {
				if got.Name == "payload" {
					back = *got
				}
				return nil
			}); err != nil {
				t.Fatalf("VariablesOfScope: %v", err)
			}
			if back.Text != tc.v.Text {
				t.Errorf("stored value = %q, want the write to have landed even though the index declined it", back.Text)
			}
		})
	}
}

// A boolean is a scalar, so it is indexable — and it is the one kind whose index text
// comes from a field other than Text, which is worth pinning where the index reads it.
func TestBooleanVariableIsIndexed(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutVariable(&model.VariableValue{
			ScopeKey: 10, Name: "gesperrt", Kind: model.VarBool, Bool: true, Indexed: true,
		})
	})
	if got, want := found(t, s, "gesperrt", "true", false), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("gesperrt=true = %v, want %v", got, want)
	}
	if got := found(t, s, "gesperrt", "false", false); got != nil {
		t.Errorf("gesperrt=false = %v, want nothing", got)
	}
}

// SetVariableIndexed is how a variable joins or leaves the value index after it has
// already been written — the command behind a process declaring a variable
// searchable once instances of it are already running.
//
// It had no test at all. These pin the three answers it gives, because two of them
// are "do nothing" and a no-op is exactly the kind of behaviour that rots unnoticed.

// Turning membership on moves an existing variable into the index, so an instance
// that was already running when the declaration arrived becomes findable.
func TestSetVariableIndexedAddsAnExistingVariableToTheIndex(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 10, "identityId", "MT-1998", false)
	if got := found(t, s, "identityId", "MT-1998", false); len(got) != 0 {
		t.Fatalf("found %v before the variable was indexed, want none", got)
	}

	commit(t, s, func(tx *state.Tx) error {
		return tx.SetVariableIndexed(10, "identityId", true)
	})

	if got := found(t, s, "identityId", "MT-1998", false); !reflect.DeepEqual(got, []uint64{10}) {
		t.Errorf("found %v, want the instance that holds the value", got)
	}
}

// And turning it off removes the entry, so the index never answers with a value the
// caller has stopped declaring.
func TestSetVariableIndexedRemovesTheEntryAgain(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 11, "identityId", "MT-1998", true)
	if got := found(t, s, "identityId", "MT-1998", false); len(got) != 1 {
		t.Fatalf("found %v, want the indexed variable", got)
	}

	commit(t, s, func(tx *state.Tx) error {
		return tx.SetVariableIndexed(11, "identityId", false)
	})

	if got := found(t, s, "identityId", "MT-1998", false); len(got) != 0 {
		t.Errorf("found %v after membership was turned off, want none", got)
	}
}

// A variable that is gone must be a no-op rather than an error. The command emits
// nothing in that case, but the fold has to survive replaying an event whose
// variable a later record deleted — which is the recovery property, not a niggle.
func TestSetVariableIndexedIsANoOpForAVariableThatIsGone(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.SetVariableIndexed(12, "neverExisted", true); err != nil {
			return err
		}
		// And for one that is already in the state being asked for.
		return tx.SetVariableIndexed(12, "neverExisted", false)
	})
	if got := found(t, s, "neverExisted", "", false); len(got) != 0 {
		t.Errorf("found %v for a variable that was never written", got)
	}
}

// Asking for the state a variable already holds writes nothing — and, in particular,
// does not re-put the record, which would move its index entry through the maintain
// path for no reason.
func TestSetVariableIndexedLeavesAVariableAlreadyInStepAlone(t *testing.T) {
	s := openStore(t)
	putIndexedVar(t, s, 13, "identityId", "MT-1998", true)
	commit(t, s, func(tx *state.Tx) error {
		return tx.SetVariableIndexed(13, "identityId", true)
	})
	if got := found(t, s, "identityId", "MT-1998", false); !reflect.DeepEqual(got, []uint64{13}) {
		t.Errorf("found %v, want the one entry unchanged", got)
	}
}
