package engine

import "strconv"

// The execution budget: how much uninterrupted work one token may drive before the
// engine stops it (ADR-draft-execution-budget).
//
// A partition has one writer (invariant I3), and every command runs on it. Nothing
// in the engine bounded how long a single instance could keep that writer: a model
// whose automatic elements form a cycle — an exclusive gateway looping back on
// itself with a condition that always holds is enough, and the compiler accepts it —
// generates a followup for every command it processes, so the queue never drains.
// RunUntilIdle means what it says, and the partition stops answering: no other
// instance advances, no timer fires, and the readiness probe cannot get a closure
// onto the loop to be answered.
//
// The budget is counted per *token* rather than per instance, because that is what
// separates a runaway from ordinary heavy work. A cycle is one token going round;
// a multi-instance activity over fifty thousand items is fifty thousand tokens each
// taking a step or two. Counting per instance would stop the second along with the
// first. A token minted by taking a sequence flow inherits its parent's count — the
// thread of control continues through a fork or a join — while a multi-instance
// iteration starts at zero, because it is a new unit of work rather than the same
// one going round again.

// DefaultExecutionBudget is how many element activations one token may drive in a
// single run before the engine parks it with an incident. Ten thousand is far above
// any plausible stretch of automatic work for one token — a token that has taken ten
// thousand sequence flows without once waiting for a job, a timer or a message is
// not making progress a person modelled — and low enough that the writer comes back
// in well under a second.
const DefaultExecutionBudget int32 = 10_000

// SetExecutionBudget sets how many steps one token may take in a single run. A value
// of zero or less restores [DefaultExecutionBudget]; there is deliberately no way to
// turn the budget off, because "off" is the behaviour this exists to remove.
func (p *Processor) SetExecutionBudget(steps int32) { p.executionBudget = steps }

// budget is the effective ceiling, defaulted.
func (p *Processor) budget() int32 {
	if p.executionBudget > 0 {
		return p.executionBudget
	}
	return DefaultExecutionBudget
}

// chargeToken counts one element activation against tokenID and reports whether the
// token has now gone past its budget. A zero token id is not charged: it belongs to
// the activation that starts an instance, before any flow has been taken, and there
// is exactly one of those per instance.
func (p *Processor) chargeToken(tokenID uint64) bool {
	if tokenID == 0 {
		return false
	}
	if p.tokenSteps == nil {
		p.tokenSteps = make(map[uint64]int32)
	}
	n := p.tokenSteps[tokenID] + 1
	p.tokenSteps[tokenID] = n
	return n > p.budget()
}

// inheritTokenSteps carries a parent token's count onto a token minted from it, so a
// cycle cannot reset its budget by passing through a fork, a join or a subprocess
// boundary — each of which mints a fresh token id for the same thread of control.
// Nothing is recorded for a parent that has not been charged, which keeps the map to
// the tokens that are actually running.
func (p *Processor) inheritTokenSteps(tokenID, parentID uint64) {
	if parentID == 0 || p.tokenSteps == nil {
		return
	}
	if n := p.tokenSteps[parentID]; n > 0 {
		p.tokenSteps[tokenID] = n
	}
}

// overBudgetMessage is what the operator reads on an element the budget stopped.
func (p *Processor) overBudgetMessage() string {
	n := strconv.Itoa(int(p.budget()))
	return "this token took " + n + " steps in one run without ever waiting, which is the execution budget; " +
		"the model most likely has a cycle of automatic elements. Resolve to allow " + n + " more"
}
