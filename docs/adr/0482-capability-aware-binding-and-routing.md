# ADR-0482: Capability-aware BPMN binding and deterministic authorized Worker selection

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-25
- **Deciders:** atlas maintainers
- **Open question:** Can a later runtime-selection mode change the pinned configured Worker without adding an extra engine event, while preserving deterministic replay, approval meaning and authorization evidence? This proposal deliberately supports deployment-time binding only until that question is proven with recovery and policy tests.
- **Question checked:** 2026-10

## Context and problem statement

A BPMN service task in atlas currently addresses a concrete integration/job type. ADR-0480 adds descriptive technical capability claims, and the proposed contract/conformance programme will make some of those claims independently verifiable. The desired experience is to author an operation by its *semantics*, not by a coincidentally named provider API, and ultimately to choose a policy-authorized Worker without surrendering event-sourcing guarantees.

A verified technical contract does not identify a configured Worker, the principal in whose name it acts, the target endpoint, or whether a particular user may perform an action. These are different concepts (ADR-0203, ADR-0475). ADR-0475's initially implemented supervised delegated SharePoint path does not mean all other Workers enforce action authorization.

A runtime search of mutable Worker catalogues would make an old deployment's behavior depend on current configuration, and a retry could pick a different principal/target. If a processor did that search per command, it would violate I1/I5; if replay repeated authorization or provider discovery, it would violate I2/I4/I6.

**Question:** How can an opt-in BPMN service task ask for an exact technical capability and obtain a predictable, authorized execution target without breaking existing deployments or replay?

## Decision drivers

- Preserve all six [engine invariants](../architecture/invariants.md), especially compile-don't-interpret, single-writer, durable-before-visible, and single `applyToState`.
- Keep standard BPMN service-task semantics; atlas-specific binding metadata remains an explicit extension and survives model import/export.
- Preserve legacy job-type bindings and deployed models by default.
- Never use a descriptive or revoked claim as an executable contract.
- Do not confuse a verified Worker Type **operation** with a configured Worker, Worker Instance, execution identity or authorization.
- A denied or unavailable Worker must not silently cause a less compatible or differently privileged target to be selected.
- No credentials, personal documents or unconstrained result payloads in deployment metadata or the WAL.
- A new capability-based runtime feature requires meaningful replay, policy and retry tests before general availability.

## Considered options

1. **Dynamic name-based runtime routing:** every job queries all Workers for a matching operation name and chooses the first available.
2. **Deployment-time verified binding:** an explicit capability declaration resolves to one chosen configured Worker/operation under a frozen deployment contract; runtime uses the existing durable job path.
3. **Dynamic selection among verified candidates at each activation:** choose from a deterministic precompiled set at runtime and persist the chosen target and applicable authorization/policy facts before any effects.
4. **One universal provider-neutral Worker:** hide all provider differences behind a generic adapter.

## Decision outcome

Proposed first executable slice: **option 2**. Option 3 remains a *separate, gated extension*, not a side effect of merging the metadata, graph or UI features. This record does not change the current execution model and must be explicitly accepted before any compiler/job work.

### 1. Opt-in authoring and exact semantic identity

A capability-bound BPMN service task declares an exact technical `id + version + profile` in atlas-specific extension metadata. No free-text label, fuzzy search or implicit provider matching can influence execution.

An author can still choose an explicit configured Worker as before. Existing BPMN service tasks, job types, definitions and persisted deployments continue unchanged. The Modeler must refuse a capability-bound model it cannot resolve rather than suggest its runtime works; an ordinary BPMN import without atlas extensions has no inferred technical binding.

The extension's precise XML syntax, worker selection field, schema version, and compatibility with existing service-task properties require a separately reviewed compiler/design-time API specification as part of CDA5. Do not add a new BPMN task type or reinterpret Call Activity semantics.

### 2. Resolve once and freeze at deployment

