# ADR-0150: Job handlers run off the run-loop goroutine

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0149](0149-bounded-connector-call-budget.md) gave every connector a bounded
outbound-call budget after a production instance wedged on a mail connector whose
OAuth token endpoint stopped answering. It named the deeper fix in the same
breath — *option 3, run the workers off the run-loop goroutine* — and deferred it
because it changes the engine's concurrency model, which is the one thing
invariant I3 constrains.

The budget bounds that stall; it does not remove it, and it does not cover
everything that runs there:

- **Ten seconds is still a total outage.** The run-loop goroutine is the only one
  that can serve *any* handler's `do` closure, so a held writer is not a slow
  connector — it is a server that answers nothing.
- **The budget is per call, not per drive.** `Drive()` dispatches every
  activatable job of every registered type before it returns, in one `do` turn. A
  backlog costs budget × backlog.
- **Script tasks are not HTTP.** ADR-0047 workers shell out to `pwsh`, `python3`
  and `node` under `--script-timeout` (30s by default). Installing an interpreter
  on a server whose script tasks had been parking makes that whole backlog
  runnable at once — a queue of 30-second jobs, executed one after another, on the
  writer.

The symptom is identical every time, and it is a nasty one: the process is alive,
`/healthz` answers `ok` (it is unconditional by design — ADR-0142), the web UI
loads because it is static, and the **login page even renders**, because
`GET /api/v1/auth/me` is rejected with 401 by the auth middleware without ever
touching the loop. Then *Sign in* does nothing at all, because
`POST /api/v1/auth/login` reads the user store through `do` and never returns.
Nothing recovers it but a restart.

The question this answers: how do we make a worker's runtime cost local to the
instance it belongs to, so that no handler — however slow, however many — can
take the server's availability with it?

## Decision drivers

- **The single writer must stay available.** I3 makes one goroutine authoritative
  for a partition. That is only safe if nothing it does can block without bound —
  and a worker's whole job is to talk to something Atlas does not control.
- **Failure should be local.** A bad host or a runaway script should stall *its*
  instance, and surface as a retry and then an incident (ADR-0061).
- **Don't change what a caller observes.** A `POST` that starts an instance
  returns after the process has advanced as far as it can. That is the in-process
  runner's contract, and hundreds of tests plus the Console depend on it.
- **The standalone runner must not change.** `job.Runner` is used as a library and
  throughout the connector tests with no server around it.
- **Regression-proof.** The hazard is invisible at the call site — `Drive()` inside
  a `do` closure reads like every other line in the file.

## Considered options

1. **Bound the drive.** Cap how many jobs one drive dispatches, or give the whole
   drive a deadline, so a backlog cannot multiply the per-call budget.
2. **Fully asynchronous execution.** Mutating requests return once their command is
   durable; a background pump dispatches jobs afterwards.
3. **Split the cycle across the writer boundary.** Claim a job on the loop, run its
   handler off the loop, apply the outcome on the loop.

## Decision outcome

Chosen option: **3 — split the cycle**, because it removes the hazard
structurally (nothing unbounded runs on the writer any more) while leaving the
observable contract of the API exactly as it was.

Specifically:

- **`job.Loop` is the seam.** A one-method interface (`Do(func())`) that
  `api/runloop.Loop` already satisfies, handed to the runner with the new
  `job.OnLoop` option. A runner built without one runs every step on the calling
  goroutine — the pre-0150 behavior, which is what a library user and every
  connector test gets.
- **One job at a time, in three steps.** `claim` (scan the activatable index, read
  the job record, mark it in flight) and `apply` (`CompleteJob` / `FailJob`, and
  the `RunUntilIdle` around them) run on the loop; the handler runs on whichever
  goroutine called `Drive`. Claiming per job rather than per batch preserves the
  existing "job vanished since the scan" behavior: a record is re-read *after* the
  previous handler was applied, not before.
- **An in-flight set replaces the old atomicity.** The whole cycle used to be one
  run-loop turn, which is what kept a job from being dispatched twice. Now two
  requests can drive concurrently, so the runner tracks what it has claimed and
  not yet applied. It is written only from `claim` and `apply` — both on the loop
  — so it needs no lock of its own (I3).
