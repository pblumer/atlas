# ADR-DRAFT: A variable is a record, a collection is a loop's harvest — two budgets

- **Status:** Draft
- **Date:** 2026-09-09
- **Deciders:** Atlas engine team

## Context and problem statement

F16 of the 2026-09-07 audit listed *variable size* among the budgets Atlas was
missing. ADR-0291 gathered every other ceiling into one place and left
this one out on purpose, because it needed a decision that change did not make: what
happens when a write is refused.

There was no ceiling at all. A value reached the variable store, and from there the
write-ahead log, at whatever size it arrived. That was not quite unbounded — every
*entry point* is bounded, an HTTP body by its own budget, a script's output since
ADR-0291, a connector's answer likewise — but "bounded because nothing upstream is
large" is a property of today's call graph, not a rule.

Two things made it worth doing properly rather than adding a number.

**An oversized variable was a hard failure, not a resumable one.** Above 64 MiB the
value failed against the log's per-record cap, and that aborts the batch: the
instance stopped with an error, and there was nothing to resolve. The budget's real
job is not to make the failure happen — it already happened — but to make it happen
somewhere an operator can act.

**The dangerous size is a product, and the iteration budget bounds only one factor.**
A multi-instance activity's output collection is assembled into one JSON value.
ADR-0276 bounds the iteration *count* at a hundred thousand; nothing bounded what each
iteration contributes. A hundred thousand results of a megabyte each is a hundred
gigabytes in one variable.

Worse, and verified while writing this: `finishMultiInstanceIteration` calls
`setListElement` once per finished iteration, which reads the whole collection, sets
one element, and writes the whole collection back. For N iterations the engine parses
and re-serialises the collection N times and makes every intermediate version
durable. The bytes written grow with **the square of the iteration count**.

## Decision drivers

- A refusal must be resumable. Every other budget in the engine parks an element with
  an incident and retries on resolve (ADR-0272, ADR-0276); this one must read the same.
- A refusal must not be silent, and must not leave the caller believing it wrote.
- The numbers must not break models that work today.

## Considered options

1. Two budgets — one for a variable's value, one for an output collection — each
   enforced where the value is produced, refusing with an incident. (chosen)
2. One budget for both.
3. One budget enforced only in `AppendVariableEvent`, the funnel every write passes.
4. Leave it: every entry point is bounded already.

## Decision outcome

Chosen option: **option 1**, two budgets.

**Why two and not one.** They answer different questions. `Variable` asks what one
business record may weigh — a customer, an order, the inputs of a decision — and a
megabyte is generous for that. `Collection` asks what a legitimate loop at the
iteration ceiling may accumulate, which is a different order of magnitude: a hundred
thousand modest results is tens of megabytes. One number cannot serve both. Set it at
the record's scale and ordinary loops stop finishing; set it at the collection's scale
and it is no ceiling for a record at all — which is the state this replaces. So
`Variable` is 1 MiB and `Collection` is 16 MiB, and the collection is measured against
its own budget everywhere it is written, including when it is promoted out of the
loop's scope: it fitted at the body, and refusing it one scope up would park a loop
for having finished.

**What `Collection` does not do.** It bounds the peak held in memory. It is *not* a
bound on what a loop writes, because of the quadratic re-serialisation above: to keep
the bytes written under ten gigabytes at the iteration ceiling, a collection would
have to stay under about 200 KB — smaller than one variable is allowed to be. A budget
that made the write amplification safe would be too small to be useful. The
amplification is a separate defect with a separate fix (write the element, not the
list), and putting it inside one of these numbers would only make it harder to find.
It is named here so the next person does not have to rediscover it.

**Why not the funnel alone (option 3).** `AppendVariableEvent` sees every write, and
that is where the *check* lives. But it cannot be the whole answer, because it cannot
fail: it returns, and its caller carries on. The first working version of this change
did exactly that, and the probe that caught it is worth recording — an oversized
collection raised its incident, the loop kept going, the instance completed, and the
completion took the incident with it. Nothing was written, nothing was reported, and
the run looked successful. So the funnel reports its verdict and the sites that
produce a model's or a worker's value act on it: the loop body stays activated rather
than seeding iterations whose results have nowhere to land, and an iteration whose
result will not fit stays where it is rather than finishing.

**What is refused and what never is.** A delete is never refused. It carries no value,
and refusing to shrink an instance would be the budget working backwards — the one
write that makes the problem smaller must always get through.

### Consequences

- **Positive:** an oversized variable is now an incident on the element that produced
  it, naming the variable and both sizes, instead of an aborted batch nobody can
  resolve.
- **Positive:** the collection has a ceiling that reflects what a loop legitimately
  does, rather than inheriting one sized for a single record.
- **Positive:** the write amplification is written down. It was found by reading the
  code for this decision and would otherwise still be waiting.
- **Negative / trade-offs accepted:** a model that today writes a variable between 1
  MiB and 64 MiB will start parking. That is a behaviour change, and in the direction
  of less progress — the same trade ADR-0291 made, for the same reason: the ceiling is
  the correction, and it will look like a regression to whoever was living above it.
- **Negative:** the check is on the serialised text, not before it. The elements are
  already in memory by then, so what this bounds is the canonical copy and everything
  downstream — the event, the log record, the state write. It is not "before the
  allocation" the way ADR-0276's is, and saying otherwise would be a nicer story than
  the truth.
- **Negative:** `AppendVariableEvent` now returns a value most of its callers ignore.
  They write engine-derived values — a loop index, a counter — that cannot exceed a
  budget sized for a record, but nothing forces a future caller to think about it.
- **Negative, and stated as a limitation rather than a follow-up:** three write sites
  refuse the value but do not yet stop what happens next — a message payload, a call
  activity's result, an io-mapping. Terminating an element clears the incident it
  carries (`engine/apply.go`), so if such an element completes, the report goes with
  it and the only remaining evidence is the missing variable. The value is still not
  written, and nothing oversized becomes durable; what is missing is the *visibility*
  of the refusal on those paths. The loop and a worker's job result have their answer
  — the body stays activated, the task stays parked — and the other three need the
  same, one at a time, each with its own view of what "and then what" means.
- **Follow-ups / risks to watch:** the quadratic re-serialisation above, and those
  three sites.

## Pros and cons of the options

### Option 1 — two budgets, refused where produced (chosen)
- Good: each number answers one question; the refusal is resumable and visible.
- Bad: two knobs instead of one, and a per-site decision at every producing write.

### Option 2 — one budget
- Good: one number to explain.
- Bad: it is either no ceiling for a record or a ceiling ordinary loops cannot clear.

### Option 3 — the funnel only
- Good: one place, every write.
- Bad: the funnel cannot fail, so the caller carries on and the incident dies with the
  instance. Measured, not assumed.

### Option 4 — leave it
- Good: nothing changes.
- Bad: "bounded because nothing upstream is large" is a property of today's call
  graph, and the 64 MiB failure it ends in is not resolvable.

## Links

- completes F16 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md),
  the one budget its list named that ADR-0291 left out
- extends [ADR-0291](0291-one-place-for-budgets.md), which gathered the budgets and
  named this gap
- follows [ADR-0276](0276-iteration-budget.md) and
  [ADR-0272](0272-execution-budget.md) in shape: check, park with an incident, retry
  on resolve
- bounds a value written under [ADR-0077](0077-multi-instance-activities.md)'s output
  collection
