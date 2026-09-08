# ADR-DRAFT: A cancellation sees the children created in its own batch

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0238](0238-child-instance-index.md) added the `childByParent` reverse index so
tearing down a call activity could find the child process instances it started
without walking every live instance. It read that index through the **committed**
store and justified the choice as a determinism measure: the teardown "must be a
pure function of what is durable".

An external architecture review reproduced what that costs. A call activity's
child and a cancellation of its caller can arrive in one batch. The child's
activation has been applied to the batch's transaction but not yet committed, so
the committed read reports no children and the cascade skips it. After the batch
the caller is gone and the child is still running — holding an element instance
and an activatable job, with no live caller to return to.

Two questions follow, and they are separable:

1. Is the committed-only read defensible? It is what makes the child invisible.
2. Even once the child *is* found, the teardown enqueues a `Terminating` command
   for it — and the child's own start event is *already* queued from the same
   batch. That command runs first, against an instance the next command is about
   to terminate, and rebuilds the execution the cancel is removing.
3. The two commands can also arrive the other way round: the caller is cancelled
   in the batch *before* the queued child-creation runs. The teardown cannot
   cascade into a child that does not exist yet, so the creation starts a process
   whose call activity is already gone.

## Decision drivers

- Determinism: `applyToState` runs live and on recovery, and replay must rebuild
  exactly what the live run built (I4, I6).
- A terminal event on a parent must leave no child, job, timer or subscription
  running behind it.
- Cost: the fix must not add per-command work to the token-movement path (I1).
- The batch boundary is an implementation detail of throughput (ADR-0005 group
  commit). Behaviour must not depend on which side of it two commands land.

## Considered options

1. Keep the committed read; accept the orphan as a known limitation.
2. Read the reverse index through the transaction, and separately stop a
   terminated instance's queued commands from running.
3. Read through the transaction, and guard every element activation on its
   process instance still being live.

## Decision outcome

Chosen option: **option 2**, in three parts — one per question above.

**The reverse index is read through the transaction.** ADR-0238's determinism
argument does not hold. The records a transaction has already applied are a
function of the commands processed so far, in order; replay applies the same
records in the same order. Seeing them is exactly as reproducible as not seeing
them, and strictly more correct. The engine already depends on this reading
elsewhere — `ElementInstancesOfProcess` reads through the batch precisely so a
parallel join counts a token that arrived earlier in the same batch. A call
activity's teardown is the same kind of question about the same kind of state.

The committed-store form stays on `queries` as the off-loop and state-level
query: a test asserting that the index itself is maintained correctly wants to
read what was committed.

**A terminated instance's queued commands are dropped.** Finding the child is
necessary and not sufficient. `handleProcessInstanceTerminating` records the
instance in a per-batch set, and `advanceQueue` drops the commands belonging to
it — whether carried over from an earlier batch or scheduled as followups during
this one. Commands are never persisted and never replayed (I6), so this changes
what runs next and nothing about what recovery rebuilds.

**A child creation checks that its caller is still there.** The reverse order
needs its own answer, because there is no child yet for the cascade to find.
`handleProcessInstanceActivating` reads the `ParentElementInstanceKey` the command
carries and, when that element instance is gone from the transaction, does not
start the child. Only child creations are gated: an API, timer, message or signal
start carries no parent element and is always free to start.

Option 3 was implemented first and rejected on evidence. Guarding every
activation on "is my instance still live" also fires for instances that
*completed* normally, and it broke compensation: a compensation throw's scope
drains and completes the instance before the throw's outgoing flow is activated,
so the guard dropped the token that should have continued past it. That is a real
defect in scope accounting and it deserves its own record — but it is a different
one, and a fix for cancellation should not silently change what completion does.
Dropping work for instances that were *terminated* touches only the path this
record is about.

### Consequences

- **Positive:** cancelling a caller tears down a child created in the same batch,
  with no orphaned element instance or activatable job — and in either order, so
  behaviour no longer depends on which side of a batch boundary the two commands
  fall on.
- **Positive:** the transactional read is one line and reuses the index ADR-0238
  built; the queue filter runs only when something was terminated, so the
  ordinary path is a length check.
- **Negative / trade-offs accepted:** `advanceQueue` now inspects each carried
  command when a termination happened in that batch. It is O(queue) on a batch
  that cancelled something, and free otherwise.
- **Negative:** the queue filter answers only for the three token-carrying value
  types. Process-instance commands are excluded deliberately: their key does not
  mean one thing across intents (a creation mints its instance key in the handler)
  and `IntentPurging` operates on an instance that has *already* finished, so
  dropping it would leave history that is never reclaimed.
- **Negative:** dropping commands is a scheduling side effect inside a handler.
  It is confined to one per-batch set and one filter, and it is invisible to
  replay, but it is a new kind of operation in the processor and worth knowing
  about.
- **Follow-ups / risks to watch:** the compensation defect above — an instance
  completing while a token is still in transit out of a throw event — is
  unaddressed and now has a written description. The durable-continuation work
  (F01 in the review) will change how queued work is represented; this filter
  will have to move with it.

## Pros and cons of the options

### Option 1 — keep the committed read
- Good: no change; ADR-0238's stated contract stands as written.
- Bad: a cancelled business transaction keeps making external calls through a
  child nobody can see or stop.

### Option 2 — transactional read plus dropping terminated work
- Good: fixes both halves of the reproduced failure; scoped to cancellation.
- Bad: introduces command dropping; leaves the compensation defect standing.

### Option 3 — transactional read plus a liveness guard on every activation
- Good: one rule, uniformly applied, no queue surgery.
- Bad: a state read on the activation path; and it changes completion as well as
  cancellation, which broke compensation in practice.

## Links

- supersedes the committed-only reasoning in
  [ADR-0238](0238-child-instance-index.md)
- relates to [ADR-0076](0076-call-activities.md) (call activity and child teardown)
- relates to [ADR-0005](0005-group-commit-and-fsync-strategy.md) (batching, and why a batch boundary
  must not be observable)
- reported as F07 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
