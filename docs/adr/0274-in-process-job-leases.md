# ADR-0274: The in-process runner claims what it works

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0157 step 6 moved in-process handlers off the run loop: a connector's outbound
call no longer holds the single writer. What it left behind was a second lock.

`Server.drive()` held `driveMu` across the *whole* loop — claim, work, submit —
and the comment said why:

> Drivers are serialized. Two concurrent callers must not claim the same job and
> work it twice.

That is a true statement about a runner with no claim identity. `Runner.Claim`
collected whatever was activatable and took nothing on it; nothing marked a job as
being worked. Two drivers would both be handed the same job, so the mutex stood in
for the identity the jobs did not have.

The cost is what an external review reported. Every request path that must reach
quiescence drives — starting an instance, completing a task, cancelling one — so a
single worker waiting on a dead host blocked *all* of them for the length of its
timeout. The run loop was free the whole time, which is no comfort to a caller
queued behind a mutex.

The review's own note is the one that matters: the identity is the work, not the
removal of the mutex.

## Decision drivers

- **At most one in-process worker per job**, before any lock is relaxed.
- **A hanging handler must not delay unrelated work.**
- **Recoverable without an operator.** A wedged handler must not hold its job
  forever.
- **Reuse the mechanism external workers already have.** Atlas has leases, lease
  epochs, lease timers and a fencing check; a second scheme would be a second thing
  to get right.
- **Keep "my request's work is done when it returns".** Callers and tests depend on
  it.

## Considered options

1. Give the in-process claim a lease, then narrow the mutex to claim and submit.
   (chosen)
2. Drop the mutex entirely once claims lease.
3. Keep the mutex and bound the handlers with a timeout.
4. Mark jobs in memory as in-flight instead of leasing them.

## Decision outcome

Chosen option: **option 1.**

**`Runner.Claim` leases.** It scans the activatable index as before, then activates
each key under the worker name `atlas:in-process` for `DefaultLease` (five minutes,
settable), runs the processor so the activations apply, and keeps the jobs it
actually holds. The activation takes a job off the activatable index, so a second
claim cannot see it — which is the identity that was missing.

**`Runner.Submit` is fenced.** Each outcome is checked against the job's current
lease and epoch before it is applied, the same check the HTTP completion endpoint
makes on an external worker's report (ADR-0007). A round whose lease elapsed and was
handed on presents an epoch the job has moved past, and its outcome is dropped
rather than applied to work somebody else is now doing.

**The mutex covers the scheduler halves only.** `drive()` locks around the claim and
around the submit, and not around `Work`. A hanging handler now delays exactly one
caller: the one that dispatched it.

Option 2 was rejected for what it costs elsewhere: two unsynchronised drivers would
each loop until *their* claim came back empty, which is not the same as the system
being idle, and "the work my request caused is done when it returns" is a contract
every request path and a great many tests rely on. Serializing two short steps keeps
it and costs nothing measurable. Option 3 is ADR-0149's answer, which
ADR-0157 then removed; a timeout bounds the stall, it does not remove it.
Option 4 was rejected because an in-memory set is lost on restart and invisible to
the operator views, where a lease is durable, expires by itself and shows up as an
assignee.

### Consequences

- **Positive:** a worker waiting on a dead host no longer blocks starting,
  completing or cancelling anything else. There is a test that hangs a handler and
  requires an unrelated instance to start while it hangs — and that test fails
  against the previous code.
- **Positive:** an in-process job is visibly held while it is worked: it leaves the
  activatable index and names `atlas:in-process` as its assignee, so "what is being
  worked right now" is answerable from state rather than from a goroutine dump.
- **Positive:** a wedged handler releases its job when the lease expires, without an
  operator, exactly as an external worker's does.
- **Negative / trade-offs accepted:** claiming now writes events — one `JobActivated`
  and one lease timer per claimed job — where it used to be a pure read. That is
  real cost on the writer, paid per job rather than per round, and it is the price of
  the job being *held* rather than merely dispatched.
- **Negative:** a claim that is never submitted (a caller that abandons a round)
  holds its jobs until the lease expires, where before they stayed available. That
  is what a lease means, and five minutes is the exposure.
- **Negative:** `Engine` gains `ActivateJob`, so anything implementing that
  interface — the test fakes — must grow a method.
- **Follow-ups / risks to watch:** the pull endpoint still refuses a type the runner
  serves (`Handles`). With leasing that refusal is no longer strictly necessary — an
  external worker and the runner would now contend safely — but relaxing it is a
  separate decision about who is supposed to do the work, not about whether it is
  safe.

## Pros and cons of the options

### Option 1 — lease the claim, narrow the mutex (chosen)
- Good: removes the stall; reuses the lease, epoch and timer that already exist;
  keeps the quiescence contract.
- Bad: claiming writes; an abandoned round holds its jobs for the lease.

### Option 2 — drop the mutex entirely
- Good: no serialization at all.
- Bad: "my request's work is done when it returns" stops being true, and every
  request path and test that leans on it changes meaning.

### Option 3 — keep the mutex, bound the handlers
- Good: no new semantics.
- Bad: bounds the stall instead of removing it; this is what ADR-0149 did, and
  ADR-0157 removed the need for it on the run loop without touching this one.

### Option 4 — an in-memory in-flight set
- Good: no writes on the claim.
- Bad: lost on restart, invisible to the operator, and a second mechanism beside the
  leases that already exist.

## Links

- completes [ADR-0157](0157-worker-processes-supervision-and-console.md) step 6,
  which moved the handlers off the loop and left the driver's mutex behind
- uses [ADR-0007](0007-job-worker-protocol.md)'s lease, epoch and lease timer
- supersedes [ADR-0149](0149-bounded-connector-call-budget.md)'s timeout as the
  answer to a slow worker: the budget bounded the stall, the lease removes it
- relates to [ADR-0002](0002-single-writer-partition-model.md) (the writer this
  keeps free)
- relates to [`draft-bounded-job-polling`](0270-bounded-job-polling.md), which
  bounded the same claim's size
- reported as F13 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
