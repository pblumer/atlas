# The Atlas runtime contract

**Contract version:** 0 (pre-1.0)
**Applies to:** Atlas 0.6.x
**Decided by:** [ADR-0176](adr/0176-standards-boundary-and-runtime-contract.md)

BPMN, DMN and FEEL define what a *model* means. They do not define what happens when you
deploy one, start it, hand its work to a worker, or repair it after a failure. That
behaviour is Atlas's, and this document is where Atlas states it — so that "supported"
is a claim you can check rather than infer.

Two questions it exists to answer:

- **May I depend on this?** If it is named below, yes, under the stability rules in §5.
  If it is in §6, no: it is an implementation detail and it will change.
- **Is this behaviour the standard's or Atlas's?** §2 says how every model-layer feature
  is labelled, so a BPMN element Atlas draws is never silently mistaken for a BPMN
  element Atlas executes.

This document is deliberately short. It is a map of the contract, not a copy of it: the
normative description of each surface is the OpenAPI document the server serves, and the
records linked from each section carry the reasoning.

## 1. What the contract is

The **Atlas runtime contract** is the behaviour needed to operate Atlas that no model
standard defines. Concretely:

| Area | What is contractual |
|---|---|
| Deployment and versioning | Deploying a model, what a version is, how an instance binds to a definition, application releases and promotion |
| Instance lifecycle | Creation, termination, cancellation, migration between versions, message correlation |
| Variables | Scopes, the scope chain, input/output mapping, structured JSON values and their limits |
| Jobs | Activation, leases, fencing, completion, failure, retries, at-least-once delivery, worker idempotency requirements |
| User tasks | Assignment, claim, scheduling, forms, completion |
| Incidents | Creation, inspection, repair, resolution, retry |
| Queries | Filters, ordering, pagination, continuation |
| Errors | Rejection shape, concurrency and idempotency semantics |
| Export | Public event-export payloads and their schema versions |

The **HTTP API and its OpenAPI description are the primary wire representation.** MCP
tools ([ADR-0016](adr/0016-mcp-server-over-http-api.md)) and worker SDKs adapt the same
behaviour; they do not define parallel semantics and do not reach engine state directly.
A concept keeps the same names, states, identifiers, errors and idempotency rules across
those surfaces unless an adapter documents a transport-specific reason.

## 2. Model-layer features are labelled

A BPMN element being parseable, or drawable in the Modeler, does not by itself mean
Atlas claims its execution semantics. Every model-layer feature falls into one of four
classes, and the class is visible from where the feature lives:

- **Standard and implemented** — the specification's semantics, verified by tests or the
  conformance suite in [`conformance/`](../conformance).
- **Standard but not implemented, or restricted** — parsed or drawn, with the restriction
  stated. Where the element is one Atlas recognises but does not execute, the compiler
  refuses the deploy and names it, rather than accepting a model whose behaviour would
  silently differ from the notation. `<complexGateway>` is the worked example: it is
  parsed for no reason other than to produce that message.
- **Compatibility extension** — an adopted vendor extension, in that vendor's namespace.
  The `zeebe:` namespace is the case Atlas carries.
- **Atlas extension** — Atlas's own, in the `atlas:` namespace.

The namespace is the ownership signal. Atlas uses a standard element or attribute
wherever the standard expresses the requirement; an extension is added only where it
does not.

Support is stated against a named specification version. Where a claim is made, a test
or a conformance case backs it.

## 3. The guarantees you may rely on

Atlas's architectural invariants ([`architecture/invariants.md`](architecture/invariants.md))
are guarantees about behaviour. Their externally visible effects are contractual:

- **An acknowledged command has crossed the durability boundary.** When a write returns
  2xx, its events are in the log and fsynced. A crash immediately afterwards does not
  lose it.
- **Recovery reproduces the same state.** Replaying the log rebuilds exactly what the
  live run produced, so an instance's history and current state agree after a restart.
- **Ordering within a partition is total.** Commands against one partition are applied
  in one order by one writer.
