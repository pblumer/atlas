# ADR-0276: A loop's size is checked before it is built

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine maintainers

## Context and problem statement

A multi-instance activity's iteration count comes from the model or, more often,
from an instance variable. The engine turned that number into work like this:

```go
if n, ok := expr.AsInt(v); ok && n >= 0 {
    return nullList(n)   // n FEEL nulls, allocated in one call
}
```

There is no check between the number and the allocation. A variable holding a
billion is a billion `expr.Value`s asked for at once, on the processor goroutine —
so one instance's bad data takes the whole partition down, and it does so before
anything has a chance to say why. An external review named the line.

The collection form has the same shape one step later: the list itself already
exists (it came from a variable), but seeding an element instance, a loop counter
and an item variable *per element* does not, and nothing bounded that either.

Atlas already limits this kind of thing where the input arrives over HTTP —
`io.LimitReader` on every request body, a decompression cap on restore. What it did
not do was limit work sized by a *model* or by a *variable*, which is the one input
an operator cannot put a proxy in front of.

## Decision drivers

- **The limit must precede the allocation.** Checking a slice's length after
  building it is not a limit, it is a report.
- **One instance's bad data must not stop a partition (I3).**
- **A refusal must be visible and repairable**, like every other way a token gets
  stuck: an incident on the element, resolvable after the data is fixed.
- **A legitimate large loop must still be possible**, by configuration rather than
  by editing the engine.
- **This is a different limit from the execution budget.** That one bounds a
  *rate* — how far one token travels in a run. A hundred thousand iterations are a
  hundred thousand tokens taking one step each, which it is deliberately built not
  to stop.

## Considered options

1. Cap the count before the allocation and refuse with an incident. (chosen)
2. Truncate silently to the cap and run the first N iterations.
3. Validate the cardinality expression at deploy time.
4. Leave it; a runaway allocation is an operational problem.

## Decision outcome

Chosen option: **option 1.** `multiInstanceItems` takes the ceiling
(`DefaultMaxIterations`, 100,000, settable with `SetMaxIterations`) and reports the
count it was asked for instead of building the list. `seedMultiInstance` then parks
the body: no iteration is seeded, the body stays activated holding its token, and an
incident names the number asked for and the limit. `model.IncidentReason` gains
`IncidentTooManyIterations`, so resolving re-runs the behavior — which re-evaluates
the count against current variables. Fix the data, or raise the budget, and the loop
runs; leave it, and it parks again. That is the same "resolve is a genuine retry"
discipline the gateway and the execution budget follow.

The limit itself is allowed: a budget that refused the number it names would be a
budget of one less. There is a test for limit−1, limit and limit+1.

Option 2 was rejected outright — a loop that silently runs 100,000 of the million
iterations a model asked for produces a wrong answer and reports success, which is
worse than an incident by any measure. Option 3 was rejected because the dangerous
count is almost never a constant: it is `=items` or `=count(orders)`, and its value
exists only at runtime. Option 4 is what the code did.

### Consequences

- **Positive:** a variable-driven loop can no longer take the partition down, and
  what it asked for is written on the incident, so the operator sees the number
  rather than a memory profile.
- **Positive:** the refusal is repairable in place, so the work already done in the
  instance is not lost to a cancel.
- **Negative / trade-offs accepted:** a legitimate loop over more than 100,000
  items now needs `SetMaxIterations`. That is the intended shape — a number an
  operator chooses beats a number nobody chose — but it is a behaviour change for a
  model that was quietly running such a loop.
- **Negative:** the budget is not surfaced in installation settings; it is a
  processor setter, like the execution budget. Wiring both through is one follow-up,
  not two.
- **Negative:** the sequential path re-evaluates the collection on each iteration and
  deliberately ignores a fresh over-budget answer there. A collection that grows
  mid-loop yields an empty list and ends the loop, which is what a shrinking one
  already did.
- **Follow-ups / risks to watch:** the review asks for *unified*, configurable
  budgets — response bytes, script output, variable size, active work — and this is
  one of them. The HTTP-facing ones already have limits; what is missing is one place
  that names them all and one way to configure them. That unification is not done.

## Pros and cons of the options

### Option 1 — check before allocating, refuse with an incident (chosen)
- Good: bounded before the damage; visible; repairable; configurable.
- Bad: a behaviour change for a model that was running an enormous loop, and one
  more number with a default.

### Option 2 — truncate to the cap
- Good: nothing ever fails.
- Bad: a wrong answer reported as success.

### Option 3 — validate at deploy
- Good: the modeller learns earliest.
- Bad: the dangerous count is a variable, and its value does not exist yet.

### Option 4 — leave it
- Good: no change.
- Bad: one instance's data stops the partition, with no incident and no message.

## Links

- relates to [ADR-0077](0077-multi-instance-activities.md) (the loop whose size this
  bounds)
- relates to [ADR-0133](0133-standard-loop-activities.md) (the runaway *loop*
  ceiling, which bounds repetitions rather than width)
- relates to [`draft-execution-budget`](0272-execution-budget.md), the rate limit
  this size limit does not replace
- relates to [ADR-0061](0061-incident-model.md) (the incident and its resolve path)
- reported as F16 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
