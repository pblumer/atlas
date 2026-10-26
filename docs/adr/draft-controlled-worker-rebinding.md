# ADR-DRAFT: Controlled configured-Worker rebinding without process redeployment

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-25
- **Deciders:** atlas maintainers
- **Open question:** What coordination and effective-revision acknowledgement across partitions is sufficient for a controlled rebinding rollout without pretending that a multi-partition change is atomic? This proposal uses per-partition durable cutovers and a visible rollout state; a global atomic switch is explicitly out of scope pending proof.
- **Question checked:** 2026-10
- **Review deferred:** The maintainer deferred this record's review questions (binding-slot scope, future jobs only, partial partition cutover, CDA6 slot reservation) until CDA7 has landed. Only the binding-slot scope may be settled earlier, when CDA6 starts; until then this record guides CDA6's compatibility and authorizes no implementation.

## Context and problem statement

[ADR-0203](0203-worker-execution-model.md) distinguishes a **Worker Type** (implementation), configured **Worker** (target and identity/configuration), and **Worker Instance** (job consumer). Adding, replacing or restarting Worker Instances for the *same* configured Worker is a different problem from changing which configured Worker receives future work.

[ADR-0482](0482-capability-aware-binding-and-routing.md) proposes an exact technical contract with one configured Worker pinned during deployment. This is a safe first execution design, but leaves a product gap: operators may need to replace a failed endpoint, change a service identity, migrate to another verified implementation, or rotate a target without redeploying the BPMN model or restarting every long-running process instance.

A plain mutable name/alias lookup at each job activation is not a solution. It would let old deployments silently acquire new target/identity semantics, make replay depend on the present catalogue, and create a potential policy bypass. An already leased job may have contacted the old provider and therefore cannot be safely "reassigned" as if no external effect had occurred.

**Question:** For explicitly opted-in capability-bound service tasks, how may an authorized operator change the configured Worker for *future* work of an existing deployed process version, without changing its semantic contract or compromising durable routing, authorization, or recovery?

## Decision drivers

- Keep immutable BPMN/compiled deployments and legacy explicit Worker bindings intact.
- Preserve all six [engine invariants](../architecture/invariants.md): no per-command allocation, durable-before-visible, partition single writer, deterministic replay via one `applyToState`, compilation before execution, and fact-bearing events.
- Enforce exact contract identity and independently reviewed conformance per [ADR-0483](0483-technical-capability-conformance-governance.md); a manifest claim alone is never enough.
- Make target-system identity, tenant/resource scope and policy effects visible to a human before the switch.
- No unapproved fallback after a Worker error, denial, missing identity, expired evidence or lease timeout.
- Keep work created before the switch bound to its original durable decision.
- Permit controlled rollback as a new recorded decision, not by rewriting the previous history.

## Considered options

1. **Redeploy the process** for every configured Worker change.
2. **Mutate the Worker reference in stored compiled deployments** or rename/replace the target behind a reused Worker ID.
3. **Use a mutable, runtime-discovered Worker alias**, resolving the latest Worker for each lease.
4. **Introduce an explicitly opted-in logical binding slot and durable, versioned operator-driven rebinding revisions**. A new job freezes the binding revision and target in its durable routing facts; work already created does not move.

## Decision outcome

Proposed choice: **option 4**, as a **separate delivery slice after the initially pinned, governed execution path** in ADR-0482. This draft does not authorize changes to current engine routing or create an API.

The intended product capability is **controlled rebinding without BPMN redeployment**, **not** automatic dynamic worker selection, a global failover mechanism, or transparent re-execution of already-started external actions. Acceptance of this proposal and the required event/recovery design are gates before implementation.

### 1. Distinguish the three kinds of replacement

| Change | Proposed behavior |
|---|---|
| Worker Instance replaced; same configured Worker and contract | Continue using the established lease/fencing protocol (ADR-0203); no capability rebinding |
| Configured Worker A replaced by configured Worker B for an opt-in binding | Explicit authorized rebinding revision for **future job creation**, with no BPMN redeployment |
| Worker B chosen autonomously per job from candidates | **Out of scope**; needs another accepted ADR covering event-frozen selection, policy and recovery |

### 2. Immutable model with a versioned binding slot

A capability-bound Service Task names an exact `(id, version, profile)`
**semantic contract** plus a stable **logical binding slot**. The slot is a
deployment-scoped identity (process deployment version + BPMN element ID),
not a new global Worker Type or a display name.

