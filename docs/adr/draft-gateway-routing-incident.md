# ADR-DRAFT: A gateway that cannot route parks, it does not complete

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine maintainers

## Context and problem statement

An exclusive gateway's `OnCompleting` emitted the `Completed` event first and then
looked for an outgoing flow. When no condition held and the model had no default
flow, it returned — with the token already consumed. The source said so plainly:

> This is a modeling error that becomes an incident once incidents land
> (Milestone 2); for now the branch simply ends here.

Incidents landed (ADR-0061), and the comment stayed. An external architecture
review reproduced the result: a process instance still marked active, with zero
element instances, zero jobs and zero incidents. Nothing runs, nothing is waiting,
and nothing tells an operator that anything is wrong. The instance is only
distinguishable from a healthy long-running one by counting its element instances.

The same handler has a second defect, in the same three lines. A condition whose
FEEL *fails to evaluate* was folded into "false":

```go
v, err := f.Condition.Eval(...)
if err == nil && expr.IsTrue(v) { return flowID }
```

So a broken condition is indistinguishable from a condition that legitimately did
not hold. With a default flow present it silently routes the token down the
default — a branch the model says to take when the conditions *were evaluated* and
none matched. The inclusive (OR) split has both defects verbatim, and the OR join
has a worse form of the first: it consumes every token parked on it and *then*
looks for a flow, so an unroutable join destroys several tokens rather than one.

## Decision drivers

- **A token is never lost silently.** Every other way a token can get stuck —
  a failed job, an unresolvable timer schedule, a runaway loop — leaves an
  incident an operator can see and resolve. Routing must not be the exception.
- **A modeling error must be repairable in place**, not only by cancelling the
  instance: the work already done upstream of the gateway is real.
- **Determinism (I4/I6).** The parked state must be a fact on the log, so replay
  rebuilds it without re-deciding.
- **An evaluation failure is not a value.** Atlas's rule elsewhere is that a failed
  FEEL evaluation writes null and the token carries on (the rule
  `TestFeelEvaluationFailureWritesNull` pins). That rule is about *producing a
  value*, where null is a defensible answer and halting the single-writer
  processor over one instance's bad expression would be far worse.
- **Cost.** No extra work on a gateway that routes (I1).

## Considered options

1. Complete the gateway and raise an incident on the process instance.
2. Decide before completing; on "no route" or "cannot evaluate", leave the element
   Activated and raise an incident on it.
3. Treat an unroutable gateway as an error that fails the batch.
4. Fix only "no route" and leave an evaluation failure folded into false.

## Decision outcome

Chosen option: **option 2**, in three parts.

**The decision moves before the completion.** `selectExclusiveFlow` runs first; only
a decision that produced a flow emits `Completed` and activates the target. The
inclusive gateway gets the same shape: `inclusiveRouteOrPark` decides, and the join
consumes its parked arrivals only after a decision exists. Ordering is the whole
fix for the token-loss half — the old code could not park, because by the time it
knew it had a problem the token was gone.

**A gateway that cannot decide parks.** `parkUnroutableGateway` emits no element
event and one `IncidentCreated` naming the element instance, exactly as
`parkRunawayLoop` does for a loop that hit its ceiling (ADR-0133). The token stays
where it is, Activated, visible in every incident view Atlas already has.

**An evaluation failure is its own outcome.** `selectExclusiveFlow` returns a
failure message rather than folding the error into false, and the failure stops the
scan — it does not fall through to the default flow. The incident names the flow by
its target's BPMN id and carries the FEEL error, so the operator sees which
condition broke rather than "no route". This does not contradict the write-null
rule: that rule answers "what value did this expression produce"; there is no value
here, only a branch nobody chose.

**Resolving is a real retry.** A job-less incident already routes through
`resumeParkedElement` (renamed from `rearmTimerElement`, which had outgrown its
name). A gateway resolves by re-running the behavior's `OnActivated` — the same
entry point the arrival ran. A gateway that still cannot route parks again on a
fresh incident; one that now can (because an operator supplied the variable the
condition reads) takes its flow exactly once, and nothing upstream re-runs, because
nothing upstream was ever un-completed.

Option 1 was rejected because an incident that names only the instance does not say
*where*, and because completing the gateway still destroys the token — resolving
would have nothing to resume. Option 3 was rejected on invariant I3: a modeling
error in one instance must never stop a partition. Option 4 was rejected because the
two defects are three lines apart and the second is the more dangerous of the two: a
silently mis-routed token looks like a working process.

### Consequences

- **Positive:** an unroutable gateway is now an incident on a named element with a
  message that says why, resumable in place. No token disappears.
- **Positive:** a broken condition can no longer masquerade as a false one, so a
  default flow means what the model says it means.
- **Positive:** replay rebuilds the parked state from the incident event; the
  existing no-match test's replay half asserts exactly that.
- **Negative / trade-offs accepted:** behaviour changes for existing models. A
  model that relied on an XOR silently swallowing tokens — deliberately or not —
  now accumulates incidents instead of quietly finishing. That is the point, but it
  is a visible change for anyone upgrading, and it belongs in the release notes.
- **Negative:** an unroutable gateway now holds an element instance for as long as
  the incident is open, where before it held nothing. That is the cost of being able
  to resume it.
- **Negative:** the inclusive gateway's flow selection now writes into a
  processor-owned buffer rather than activating as it goes. It is one more piece of
  reused per-batch state, and it aliases: the returned slice is valid only until the
  next routing decision. Both callers take the flows immediately.
- **Follow-ups / risks to watch:** conditional events (`engine/conditional.go`) still
  treat an evaluation error as false. That is a different question — an event that
  does not fire is not a token going nowhere — but it is the same expression rule
  applied differently in two places, and it deserves its own look.

## Pros and cons of the options

### Option 1 — complete, then raise an instance-level incident
- Good: smallest change to the ordering.
- Bad: the token is already gone, so the incident cannot be resumed; and it names no
  element, so the operator has to find the gateway themselves.

### Option 2 — decide first, park with an element incident (chosen)
- Good: no token loss; resumable; consistent with every other parked state in the
  engine; the evaluation failure gets a distinct, informative message.
- Bad: changes behaviour for existing models; adds a reused buffer and an aliasing
  contract on the OR path.

### Option 3 — fail the batch
- Good: impossible to ignore.
- Bad: one instance's modeling error stops every instance in the partition (I3).

### Option 4 — fix only the missing route
- Good: half the diff.
- Bad: leaves the more dangerous defect — a token routed down a branch nobody chose,
  with nothing to show for it.

## Links

- relates to [ADR-0061](0061-incident-model.md) (incidents, and
  the resolve path a job-less incident takes)
- relates to [ADR-0133](0133-standard-loop-activities.md) (parkRunawayLoop: the same park-with-
  an-incident shape)
- relates to [ADR-0086](0086-gateway-conditions-resolve-over-scope-chain.md) (where a
  condition's variables come from)
- relates to [ADR-0033](0033-inclusive-gateway-join.md) (the OR join whose consume
  order this record changes)
- reported as F08 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
