package state

import (
	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// Reader is the read surface a job handler needs: the element instance its job
// sits on, and the variables in scope there.
//
// It exists so a handler can be given a *consistent* view instead of the live
// store. An in-process handler used to run on the single-writer goroutine, where
// its several reads could not interleave with a write; moving it off that
// goroutine (ADR-0149 option 3, ADR-0157 step 6) takes that guarantee away, and a
// [ReadView] gives it back. Both *Store and *ReadView satisfy it, so a handler
// neither knows nor needs to know which it holds.
type Reader interface {
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
	VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
}

// ReadView is a consistent read-only view of the store as of the moment it was
// taken. Later writes are invisible to it, so a handler reading an element
// instance and then its variables sees one coherent state rather than two halves
// of different ones.
//
// It holds resources in the store's engine, so it must be closed. Take one on the
// run loop (the store's owner), use it off the loop, close it when the work is
// done — the same lifetime as the job it was taken for.
type ReadView struct {
	snap *pebble.Snapshot
}

// ReadView takes a consistent read-only view. The caller owns it and must Close it.
//
// This is deliberately not called Snapshot: [Store.Snapshot] already means the
// on-disk backup checkpoint (ADR-0107), and conflating a durable copy of the whole
// store with an in-memory read view would be a genuinely dangerous ambiguity.
func (s *Store) ReadView() *ReadView { return &ReadView{snap: s.db.NewSnapshot()} }

// Close releases the view. A view left open holds back the compaction of
// everything written since it was taken, so it must not outlive the work it was
// taken for.
func (v *ReadView) Close() error { return v.snap.Close() }

// GetElementInstance implements [Reader] against the view.
func (v *ReadView) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	return decodeElementInstance(getCopy(v.snap, keyElementInstance(key)))
}

// VariablesOfScope implements [Reader] against the view.
func (v *ReadView) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	return scanPrefixWith(v.snap, variablePrefix(scope), func(_, raw []byte) error {
		return decodeVariable(raw, fn)
	})
}

// maxScopeDepth bounds a scope-chain walk, a defensive guard against a cyclic or
// corrupt FlowScopeKey chain. Real nesting (activity-local scopes over subprocess
// scopes over the process scope) is far shallower; it mirrors the engine's own
// guard (engine/scope.go).
const maxScopeDepth = 64

// VisibleVariables calls fn with every variable *visible* at an element instance:
// its own activity-local scope first (the zeebe:ioMapping inputs written on
// activation), then each enclosing scope up to the process root, with the nearest
// scope winning when a name is shadowed (ADR-0068). It is the read a job handler
// wants — the engine binds an activity's own FEEL exactly this way
// (bindInputsChain) — and reading a single scope instead is how a worker comes to
// ignore its task's input mappings.
//
// The chain is walked via each scope's element instance's FlowScopeKey; the
// process-instance root has no element instance, which ends it. An activity with no
// mappings has an empty local scope, so this degenerates to reading the enclosing
// scope, exactly as a single-scope read did.
//
// It takes a [Reader] rather than being a method so every worker shares
// one implementation of the walk — they used to grow a copy each.
func VisibleVariables(r Reader, elementInstanceKey uint64, fn func(v *model.VariableValue) error) error {
	seen := map[string]bool{}
	scope := elementInstanceKey
	for depth := 0; depth <= maxScopeDepth; depth++ {
		if err := r.VariablesOfScope(scope, func(v *model.VariableValue) error {
			if seen[v.Name] {
				return nil // a nearer scope already provided this name (shadowing)
			}
			seen[v.Name] = true
			return fn(v)
		}); err != nil {
			return err
		}
		ei, ok, err := r.GetElementInstance(scope)
		if err != nil {
			return err
		}
		if !ok || ei.FlowScopeKey == 0 || ei.FlowScopeKey == scope {
			return nil // reached the process-instance root, or a chain that ends here
		}
		scope = ei.FlowScopeKey
	}
	return nil
}

// VisibleVariablesMap is [VisibleVariables] collected into a map keyed by name — the
// shape a worker binds FEEL against, so it resolves the names an expression reads
// without a per-name store lookup.
func VisibleVariablesMap(r Reader, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	if err := VisibleVariables(r, elementInstanceKey, func(v *model.VariableValue) error {
		vars[v.Name] = *v
		return nil
	}); err != nil {
		return nil, err
	}
	return vars, nil
}

// LocalVariablesMap reads one scope's own variables into a map keyed by name,
// inheriting nothing. It is what an outbound body wants when its task carries input
// mappings: at activation the activity-local scope holds exactly what those mappings
// wrote (a job's result is written there only on completion), so the model states
// what leaves the process rather than spilling every variable it can see
// (ADR-0174).
func LocalVariablesMap(r Reader, scope uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	if err := r.VariablesOfScope(scope, func(v *model.VariableValue) error {
		vars[v.Name] = *v
		return nil
	}); err != nil {
		return nil, err
	}
	return vars, nil
}
