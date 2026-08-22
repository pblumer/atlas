# ADR-DRAFT: Replicated partition cells for horizontal scale-out

- **Status:** Proposed
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas is currently a single-node, single-partition engine. The processor and key
model already carry a partition id, and ADR-0002 establishes one single writer per
partition, but the server opens one WAL and one state store and constructs partition
1 only. The Kubernetes chart therefore correctly fixes the StatefulSet at one
replica: a second process opening the same WAL and Pebble store would corrupt the
durable state.

Milestone 5 requires Atlas to scale beyond one node. This is two different
requirements:

1. **Horizontal throughput:** independent process instances must execute on more
   partition leaders and therefore on more CPU cores and disks.
2. **High availability:** an acknowledged command must survive a node loss, and
   another node must take ownership without replaying side effects or losing
   committed progress.

Replication alone does not increase write throughput, and partitioning alone does
not provide high availability. Atlas needs both without weakening its invariants:
single writer per partition, durable before visible, one deterministic
`applyToState`, events as facts, and no allocation or interpretation on the runtime
hot path.

ADR-0006 already requires asynchronous cross-partition communication, but it does
not decide the replicated log, placement, failover, correlation, or Kubernetes
operating model. This record provides the umbrella decision for those Milestone-5
slices while leaving individual wire formats and the final Raft library to focused
follow-up records.

The standalone single-binary mode remains a first-class deployment. Cluster mode is
additive; it must not make a small installation provision a broker, SQL database, or
Kubernetes control plane.

## Decision drivers

- Preserve invariants I1-I6 rather than introduce a separate distributed execution
  path.
- Scale aggregate write throughput by adding independent partition leaders.
- Provide zero loss for acknowledged commands under the configured Raft failure
  model.
- Keep one authoritative event order per partition and one state writer per local
  replica.
- Make cross-partition delivery durable, retryable, and idempotent without
  distributed transactions.
- Keep the common single-partition execution path free of cross-partition
  coordination.
- Reuse Atlas's event records, Pebble materialization, checkpoints, metrics, and
  exported-log machinery.
- Support Kubernetes and OpenShift placement, failure domains, rolling upgrades,
  and controlled rebalancing.
- Keep external calls and connector credentials outside partition leaders.
- Avoid making an external broker or distributed SQL database part of the engine
  durability contract.

## Considered options

1. Embedded multi-Raft partition cells, with one Raft group per Atlas partition.
2. An external replicated log such as Kafka or Redpanda as Atlas's primary WAL.
3. A distributed SQL database as the authoritative runtime state.
4. One active server with shared or storage-layer-replicated disk and
   active/passive failover.
5. Multiple active Atlas servers opening one shared persistent volume.

## Decision outcome

Chosen option: **embedded multi-Raft partition cells**, because it extends Atlas's
existing partition and event-sourcing model instead of replacing it.

A logical Atlas partition becomes a **partition cell**. A cell has a stable
partition id, one Raft group, one leader, and a configurable replica set. Three
voting replicas are the default. The leader is the only replica that accepts and
orders commands. Every replica applies committed Atlas events on one goroutine to
its own Pebble materialization; replicas never share process state or storage.

Adding replicas improves availability and may increase read capacity. Adding
partition cells and distributing their leaders across nodes increases write
throughput. A process instance remains entirely inside one partition.

### Runtime roles

Cluster mode separates four roles. They may remain subcommands of the same Atlas
binary and images of the same release.

- **Atlas Gateway:** stateless REST, MCP, and worker endpoints. It resolves the
  owning partition and forwards a request to its current leader. Multiple gateway
  replicas may run behind one Kubernetes Service.
- **Atlas Node:** a stateful process that hosts replicas from many partition cells.
  Each local replica owns its Pebble state and participates in its Raft group.
- **Atlas Operator:** a Kubernetes controller that reconciles node count, replica
  placement, membership changes, upgrades, backup policy, and partition health.
- **Atlas Worker:** a separately scalable process for service-task and connector
  side effects, following ADR-0168.

The Kubernetes API expresses desired state and placement. It is not used in the
command or event data path and is not the authority for Raft leadership.

### Stable routing and partition creation

Partition ids never change and are never derived from the current number of
partitions. Existing entity keys continue to route through the partition bits in
their high 16 bits.

For a new process instance, the catalog selects an active partition using a
load-aware placement policy. That partition mints the instance key, permanently
recording ownership in the key. Adding a partition changes placement for new
instances only; it never remaps existing keys. The cluster must not use
`instanceKey % partitionCount`, because changing the count would move ownership
without moving state.

A first implementation does not migrate a live process instance between logical
partitions. Rebalancing moves a whole partition replica or its leadership between
nodes. Live instance migration across partition ids is a separate future decision.

