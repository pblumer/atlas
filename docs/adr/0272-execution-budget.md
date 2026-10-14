# ADR-0272: One token may not hold the writer forever

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine maintainers

## Context and problem statement

A partition has exactly one writer (invariant I3, ADR-0002): the processor
goroutine, on which every command runs and behind which every HTTP handler queues
through `runloop.Loop.Do`. `RunUntilIdle` processes batches until the queue —
including the followups each batch generates — drains.

Nothing bounded that. A model whose automatic elements form a cycle produces a
followup for every command it processes, so the queue never drains and
`RunUntilIdle` never returns. An exclusive gateway looping back on itself under a
condition that always holds is enough, and the compiler accepts it. An external
architecture review drove exactly that model and reported the consequence: after
1100 batches the queue was still full, with the partition's only writer inside the
loop. While it is there no other instance advances, no timer fires, and the
readiness probe cannot get its empty closure onto the loop to be answered — the
partition is indistinguishable from a wedged one.

The engine already has this idea for one shape of runaway: a standard loop with no
stated maximum stops after `SafeLoopCeiling` runs and raises an incident on its
body (ADR-0133). That covers a loop the compiler recognises as a loop. It does not
cover a cycle drawn in sequence flows, which is the same runaway with no marker on
it.

## Decision drivers

- **Fairness with bounded latency (I3).** Independent instances, timers, health
  probes and — the important one — a cancellation of the offending instance must
  all get their turn while a runaway is running.
- **Do not ban valid cycles.** BPMN cycles are ordinary modelling. A rework loop
  that waits for a human every lap is not a runaway and must be untouched.
- **Do not punish legitimate heavy work.** A multi-instance activity over fifty
  thousand items is a lot of activations for one instance and is exactly what the
  engine is for.
- **Determinism (I4/I6).** Whatever stops the runaway must be a fact on the log, so
  recovery rebuilds the stopped state rather than re-deciding it.
- **Cost (I1).** The accounting sits on the token-movement path and must not
  allocate per token movement.

## Considered options

1. **Yield.** Bound the *run*: return from `RunUntilIdle` with work still queued, and
   let a driver come back. The writer is shared; the cycle still runs forever.
2. **A budget per process instance per run.** Count element activations for an
   instance and stop it past a ceiling.
3. **A budget per token per run, inherited across minted token ids.** (chosen)
4. **Refuse the model at compile time** — reject a strongly connected component of
   automatic elements at deploy.

## Decision outcome

