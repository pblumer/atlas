# ADR-0002: Single-writer partition model

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

The processor mutates process state and emits events. The obvious way to "go fast" in Go is to parallelize: many goroutines processing commands concurrently. But concurrent mutation of shared process state requires locking, and lock contention on hot state plus cache-line bouncing across cores typically *costs* more than it gains for this workload. It also makes execution non-deterministic, which complicates recovery.

We need a concurrency model that is fast, deterministic, and recovery-friendly.

## Decision drivers

- Throughput on realistic workloads (not microbenchmarks)
- Deterministic execution (for replay-based recovery)
- Simplicity of reasoning (no lock hierarchies, no data races)
- Linear horizontal scalability

## Considered options

1. **Multi-threaded processor with locks** over shared state
2. **Single-writer per partition**: one goroutine processes commands sequentially; scale by adding partitions
3. **Actor model** with fine-grained actors per instance

## Decision outcome

Chosen option: **single-writer per partition.** Each partition has exactly one goroutine that processes its commands sequentially. Horizontal scale comes from running N independent partitions, each with its own queue, processor, WAL, and state store. A process instance lives entirely within one partition; routing uses the partition encoded in its key.

### Consequences

- **Positive:** No locks, no mutex contention, no data races on process state. State access stays on one hot core (good L2/L3 behavior). Execution is deterministic, making recovery a trivial replay. Scaling is close to linear in cores via partition count. Backpressure is natural (a full queue slows producers).
- **Negative / trade-offs accepted:** A single instance cannot use more than one core's worth of CPU — but instances are independent, so aggregate throughput still scales. Cross-partition interactions (message correlation, call activities spanning partitions) require explicit message passing rather than shared memory.
- **Follow-ups:** Cross-partition communication design (ADR-0006); partition rebalancing strategy (roadmap).

## Pros and cons of the options

### Multi-threaded with locks
- Good: uses many cores for one logical stream.
- Bad: contention, cache bouncing, non-determinism, hard-to-debug races; often slower in practice for stateful hot paths.

### Single-writer per partition
- Good: lock-free, deterministic, cache-friendly, linearly scalable by partitions.
- Bad: one instance is single-core bound; cross-partition needs explicit messaging.

### Actor model
- Good: conceptually clean isolation.
- Bad: scheduling/mailbox overhead, allocation pressure, less predictable batching for group commit.

## Links

- pairs with ADR-0001 (event sourcing) and ADR-0005 (group commit)
- constrains ADR-0006 (cross-partition)
