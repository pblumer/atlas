# Capability-driven Automation: delivery programme

**Programme:** CDA (Capability-driven Automation)  
**Baseline checked:** 2026-10-25, `main` including merged [PR #1346](https://github.com/pblumer/atlas/pull/1346). ADR-0483 accepted; CDA2a [PR #1357](https://github.com/pblumer/atlas/pull/1357) (schema-only) merged; draft [PR #1360](https://github.com/pblumer/atlas/pull/1360) (content digests) is open.  
**Programme owner:** pblumer (accepted 2026-10-25). Slice coordinators are not yet assigned.  
**Roadmap owner:** [Milestone CDA](../../ROADMAP.md#milestone-cda--capability-driven-automation-) in `ROADMAP.md`.  
**Detailed historical slices:** [T0–T5 technical plan](technical-capability-contract.md); this programme is the **status and dependency authority** for cross-milestone delivery.

## Programme boundary: policy authority

[Identity-aware Execution (IAE)](identity-aware-execution-program.md) is the separate programme authority for linked external identities, managed Connections, delegated OAuth, versioned PDP/PEP policy, approval obligations, separation of duties and durable action evidence, governed by proposed/partial ADR-0475. CDA does **not** implement a second authorization system. CDA6/7 use the supported IAE provider-specific authorization seam after exact semantic contract binding; IAE4 is a hard acceptance gate for CDA7's chosen Worker. The independently reviewed technical claim/conformance programme remains CDA-owned. Do not treat merged [PR #1346](https://github.com/pblumer/atlas/pull/1346) as a policy-enforcement PR.

## Outcome, not a synonym for job dispatch

Connect what the business needs, the process that realizes it, the precisely
defined technical operation, the Worker Type claiming that operation, the
configured Worker, and the identity/policy permitting that Worker to act:

```text
Value Stream --has-stage--> Stage --requires--> Business Capability
Business Capability --realized-by (authored)--> BPMN Process
BPMN Service Task --uses-operation (derived from explicit binding)--> Worker Type Operation
Worker Type Operation --claims--> Technical Capability Contract
Claim + independently reviewed evidence --may verify--> operation conformance
Configured Worker --realizes--> Worker Type
Worker Instance --leases--> job for configured Worker
Identity + policy --authorizes--> one external action, before its effect
```

These are **different edge types**, not a single inferred chain. Business
realization is **authored** in `api/capability`; invoking one Worker operation
cannot prove that the process implements a Business Capability. A contact is
not an authentication account, and an LDAP entry may not even represent a
person. A claim is not a conformance certificate, and conformance is not
authorization. No new persisted integration-capability record or graph
database is introduced.

## Current baseline and evidence

| Area | Current evidence | State as of 2026-10-25 |
|---|---|---|
| Business capabilities, Value Streams, realizations and Panorama binding | [ADR-0305](../adr/0305-business-capabilities-and-value-streams.md) (Accepted/Partial), [ADR-0308](../adr/0308-panorama-binds-the-capability-register.md) (Accepted/Landed), `api/capability`, `api/panorama` | Implemented for the earlier business architecture scope; not a technical capability graph |
| Worker Type / configured Worker / Worker Instance identity | [ADR-0203](../adr/0203-worker-execution-model.md), [ADR-0208](../adr/0208-worker-type-packages.md) (Accepted/Partial) | Existing model and catalog; third-party package provenance, installation and pinned versions still partial |
| Technical claims in manifests (T0) | [PR #1343](https://github.com/pblumer/atlas/pull/1343) merged; [ADR-0480](../adr/0480-technical-capability-contract.md) (Proposed/Partial); `workertype/capability.go`, `workertype/manifest.go` | **Code landed; architecture approval and B11 completion not claimed** |
| Semantic contract envelope (T1a) | [PR #1346](https://github.com/pblumer/atlas/pull/1346) merged; [ADR-0485](../adr/0485-technical-capability-semantics.md) Proposed/Partial | Parser and envelope on main; architecture acceptance pending, **not conformance** |
| Canonical conformance and publication | [ADR-0483](../adr/0483-technical-capability-conformance-governance.md) (Accepted/Partial); [PR #1357](https://github.com/pblumer/atlas/pull/1357) merged, draft [PR #1360](https://github.com/pblumer/atlas/pull/1360) | Offline schema validation on main (schema-only); digests in review; no publication, evidence or withdrawal code |
| Single cross-layer query model | [ADR-0420](../adr/0420-one-graph-model-several-projections.md) (Proposed/Not started) | Not implemented. Previous **Milestone P is complete only for its original scope**, not this follow-on |
| Governed external action execution | [ADR-0475](../adr/0475-identity-aware-worker-execution.md) (Proposed/Partial) | Supervised delegated SharePoint example exists; do **not** assume equivalent enforcement for all Worker Types |
| Capability-based BPMN routing | [Binding and routing draft](../adr/0482-capability-aware-binding-and-routing.md) | Proposed decision; **no runtime feature yet** |

## Delivery slices, dependencies and gates

The source plan used T0–T5. For tracking, the combined T1 and T5 steps
are split where their validation/trust boundaries differ:

| Slice | Status | Previously | Ownership / placement | Depends on | Deliverable and acceptance gate |
|---|---|---|---|---|---|
| **CDA0 — Claims** | 🚧 Code merged, ADR-0480 Proposed | T0 / B11 | Worker Type contract; Milestone B11 | ADR-0203/0208 | Optional version/profile claims with valid/invalid and legacy manifest tests; do not call it landed until ADR approved and verification evidence is reviewed |
| **CDA1 — Semantic envelope** | 🚧 PR #1346 merged; ADR-0485 Proposed | T1a | `workertype`; Milestone 6 | CDA0 design review | Exact contract identity, resource semantics, schema root and strict parser on main; architecture acceptance pending, no provider conformance claim |
| **CDA2 — Governance and conformance** | 🚧 ADR-0483 Accepted; PR #1357 (schema-only) merged; draft PR #1360 (digests) in review | T1b | Contract maintainers and reviewers; Milestone 6 | CDA1, ADR-0208 trust boundaries | Immutable contract/digest, offline full JSON Schema validation, positive/negative fixtures, and independently reviewed evidence/withdrawal lifecycle. PR #1357 covers **only** JSON Schema/fixtures and PR #1360 **only** content digests; provider verification remains unimplemented |
| **CDA3 — Source-derived Worker claims** | 🔲 Not started | T2 | Provider catalogue owners; Milestone 6 | CDA2 | Entra or Google Contacts first, LDAP afterwards; correct per-operation contract/profile, no duplicate operation source, drift and negative-equivalence tests; label unknown/candidate/verified precisely |
| **CDA4 — Technical-to-business graph** | 🔲 Not started | T3 | Graph/Panorama architecture, **new CDA track** | CDA2/CDA3, ADR-0420 acceptance or explicit extension | Provenance-bearing, stable-ID graph edges from Value Stream and authored realization to operation/contract and configured Worker; server-side bounded traversal and no unauthorized leakage |
| **CDA5 — Impact and discovery** | 🔲 Not started | T4 | API/MCP/Panorama, **new CDA track** | CDA4 | Read-only contract/coverage/gap/impact queries with precise evidence and visibility; UI distinguishes authored/derived/observed; no runtime support implied |
| **CDA6 — BPMN binding validation** | 🔲 Not started | First half of T5 | BPMN/compiler/modeler, **new CDA track** | CDA2/CDA3, accepted binding ADR, Worker package pinning prerequisites | Opt-in exact contract and explicit Worker binding, immutable deployment resolution, fail closed on missing/ambiguous/unverified candidates, legacy BPMN compatible. **Design-time only** |
| **CDA7 — Governed execution** | 🔲 Not started | Second half of T5 | Engine/jobs/authz, **new CDA track** | CDA6, relevant ADR-0475 provider enforcement, accepted routing ADR | One opt-in executable vertical slice; durable authorized action decision, lease fencing, no fallback, post-fsync effects, deterministic live/replay equivalence and no hot-path allocations |

**Ownership** names the subsystem requiring maintainers' review, not a claim
that a specific individual or team has accepted the assignment.

No milestone above introduces a commitment to support every provider. For
CDA3, the first verified **contact** contract must not accidentally establish
the **account** contract, nor vice versa.

### Ordered dependencies

```text
ADR-0203/0208 -> CDA0 (code merged; ADR-0480 still Proposed)
                        |
                     CDA1 (#1346)
                        |
      Governance ADR -> CDA2 conformance -> CDA3 provider evidence
                                             |
                         +-------------------+-------------------+
                         |                                       |
             ADR-0420 accepted                              Binding ADR
                         |                                       |
                  CDA4 read graph                        CDA6 pinned BPMN binding
                         |                                       |
                  CDA5 discovery                         ADR-0475 provider enforcement
                                                                 |
                                                        CDA7 governed execution
```

CDA6 can prototype deployment validation once CDA2/CDA3 are available;
**CDA5 is not an engine prerequisite** for that prototype. Its user-visible
discovery and provenance model must nevertheless be agreed before
claiming an end-to-end product experience. Don't hold core correctness
hostage to a graph UI, but don't present a routing feature without honest
discovery and authorization.

## Architecture decision register

| Decision | Status | Required before |
|---|---|---|
| [ADR-0480 — Operation-level descriptive claims](../adr/0480-technical-capability-contract.md) | Proposed, code Partial | Declare CDA0 architecturally done |
| [ADR-0485 — Semantic envelope](../adr/0485-technical-capability-semantics.md) | Proposed / Partial; PR #1346 merged | CDA1 accepted |
| [ADR-0483](../adr/0483-technical-capability-conformance-governance.md) | **Accepted** / Partial: PR #1357 merged, draft PR #1360 open | CDA2, then trusted CDA3 |
| [ADR-0420 — One graph](../adr/0420-one-graph-model-several-projections.md) | Proposed / Not started | CDA4. Must explicitly cover technical capability nodes, operation edges, visibility and provenance before acceptance |
| [ADR-0475 — Identity-aware Worker authorization](../adr/0475-identity-aware-worker-execution.md) | Proposed / Partial | CDA7 provider-specific governed action |
| [ADR-0482](../adr/0482-capability-aware-binding-and-routing.md) | Proposed / Not started | CDA6 validation and CDA7 execution |
| [ADR-0208 — Worker Type packages](../adr/0208-worker-type-packages.md) | Accepted / Partial | Verified package version/digest and safe configuration pinning |

The existing **Milestone P** stays marked complete for its *defined*
Panorama deliverables; CDA4/CDA5 are new functionality on top, not a
retroactive reopening. ADR-0420's proposed graph does not become
implemented merely because Panorama already has a starmap.

### Open decisions that cannot be treated as implementation details

1. **Contract identity and profiles.** CDA1's `(id, version, profile)`
   contracts versus CDA0's within-operation `id@version` duplicate
   rejection: agree whether one operation may later claim multiple
   profiles, and test any manifest change explicitly.
2. **Evidence authority and expiry.** Review authority is decided in
   ADR-0483: repository provenance (`CODEOWNERS`, protected `main`) with the
   maintainer as sole reviewer for now. Still open: the canonical artifact
   encoding (PR #1360's Go `encoding/json` profile versus RFC 8785), the
   digest of a built-in Worker Type's implementation, the conformance runner,
   revalidation triggers and withdrawal semantics; no third-party trust
   without ADR-0208.
3. **Graph provenance.** Update proposed ADR-0420 before accepting it:
   namespaced `technical-capability` and `worker-type-operation`
   nodes, `declares` vs `verified-by` distinctions, and how a BPMN
   task references a specific operation. Preserve "stop at restricted"
   by default, no hidden node counts and no invented realization edges.
4. **BPMN semantic binding.** Define the atlas extension payload and
   its strict deployment diagnostics; do not reinterpret BPMN Task or
   Call Activity semantics.
5. **Routing and authorization.** First executable mode binds one
   verified configured Worker at deployment, then authorizes each
   governed action. Selection from multiple Workers at execution
   needs a *later separately approved* durable event/policy decision,
   not an optimization hidden in a service-task implementation.
6. **Privacy and operations.** Review exposure of contract metadata,
   graph visibility, delegated tokens, authorization event contents,
   retention, upgrades, rollback and incident behavior.

## First independently demonstrable end-to-end slice

**Onboarding architecture query without runtime changes (CDA0–CDA5).**
An architect models an employee onboarding Value Stream with an
`Identity management` Business Capability, explicitly realized by a
deployed BPMN process. One service task explicitly invokes a configured
Entra Worker. The read-only graph resolves the task to its authoritative
provider operation and either a verified contract **or a visibly
unverified candidate**, with the exact evidence/source. A query can
trace impact from that Worker to the Value Stream without exposing
restricted resources.

**Negative acceptance tests:** no inferred business realization,
no mapping from Google `create-contact` to an Entra account contract,
no eligible capability route merely because the same word "create"
appears, and no hidden names/counts in an unauthorized traversal.

**First governed executable slice (CDA6–CDA7).** A new opt-in BPMN
service task requests one exact verified contract and pins one
configured Worker during deployment. An authorized supervised Worker
Instance executes through ADR-0475's durable permit seam. A denial,
revocation, worker mismatch or restart has deterministic semantics,
with live state equal to replayed WAL state and no external effects
during replay. Use one already-supported governed provider path
first; do not assume Entra/LDAP/Contacts have action authorization
until separately proven.

## Definition of done and delivery discipline

For **every slice**, link the PR, its ADR, current status, dependency
decision, evidence and remaining gaps back to this programme and
the `ROADMAP.md` CDA track. "Merged" is not synonymous with
"accepted", "landed" or "production-ready".

- Docs-only work: verify ADR draft format, valid links, no premature
  ADR numbers/index changes, and `go test ./docs/adr` in CI. No
  unrelated feature or changelog claims for a planning-only PR.
- Code: TDD (red/green), table-driven negative tests, regressions and
  meaningful assertions; full `go build ./...`, `go test ./...`,
  `go test -race -timeout=45m ./...`, `go vet ./...`,
  `gofmt -l .` (no output), repo-wide >=95% statement coverage,
  and platform CI.
- Runtime, persistence, jobs, policy or compiler changes: recovery
  test (process -> fsync -> restart -> replay -> state equality);
  deterministic clock/scheduling; verify effects only after fsync,
  no replay reauthorization, no per-command allocations, no direct
  cross-partition reads/mutations. Record versioning, migration and
  rollback behavior and verify existing BPMN models unchanged.
- HTTP/MCP/UI changes: route registry and tool parity tests,
  identity/sharing-scope checks, architecture/UI provenance
  distinctions; no feature tile or Console claim until implemented.

## Relationship to earlier T0–T5 plan

- T0 = CDA0
- T1a = CDA1; T1b = CDA2
- T2 = CDA3
- T3 = CDA4
- T4 = CDA5
- T5 = CDA6 **plus** CDA7

The detailed [T0–T5 plan](technical-capability-contract.md) remains
useful for technical task lists. PR #1346 delivered the T1a envelope;
CDA2 continues after the merged schema-only PR #1357 with digest PR #1360, followed by independent evidence,
publication and withdrawal. Neither acceptance of ADR-0483 nor completion
of the schema-only slice alone certifies a Worker.
