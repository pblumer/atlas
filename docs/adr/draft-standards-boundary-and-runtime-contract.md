# ADR-DRAFT: Standards boundary and the Atlas runtime contract

- **Status:** Proposed
- **Date:** 2026-08-24
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas executes models expressed in established standards: BPMN defines the process
model, XML interchange and much of the execution semantics; DMN defines decision
models; and FEEL, as DMN's expression language, supplies expressions used by both the
decision and process layers. Those standards answer questions such as which sequence
flow an exclusive gateway takes or how a decision table evaluates. They do not define
the complete contract of an operational workflow platform.

In particular, there is no broadly adopted standard that defines deployment and
versioning, process-instance commands, job activation and leasing, user-task
assignment and forms, incidents and retries, API error and idempotency semantics,
event export, recovery, partitioning, or instance migration as one interoperable
workflow-engine runtime. Adjacent standards solve narrower problems: OpenAPI describes
an HTTP interface, MCP exposes tools to agents, Prometheus/OpenMetrics describes
metrics interchange, CloudEvents can envelope events, and XES can exchange process
mining logs. None assigns Atlas domain meaning to an operation such as `CompleteJob`
or a fact such as `IncidentRaised`.

Atlas already has concrete answers for many of these areas. The engine is an
event-sourced single writer per partition (ADR-0001/0002), jobs follow an Atlas
activation/completion protocol (ADR-0007), the HTTP surface is described by OpenAPI
(ADR-0043), and MCP adapts that HTTP surface rather than accessing engine state
directly (ADR-0016). The internal record model, WAL, Pebble indexes and compiled graph
also embody important decisions, but they are not all suitable public compatibility
contracts.

Without an explicit boundary, three problems follow:

- Atlas-specific behavior can be presented accidentally as BPMN- or DMN-standard
  behavior, overstating portability.
- Clients can couple to storage and record details that Atlas must remain free to
  change, especially before 1.0.
- Each API, MCP tool, SDK and exporter can invent a slightly different runtime
  vocabulary instead of adapting one coherent contract.

The question is: **which parts of Atlas are governed by external standards, which
parts make up a public Atlas runtime contract, and which parts remain internal
implementation details?**

## Decision drivers

- Make BPMN, DMN and FEEL conformance claims precise and testable.
- Prefer open standards at interoperability boundaries without pretending that an
  envelope or interface-description standard defines workflow semantics.
- Give HTTP clients, MCP tools and future worker SDKs one coherent runtime vocabulary.
- Keep internal persistence, partitioning and hot-path representations evolvable.
- Preserve every engine invariant, especially durable-before-visible and the single
  writer per partition.
- Make Atlas-specific extensions and compatibility extensions explicit in models and
  documentation.
- Provide a credible path to the Roadmap's Milestone 4 public API and Milestone 6
  1.0 API stability commitment without freezing pre-1.0 internals prematurely.

## Considered options

1. **Leave the boundary implicit.** Treat BPMN/DMN support as sufficient and document
   each additional API or feature independently.
2. **Adopt another vendor's runtime contract.** Copy a Zeebe/Camunda-compatible API,
   lifecycle and extension vocabulary as Atlas's primary external contract.
3. **Expose Atlas's event log and record model as the public contract.** Make
   `ValueType × Intent`, binary records and WAL order the integration surface.
4. **Standards at the boundaries, one versioned Atlas runtime contract in the
   middle.** Use external standards for the concerns they actually standardize,
   define the remaining public workflow semantics as Atlas-specific, and keep storage
   representations private.

## Decision outcome

Chosen option: **4 — standards at the boundaries, one versioned Atlas runtime
contract in the middle**, because it maximizes honest interoperability while
preserving Atlas's freedom to evolve its engine internals.

### 1. Standards define the model layer

Atlas treats the following as normative inputs where the corresponding capability is
declared supported:

- **BPMN** defines process notation, XML/DI interchange and execution semantics.
- **DMN** defines decision models and decision-table semantics.
- **FEEL** is treated as DMN's expression language, reused by Atlas for process
  expressions through the boundary selected in ADR-0015.

Support is stated against a named specification version and verified by tests or a
conformance suite. A BPMN element being parseable or drawable does not by itself mean
that Atlas claims its execution semantics. Documentation distinguishes:

- standard and implemented;
- standard but not implemented or restricted;
- a declared compatibility extension, such as an adopted `zeebe:` extension; and
- an Atlas extension in the `atlas:` namespace.

Atlas uses a standard element or attribute when it expresses the requirement. A
vendor or Atlas extension is added only where the standard model has no adequate
field, and its ownership is visible in the namespace and documentation.

### 2. Cross-cutting standards describe transports and envelopes

Atlas continues to use cross-cutting standards where they improve tool and platform
interoperability:

- HTTP and JSON carry the public service surface, described by OpenAPI as decided in
  ADR-0043.
- MCP exposes an agent-facing adapter over that HTTP API as decided in ADR-0016.
- Prometheus exposition carries operational metrics under ADR-0142.
- Future event integrations may map committed Atlas events to CloudEvents envelopes or
  XES process-mining logs.

These standards retain their limited meaning. OpenAPI does not define Atlas endpoint
semantics; MCP does not define Atlas tool semantics; CloudEvents does not define the
event payload; and XES is not the engine's execution or recovery model.

Adding such an adapter must not create a second execution path. In particular, event
export consumes already-durable facts off the processor path, following the boundary
established by ADR-0114, and never becomes the source used by `applyToState`.

### 3. Atlas defines the public runtime contract

Behavior needed to operate Atlas but not normatively defined by the standards above is
the **Atlas runtime contract**. It includes, as those surfaces are implemented:

- deployment, process/application versioning and definition binding;
- process-instance creation, termination, migration and message correlation commands;
- variables, scopes and input/output mapping behavior beyond what the model standards
  settle;
- job activation, leases, fencing, completion, failure, retries, at-least-once
  delivery and worker idempotency requirements;
- user-task assignment, claim, scheduling, forms and completion;
- incident creation, inspection, repair, resolution and retry behavior;
- query filters, ordering, pagination and continuation;
- API errors, command rejection, concurrency and idempotency semantics; and
- public event-export payloads and their schema versions.

The HTTP API and its OpenAPI description are the primary wire representation of this
contract. MCP tools and worker SDKs adapt the same behavior; they do not define
parallel semantics or access engine state directly. A concept uses the same names,
states, identifiers, errors and idempotency rules across these surfaces unless an
adapter has a documented transport-specific reason not to.

The runtime contract is versioned with Atlas. Before 1.0, a breaking public change is
allowed but must be deliberate, covered by contract tests, and called out in the
changelog. The 1.0 stability commitment applies to this public contract, not to the
on-disk or in-memory implementation unless a separate compatibility decision says so.

### 4. Engine internals are guarantees, not wire representations

Atlas's architectural invariants remain non-negotiable guarantees:

- append events and fsync before making work visible;
- one writer per partition;
- one deterministic `applyToState` for live execution and replay;
- compile rather than interpret on the runtime hot path; and
- persist events as facts, including generated keys and timestamps.

Clients may rely on the externally stated effects of those guarantees, for example
that an acknowledged command has crossed the documented durability boundary. They may
not couple to the representation used to provide them.

Unless separately declared public and versioned, the following remain implementation
details: numeric `ValueType`/`Intent` encodings, record binary layout, WAL frames and
segment layout, Pebble key prefixes, compiled integer indices, partition-bit layout,
checkpoint format and internal follow-up commands. The OpenSearch export selected in
ADR-0114 is an Atlas export representation; its generic record JSON does not silently
become a stable engine-neutral event standard.

### 5. This ADR does not create a new protocol

“Atlas runtime contract” names and governs the semantics already exposed through the
HTTP API and related adapters. It does not add a parallel network protocol, freeze the
current pre-1.0 surface, or require the processor to translate internal records on the
hot path.

It also does not claim drop-in runtime compatibility with Camunda, Zeebe or another
engine. Compatibility profiles may be added deliberately, but each one maps to the
Atlas contract at an adapter or compile-time boundary and documents semantic gaps.

### Consequences

- **Positive:** Atlas can make precise standards claims without understating its own
  platform semantics. HTTP, MCP and future SDKs share one vocabulary. Integrators know
  which guarantees are stable and which structures are private. Storage, compiler and
  partition representations remain free to evolve. Optional CloudEvents/XES mappings
  can improve interoperability without weakening replay or durability.
- **Negative / trade-offs accepted:** Atlas owns the specification, documentation and
  compatibility burden for its runtime contract. Every public feature must classify
  itself and stay aligned across OpenAPI, MCP and SDKs. Atlas-specific behavior still
  requires adapters for users migrating from another engine; standards at the model
  layer do not make runtime operations portable automatically.
- **Follow-ups / risks to watch:** publish a compact `docs/runtime-contract.md` before
  the 1.0 stability commitment; add contract-version and compatibility information to
  the API metadata; define stable event-export schemas before advertising them as a
  public integration surface; evaluate CloudEvents and XES only as off-path mappings;
  and add a documentation/conformance matrix that labels standard, compatibility and
  Atlas-extension behavior. These are Milestone 4/6 slices, not prerequisites for
  accepting this boundary decision.

## Pros and cons of the options

### Option 1 — leave the boundary implicit

- Good: no new policy or documentation work.
- Bad: encourages accidental vendor lock-in, inconsistent vocabulary and overstated
  standards claims; leaves the 1.0 compatibility boundary undefined.

### Option 2 — adopt another vendor's runtime contract

- Good: immediate familiarity and a larger existing SDK/tool ecosystem.
- Bad: imports vendor-specific semantics that are not BPMN standards; constrains Atlas
  around another engine's architecture; still cannot make persistence, recovery and
  operational behavior truly compatible without copying that engine.

### Option 3 — expose the internal event/record model

- Good: one representation for recovery, export and integrations; little translation.
- Bad: freezes hot-path and on-disk structures as public API, couples clients to
  compiled indices and partition details, and makes safe storage evolution a breaking
  integration change.

### Option 4 — standards plus an Atlas runtime contract

- Good: honest separation of model portability, public Atlas semantics and private
  implementation; allows standard adapters without parallel execution paths; gives
  Milestone 4/6 a concrete compatibility boundary.
- Bad: requires deliberate versioning, mapping and contract tests; Atlas must maintain
  its own runtime vocabulary.

## Links

- relates to ADR-0001 and ADR-0002 (event sourcing and the single-writer partition
  model — internal architecture and externally relevant guarantees)
- relates to ADR-0007 (the Atlas-specific job worker protocol)
- relates to ADR-0009 (record serialization remains an internal format)
- relates to ADR-0014 and ADR-0015 (DMN and FEEL execution boundaries)
- relates to ADR-0016 (MCP adapts the HTTP API rather than defining another runtime)
- relates to ADR-0043 (OpenAPI describes the primary public HTTP contract)
- relates to ADR-0068 (declared Zeebe-compatible input/output mapping extensions)
- relates to ADR-0114 (event export consumes durable facts off the hot path)
- relates to ADR-0142 (standard metrics exposition without changing engine semantics)
- advances Roadmap Milestone 4 (public API) and Milestone 6 (ecosystem and 1.0 API
  stability)
