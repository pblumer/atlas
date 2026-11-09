package api

import "github.com/pblumer/atlas/compiler"

// What a scope in an instance's replay is (variableView.ScopeKind). The process scope
// carries none.
const (
	scopeKindSubprocess        = "subprocess"
	scopeKindMultiInstanceBody = "multiInstanceBody"
	scopeKindLoopBody          = "loopBody"
	// scopeKindLocal is an element instance's own scope: what it was handed (its
	// inputs) and what it wrote there itself, such as a multi-instance round's result.
	scopeKindLocal = "local"
)

// What a constant's variable holds (variableView.Constant, ADR-0454/0456).
const (
	constantKindProcess = "process"
)

// constantKind says whether a process-scope variable is one the model's constants
// occupy, read through the definition in force where the variable was written: a
// namespace the installation fills names its source, a process constant's own variable
// is "process", and anything else — or a definition that is gone — is "".
func constantKind(cp *compiler.CompiledProcess, name string) string {
	if cp == nil || !cp.HasConstants() || !cp.IsConstant(name) {
		return ""
	}
	switch name {
	case compiler.ApplicationNamespace, compiler.ServerNamespace:
		if cp.Binds(name) {
			return name
		}
	}
	return constantKindProcess
}
