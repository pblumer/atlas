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

## Links

- depends on ADR-0005 (notify only after fsync)
- failures escalate to incidents (see architecture: failure handling)
