package scim_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/scim"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A store read that *fails* is not the same as an element instance that is *gone*,
// and the two branches sit next to each other in every handler — which is exactly how
// they get swapped. Gone is a no-op: the activity already completed and there is
// nothing to do. Failed must fail the job, so it is retried and then raised as an
// incident (ADR-0061); treating it as gone would silently drop the work.
func TestHandlerFailsWhenTheStoreDoes(t *testing.T) {
	boom := errors.New("read view is gone")
	lookup := func(uint64) *compiler.CompiledProcess { return nil }

	store := state.Reader(unreadableStore{err: boom})
	if _, err := scim.Handler(store, lookup, nil, nil)(job.Job{ElementInstanceKey: 1}); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the store's own failure rather than a silent no-op", err)
	}

	// And the neighbouring branch, to prove they are not the same one: gone completes
	// with no variables and no error.
	store = state.Reader(unreadableStore{})
	if vars, err := scim.Handler(store, lookup, nil, nil)(job.Job{ElementInstanceKey: 1}); err != nil || vars != nil {
		t.Errorf("gone element instance: vars=%v err=%v, want no variables and no error", vars, err)
	}
}

// unreadableStore answers every element-instance read the same way: with err when it
// has one, and "not found" otherwise.
type unreadableStore struct{ err error }

func (s unreadableStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, s.err
}

func (unreadableStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error { return nil }
