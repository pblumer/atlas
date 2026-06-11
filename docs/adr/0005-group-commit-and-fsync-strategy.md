# ADR-0005: Group commit and fsync strategy

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

Durability comes from `fsync`: until the OS has flushed an event to physical disk, a crash can lose it. But `fsync` is expensive — one per event caps throughput at a few thousand events per second. Without `fsync`, "durable" is a lie; with one `fsync` per event, "high throughput" is a lie. We need both.

## Decision drivers

- Real durability (events survive crashes)
- High throughput (millions of events/s aspiration)
- Low latency under light load
- No manual tuning knobs where avoidable

## Considered options

1. **fsync per event** — simplest, safe, slow
2. **No fsync / rely on OS flush** — fast, not durable
3. **Group commit** — batch many events, one fsync per batch
4. **Time-based batching** — flush every N milliseconds

## Decision outcome

Chosen option: **group commit driven by queue drain.** The processor collects commands into a batch until the command queue is momentarily empty (a `select`/`default` check) or a max batch size is reached, then appends the whole batch to the log and issues **one** `fsync`. State is committed and side effects run only after that fsync.

This self-tunes: under light load batches are tiny (low latency), under heavy load batches grow (high throughput), with no timer and no configuration.

### Consequences

- **Positive:** Throughput scales with batch size, not disk round-trips. Latency stays low when idle. No tuning knob. The "durable before visible" ordering (fsync → state commit → side effects) guarantees we never expose or act on an event that isn't on disk.
- **Negative / trade-offs accepted:** Worst-case latency for a single command includes waiting for the current batch's fsync. A crash between a command being accepted and the batch's fsync loses that command (the client must treat submission as pending until acknowledged post-fsync). Max batch size must bound memory.
- **Follow-ups:** Acknowledge commands to clients only after fsync; expose batch-size and fsync-latency metrics; consider optional fsync-coalescing across the WAL segment.

## The durability contract

1. A command is *accepted* when enqueued, not yet durable.
2. Its event(s) become *durable* when the batch's `fsync` returns.
3. State is committed and side effects (incl. client acks and worker notifications) run only after (2).

Therefore no externally observable effect ever precedes durability.

## Pros and cons of the options

### fsync per event
- Good: trivially correct.
- Bad: throughput ceiling of a few thousand/s.

### No fsync
- Good: fastest.
- Bad: not durable; unacceptable.

### Group commit (queue-drain)
- Good: durable + high throughput + low idle latency + self-tuning.
- Bad: per-command worst-case latency tied to batch fsync.

### Time-based batching
- Good: predictable flush cadence.
- Bad: adds latency floor under light load; needs a tuning knob.

## Links

- depends on ADR-0001 (append-only log) and ADR-0002 (single writer)