The initial deployment validates conformance, target scope and intended
identity mode, and creates **binding revision 1** with one explicitly
selected configured Worker. Existing job-type indices and compiled graph
remain immutable. The compiled graph references a precomputed integer slot
index; the partition-owned, persistently materialized slot state holds the
currently effective revision. There is no per-job schema parsing, dynamic
name lookup, provider discovery or cross-partition shared mutable cache.

A later rebind **does not edit compiled process metadata or past revisions**.
It creates a new, immutable binding revision for the same semantic contract.
The operation is opt-in for new capability-bound deployments; legacy service
tasks keep their present behavior unless explicitly migrated through a
separately approved compatibility path.

### 3. Preconditions and operator experience

A privileged operator requests a switch from Worker A to Worker B, with an
`expectedRevision` (optimistic concurrency), reason and effective deployment
scope. The Console should offer **Preview -> Validate -> Approve -> Apply ->
Observe**, including old/new Worker identity, Worker Type package/digest,
contract/profile, endpoint/tenant/resource scope, credential mode, policy
requirements, pending/leased jobs and the expected cutover boundary.

Before any mutation, validate:

- The **same exact immutable technical contract ID/version/profile**, with
  current, independently reviewed conformance evidence for B's operation.
  Any compatibility across distinct versions/profiles needs its own reviewed
  rule, not an inferred semantic upgrade.
- Worker Type/version trust and installed operation/job type compatibility
  (ADR-0208), the configured Worker revision and its target scope.
- Authorization of the **operator's administrative rebind action** and the
  future external actions under the new principal/target (ADR-0475). A
  valid contract is not an authorization grant.
- Existing jobs' outstanding leases, authorization approvals, provider
  idempotency and side-effect ambiguity. A new Worker may point to another
  tenant and therefore to **different business resources**, even when
  its schema and technical contract are identical.

If the replacement changes the logical tenant, resource identity or other
business meaning, reject the transparent change or demand an explicit,
reviewed migration plan; do not call it compatible solely because schemas
match. Never transfer an earlier approval or credential to Worker B by
default. No secrets or personal source payloads enter the binding record.

If the operator, policy, package/evidence or target is invalid, **deny**
the rebind without changing the effective revision. Multiple eligible
Workers do not result in first-match selection.

### 4. Durable cutover boundary

Define new, versioned *facts* (exact Go event names and field encodings are
an implementation decision subject to ADR review):

1. An approved replacement intent produces a **new binding revision**
   recording old/new configured Worker references, precise contract
   identity/digest, immutable configuration/package revision, authorized
   actor/decision reference, reason code and target scope.
2. The owning partition processes a rebind command and durably commits the
   new effective revision in the normal event -> WAL -> fsync -> state
   order. Only then may the API acknowledge that partition's cutover or
   expose any resulting new work.
3. Any **new JobCreated after that partition's effective-cutover event**
   freezes the chosen configured Worker, operation and binding revision
   as durable routing facts (or through an equivalent explicitly versioned
   event before any lease). Replay reads those facts through
   `applyToState` and never queries today's Worker catalogue.
4. Jobs created **before** that cutover, whether queued or leased, retain
   their original target and authorization context. Failed/leased jobs
   cannot silently move to B; they must complete, drain, park as an
   incident or undergo a **separate explicit, fenced and safely
   reconciled job-migration** operation, if such a feature is later
   approved. At-least-once provider effects are not erased by fencing.
5. Rolling back means creating **revision N+1** pointing to the previous
   Worker after fresh validation, not deleting N or mutating older events.

No routine state mutation or network side effect may be put outside the
single-writer/one-apply path. The active revision lookup must be
integer-indexed and allocation-free in the processor cycle.

### 5. Partition scope, availability, and failure semantics

A deployment may run in several partitions. A coordinator may send
**asynchronous, partition-targeted** rebind commands and track their
durable acknowledgements, but may never read/write a second
partition's mutable state directly. A partially applied rollout is a
real **visible state**; it is not an atomic, all-partition switchover.
During it, jobs created on acknowledged partitions may use B while
jobs on others still use A. The operator must see this and may wait for
a drain/cutover barrier where workload consistency demands it.

