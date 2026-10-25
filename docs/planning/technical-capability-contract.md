# Capability-driven Automation: implementation plan

**Date:** 2026-10-24  
**Scope:** Technical Capability Contract, technical-to-business traceability, and later authorized capability-aware routing.  
**Governing draft:** [ADR-0480](../adr/0480-technical-capability-contract.md)

## Goal

Allow architects, process authors and operators to answer which configured Worker Type operations support a business capability and which value streams depend on them, without confusing similar provider methods or bypassing policy. Later, after separate architectural approval, allow a BPMN process to request a precisely versioned technical operation contract with deterministic, authorized resolution.

This plan is intentionally **incremental**. No universal Directory Worker Type is introduced, and no existing process changes its routing as a side effect.

## Baseline verified against main on 2026-10-24

| Area | Source of truth | Current state |
|---|---|---|
| Business capabilities and value streams | `api/capability`, ADR-0305 / Milestone B | Records, realization and gap/coverage/measurement APIs implemented; document-level exchange B9 open |
| Panorama business bindings | ADR-0308, `api/panorama` | Implemented |
| Worker Type package manifest | `workertype/manifest.go`, ADR-0207/0208 | Manifest supports optional validated `capabilities` claims since PR #1343; package lifecycle is partly implemented |
| Built-in Worker Type metadata | `api/builtinworkertypes.go`, `api/workertypes.go` | Type/version/runtime-mode view exists; do not duplicate provider operation tables |
| Identity-aware authorization | `authz`, `api/authorize.go`, ADR-0475 | Partial; supervised delegated SharePoint path is the proven example; no all-provider policy enforcement assumption |
| Unified architecture graph | ADR-0420 | Proposed, not implemented; separate provenance-aware model and server traversal are prerequisite |
| Runtime semantics | `engine`, `compiler`, `wal`, `state` | Single writer, post-fsync effects, one deterministic apply path remain unchanged |

### Constraints

1. **I1/I5:** Parse and validate metadata during installation or model deployment, never per command.
2. **I2/I4/I6:** New runtime routing, when authorized separately, must freeze its decisions in durable events and preserve replay without network calls.
3. **I3:** No cross-partition state reads or direct mutations.
4. **Secrets:** No credentials or personal source payloads in capability metadata, BPMN, or WAL.
5. **BPMN:** Provider technical equivalence cannot be inferred from task labels; existing models remain valid.
6. **Design-time:** Do not duplicate business Capability records or technical operation sources in a new store.
7. **UI:** Nothing in Panorama or the Modeler may claim runtime support that is not implemented.

## T0 — Optional capability claims in the manifest (this PR)

**Target:** Milestone B11 / Worker Type package extension.  
**Implementation:** `workertype/capability.go`, `workertype/manifest.go`, `workertype/capability_test.go`.

**Behaviors:**
- Each `spec.operations[]` may have optional `capabilities[]` with `id`, exact semantic `version`, and optional provider `profile`.
- Reject malformed and duplicate claims, and preserve `DisallowUnknownFields` for unrelated metadata.
- Legacy manifests marshal without `capabilities`; compatibility checks remain unchanged.
- All claims are **advisory**, not proof of interchangeability, authorization, support by a configured Worker, or route eligibility.

**Acceptance gates:**
- [ ] Tests demonstrate old and new manifest round-trip and negative cases (red then green).
- [ ] `go build ./...` and `go test ./...`.
- [ ] `go test -race -timeout=45m ./...` and `go vet ./...`.
- [ ] `gofmt -l .` prints nothing.
- [ ] Repo-wide coverage remains >=95%.
- [ ] ADR approved before declaring the slice landed; PR checks green.

**Not included:** Built-in provider mappings, schema conversion, public API, graph, UI or changed job routing.

## T1 — Canonical semantic contracts and conformance

**Target:** Milestone 6. **Depends on:** T0 and design review.