On deployment, *off the processor hot path*, resolve the exact contract identity to:

- A published, current, **verified** Worker Type operation and its package/runtime digest.
- One explicit configured Worker reference and an immutable configuration revision identifying the intended target and permitted execution mode.
- A canonical contract digest, operation ID, job type and any required semantic profile.
- The requested identity *mode* and runtime authorization requirements, never the underlying credential or token.

If zero verified and eligible configured Workers match, reject the deployment with an actionable diagnostic. If several match, require the author/operator to select one explicitly; **do not choose the first lexically, fastest, cheapest, or most privileged**. After the decision the compiled deployment points at one immutable binding. Changing Worker configuration or verified evidence in a way that changes semantics cannot silently retarget an existing deployment; fail closed and require an explicit migration/redeployment path.

**Design-time compatibility for a later controlled replacement (new requirement, not accepted here):** [ADR-draft-controlled-worker-rebinding](draft-controlled-worker-rebinding.md) proposes a stable, deployment-scoped logical binding slot with separately versioned, durably authorized target revisions for **future jobs** of an existing opt-in deployment. CDA6 must not paint this extension into a corner by making a concrete Worker ID the *only* persistent task identity. The current proposed CDA6/CDA7 route nevertheless remains pinned; the new feature is **CDA8**, requires its own accepted ADR and event/recovery tests, and cannot silently alter jobs or deployments.

No provider network calls, schema parsing, authorization requests or string-based discovery are permitted in the processor hot path. Do not introduce a new cross-partition state read or a Worker-instance scheduling shortcut.

### 3. The runtime authorization seam

The immutable binding is only a target, **not a permission**. A Worker Instance still acquires a properly fenced lease through the existing job protocol. Before a governed external action, follow ADR-0475's authorization path: trusted server-side principal/context, exact action and parameter digest, Worker and Worker Instance, policy version, explicit permit/deny decision, and durable authorization evidence.

Only after the relevant decision is on the WAL and the batch has completed `fsync` may a token be brokered, an external call be made, or a notification claim success. On denial, missing identity, revoked contract/evidence, incompatible configuration, absent broker or stale lease, **fail closed** and surface an incident or documented BPMN error according to existing semantics; never try another Worker as an unapproved fallback.

ADR-0475 is Proposed / Partial. Supervised delegated SharePoint support is not evidence that Entra, LDAP, Google Contacts or an arbitrary external Worker have the same enforcement. A first governed capability-bound operation must be tested end-to-end with its real enforcement seam *before* enabling the binding mode for that Worker Type.

### 4. Durable selection and replay boundaries

For the proposed **deployment-time pinned** slice, the selected Worker, operation, contract version/profile/digest and immutable configuration revision form part of deployment metadata, which is immutable for running instances. Existing engine job creation and authorization events remain the authority for observed execution.

If a subsequent option-3 dynamic selector is adopted, the chosen configured Worker, binding revision, identity/policy context and route decision must be captured in a durable, versioned event **before** leasing or exposing the choice. Replay folds that fact through the existing `applyToState`; it must never query current Worker lists, re-evaluate policy, consult wall time, or generate another choice. The new event and its recovery format require an additional accepted ADR or accepted amendment to this proposal, with a migration story. It is not authorized by the present record.

Persisting the target does not solve the at-least-once side-effect problem: provider success followed by a crash can still cause a retry. The contract's idempotency and compensation semantics, provider-side request keys where supported, lease fencing and incident policy determine whether a retry is allowed.

### 5. Policy, privacy and version upgrades

- Identity linkage, action policy and delegated consent are governed by ADR-0475; capability verification never overrides denial.
- Package/contract upgrade, evidence withdrawal, Worker reconfiguration and policy changes have separate semantics. Existing deployments may continue only if the required pinned contract and a still-valid **authorized** target remain available under the approved compatibility policy; otherwise they stop visibly. Never "upgrade" a running process merely by changing a catalogue row.
- The WAL carries only bounded facts required to reproduce execution and audit, not credentials, authorization tokens, documents or personal source payloads. The privacy classification and retention of any new authorization metadata must be reviewed before enabling it.
- Capabilities are not automatically substitutable even when provider request/result schemas match. Profile, identity scope and conformance evidence must all match.

