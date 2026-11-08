# Scale-out readiness: mandatory design constraint

Atlas must be able to run its services across multiple nodes without redesigning every feature introduced while the engine is still single-node. **Every new feature and architectural change must be assessed for horizontal scale-out compatibility before implementation.** This is a design and review constraint, not a claim that clustering has already shipped, and it is not a replacement for invariants I1–I6.

## Authority and status

- [ADR-0175](../adr/0175-replicated-partition-cells.md) describes the **proposed, not yet accepted or implemented** multi-Raft partition-cell target, including gateway, node, operator and Worker roles.
- [ADR-0002](../adr/0002-single-writer-partition-model.md) and [ADR-0006](../adr/0006-partition-routing-and-cross-partition.md) establish the accepted single-writer and asynchronous partition-boundary rules.
- [Milestone 5](../../ROADMAP.md#milestone-5--scale-out-) contains the future implementation work.
- The [six runtime invariants](invariants.md) remain binding. Scale-out readiness is a **cross-cutting design constraint**, not a seventh runtime invariant or permission to change an accepted ADR.
- The standalone single-binary deployment remains a first-class product. No new cluster dependency may become mandatory in standalone mode merely to anticipate scale-out.

Decisions still open in ADR-0175 (including the consensus library, recoverable continuations, and some cross-partition semantics) must **not** be silently settled by a feature PR. Escalate an incompatible design, document alternatives and trade-offs, and obtain an accepted ADR before changing an architectural contract.

## Design review: apply to every feature

For each area below, explain either **Compatible**, **Requires an explicit seam/follow-up**, or **Not applicable (with reason)**. A feature does not need to implement clustering now, but must not bake in an assumption that makes clustering require a costly rewrite later.

| Concern | Scale-out-ready design rule |
| --- | --- |
| State ownership | Identify the authoritative owner and its lifetime. Process execution state is partition-owned; do not add process-wide shared mutable state or assume that every entity is available in the current process. For global/design-time metadata, describe the eventual catalog or replicated-store boundary instead of making a host-local directory an implicit cluster-wide source of truth. |
| Stable identity and routing | Entity keys carry a stable partition identity; expanding the partition count must never remap existing instances. Never route with `instanceKey % partitionCount` or use the node or pod address as an entity identity. Use an explicit partition owner/router boundary. |
| Service boundaries | HTTP/MCP and interactive services should be capable of running as stateless gateways where practical. Keep transport, ownership resolution, and partition-local execution separable; never require handlers to access a different partition's in-process fields. |
| Durable cross-partition effects | Cross-partition workflows are asynchronous and event-driven. Plan for durable outbox/inbox delivery, stable message IDs, deduplication, retries, and crash recovery. Do not add synchronous calls from a partition writer to another partition or assume exactly-once network delivery. |
| Durability and leadership | No response, notification, Worker dispatch, or other externally visible side effect precedes the authoritative durability point. Today this is local WAL fsync; the proposed cluster mode would use quorum commit. Followups and side effects must survive or be fenced across leader failover; no correctness-critical continuation may exist only in one process's memory. |
| Workers and external systems | Keep external I/O outside the processor. Support independently scalable Worker Instances, idempotency/retries, and leadership/lease fencing; do not tie a Worker to the lifetime or memory of one gateway/node. |
| Queries, search and UI | Distinguish partition-owned authoritative reads from cluster-wide projections. Do not make instance-count-dependent global scans or synchronous scatter/gather the default UI query path. Design pagination, watermarks, freshness/consistency and access-control enforcement explicitly. |
| Deployment, storage and operations | Separate local replica files from shared immutable artifacts and cluster metadata. Do not share a Pebble DB or WAL between nodes. Consider node replacement, placement, tenant boundaries, backup/restore, upgrade/version compatibility, observability and operator-driven stateful scale-down. |
| Standalone compatibility | Preserve a fast, low-dependency single-node path behind the same public semantics. New abstractions should not add remote RPC, network coordination or Raft overhead to every standalone command. |

These constraints apply beyond `engine/`: new API services, background work, Tasks, Operations, Insights, the Modeler, atlas hub, identity/policy stores, connectors/Workers, and external integrations must all consider their state owner and multi-node behaviour.

## Required change-plan and PR evidence

Before implementing a feature, record:

1. **State owner:** what is durable, transient, partition-local, service-local, or globally shared?
2. **Routing and boundary:** how will requests, jobs and reads find the correct owner after placement/leadership changes?
3. **Failure and retry:** what happens on restart, duplicate delivery, partial commit, failover and stale dispatch?
4. **Consistency and visibility:** what is authoritative, what may be eventually consistent, and when may effects become visible?
5. **Standalone and migration:** what interface lets today's implementation be replaced by distributed placement/replication without changing the feature contract?
6. **Verification:** what tests or review evidence prove the current invariants, and what cluster-specific test is deferred to Milestone 5?

Use the `Scale-out readiness` section of the [PR template](../../.github/PULL_REQUEST_TEMPLATE.md). State **Compatible**, **Requires follow-up** (link an issue/ADR and the boundary maintained now), or **N/A** (reason). A naked "N/A" or "we will fix this when we cluster" is not an adequate assessment for stateful or distributed-service changes.

For implementation touching persistence, events, processor, jobs, messages or state mutation, keep the existing deterministic recovery tests. When cluster infrastructure becomes available, add failover, duplicate/reorder, fencing and leader/follower/replay equivalence tests at the corresponding Milestone-5 slice. Do not imply these tests pass before the infrastructure exists.

## Examples of design mistakes to reject now

- Routing an existing instance via `instanceKey % N`, where `N` is the active partition count.
- Keeping a durable pending continuation only in a goroutine/channel that disappears on leader failure.
- Making a new API's authoritative data live only in a particular gateway process's memory.
- Performing a network call or another partition's synchronous state operation from the single writer.
- Requiring a shared filesystem/Pebble database to scale node replicas.
- Building global Operations/Insights views by blocking a partition writer while all instances are scanned.
- Treating a locally configured Worker or sidecar store as globally authoritative without a portable contract.

**If a design cannot satisfy the guardrail:** do not conceal the debt. Describe the conflicting requirement, options, a migration seam, explicit acceptance criteria and an ADR or tracked Milestone-5 dependency. Architecture review decides whether to proceed.