- **A handler panic becomes a job failure.** On the loop a panic killed the
  process; off it, it unwinds into an HTTP handler where `net/http` swallows it,
  which would strand the claim forever. It is converted to an error and routed
  into the incident model like any other handler failure.
- **Every `Drive()` in `api` moved out of its `do` closure.** Two callers only ever
  needed their own commands processed, not a worker dispatch: `deployModel`'s
  timer-start arming (ADR-0051) and the retention sweep's purges (ADR-0115). They
  call `proc.RunUntilIdle()` and stay inside the single turn they already held.
- **A drift test enforces it.** `TestJobsAreNotDrivenOnTheRunLoop` parses package
  `api`, collects every function that reaches `Drive` — transitively, over the
  package's own call graph — and fails if one of them is called inside a `do`
  closure. Reintroducing the hazard now fails the build, and so does the deadlock
  it would otherwise cause (a drive dispatched from the loop waits on the loop).

### Consequences

- **Positive:** No worker can hold the single writer, whatever it does and however
  many of them are queued. A hung host or a runaway script stalls one request and
  parks one instance; every other request is served throughout. ADR-0149's budget
  becomes ordinary policy — how long *this* connector may take — rather than the
  only thing between a third party and an outage. `/readyz`'s "the partition
  writer is not responding" (ADR-0142) goes back to meaning what it says: a
  blocked fsync or a genuine deadlock, not a worker doing its job.
- **Negative / trade-offs accepted:**
  - **A mutating request is no longer one turn.** The command turn and the drive
    are separate, so another request can interleave between them. Each turn is
    durable on its own and nothing in the engine depended on the wider atomicity,
    but an operator watching two concurrent operations can now observe an
    intermediate state that was previously impossible.
  - **A handler's reads are concurrent with the writer.** Workers read their
    element instance and its scope variables straight from the store. Each read is
    atomic, and nothing advances the element instance a job is parked on — but a
    *parallel branch of the same instance* can now move while a handler reads. That
    is exactly the visibility an external worker has under ADR-0007; it arrives
    here ahead of the transport.
  - **The drive still blocks its caller, and still runs jobs serially.** That is
    deliberate: it is what keeps the API's synchronous contract. A slow worker
    therefore still slows the request that triggered it, and the timer scheduler
    still waits for the drive it started (ticks coalesce; timers are "fire at or
    after due", ADR-0051).
- **Follow-ups / risks to watch:** a snapshot-scoped read handle so a handler sees
  one consistent view of its instance; parallel dispatch within a drive, once
  there is a reason to want it; and ADR-0007's external-worker transport, after
  which this split becomes the transport boundary rather than a goroutine
  boundary.

## Pros and cons of the options

### Option 1 — bound the drive
- Good: tiny change; keeps every existing guarantee about atomicity.
- Bad: still stalls the whole server, just for a chosen number of seconds; picking
  the number trades availability against throughput with no good answer; a single
  slow job still exceeds it.

### Option 2 — fully asynchronous execution
- Good: the cleanest end state, and where ADR-0007's streaming workers lead.
- Bad: changes what every mutating endpoint returns (the instance has not advanced
  yet when the response is written), and with it the Console, the MCP tools and a
  large part of the test suite. A behavioral change of that size is a decision of
  its own, not a fix for an outage.

### Option 3 — split the cycle across the writer boundary (chosen)
- Good: removes the hazard structurally rather than bounding it; leaves the
  observable contract untouched; the standalone runner is unaffected; the rule is
  mechanically enforced.
- Bad: gives up the "one turn per request" atomicity and lets workers read
  concurrently with the writer; the drive must never be called from the loop, a
  rule that needs the drift test to stay true.

## Links

- deepens [ADR-0149](0149-bounded-connector-call-budget.md) (option 3, named there
  as the right long-term direction)
- protects invariant I3 (single writer) — see [invariants](../architecture/invariants.md)
- the runner and its eventual transport: [ADR-0007](0007-job-worker-protocol.md)
- failure surfaces through [ADR-0061](0061-incident-model.md) (retries and incidents)
- the interpreters that made the backlog case concrete: [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)
- the endpoint that names a stuck writer: [ADR-0142](0142-prometheus-metrics.md)
