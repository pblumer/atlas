# ADR-0007: Job worker protocol

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

Service tasks represent work done outside the engine (call an API, run a computation). The single-writer processor must never block on that external work, or the whole partition stalls. We need a protocol for handing work to external workers and getting results back that keeps the processor at full speed and tolerates worker failures.

## Decision drivers

- The processor must never block on external work
- Tolerate worker crashes, restarts, slowness, and duplicates
- Backpressure when workers fall behind
- Language-agnostic workers

## Considered options

1. **Inline execution** — the processor calls the external system directly
2. **Push** — engine pushes jobs to registered worker endpoints
3. **Long-poll / streaming pull** — workers subscribe by job type and pull/stream jobs; results come back as commands

## Decision outcome

Chosen option: **streaming pull with completion-as-command.**

1. Activating a service task creates a **job** (`JobCreated` event) and indexes it as activatable by job type.
2. After the batch's fsync, a side effect notifies workers subscribed to that job type.
3. Workers stream/long-poll jobs of types they handle, each job leased with a deadline.
4. A worker does the work and submits `CompleteJob` (or `FailJob`); that command flows back through the processor, moving the element instance to `COMPLETING`.

Workers are external processes speaking a gRPC streaming API, so they can be written in any language.

### Consequences

- **Positive:** The processor never blocks. Backpressure is natural — if no worker pulls, jobs simply queue in state. Worker crashes are handled by job lease timeouts (`JobTimedOut` → retry). At-least-once delivery with idempotency via the job key. Workers scale independently of the engine.
- **Negative / trade-offs accepted:** At-least-once means a job may be delivered/executed more than once (worker must be idempotent or tolerate it). Lease management and retry/backoff add complexity. Completion of a stale lease must be rejected (fencing via job key + lease epoch).
- **Follow-ups:** Lease epoch / fencing token design; configurable retry/backoff; worker-side SDK ergonomics; streaming flow-control.

## Lifecycle

```
JobCreated ──notify──► worker pulls (lease) ──► work ──► CompleteJob command
     │                                                         │
     └── lease expires ──► JobTimedOut ──► retry (or Incident if retries exhausted)
```

## Pros and cons of the options

### Inline execution
- Good: simplest.
- Bad: blocks the single writer; a slow API stalls the partition. Unacceptable.

### Push
- Good: low latency to a known worker.
- Bad: engine must track worker health/endpoints; backpressure is awkward; retries on push failure are messy.

### Streaming pull
- Good: non-blocking, natural backpressure, independent scaling, language-agnostic.
- Bad: lease/retry/idempotency complexity.

## Amendment (2026-08-20): transport, leases, and what blocks the pull

The decision above was written before any of it was built. Three things have to be
corrected or made concrete now that it is being implemented.

### The transport is HTTP, not gRPC

The original text says workers speak "a gRPC streaming API". Atlas speaks no gRPC
anywhere: the whole product surface is HTTP+JSON with an OpenAPI document, and ADR-0142
declined the official OpenTelemetry exporter specifically to keep gRPC and protobuf out
of the binary (66 packages, ~13MB, measured). Adding a gRPC server for this alone would
contradict that decision and ADR-0010's "few dependencies" driver.

Workers therefore speak the same HTTP the rest of the product speaks. Nothing in the
decision depends on the transport: the protocol is "lease work, report the outcome as a
command", which HTTP carries as well as a stream. Long-poll — a request that waits for
the notification ADR-0005 already fires after fsync — replaces streaming, and is a later
slice.

### A lease is a hold on the job, not a lock

Implemented as the mechanism ADR-0111 already proved for retry backoff, rather than a new
one: the job stays stored, comes **off the activatable index**, records who holds it
(`Assignee`) and until when (`LeaseExpiresAt`), and a timer releases it. That reuse is
the point — the timer path is durable, replayed by `applyToState`, and already
recovery-tested.

Two holds can sit on one job at once, because a worker can lease a job and then fail it
with a backoff. Each timer releases **only its own hold** (`TimerValue.JobKind` says
which), and the job returns to the index only when nothing holds it. Without that, a
lease expiring mid-backoff would hand out a job the worker asked to defer.

The lease is deliberately *not* stored in `JobValue.Deadline`. That field already means
the **user task due date** (ADR-0032) — conflating them takes every user task with a due
date off the worker-visible index, which is how this was found.

### What blocks the type-keyed pull

The protocol a worker actually wants is "give me the next `send-email` job". That cannot
be built correctly on the current index, and the reason is worth recording precisely:

**Job type indices are interned per compiled process.** `compiler/builder.go` says so and
reserves indices 0-6 for the built-in types (DMN, user task, script languages) *because*
of it. But the activatable index is global — `jobActivatable:<jobType>:<jobKey>` — so two
definitions disagree about what a given index means. Measured, on the current tree:

```
send-email  in process A -> index 16
charge-card in process C -> index 16
```

A worker subscribing to `send-email` would be handed `charge-card` jobs. So the pull
endpoint waits on a **global job-type registry**: one durable name↔index table for the
whole engine, with a migration for jobs already written with per-process indices. That is
its own slice, and it is a correctness prerequisite rather than an optimisation.

Until then, a job can be leased **by key** (`POST /api/v1/jobs/{key}/activate`), which is
unambiguous — a worker that lists an instance's jobs can lease and work them today.

### Still open, in order

1. **Global job-type registry**, then the type-keyed pull (`POST /api/v1/jobs/activate`).
2. **Long-poll**, so workers stop busy-polling; the post-fsync notifier already exists.
3. **Fencing.** The original text lists this as a follow-up and it still is: nothing stops
   a worker whose lease expired from completing the job afterwards, while a second worker
   holds it. At-least-once delivery is accepted above, so this is a known and bounded gap
   — but a lease epoch on the job, presented on completion, is what closes it.
4. **Activation and timeout counters** (ADR-0142 slice 5 deferred them for want of these
   events; `JobActivated` and `JobTimedOut` now exist).

## Links

- depends on ADR-0005 (notify only after fsync)
- failures escalate to incidents (see architecture: failure handling)
- reuses ADR-0111's hold-and-timer mechanism for retry backoff
- ADR-0142 supplies the counters, and its dependency decision is why the transport is HTTP