### Replicated event batches

Cluster mode preserves the distinction between commands, events, and state.
Commands remain intentions and are not persisted. The leader processes a command
against an invisible, rollbackable transaction and produces the same encoded Atlas
event records as standalone mode. Generated keys, timestamps, routing choices, and
evaluated results are frozen into those records before replication.

The records produced by one processor batch form one Raft proposal:

```text
Command batch
-> staged Atlas events
-> Raft event-batch proposal
-> quorum persistence and commit
-> Pebble state commit
-> local followups
-> responses, notifications, and side effects
```

The Raft storage integration must not report an entry committed to Atlas until the
entry is durably stored on a quorum. Nothing externally observable may occur before
that commit. Cluster mode therefore strengthens ADR-0005's durability point from
one local WAL fsync to a quorum commit.

On the leader, the staged transaction may be committed only after the matching
proposal commits. On followers and during recovery, the exact event records are
passed through the same `applyToState`. No follower reprocesses the original
command and no routing, key, time, expression, or BPMN decision is recomputed.

A partition initially permits one in-flight processor batch. This keeps speculative
state bounded and prevents a later proposal from depending on an uncommitted batch.
Pipelining is a future optimisation that requires an explicit speculative-state
design and benchmarks; it is not implicit in the first distributed implementation.

### The Raft log is the cluster WAL

Cluster mode does not maintain two authoritative logs. Raft entries contain Atlas
event batches and form the durable WAL suffix for that partition. Standalone mode
continues to use the current local WAL.

The storage boundary should therefore be abstracted so that the processor can use:

- the current append-and-fsync log in standalone mode; or
- a replicated Raft commit log in cluster mode.

Atlas event positions remain monotonically increasing per partition and remain
distinct from Raft term and entry index. Checkpoint manifests gain the Raft group,
term/index, partition epoch, event position, key counter, and deployment artifact
hashes needed to restore a replica safely.

Raft-log compaction is permitted only after a verified state checkpoint covers the
entries and every required consumer watermark, such as the event exporter, has
advanced past them. This extends ADR-0131 rather than creating a second snapshot
mechanism.

### Recoverable continuations

A committed event batch must not depend solely on the old leader's in-memory
followup queue. If that leader fails after quorum commit and before enqueueing a
continuation, the new leader must recover the work without persisting the original
command.

The implementation must make continuations recoverable either by:

- deriving pending work deterministically from durable transitional state on
  recovery and leadership acquisition; or
- recording explicit continuation facts as events and materialising a pending
  continuation index.

The choice requires a focused follow-up ADR and recovery tests. Persisting commands,
running side effects from `applyToState`, or relying only on leader memory are not
valid options.

### Cross-partition envelopes

Cross-partition work uses an outbox/inbox protocol, never direct state access and
never a synchronous call from one partition writer into another.

1. Source partition A records an `EnvelopeCreated` fact in the same committed
   event batch as the state change that produced it.
2. A post-commit dispatcher sends the envelope to target partition B and retries
   until acknowledged.
3. B deduplicates by a globally stable message id, for example source partition
   plus source event position.
4. B commits the inbox receipt and all resulting business events atomically in its
   own Raft group.
5. B acknowledges only after that quorum commit.
6. A records `EnvelopeAcknowledged`; retention may later remove acknowledged
   outbox state.

The transport is at least once. The effect in the target state is exactly once.
Partitions remain eventually consistent and no two-phase commit is introduced.
Pending outbox and inbox records are included in checkpoints, so a restored cluster
resumes delivery.

ADR-0075's inbound source/sequence high-water mark is a useful first pattern but
does not replace arbitrary message-id deduplication for cluster envelopes.

### Sharded message correlation directory

An arbitrary BPMN message correlation key may be created or changed after instance
creation, so a gateway cannot always derive the process partition directly from the
key.

Message correlation is therefore owned by sharded directory partitions:

```text
hash(tenant, message name, correlation key) -> directory shard
```

Subscription registrations and published messages are delivered durably to the
same directory shard. The shard stores the target partition and subscription key,
matches one publication according to BPMN and Atlas correlation semantics, and
emits an envelope to the process partition. It may retain unmatched messages under
an explicit TTL, allowing a publication that arrives before its subscription to be
matched later.

The directory is sharded and is not a global coordinator. A publish does not scan
or broadcast to every execution partition.

Call activities place a child instance in the parent's partition by default in the
first cluster slice. Cross-partition call activities use the same envelope
foundation only after their parent/child failure semantics receive a focused ADR.

### Cluster catalog

A small replicated catalog group, conventionally group 0, holds cluster-wide
metadata:

- partition membership and placement epochs;
- process definition keys, versions, and artifact hashes;
- job-type registry metadata;
- cluster feature and wire-format versions; and
- references to global design-time configuration needed by every gateway or node.

Large BPMN, DMN, form, and checkpoint artifacts may live in an S3-compatible object
store and are addressed by immutable content hash. The catalog stores the durable
reference, not large blobs in every metadata entry.

Before a node may lead or serve a partition using a definition, it must possess and
compile the referenced immutable artifacts. Compilation happens at deploy or
replica preparation time, never when a token reaches an element. Nodes verify the
artifact and compiler-version hashes before becoming ready.

The catalog coordinates placement and deployment only. Normal process commands do
not pass through group 0, so it is not the global execution coordinator rejected by
ADR-0006.

### Kubernetes and OpenShift placement

Atlas nodes run as a StatefulSet behind a headless Service. Each pod has a stable
node id and its own ReadWriteOnce volume. Replicas of one partition must be spread
across distinct nodes and, where available, distinct zones. A PodDisruptionBudget
must prevent voluntary disruption from removing a Raft quorum.

The operator performs membership changes in safe stages:

1. add a non-voting learner on the destination node;
2. restore a verified snapshot and catch up the log;
3. promote the learner through the Raft membership protocol;
4. transfer leadership when required;
5. remove the old voter; and
6. release its local state only after the new configuration is committed.

Scaling the StatefulSet down is forbidden until the operator has evacuated every
partition replica from the affected nodes. Generic HPA may scale stateless gateways
and workers, but stateful node scale-down is always operator-controlled.

A node-local PVC stores only replicas hosted by that node. No two Atlas nodes open
the same Pebble database or application WAL.

### Workers and side effects

Partition nodes do not perform connector network calls in cluster mode. Atlas
workers lease jobs through the gateway and scale independently. The engine resolves
compiled task detail and variables into a portable job payload; the worker owns its
endpoint and credential as decided by ADR-0168.

A job completion routes directly through the partition encoded in the job key.
Leases and completion tokens must be fenced across leadership changes. Duplicate
worker execution remains possible under at-least-once delivery, so connector
contracts must expose or derive an idempotency key from the durable job key where
the target system supports it.

Only the current partition leader may dispatch post-commit notifications or
cross-partition outbox records. Dispatchers are fenced by the Raft term or a
monotonic leadership epoch and stop immediately when leadership is lost.

### Reads, global queries, and observability

A point read for an instance, job, or other partition-owned entity routes by its
key. Correctness-sensitive reads use the leader or a Raft ReadIndex and wait until
the local state machine has applied that index. Explicitly stale follower reads may
be offered only as a documented query mode.

Cluster-wide lists, search, history, and analytics do not synchronously scan every
partition for every UI request. Each partition exports committed events with an
idempotent identity of partition plus event position. OpenSearch or another
replaceable projection provides the eventual cluster-wide read model, while detail
views return to the authoritative partition. Responses surface projection
watermarks so operators can distinguish current state from indexed state.

Prometheus metrics and OpenTelemetry traces include node id, partition id, Raft
group, role, term, commit index, applied index, queue depth, proposal latency,
snapshot progress, replica lag, and outbox backlog. Metrics are not part of the
consensus or state-mutation path.

### Consensus implementation boundary

This record decides the multi-Raft architecture, not the final library.

The implementation must hide the chosen library behind an Atlas-owned replicated
log boundary and retain Atlas-owned event and snapshot formats. The first technical
spike compares:

- `etcd/raft`, a stable Raft core for which Atlas supplies transport, storage,
  multi-group scheduling, and snapshots; and
- Dragonboat, a multi-group Raft implementation with integrated transport,
  storage, membership, and snapshot facilities.

The comparison must cover maintenance status, Go and Pebble dependency
compatibility, allocation behaviour, batching, failure injection, snapshot
interoperability, rolling upgrades, and throughput with Atlas-sized event batches.
Selecting and pinning the production library is a separate ADR based on that
evidence.

### Delivery sequence

Milestone 5 is delivered as vertical, independently testable slices:

1. **Local partition fabric:** one process hosts multiple independent processors,
   stores, and logs behind a router. Prove multi-core scaling before networking.
2. **Durable envelopes:** two local partitions exchange outbox/inbox messages and
   correlation registrations with duplicate and crash tests.
3. **Replicated partition cell:** three processes replicate one event-batch stream,
   fail over a leader, and prove live, follower, and replay state equivalence.
4. **Cluster gateway and workers:** route writes, reads, job leases, and completions
   across several partition cells.
5. **Kubernetes operator:** reconcile StatefulSet nodes, placement, quorum-safe
   membership, probes, topology spread, and rolling upgrades.
6. **Rebalancing, backup, and projections:** move replicas, take barriered
   checkpoints to object storage, compact safely, and serve cluster-wide search.