### 6. Delivery gates

CDA6 first delivers authoring and **deployment validation**, without executable capability routing: reject ambiguous/unverified claims and show the frozen binding plan. CDA7 then enables the first *narrow* governed executable path only after:

1. Contract evidence is current under [ADR-0483](0483-technical-capability-conformance-governance.md).
2. ADR-0475's relevant provider enforcement and identity boundary are in place, with fail-closed negative tests.
3. Tests prove no allocation or string/schema resolution was introduced into processor commands.
4. At least one live/WAL-replay equivalence test verifies deployments, worker replacement/revocation, denied/approved action, restart, retry and lease fencing, plus that side effects are never repeated by replay.
5. Explicit compatibility tests demonstrate that all existing service tasks run unchanged.
6. A public API/MCP/Modeler surface, if provided, reports candidate versus verified versus executable separately, with role/visibility filtering and no false claims.

A future dynamic selector must first settle the **open question above**, prove the chosen route can be persisted as an event before any visibility, and publish its own recovery/upgrade rules. No new provider-specific shortcut is permitted.

### Consequences

- **Positive:** Process authors can request exact semantics without treating generic CRUD methods as equivalent.
- **Positive:** First executable slice reuses existing deterministic job routing and ADR-0475's governing seam instead of inventing an unsafe dynamic router.
- **Negative:** Deployment-time pinning does not offer instantaneous fallback or load balancing across configured Workers; ambiguity requires explicit human choice. A later explicit operator-driven rebind is a separate, not-yet-approved capability (CDA8), **not** automatic fallback.
- **Follow-ups / risks:** Approval of ADR-0480, contract governance, Worker Type installation/version pinning (ADR-0208), provider-specific identity enforcement (ADR-0475), the exact BPMN extension and recovery tests. True policy-gated dynamic selection is explicitly *not yet decided*.

## Pros and cons of the options

### Option 1 — Name-based dynamic routing
- Good: Little design-time setup.
- Bad: Nondeterministic target, false semantic equivalence, policy bypass and replay drift.

### Option 2 — Pinned verified target at deployment (proposed)
- Good: Explicit, reproducible, compatible with existing job/Worker boundaries.
- Bad: No transparent live failover; deployment changes require review and redeployment.

### Option 3 — Dynamic event-frozen selection
- Good: Potential operational resilience and policy-aware candidate choice.
- Bad: Requires new durable routing events, tested deterministic selection rules, lease/fencing semantics and an explicit authorization design.

### Option 4 — Universal adapter
- Good: Uniform authoring.
- Bad: Masks resource identity, permission and side-effect differences; expands the runtime surface.

## Links

- [CDA programme and gates](../planning/capability-driven-automation-program.md)
- [ADR-0480 — Technical capability claims](0480-technical-capability-contract.md)
- [ADR-0483](0483-technical-capability-conformance-governance.md)
- [ADR-draft-controlled-worker-rebinding — proposed no-redeploy replacement](draft-controlled-worker-rebinding.md)
- [ADR-0203 — Worker execution](0203-worker-execution-model.md)
- [ADR-0208 — Worker Type packages and trust](0208-worker-type-packages.md)
- [ADR-0420 — Unified graph](0420-one-graph-model-several-projections.md)
- [ADR-0475 — Identity-aware Worker actions](0475-identity-aware-worker-execution.md)
- [ADR-0001 — Event sourcing and replay](0001-event-sourcing-and-log-structured-state.md)
- [ADR-0004 — Compile BPMN at deployment](0004-compile-bpmn-to-indexed-graph.md)
