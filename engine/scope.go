package engine

import "github.com/pblumer/atlas/model"

// maxScopeDepth bounds a scope-chain walk, a defensive guard against a cyclic or
// corrupt FlowScopeKey chain. Real nesting (subprocesses, activity-local scopes)
// is far shallower.
const maxScopeDepth = 64

// resolveInChain resolves name by walking from startScope up the parent scope
// chain, nearest scope wins (a local variable shadows an inherited one — Camunda's
// rule, ADR-0068). getVar reads a scope's variable (nil if absent); parentOf
// returns a scope's parent scope and whether it has one (false at the root). With
// no local scopes — the state today — startScope is the process instance, parentOf
// is immediately false, and this degenerates to a single read.
//
// It is the pure core of scope resolution so the walk is unit-testable without an
// engine; [ProcessingContext.ResolveVariable] wires it to live element/variable state.
func resolveInChain(startScope uint64, name string,
	getVar func(scope uint64, name string) *model.VariableValue,
	parentOf func(scope uint64) (uint64, bool)) *model.VariableValue {
	scope := startScope
	for depth := 0; depth <= maxScopeDepth; depth++ {
		if vv := getVar(scope, name); vv != nil {
			return vv
		}
		parent, ok := parentOf(scope)
		if !ok || parent == scope {
			return nil
		}
		scope = parent
	}
	return nil // chain longer than maxScopeDepth: treat as unresolved
}

// ResolveVariable reads name resolving up the scope chain from startScope (nearest
// scope wins), the lookup activity-local scopes and Camunda-style I/O mappings need
// (ADR-0068). A scope's parent is its element instance's FlowScopeKey; the root
// process-instance scope has no element instance, which ends the walk. Reads go
// through the in-flight transaction, so they see this batch's writes.
func (c *ProcessingContext) ResolveVariable(startScope uint64, name string) *model.VariableValue {
	return resolveInChain(startScope, name, c.GetVariable, func(scope uint64) (uint64, bool) {
		ei := c.GetElementInstance(scope)
		if ei == nil {
			return 0, false
		}
		return ei.FlowScopeKey, ei.FlowScopeKey != 0 && ei.FlowScopeKey != scope
	})
}

// definingScopeInChain finds the nearest scope from startScope up the parent chain that
// already holds a variable called name — the scope a write to that name updates under
// Zeebe-style propagation. has reports whether a scope holds the name; parentOf is as for
// resolveInChain. ok is false when no scope on the chain holds it (or the chain is longer
// than maxScopeDepth), and the caller then writes to the instance root.
//
// It is the pure core of payload propagation so the walk is unit-testable without an
// engine; [ProcessingContext.propagationScope] wires it to live state
// (ADR-draft-scope-aware-messaging-and-compensation).
func definingScopeInChain(startScope uint64, name string,
	has func(scope uint64, name string) bool,
	parentOf func(scope uint64) (uint64, bool)) (scope uint64, ok bool) {
	scope = startScope
	for depth := 0; depth <= maxScopeDepth; depth++ {
		if has(scope, name) {
			return scope, true
		}
		parent, more := parentOf(scope)
		if !more || parent == scope {
			return 0, false
		}
		scope = parent
	}
	return 0, false
}

// propagationScope is where a delivered payload variable called name lands when its
// receiver has no output mappings: the nearest scope from startScope up the chain that
// already declares it, otherwise root (the process instance). A subprocess or
// event-subprocess run that declared a variable locally — an input mapping to null, say —
// so receives the value it waits for, instead of having it written to the root behind its
// own local (ADR-draft-scope-aware-messaging-and-compensation).
func (c *ProcessingContext) propagationScope(startScope, root uint64, name string) uint64 {
	s, ok := definingScopeInChain(startScope, name,
		func(scope uint64, n string) bool { return c.GetVariable(scope, n) != nil },
		func(scope uint64) (uint64, bool) {
			ei := c.GetElementInstance(scope)
			if ei == nil {
				return 0, false
			}
			return ei.FlowScopeKey, ei.FlowScopeKey != 0 && ei.FlowScopeKey != scope
		})
	if !ok {
		return root
	}
	return s
}