**T1a — Contract envelope (PR #1346 merged; ADR-0485 remains Proposed / Partial).** Add a separately versioned technical
contract document, with resource kind and identity scope, JSON Schema Draft 2020-12
roots, semantic error codes, idempotency, consistency and pagination constraints.
Parse strictly in `workertype` at design time; reject duplicate identities, malformed
metadata and external JSON Schema references. Sample account and contact fixtures
are explicitly **not** a published canonical corpus. See
[ADR-0485](../adr/0485-technical-capability-semantics.md).
No runtime routing or process-state changes.

**T1b — Full schemas, conformance and publication (partially started).**
The first schema-only foundation is [CDA2a PR #1357](https://github.com/pblumer/atlas/pull/1357)
(tracked by [issue #1358](https://github.com/pblumer/atlas/issues/1358)):
full JSON Schema Draft 2020-12 *keyword* validation of bounded input/output
samples, offline compilation (external schema loaders disabled) and reviewed
positive/negative fixtures for account/contact schemas. Results explicitly say
`schemaOnly`; **they do not prove that an actual Worker implements the
contract**, publish or withdraw evidence, mark a claim as `verified`, or
authorize any job. Source-controlled schema fixtures are not live-provider tests.

**Still open for T1b:** under [ADR-0483](../adr/0483-technical-capability-conformance-governance.md),
add contract/content digest identity and canonical artifact publication, a
provider-agnostic mock harness, provider-specific identity/error/idempotency
checks, independently reviewed evidence, invalidation/revocation and controlled
live integration tests. No automatic claim promotion or routing is allowed.

**Acceptance:** Invalid descriptors and duplicate contract IDs fail immediately.
T1b provider conformance cases fail on meaningful mismatches. No shared
`directory.user.create` claim is published for Entra/LDAP/Contacts without
independent evidence. Third-party claims are untrusted until verified.

## T2 — Project built-in Worker Type operation capabilities

**Target:** Milestone 6. **Depends on:** T1.

Generate read-only catalog projections from the canonical provider operation tables (not parallel copies). Start with Entra, LDAP and Google Contacts; then try SharePoint, Exchange and Paperless to prove this is generic. Distinguish `verified`, `candidate` and `unsupported` claims only after a specific schema and evidence source have been accepted.

**Acceptance:** Each published claim points to an existing operation and supported contract; tests prevent catalog drift and false equivalent mappings; unchanged worker job types.

## T3 — Cross-layer graph relationships

**Target:** Milestone P / ADR-0420. **Depends on:** T1/T2 and ADR-0420 acceptance.

Add source-resolved graph relationships:
`value-stream -> stage -> business-capability -> process -> operation -> technical-capability`, plus configured Worker realizations. Identity is stable and namespaced; every node/edge has modeled/derived/observed provenance. Query runs off-loop where it can scale with data. Permission filtering may hide or stop traversal, never leak identities/counts.

**Acceptance:** Integration test traverses from a Value Stream to the Worker Type operation; absent mappings generate an explicit gap, not a guessed edge.

## T4 — Panorama, HTTP and MCP read-only discovery

**Target:** Milestone B / P. **Depends on:** T3.

Provide an understandable technical capability detail view in Panorama and filterable catalog/impact queries over the HTTP API and MCP. Keep runtime overlays visually distinct from authored architecture claims. Preserve the existing `api/capability` ownership and `bpmn-js` renderer.

**Acceptance:** An architect can answer what value stream would be affected if a configured Worker becomes unavailable, with exact provenance and visibility restrictions.

## T5 — Capability-aware service task binding and policy-gated routing

**Target:** Later runtime milestone; **requires a new accepted ADR**, T1–T4, and relevant ADR-0475 enforcement.

Before implementation, decide:
- Binding location and immutable deployment snapshot; explicit version/profile selection.
- Deterministic choice when multiple Workers support the same verified contract.
- No fallback from a denied or failed request to a less-privileged/less-compatible Worker unless separately authorized.
- Which event freezes selected Worker, target identity, operation contract, policy revision, and action digest.
- Leases, retries, worker upgrades and at-least-once side effects, especially non-idempotent operations.
- Credential brokering for supervised vs external instances; fail-closed policy treatment.
- Compatibility for already deployed service tasks, and rollback/resume after restart.

**Acceptance:** Regression tests for denied choices, ambiguous matches, missing identity/contract, semantic profile mismatch, retries after provider success, and live-versus-WAL-replay equality; zero new hot-path allocations.

## First realistic proof of value

A modeled employee onboarding value stream names the `Identity management` business capability. The existing BPMN onboarding process invokes a configured Entra worker. A read-only technical capability view connects the process step to Entra's known operation, without claiming it is interchangeable with LDAP or Google Contacts. The same graph can show coverage, known gaps and unavailable Workers. No process deployment or execution policy changes are required to demonstrate this first value.

## Decisions still required

1. **Accept or revise ADR-0480.** Is the optional metadata shape sufficient for the first slice?
2. **Accept or revise ADR-0420.** Its graph identity and provenance contract must own technical capability nodes/edges.
3. **Canonical contract ownership:** which repository/package maintains schemas and conformance evidence? Decide before T1.
4. **Separate capability-based routing ADR:** required before T5; no implicit runtime decision from this plan.

## Testing and delivery

Develop each slice test-first and separately reviewable. Publish a PR only with exact checks that were run and their results. For any change touching the compiler/processor/jobs/events or authorization, include a recovery test comparing live state against replayed WAL state. Mark a slice complete only after `go build ./...`, `go test ./...`, `go test -race -timeout=45m ./...`, `go vet ./...`, `gofmt -l .`, and repository CI are green.

Keep T0's implementation **advisory** until the ADR is accepted; none of T1–T5 is represented as already shipped.