Chosen option: **option 3.** A token may drive `DefaultExecutionBudget` (10,000)
element activations in a single run. Past that the element it was about to run is
activated and then *stopped*: its behavior never runs, and an incident on it says
why. `RunUntilIdle` therefore always returns, and everything else the report asks
for follows from that — other instances advance, timers fire, the probe is
answered, and a cancellation of the culprit reaches the loop (where the terminated
instance's queued commands are dropped, ADR-0284).

**Why per token.** It is what separates a runaway from heavy work. A cycle is one
token going round; a multi-instance activity over fifty thousand items is fifty
thousand tokens each taking a step or two. Option 2 cannot tell them apart — any
ceiling low enough to stop a cycle promptly also stops a large multi-instance, and
any ceiling high enough to spare it leaves the writer occupied for seconds. Per
token, the two are different by construction, which is what lets the default be
tight.

**Why inherited.** A fork's branches, a parallel join's continuation and a
subprocess's exit each mint a fresh token id for what is still one thread of
control. Counting naively, a cycle passing through any of them would reset its own
budget every lap. So `activateElement` carries the parent's count onto a token it
mints — reading the lineage from `ParentTokenID` where the continuation's own
`TokenID` has already been cleared. A multi-instance iteration deliberately does
*not* inherit: `seedMultiInstanceIteration` builds its command directly, and an
iteration is a new unit of work rather than the same one going round again. That
distinction is the whole rule: **a sequence flow continues a thread of control; an
iteration starts one.**

**Why per run.** The counter is cleared when `RunUntilIdle` starts, because that is
where the writer was last free. A token that waited for a job, a timer or a message
between laps was never the problem this record is about, and a rework loop that
waits for a human every lap is untouched no matter how long it runs. The counter is
therefore in-memory and not durable, which is also what keeps it off the log.

**Stopping, not failing.** The element is activated first and stopped second, which
is the one point where stopping costs nothing: the element instance is on the log
and can carry an incident, and nothing downstream has been set in motion. The token
is exactly where an operator can see it.

**Resolving runs the element.** An incident with no job key used to be resolved by
inferring the resume from the element's *node type* — re-arm a timer, re-attempt a
mockup, continue a loop. That works only while each node type has one way of
getting stuck, and the budget can stop any element of any type. So
`model.IncidentValue` gains a `Reason`, and `resumeParkedElement` reads it before
the node-type switch: an element the budget stopped simply runs, through the same
`runElementBehavior` a fresh activation runs. The field rides after the message and
is append-compatible — a record written before it existed is one byte shorter and
decodes as `IncidentUnclassified`, which is what every other source still writes.

Option 1 was rejected as insufficient on its own: it answers fairness and leaves
the cycle running forever, and this record's own criterion is a ceiling. It remains
a reasonable *addition* if a single legitimate run ever needs slicing. Option 4 was
rejected because it bans models that are valid — a cycle whose automatic-looking
component contains a message catch is not a runaway — and because a deploy-time
refusal cannot see what a FEEL condition will do at runtime.

### Consequences

- **Positive:** an automatic cycle now stops, visibly, in bounded time, and the
  partition keeps serving everything else. The failure mode the review reported —
  a partition that answers nothing and reports nothing — is gone.
- **Positive:** ordinary heavy work is untouched, and there is a test that says so
  rather than a hope.
- **Positive:** `IncidentReason` gives the resolve path something better than
  guessing from a node type, and the operator list can finally label a budget stop
  as what it is instead of as a timer.
- **Negative / trade-offs accepted:** a map write per element activation. It is
  reused across runs like the per-batch sets beside it, so it does not allocate in
  the steady state, but it is real work on the token-movement path.
- **Negative:** the default is a number, and a number is a guess. A model that
  legitimately drives one token through more than ten thousand activations without
  ever waiting will stop; `SetExecutionBudget` raises it, and there is deliberately
  no way to switch the budget off.
- **Negative:** the budget is not surfaced in installation settings yet — it is a
  processor setter. Wiring it through is a follow-up, not a decision.
- **Negative:** the four other job-less incident sources (a timer schedule, a
  mockup failure, a runaway loop, a gateway that cannot route) still carry
  `IncidentUnclassified` and are still resolved by node type. Classifying them is a
  change to each of those sources and is deliberately not made here.
- **Follow-ups / risks to watch:** the review also suggests marking an automatic
  strongly connected component as a *risk* at deploy time, which is a warning
  rather than a refusal and would give a modeller the signal before the incident.
  That is unbuilt.

## Pros and cons of the options

### Option 1 — yield the run
- Good: simple; the writer is shared; nothing is ever stopped that a person wanted.
- Bad: the cycle runs forever, burning a partition's throughput indefinitely and
  reporting nothing.

### Option 2 — budget per process instance
- Good: catches every cycle shape without reasoning about token lineage.
- Bad: cannot distinguish a runaway from a large multi-instance, so the ceiling is
  either too low for real work or too high to be a bound.

### Option 3 — budget per token, inherited (chosen)
- Good: separates a runaway from heavy work by construction; a tight default; the
  stopped token is visible and resumable.
- Bad: needs the inheritance rule to be right at every place a token id is minted,
  and needs a map on the activation path.

### Option 4 — refuse automatic cycles at deploy
- Good: the modeller learns before anything runs.
- Bad: refuses valid models, and cannot know what a condition will evaluate to.

## Links

- relates to [ADR-0002](0002-single-writer-partition-model.md) (the writer this protects)
- relates to [ADR-0133](0133-standard-loop-activities.md) (the same park-with-an-incident
  shape, for the one runaway the compiler can already see)
- relates to [ADR-0061](0061-incident-model.md) (incidents, and the resolve path)
- relates to [ADR-0077](0077-multi-instance-activities.md) (why an iteration is a new
  token and not a continuation of one)
- reported as F12 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
