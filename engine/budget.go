package engine

import "strconv"

// The execution budget: how much uninterrupted work one token may drive before the
// engine stops it (ADR-0272).
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

// DefaultMaxIterations is how many iterations one multi-instance activity may ask
// for before the engine refuses it with an incident.
//
// It is a *size* budget where DefaultExecutionBudget is a *rate* one, and neither
// substitutes for the other: a hundred thousand iterations are a hundred thousand
// tokens taking one step each, which the execution budget is deliberately built not
// to stop. What makes them dangerous is that the count comes from the model or from
// an instance variable, and the engine allocated from it before looking — a
// variable holding a billion is a billion FEEL nulls, asked for in one call
// (ADR-0276).
//
// A hundred thousand is far above what a modelled loop plausibly wants and far
// below the point where the allocation is the problem.
const DefaultMaxIterations = 100_000

// SetMaxIterations sets how many iterations one multi-instance activity may ask
// for. Zero or less restores [DefaultMaxIterations].
func (p *Processor) SetMaxIterations(n int) { p.maxIterations = n }

// iterationCeiling is the effective limit, defaulted.
func (p *Processor) iterationCeiling() int {
	if p.maxIterations > 0 {
		return p.maxIterations
	}
	return DefaultMaxIterations
}

// tooManyIterationsMessage is what the operator reads on a loop the budget refused.
func (p *Processor) tooManyIterationsMessage(asked int) string {
	return "this multi-instance activity asked for " + strconv.Itoa(asked) +
		" iterations; the limit is " + strconv.Itoa(p.iterationCeiling()) +
		". Check the collection or cardinality it reads, then resolve to try again"
}

// The size budgets: how large a single variable's value, and a multi-instance
// activity's assembled output collection, may be
// (ADR-draft-a-variable-is-a-record).
//
// They are two numbers rather than one because they bound different things. A
// variable is a business record — a customer, an order, the inputs of a decision —
// and a megabyte is already generous for one. An output collection is what a
// legitimate loop accumulates at the iteration ceiling, which is a different order
// of magnitude. One number could not serve both: it would either be so large that it
// is no ceiling for a record, or so small that an ordinary loop cannot finish.
//
// Neither is a bound on what a loop *writes*. The collection is re-serialised once
// per iteration, so the bytes written grow with the square of the iteration count,
// and a budget small enough to make that safe would be smaller than one record. That
// is a defect with its own fix, and hiding it inside one of these numbers would only
// make it harder to find.

// DefaultMaxVariable is how large one variable's value may be. A megabyte holds a
// business record with room to spare; past that it is a document, and a document in a
// token's scope is rewritten into the log on every touch.
const DefaultMaxVariable int64 = 1 << 20

// DefaultMaxCollection is how large a multi-instance activity's output collection may
// be. It has to clear a legitimate loop at the iteration ceiling — a hundred thousand
// modest results — while staying far below what costs the host its memory.
const DefaultMaxCollection int64 = 16 << 20

// SetMaxVariable sets how large one variable's value may be. Zero or less restores
// [DefaultMaxVariable]; as with every budget here there is no way to turn it off.
func (p *Processor) SetMaxVariable(n int64) { p.maxVariable = n }

// SetMaxCollection sets how large an output collection may be. Zero or less restores
// [DefaultMaxCollection].
func (p *Processor) SetMaxCollection(n int64) { p.maxCollection = n }

// variableCeiling is the effective limit for one value, defaulted.
func (p *Processor) variableCeiling() int64 {
	if p.maxVariable > 0 {
		return p.maxVariable
	}
	return DefaultMaxVariable
}

// collectionCeiling is the effective limit for an output collection, defaulted.
func (p *Processor) collectionCeiling() int64 {
	if p.maxCollection > 0 {
		return p.maxCollection
	}
	return DefaultMaxCollection
}

// tooLargeVariableMessage is what the operator reads on an element whose write was
// refused. It names the variable, because an instance has many and only one of them
// is the reason this element is parked.
func (p *Processor) tooLargeVariableMessage(name string, size, ceiling int64) string {
	return "the value written to \"" + name + "\" is " + strconv.FormatInt(size, 10) +
		" bytes, and the limit is " + strconv.FormatInt(ceiling, 10) +
		". Check what produced it, then resolve to write it again"
}
