# ADR-0006: Partition routing and cross-partition communication

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

The single-writer model (ADR-0002) scales by partitioning. This raises two questions: how is work routed to a partition, and how do the rare interactions that cross partition boundaries (message correlation to an instance in another partition, a call activity whose child instance lands in another partition) work without reintroducing shared state and locks?

## Decision drivers

- Cheap, deterministic routing
- Preserve the single-writer guarantee (no cross-partition shared mutation)
- Correctness of cross-partition message delivery
- Keep the common case (no cross-partition interaction) fast

## Considered options

1. **Hash routing on a correlation key** with synchronous cross-partition calls
2. **Hash routing with asynchronous message passing** between partitions
3. **A global coordinator** that serializes cross-partition operations

## Decision outcome

Chosen option: **partition encoded in the entity key; cross-partition interactions handled by asynchronous command/message passing, never shared memory.**

Routing: the partition is in the high bits of every key, so resolving the owning partition is a bit-shift. New instances are assigned a partition (round-robin / hash of a business key); everything about that instance stays there.

Cross-partition: when partition A must affect an entity owned by partition B, A emits a *message* (itself a durable event in A's log) that is delivered to B's command queue. B processes it as an ordinary command. No partition ever touches another's state directly.

### Consequences

- **Positive:** The single-writer guarantee is preserved end-to-end. The common case (no cross-partition work) pays nothing. Cross-partition delivery inherits the durability of the log on both sides.
- **Negative / trade-offs accepted:** Cross-partition operations are eventually consistent and require an at-least-once delivery + idempotency mechanism (dedupe on message id). Message correlation that could match instances in several partitions needs a correlation index strategy (e.g. partition by message correlation key so a message routes to exactly one partition).
- **Follow-ups:** Inter-partition transport (in-process channel now; networked later for multi-node); dedupe/idempotency for delivered messages; correlation-key partitioning rules. Detailed design is on the roadmap (multi-node milestone).

## Pros and cons of the options

### Synchronous cross-partition calls
- Good: simpler mental model.
- Bad: reintroduces blocking/coordination into the single writer; hurts throughput and liveness.

### Asynchronous message passing
- Good: preserves single-writer; durable on both ends; scales.
- Bad: eventual consistency; needs idempotency and a correlation strategy.

### Global coordinator
- Good: strong ordering across partitions.
- Bad: central bottleneck; defeats the point of partitioning.

## Links

- constrained by ADR-0002 (single writer)
- builds on ADR-0001 (durable log on both ends)
