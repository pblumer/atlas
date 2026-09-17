package compiler

import "github.com/pblumer/atlas/expr"

// compileFEEL compiles one of a model's FEEL expressions and refuses it when it
// contains a call this build can only ever answer with null
// (ADR-draft-a-call-that-can-only-be-null-is-refused-at-deploy).
//
// Every expression a BPMN model carries goes through here rather than through
// expr.CompileAuto directly, so the refusal is one decision in one place instead
// of eighteen call sites each deciding for itself. The callers already wrap what
// comes back with the element and the field it came from, which is what makes the
// message findable in a model.
//
// It is deliberately *not* in expr.CompileAuto. The FEEL engine keeps invocation
// total because DMN requires a decision to stay executable, and Atlas evaluates
// DMN through that same engine — so the refusal belongs where a deploy happens,
// not where an expression is compiled.
func compileFEEL(text string) (*expr.Compiled, error) {
	if err := expr.CheckCallsError(text); err != nil {
		return nil, err
	}
	return expr.CompileAuto(text)
}