The standalone engine remains green and supported after every slice.

### Correctness gates

Cluster mode is not complete until deterministic tests and fault injection prove:

- an acknowledged command survives every single-node failure permitted by the
  replica count;
- no response, notification, connector call, or envelope precedes quorum
  durability;
- live leader state, follower state, and state rebuilt from snapshot plus log are
  equivalent;
- duplicate, delayed, reordered, and retried envelopes produce one target-state
  effect;
- a leader failure between commit, apply, followup scheduling, and response loses
  no continuation and exposes no uncommitted result;
- a stale leader cannot dispatch new jobs or side effects;
- membership change and Kubernetes node drain never remove quorum;
- a corrupt or incompatible snapshot is rejected and falls back safely;
- process hot-path allocation remains bounded and benchmarked; and
- aggregate throughput grows with additional partition leaders until a measured
  CPU, network, or disk limit is reached.

A deterministic simulated transport and clock are required for the normal test
suite. A black-box failure suite must additionally cover process kills, network
partitions, disk-full errors, slow followers, snapshot transfer, and operator
restart.

### Consequences

- **Positive:** Atlas gains horizontal write scale and high availability without
  abandoning its event records, Pebble state, deterministic replay, or
  single-writer execution model. Standalone mode keeps its zero-dependency
  posture. Partition placement, replica health, and failover become explicit and
  operable.
- **Negative / trade-offs accepted:** Cluster mode introduces consensus latency,
  replicated storage cost, membership and wire-format compatibility, more complex
  recovery, and a substantial operator surface. One hot process instance still
  cannot use more than one partition leader. Cross-partition behaviour is
  eventually consistent.
- **Follow-ups / risks to watch:** recoverable continuations, the consensus-library
  decision, catalog migration from local sidecar stores, correlation semantics,
  worker fencing, object-store backup security, mixed-version upgrades, and
  partition-level load skew each require focused records and adversarial tests.

## Pros and cons of the options

### Embedded multi-Raft partition cells

- Good: directly matches ADR-0002 and ADR-0006; keeps the event log and state
  machine inside Atlas; allows one node to host many groups; preserves cheap local
  execution for the common case.
- Bad: Atlas must own distributed-systems correctness, transport, membership,
  snapshots, upgrades, and an operator. A library reduces but does not remove that
  responsibility.

### External replicated log

- Good: mature broker operations, replication, retention, and ecosystem; natural
  integration stream.
- Bad: makes a broker part of every durable state transition, creates a second
  placement and ownership system, complicates atomicity with Pebble, and removes
  the standalone performance profile from cluster mode. It remains a good export
  sink, not the engine WAL.

### Distributed SQL database

- Good: mature replication, queries, transactions, and operational tooling.
- Bad: replaces the log-structured execution design, makes runtime state depend on
  SQL transactions and indexes, and undermines Atlas's compile-and-batch
  performance model.

### Active/passive server over replicated storage

- Good: smaller implementation and an easy migration from the current
  single-node server.
- Bad: improves recovery but not horizontal write throughput; storage attachment
  and fencing dominate failover; it does not satisfy Milestone 5.

### Multiple writers on one shared volume

- Good: superficially simple Kubernetes scaling.
- Bad: violates the single-writer invariant and Pebble ownership, provides no
  authoritative ordering, and risks corruption. Rejected unconditionally.

## Links

- builds on [ADR-0001](0001-event-sourcing-and-log-structured-state.md)
  (events and one deterministic state fold)
- builds on [ADR-0002](0002-single-writer-partition-model.md)
  (one writer per partition)
- extends [ADR-0005](0005-group-commit-and-fsync-strategy.md)
  (quorum commit becomes the cluster durability point)
- elaborates [ADR-0006](0006-partition-routing-and-cross-partition.md)
  (asynchronous cross-partition communication)
- depends on [ADR-0007](0007-job-worker-protocol.md)
  (leased external work and fencing)
- preserves [ADR-0011](0011-single-binary-distribution-and-web-ui.md)
  (standalone remains one binary with no runtime dependencies)
- generalises [ADR-0075](0075-clio-inbound-event-bridge.md)
  (durable inbound idempotency)
- extends [ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)
  (replica snapshots and Raft-log compaction)
- extends [ADR-0142](0142-prometheus-metrics.md)
  (partition and replication observability)
- follows [ADR-0168](0168-connector-work-on-a-worker.md)
  (connector work and credentials live with workers)
- belongs to [Milestone 5 — Scale-out](../../ROADMAP.md#milestone-5--scale-out)
- candidate consensus libraries:
  [etcd/raft](https://github.com/etcd-io/raft) and
  [Dragonboat](https://github.com/lni/dragonboat)
