# ADR-0480: Technical capability claims on Worker Type operations

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-24
- **Deciders:** atlas maintainers
- **Open question:** What reviewed, versioned canonical contract and provider conformance evidence are sufficient to classify two operations as substitutable? This first slice makes no interchangeability claim; a future ADR must settle equivalence before routing.
- **Question checked:** 2026-10

## Context and problem statement

atlas already models business capabilities and value streams as design-time records (ADR-0305), links them to Panorama views (ADR-0308), executes durable jobs via Worker Types, Workers and Worker Instances (ADR-0203), and has an accepted Worker Type package contract (ADR-0208). Operations in `workertype.Manifest.Spec.Operations` currently name an operation and its job type, but do not state what **technical capability** it offers.

The same word *capability* currently appears at two different levels:

- A **business capability** is an implementation-independent responsibility owned by the business. It is held in `api/capability` and may be realised manually, by a system, by a worker or by a process.
- A **technical capability** is a specific, versioned operation contract an implementation claims to offer. It belongs to the Worker Type operation that implements it, not to a second business-capability register.

Microsoft Entra ID, LDAP and Google Contacts motivated this question. Each exposes people or group-shaped data, but an Entra user is an account with authentication and tenancy semantics, an LDAP entry is a schema-specific directory entry, and a Google contact is generally not an account. Similar verbs do not make their operations interchangeable.

ADR-0305 expressly excluded Level-5 integration capabilities **as independent record kinds** because Worker Types already provide that level. ADR-0420 proposes a unified, *read-time derived* graph of atlas resources, not a new database. ADR-0475 establishes an identity-aware, policy-gated execution seam, partly implemented. Any technical capability contract must respect all three decisions rather than quietly supersede them.

**Question:** How can atlas describe technical capabilities generically and progressively, across Worker Types, without duplicating state, overclaiming equivalence, making authorization depend on untrusted metadata, or changing the execution path before its semantics have been proved?

## Decision drivers

- Keep business capabilities distinct from technical operation contracts and Worker Type / Worker / Worker Instance identities.
- Introduce no additional design-time store, run-time cache or graph database.
- Preserve old manifests, deployed BPMN definitions, job type indices, leases, event formats and recovery.
- Validate metadata once at admission, not in the processor hot path (I1, I5).
- Do not infer provider equivalence from CRUD verbs or similar-looking resource fields.
- Do not let metadata become an authorization grant. ADR-0475 policy and server-side identity remain authoritative.
- Support bundled and external Worker Type packages through the existing versioned manifest (ADR-0207/0208).
- Keep eventual discovery/graph projections scoped by the source area's existing visibility rules (ADR-0420).

## Considered options

1. **One universal Directory Worker Type.** Normalize Entra, LDAP and Google into a single runtime adapter and hide individual operations.
2. **A new persisted integration capability register.** Store technical capability records and realization links beside the business capability register.
3. **An optional versioned claim on each Worker Type operation**, leaving provider operations, identity, routing and persistence unchanged.
4. **Immediate capability-based job routing.** Change BPMN service-task authoring and job assignment to route by capability rather than by a configured Worker.

## Decision outcome

Chosen for the first vertical slice: **option 3 — optional, versioned, design-time capability claims on existing operations.**

### 1. The first contract is descriptive, not executable

A Worker Type manifest's existing operation accepts an optional `capabilities` array:

```json
{
  "id": "add-entry",
  "jobType": "io.example.ldap",
  "capabilities": [
    {
      "id": "io.example.directory.entry.create",
      "version": "1.0.0",
      "profile": "ldap-entry"
    }
  ]
}
```

The entries have three attributes:

| Field | Meaning |
|---|---|
| `id` | Lower-case dotted, namespaced technical capability identifier. Unlike a Worker Type, this names *what the operation claims*, not *who implements it*. |
| `version` | Exact semantic version of the claimed contract. It is not the package version, job protocol version or provider API version. |
| `profile` | Optional lower-case kebab-case semantic profile for provider constraints. Omitting it is **not** a claim of generic interoperability. |

The list is optional. Parsing and validation reject malformed identifiers, invalid versions, malformed profiles and duplicate `id@version` claims **within the same operation**. Legacy manifests with no claims retain their existing meaning, JSON representation and compatibility checks.

This slice deliberately does **not** introduce a canonical contract registry, shared resource schema, provider conformance suite, runtime routing, new job type, or policy integration. A declaration is useful for architecture review and discovery, but is not yet a guarantee. Cross-provider equivalence, input/output conversion, idempotency, error mappings and consistency guarantees are separate properties to prove in later slices.

### 2. One owner for each fact

- `workertype.Manifest` owns externally packaged Worker Type metadata. `workertype` parses and validates it.
- Existing built-in operation catalogues remain authoritative for their provider operation lists; a subsequent slice must derive any built-in technical claim projections from them rather than maintain parallel operation tables.
- `api/capability` continues to own **business** capabilities and value streams. No `parent` or new integration register is added.
- `api/panorama` and, if accepted, ADR-0420's `api/graphmodel` expose **read-time** associations; no Graph/BPMN/Worker facts are copied into another mutable store.
- The configured Worker owns target/identity, while the Worker Instance is the current job consumer. Neither identity nor secrets belong in a capability claim.

### 3. What interoperability would require

Before two operations may be used as substitutes under one contract, a later decision must specify and test at least:

1. Resource semantics: person vs. account vs. entry, identifiers, tenancy, lifecycle and optional attributes.
2. Typed request/response schemas, null/absent behavior, pagination, ordering and bounded payloads.
3. Errors and negative outcomes: not-found, duplicate, insufficient privileges and partial failures.
4. Side effects and safety: idempotency keys, retry behavior, eventual consistency and compensation.
5. Conformance evidence and governance for `id` / `version` / `profile` assignments, including third-party packages.

Entra `create-user`, LDAP `add` and Google Contacts `create-contact` must **not** initially be assigned a shared `directory.user.create` claim merely because each can create a person-shaped object.

### 4. Graph and Panorama are projections, not routing authorities

Once claims can be discovered, a graph projection may expose:

```text
value-stream --has-stage--> stage --uses--> business-capability
business-capability --realized-by--> process
process --uses-operation--> worker-type-operation
worker-type-operation --declares--> technical-capability
configured-worker --is-of-type--> worker-type
```

The precise edge vocabulary and node identity belong to ADR-0420, which is **Proposed**, not implemented. The graph must distinguish authored, derived and observed edges; never infer that a BPMN process implements a business capability solely because it uses an operation. Hidden nodes must not leak through counts or traversal.

### 5. Runtime routing and policy are separate decisions

The manifest contract must **never** authorize execution. A future capability-aware binding would first resolve a candidate Worker and semantic profile against an immutable deployment/configuration snapshot, then apply ADR-0475's trusted identity and policy evaluation at the authorization seam. The chosen target, contract/profile, identity, policy version and decision must be durably auditable where necessary. External side effects remain after `fsync`; replay must never re-run provider discovery or the policy decision. No credentials or sensitive source documents enter the WAL for the sake of routing.

Dynamic worker selection has not been accepted by this ADR. It requires its **own** architectural decision addressing the immutable routing contract, version pinning, fallback semantics, lease fencing, provider-side at-least-once behavior, authorization, recovery tests and security boundary.

### 6. Small-slice implementation and validation

The first slice changes only the `workertype` package:

- Add `CapabilityClaim` and the optional `Operation.Capabilities` field.
- Validate declarations in `Manifest.Validate`.
- Write tests first for round-trip, legacy omission, invalid claims, duplicate claims and unchanged Worker Type compatibility.
- No changes to the compiler, engine, `applyToState`, job protocol, public HTTP/MCP API or WAL.

No recovery test is required for this isolated design-time metadata slice because it emits no events and changes no process state. A later executable routing slice **must** add live-vs-replay recovery tests and allocation checks.

### Consequences

- **Positive:** Small, compatible contract; an extensible path from Worker Type operations to business architecture and eventually the estate graph.
- **Positive:** No new store, runtime interpretation, provider call, credential exposure or engine allocation.
- **Negative:** In the first slice, claims are declarative and may be wrong. Consumers must not treat them as guaranteed conformance or authorization.
- **Negative:** Built-in provider mappings and a canonical contract catalogue are intentionally deferred; the first slice alone does not power Panorama discovery.
- **Follow-ups / risks to watch:** Semantic governance and compatibility checks; existing ADR-0208 package-installation gaps; ADR-0420 graph approval; ADR-0475's currently limited provider enforcement; avoiding a second manually maintained operation catalogue.

## Pros and cons of the options

### Option 1: Universal Directory Worker Type

- Good: One apparent surface to process authors.
- Bad: Hides material semantics and credentials; encourages unsafe substitution; conflates contacts with accounts.

### Option 2: A new persisted integration capability register

- Good: Flexible user-managed taxonomy.
- Bad: Conflicts with ADR-0305's stated boundary; duplicates Worker Type metadata, needs synchronization and a second source of truth.

### Option 3: Optional operation metadata (chosen)

- Good: Incremental, versioned, backward compatible; consistent with ADR-0208 and I5.
- Bad: The relationship is only declared until normative contracts and provider tests exist.

### Option 4: Immediate capability-based routing

- Good: Attractive authoring experience.
- Bad: Changes audit and replay semantics before version and policy boundaries are established; not safe as an introductory slice.

## Roadmap placement

- **Milestone B:** Metadata-to-business-architecture linkage, without Level-5 record kinds, beginning with B11.
- **Milestone 6 — Ecosystem:** Canonical contracts, conformance tooling and built-in/external Worker Type projections.
- **Milestone P / ADR-0420:** Unified graph model and server-side provenance-aware traversal.
- **Subsequent runtime milestone:** Capability-aware binding and deterministic, policy-gated routing, only after a separate accepted ADR.

The accompanying `docs/planning/technical-capability-contract.md` defines the vertical slices and acceptance gates.

## Links

- [ADR-0203 — Worker execution model](0203-worker-execution-model.md)
- [ADR-0207 — Worker Type packaging](0207-worker-type-packaging.md)
- [ADR-0208 — Worker Type package contract](0208-worker-type-packages.md)
- [ADR-0299 — Worker Type admission criteria](0299-worker-type-admission-criteria.md)
- [ADR-0305 — Business capabilities and value streams](0305-business-capabilities-and-value-streams.md)
- [ADR-0308 — Panorama binds the capability register](0308-panorama-binds-the-capability-register.md)
- [ADR-0420 — One graph model, several projections](0420-one-graph-model-several-projections.md)
- [ADR-0475 — Identity-aware Worker execution](0475-identity-aware-worker-execution.md)
- [Project plan](../planning/technical-capability-contract.md)