- **Events are facts.** A generated key or timestamp in a response is the one persisted,
  not one recomputed later.

You may rely on those effects. You may not couple to the representation Atlas uses to
provide them — see §6.

## 4. Errors, concurrency and idempotency

- Every non-2xx response carries a JSON body of the shape `{"error": "..."}` with a
  human-readable message. The status code carries the class; the message is for a human
  reading a log, not for parsing.
- A rejected command is not persisted. A command that is accepted and then fails during
  execution surfaces as an incident, not as a rejection.
- **Jobs are at-least-once.** A worker may be handed the same job twice — after a lease
  timeout, or after a crash between doing the work and reporting it. Worker handlers must
  be idempotent; Atlas does not deduplicate side effects it cannot see.
- A lease is fenced by an epoch that increments on every activation
  ([ADR-0007](adr/0007-job-worker-protocol.md)). A report from a holder whose lease
  elapsed is refused, so a slow worker cannot overwrite a newer holder's result.

## 5. Versioning and stability

The contract is versioned with Atlas and carries the contract version above.

**Before 1.0** — where Atlas is today — a breaking change to the public contract is
allowed. It must be deliberate, covered by a test that pins the new behaviour, and
called out in [`CHANGELOG.md`](../CHANGELOG.md). "It was not documented" is not a licence
to change it silently; this document and the OpenAPI description together are what
"documented" means.

**At 1.0** the stability commitment attaches to this contract — the surfaces in §1, the
guarantees in §3, and the error semantics in §4. It does not attach to the on-disk or
in-memory implementation unless a separate decision says so.

A running server reports its own build at `GET /api/v1/info`, and its runtime identity
and feature set at `GET /api/v1/node`
([ADR-0189](adr/0189-panorama-architecture-modeling-and-live-overlays.md)).

> **Open item.** The contract version above is stated here and is not yet carried in the
> API metadata. Until it is, a client that needs to branch on contract version must read
> the product version from `/api/v1/info` and map it. ADR-0176 names this as a follow-up.

## 6. What is *not* contractual

The following are implementation details. They are visible in the source, they may
appear in a debugger or a hex dump, and they change without notice or a changelog entry:

- numeric `ValueType` and `Intent` encodings;
- the binary layout of a record;
- WAL frame and segment layout;
- Pebble key prefixes and column families;
- compiled integer element indices;
- the partition-bit layout of a key;
- checkpoint format;
- internal follow-up commands; and
- the generic record JSON of the OpenSearch export
  ([ADR-0114](adr/0114-opensearch-event-exporter.md)) — that is an Atlas *export*
  representation, not an engine-neutral event standard.

Reading any of these is fine. Building against them is not supported, and a change to
one is not a breaking change.

## 7. What this document is not

It does **not** define a second network protocol. It names and governs semantics already
exposed through the HTTP API and its adapters.

It does **not** claim drop-in runtime compatibility with Camunda, Zeebe or any other
engine. Atlas adopts `zeebe:` extensions at the *model* layer where they fit, which
makes models more portable; it does not make runtime operations portable. A compatibility
profile may be added deliberately, at an adapter or compile-time boundary, documenting
its semantic gaps — none exists today.

It does **not** freeze the pre-1.0 surface. See §5.

## Related records

- [ADR-0176](adr/0176-standards-boundary-and-runtime-contract.md) — the boundary decision this document publishes
- [ADR-0043](adr/0043-openapi-spec-and-embedded-api-explorer.md) — OpenAPI as the description of the HTTP surface
- [ADR-0016](adr/0016-mcp-server-over-http-api.md) — MCP as an adapter over that surface, not beside it
- [ADR-0007](adr/0007-job-worker-protocol.md) — leases, fencing and at-least-once delivery
- [ADR-0015](adr/0015-reuse-feel-engine.md) — the FEEL boundary
- [ADR-0114](adr/0114-opensearch-event-exporter.md) — export reads durable facts off the processor path
