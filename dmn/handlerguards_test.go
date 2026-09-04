package dmn

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// DecisionHandler is now the handler for both a local decision and a central one
// leased by a worker (ADR-0233's temis slice), so the refusals it opens with decide
// two paths rather than one. These pin them.

// unreadableStore answers every element-instance read with err when it has one, and
// "not found" otherwise.
type unreadableStore struct{ err error }

func (s unreadableStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, s.err
}

func (unreadableStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error { return nil }

// A store read that *fails* is not the same as an element instance that is *gone*,
// and the two branches sit next to each other — which is how they get swapped. Gone
// is a no-op: the activity already completed. Failed must fail the job, so it is
// retried and then raised as an incident (ADR-0061); treating it as gone would
// silently drop a decision.
func TestDecisionHandlerFailsWhenTheStoreDoesAndNoOpsWhenTheInstanceIsGone(t *testing.T) {
	boom := errors.New("read view is gone")
	noLookup := func(uint64) *compiler.CompiledProcess { return nil }

	h := DecisionHandler(unreadableStore{err: boom}, noLookup, nil, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the store's own failure rather than a silent no-op", err)
	}

	h = DecisionHandler(unreadableStore{}, noLookup, nil, nil)
	got, err := h(job.Job{ElementInstanceKey: 1})
	if err != nil {
		t.Errorf("gone element instance: err = %v, want none", err)
	}
	if got.Decision != nil || len(got.Outputs) != 0 {
		t.Errorf("gone element instance: completion = %+v, want an empty one", got)
	}
}

// foundStore reports one element instance, so a test can reach past the first guard.
type foundStore struct{ ei *model.ElementInstanceValue }

func (s foundStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return s.ei, true, nil
}

func (foundStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error { return nil }

// A job whose process definition is not deployed here fails naming the definition,
// rather than dereferencing a nil compiled process on the run loop. It is reachable
// on both paths: a worker leasing a decision for a definition this engine no longer
// holds gets the same answer an in-engine evaluation would.
func TestDecisionHandlerRefusesAJobWithNoCompiledProcess(t *testing.T) {
	store := foundStore{ei: &model.ElementInstanceValue{ProcessDefKey: 42}}
	h := DecisionHandler(store, func(uint64) *compiler.CompiledProcess { return nil }, nil, nil)

	_, err := h(job.Job{ElementInstanceKey: 1})
	if err == nil {
		t.Fatal("a job for an undeployed definition was accepted")
	}
	if want := "no compiled process"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to say %q and name the definition", err, want)
	}
}

// bindingStore reports an element instance pointing at a real business rule task, so
// a test can reach the binding step.
type bindingStore struct{ ei *model.ElementInstanceValue }

func (s bindingStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return s.ei, true, nil
}

func (bindingStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error { return nil }

// Binding is where a central decision finds its service and a local one finds its
// model, and a binding that fails must leave the job pending. It is the step that
// tells an operator "this decision has nowhere to go" — completing the job instead
// would route the process on a decision that was never made.
func TestDecisionHandlerFailsWhenBindingDoes(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask("Risk", nil, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, rule)
	b.Connect(rule, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	boom := errors.New("no decision service for this task")
	h := DecisionHandler(
		bindingStore{ei: &model.ElementInstanceValue{ElementId: rule}},
		func(uint64) *compiler.CompiledProcess { return cp },
		func(*compiler.CompiledProcess, *compiler.BusinessRuleTaskDetail) (Evaluator, error) {
			return nil, boom
		}, nil)

	if _, err := h(job.Job{ElementInstanceKey: 1}); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the binding's own failure", err)
	}
}