Failure handling: a missing/revoked Worker, unavailable provider, denied
action, uncertain external effect or stale lease yields an explicit
incident/policy outcome. It does **not** trigger automatic selection of
another Worker. Withdrawing conformance evidence can make **both**
the current target and a proposed target ineligible; the system must
fail closed rather than silently retarget.

Runtime authorization remains **per external action** as required by
ADR-0475; operator permission to rebind does not authorize the job.
When a new target is effective, policy decisions must be based on that
target and must not reuse old target-specific approvals. External calls
remain after `fsync`, with at-least-once, idempotency and incident
semantics honestly disclosed.

### 6. Admission, observability and compatibility gates

Expose rebind intent, validation failures, binding history, effective
revision per partition, pending/leased old-target jobs, authorizer and
rollout state through the authorized operator API and Operations UI.
Do not expose this capability in the Modeler, Console, MCP or public
feature marketing until each surface has a real supported backend and
source-specific permission filtering. MCP administrative actions require
their own explicitly reviewed permission and trigger model; no direct
mutation of engine state through tools.

An initial implementation must prove:

1. **Same-Worker Instance replacement** preserves existing lease/retry
   behavior without invoking the rebinding feature.
2. **A -> B without redeployment:** new jobs use B only after the
   partition-local fsynced switch; old process instances remain valid.
3. **Already queued, leased, authorized or provider-called jobs**
   remain bound to A or produce an explicit incident; no silent retry
   against B after a crash or timeout.
4. **Negative checks:** missing/stale evidence, contract/profile mismatch,
   target/tenant mismatch, privileged-identity change, denied operator
   or job policy, multiple candidate ambiguity, and concurrent
   `expectedRevision` conflict all fail closed.
5. **Crash/restart/replay:** state equality for initial bind, cutover,
   rollback, interrupted multi-partition rollout, post-fsync visibility,
   lease epoch fencing, and no external effects during replay.
6. **Compatibility/performance:** legacy service tasks and deployed
   models work unchanged; processor hot-path allocation remains zero;
   no runtime catalogue/schema parsing and no cross-partition state access.
7. **Operational proof:** dry-run preview, audit history, incomplete
   rollout and stuck old-target jobs are visible and actionable.

### Consequences

- **Positive:** Long-running processes can adopt an authorized, verified
  replacement Worker without republishing BPMN, while each job keeps a
  stable auditable execution target.
- **Positive:** Operators control cutover and rollback; no automatic
  fallback or misleading claim of exactly-once external effects.
- **Negative:** Introduces durable binding revisions and event/state
  evolution, migration and versioned API requirements; cross-partition
  rollout may be temporarily mixed.
- **Follow-ups / risks:** ADR-0482's first pinned binding must reserve
  a stable slot without promising that this proposed mutable-revision
  mode already works. Exact event representation, rollout barriers,
  approval migration and old-job reconciliation need implementation
  review and deterministic tests before acceptance.

## Pros and cons of the options

### Option 1 — Redeploy for every target change
- Good: Minimal engine change; binding remains frozen per deployment.
- Bad: Poor operations for long-running processes; no continuity of the
  existing deployed process version.

### Option 2 — Mutate deployments/configured Worker identity
- Good: Superficially simple.
- Bad: Rewrites history, hides authorization/target changes and makes
  replay depend on mutable configuration.

### Option 3 — Resolve a mutable alias at every activation
- Good: Looks flexible.
- Bad: Unversioned runtime interpretation, difficult recovery, potential
  policy bypass and hidden failover.

### Option 4 — Operator-authorized binding revisions (proposed)
- Good: Stable semantic contract, durable cutover and accountable operations.
- Bad: Requires new replayable facts, versioning, staged rollout and explicit
  old-job handling.

## Links

- [CDA delivery programme](../planning/capability-driven-automation-program.md)
- [ADR-0203 — Worker model](0203-worker-execution-model.md)
- [ADR-0208 — Worker Type lifecycle](0208-worker-type-packages.md)
- [ADR-0480 — Declarative capability claims](0480-technical-capability-contract.md)
- [ADR-0482 — Deployment-pinned capability binding](0482-capability-aware-binding-and-routing.md)
- [ADR-0483 — Conformance governance](0483-technical-capability-conformance-governance.md)
- [ADR-0475 — Governed Worker execution](0475-identity-aware-worker-execution.md)
- [ADR-0001 — Event sourcing](0001-event-sourcing-and-log-structured-state.md)
