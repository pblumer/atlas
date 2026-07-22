package dmn_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ruleProcessNode builds Start → BusinessRuleTask → End and returns the compiled
// process together with the business-rule task's element id, which a stored
// element instance must sit on for the handler to resolve its decision.
func ruleProcessNode(t *testing.T, decisionId string, inputs map[string]any) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(dishDefKey, "dinner", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask(decisionId, inputs, 3)
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
	return cp, rule
}

// openStore opens a throwaway state store for handler-level tests.
func openStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// putElementInstance commits an element instance sitting on the given node.
func putElementInstance(t *testing.T, store *state.Store, key, defKey uint64, node int32) {
	t.Helper()
	tx := store.NewTransaction()
	if err := tx.PutElementInstance(key, &model.ElementInstanceValue{
		ProcessInstanceKey: 1,
		ProcessDefKey:      defKey,
		ElementId:          node,
	}); err != nil {
		t.Fatalf("PutElementInstance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close tx: %v", err)
	}
}

// TestHandlerMissingElementInstance covers the "element instance gone" path: a
// job whose element instance no longer exists is a no-op, not an error.
func TestHandlerMissingElementInstance(t *testing.T) {
	store := openStore(t)
	h := dmn.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, dmn.NewRegistry(), nil)
	if err := h(job.Job{Key: 5, ElementInstanceKey: 999}); err != nil {
		t.Fatalf("handler for missing element instance = %v, want nil", err)
	}
}

// TestHandlerNoCompiledProcess covers the lookup-miss path: an element instance
// exists but its process definition is not resolvable.
func TestHandlerNoCompiledProcess(t *testing.T) {
	store := openStore(t)
	cp, node := ruleProcessNode(t, "Dish", map[string]any{"Season": "Winter"})
	putElementInstance(t, store, 10, cp.Key, node)

	h := dmn.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, dmn.NewRegistry(), nil)
	if err := h(job.Job{Key: 5, ElementInstanceKey: 10}); err == nil {
		t.Fatal("handler with no compiled process: got nil error, want an error")
	}
}

// TestHandlerEvaluateError covers the evaluation-failure path: the decision's
// model was never deployed to the registry, so Evaluate errors and the handler
// leaves the job pending. It also exercises the empty-inputs decode (nil inputs
// recorded on the task).
func TestHandlerEvaluateError(t *testing.T) {
	store := openStore(t)
	cp, node := ruleProcessNode(t, "Dish", nil) // no static inputs
	putElementInstance(t, store, 20, cp.Key, node)

	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	// Fresh registry: no model deployed under cp.Key, so Evaluate must error.
	h := dmn.Handler(store, lookup, dmn.NewRegistry(), nil)
	if err := h(job.Job{Key: 5, ElementInstanceKey: 20}); err == nil {
		t.Fatal("handler with undeployed decision: got nil error, want an error")
	}
}
