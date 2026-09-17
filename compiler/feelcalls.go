package compiler

import (
	"fmt"

	"github.com/pblumer/atlas/expr"
)

// feelGate carries, through one compile, what that compile does with a FEEL call
// this build can only ever answer with null (ADR-0388).
//
// A deploy refuses such a call. A reload must not, and that is not a preference:
// it is the split ADR-0177 drew, where a rule added
// to the compiler after a definition was stored is a reason to tell the operator,
// not a reason to refuse to start.
//
// ADR-0388 put its rule inside compileFEEL, which runs while the process is still
// being built — before stage 5, and so before the point where parseNamed can take
// a refusal apart and keep the process out of it. Its refusal therefore reached
// the reload as a plain error, and a stored model with one misspelled function
// name stopped a server from booting at all, with every other definition and every
// running instance unreachable behind it (ADR-draft-a-rule-added-later-is-a-gate-on-deploy).
//
// The gate restores the split without moving the rule. The strict gate refuses
// exactly as before. The tolerant one compiles the expression the way the build
// that stored it did — the engine binds an unknown callee to null, which is what
// the definition has been doing all along — and records what it let through, so
// the reload reports it as drift in the same breath as the stage-5 rules.
type feelGate struct {
	// tolerate is the reload path. It changes nothing about how the expression
	// compiles; it decides only whether the fault stops the compile or is reported.
	tolerate bool
	// found is what a tolerant gate let through, as Problems, in compile order.
	found []Problem
}

// strictFEEL is the gate for every path that is not a reload: a deploy, a dry run,
// and any [Builder] assembled directly. A strict gate never records anything — the
// fault is an error there and the compile stops — so one shared value is safe to
// hand to all of them.
var strictFEEL = &feelGate{}

// tolerantFEEL is a fresh gate for one reload. It is not shared: what it collects
// belongs to the one definition being brought back.
func tolerantFEEL() *feelGate { return &feelGate{tolerate: true} }

// compileFEEL compiles one of a model's FEEL expressions and, on a strict gate,
// refuses it when it contains a call this build can only ever answer with null
// (ADR-0388).
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
func (g *feelGate) compileFEEL(text string) (*expr.Compiled, error) {
	if err := expr.CheckCallsError(text); err != nil {
		if !g.tolerate {
			return nil, err
		}
		// Kept, not waved through: the operator is told, by the same mechanism that
		// reports a stage-5 rule a stored model no longer passes. The expression is
		// quoted because this Problem has no element to anchor to — the call sites'
		// own wrapping is what carries the element, and there is no error here to
		// wrap (ADR-0388 accepted the same limit for the refusal it does raise).
		g.found = append(g.found, Problem{
			Severity: SeverityError,
			Rule:     RuleNullCall,
			Message:  fmt.Sprintf("expression %q: %s", text, err),
		})
	}
	return expr.CompileAuto(text)
}

// problems is what a tolerant gate collected, and nil for a strict one.
func (g *feelGate) problems() []Problem { return g.found }
