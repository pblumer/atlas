# ADR-DRAFT: Measuring a capability

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0305](0305-business-capabilities-and-value-streams.md) gave a business capability
the KPIs and SLAs it is held to, and every one of them was a *declaration*. Nothing
computed one, the coverage answer said so in a field, and the record carried an open
question:

> whether that is computable at the instance volumes this is aimed at *without* the
> OpenSearch exporter ([ADR-0114](0114-opensearch-event-exporter.md)), which not every
> installation runs

with the instruction that it be answered by measurement rather than by argument.

The data was never the obstacle. Atlas records a visit and a termination count per
element ([ADR-0080](0080-runtime-aggregate-counters.md)), each instance's start and end
on its own record, and the order of elements within an instance in the step trail
([ADR-0046](0046-single-process-step-replay.md)). The question was only ever what
reading it costs.

## Decision drivers

- **The answer had to be measured.** The record said so, and a design that argued its
  way to "this will be fine" would have been the thing it was written to prevent.
- **A read that grows with the instance population must not hold the run loop**
  ([ADR-0239](0239-off-loop-queries.md)). At ~11k active instances a walk inside the
  loop stopped the engine.
- **A declared figure Atlas cannot compute must stay visible as a declaration.** The
  register's whole value is that somebody can write down what a capability is held to
  before anything can check it.
- **A number nobody authored is worse than no number.** Whatever this computes must be
  traceable to what was declared and what was recorded.

## What the measurement found

`benchmarks/measurement_test.go`, captured in
[`measurement-381825f.md`](../../benchmarks/results/measurement-381825f.md). Each op is
one whole reading over the population; unwindowed, because a window is the same scan
stopped earlier and the unbounded walk is the upper bound on any window.

| Instances | Outcome distribution | Cancellation rate | Cycle time | Per-phase duration |
|---:|---:|---:|---:|---:|
| 100 | 14.0 µs | 1.1 µs | 59.7 µs | 171 µs |
| 10 000 | 12.6 µs | 12.7 µs | 96 ms | 184 ms |
| 100 000 | 29.2 µs | — | 1.24 s | 2.86 s |

The counters are flat. A thousandfold population leaves them in microseconds — at
10 000 instances the outcome distribution is *faster* than at 1 000, which is the LSM
tree's file layout varying rather than the population. ADR-0080 claimed O(elements);
this is that claim measured rather than trusted.

The two instance readings are linear once past warm-up: 12.9× and 15.5× for a 10×
population between 10 000 and 100 000, with allocation counts linear throughout.

**A second axis was added because the first answered an easier question.** The
population runs used a three-element process, the shortest the harness builds and the
most flattering case per-phase duration will ever get, since it reads a trail with one
entry per element while cycle time reads two fields regardless. At a fixed 10 000
instances over processes of 1, 10 and 30 tasks, a thirtyfold longer process costs
per-phase duration 1.4× more and its ratio to its own control *falls*, from 2.4× to
1.9×. Within an instance the cost is the seek to the prefix, not the walk under it.

## Decision outcome

**The open question is answered yes, with one condition: the two instance readings
carry a required window.** The exporter is an optimisation for unbounded historical
analysis, not a prerequisite for measuring a capability.

`GET /api/v1/capabilities/{key}/measurement?windowDays=N` returns, per realising
process, the outcome distribution and cancellation counts, the cycle time over the
window, and each declared SLA's attainment.

### The window is required, and that is the finding rather than a preference

An unwindowed reading over 100 000 finished instances cost 1.24 seconds, and per-phase
duration 2.86. An endpoint offering a number like that with no bound is offering a way
to make somebody wait without telling them why. `windowDays` is required, at most 400 —
generous enough for an annual SLA, small enough that an unbounded reading cannot be
asked for by accident.

It is a number of days rather than a from/to pair. Every question this answers has the
shape "over the last N days", and a pair invites two failure modes a single number does
not have: reversed ends, and a window whose age drifts as the clock moves while the
caller believes it is fixed.

### Counted and walked figures are kept apart, in the response itself

The counters are all-time — a counter holds a total, not a series — and the instance
readings are windowed. Both are integers on a screen, so a response that presented them
as one kind would be wrong in a way no reader could see. Every response carries a
`countedBasis` and a `walkedBasis` sentence, so a client can label its own axes
honestly without having read this record.

### Per-phase duration is not dropped — and it was the candidate

It went into the measurement as the thing to omit if it did not carry, on the
reasoning that its cost scales with process length. The length axis refuted that: the
ratio is a constant near 2×, falling slightly as processes grow. It is the same shape
as cycle time with a larger constant, not a different class of cost.

It is not in this slice's *response* either, and that is a separate reason: a phase is
a span between two points a reader names, and the register has no field naming them.
Offering the duration between two elements a caller passes as ids would be a process
analytics endpoint wearing a capability's name. The cost is settled and the modelling
is not.

### No percentiles

A percentile needs every value at once, so its memory grows with the window — the one
thing a windowed reading is designed not to do. What a percentile is usually wanted for
here is an SLA ("90% within ten minutes"), and that is a predicate over each case, so
it is answerable by counting in one pass with two integers. Attainment is reported;
the distribution behind it is not.

### An SLA is measured only where it carries a number

`SLA.ThresholdSeconds` is new, optional, and deliberately *beside* the prose
`Threshold` rather than replacing it. "Within five business days" is what the business
agreed and what belongs in the record; no parser should be asked to decide what a
business day means at this installation. Supplying the number is what makes the SLA
measurable, so the record stays writable by somebody who has none and rewards the one
who does.

An SLA without it is listed under `notMeasured` with the reason and the remedy, and
**every KPI is listed there too**. A KPI names a goal in the business's own words;
which recorded figure "disburse within three days" refers to is a judgement, and a
guessed answer would put a number somebody acts on under a name nobody authored.

### Two kinds of absence stay distinct

A realisation this caller may not see is `restricted`; one this server does not deploy
is `deployed: false`. Neither is zero-filled. Zero cases is a finding — nothing ran —
and "you may not look" is not, and the gap report already keeps that distinction for
the same reason.

An SLA over a window that held no case is not 100% attained and not 0%: `share` is 0
because there is nothing to divide, and `cases` is beside it so the reader can see why.

### Cases pool for an SLA and not for a process

A capability realised two ways gets one cycle time per process, because adding two
implementations' cycle times produces a number describing nothing. Its SLA attainment
is over both, because an SLA is a promise about the capability rather than about one of
the things that happen to implement it.

## Consequences

- **Good:** a declared SLA with a number is now checked against what ran, on any
  installation, with no exporter.
- **Good:** ADR-0305's open question is settled with evidence that outlives the
  conversation, and the benchmark stays runnable.
- **Neutral:** the register is unchanged apart from one optional field. Nothing is
  stored about what was measured; the answer is computed on every read, like coverage.
- **Bad, and accepted:** the outcome distribution reports element ids, not names. The
  compiler interns an element name only for a user task, so an end event's name is read
  from the model and dropped — and the method asks authors to name end events distinctly
  precisely so a reader can read those names. The id is a join key and the process XML
  the API serves carries the label, which makes this a client-side join rather than a
  wall. Closing it means compiling the name, which changes the compiled process and
  belongs in its own slice.
- **Bad, and accepted:** a long window on a busy definition still costs a second or
  more. It runs off the loop so nothing else waits for it, and the ceiling bounds it,
  but this is an analytical read and it is priced like one.
