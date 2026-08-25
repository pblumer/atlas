# Atlas ADR implementation audit

**Repository:** `pblumer/atlas`  
**Branch:** `main`  
**Inspected commit:** [`9721ff8f726f4ee5be8d88c959c3221c65bd2933`](https://github.com/pblumer/atlas/commit/9721ff8f726f4ee5be8d88c959c3221c65bd2933)  
**Audit date:** 2026-08-25  
**Scope:** all 180 numbered ADRs, the ADR index, roadmap, production code, UI code, compiler/engine/state/API/worker packages, repository catalog, workflows and associated test sources.

## 1. Executive summary

The implementation is substantially ahead of its formal ADR and roadmap status.

| View | Count | Assessment |
|---|---:|---|
| Numbered ADRs | 180 | Complete corpus from ADR-0001 through ADR-0180 |
| Formally Accepted, including amended/Layer A | 137 | 107 show no material code discrepancy; 30 have an explicit limit, stale wording or a noteworthy residual item |
| Formally Proposed | 43 | 28 are already implemented, 5 are partial, 1 is superseded in substance, 9 are not implemented |
| Material implementation gaps in Accepted decisions | 5 areas | Partition scale-out, worker-protocol metrics, Prometheus completeness, and the incomplete worker migration described by ADR-0164/0168 |

The most important conclusion is therefore not “many ADRs are missing”. It is:

1. **Implementation governance has drifted.** At least 28 `Proposed` ADRs have landed code and tests without their lifecycle being reconciled.
2. **The connector worker transition is incomplete.** The mechanism exists and four kinds are offloaded by default, but several connector kinds still have only an in-process implementation.
3. **Repository/element templates stop one step too early.** Packages can be browsed and installed into a durable store, but the Modeler does not consume that store or apply an installed template.
4. **A few promised guards and documents do not exist.** Most notably ADR-0167's catalog completeness test and ADR-0176's `docs/runtime-contract.md`.
5. **Roadmap and ADR prose are now an unreliable implementation view.** There are concrete stale entries for ADR-0068, ADR-0132 and ADR-0162, plus stale statements inside several ADRs and `engine/metrics.go`.

## 2. Method and confidence

For every ADR, the audit compared the decision and any implementation-status section against current production paths and tests. The strongest evidence was given to durable model/events, compiler fields and validation, `applyToState`, API and UI reachability, recovery tests, worker registration, and build-time drift guards. An ADR reference in a comment was treated only as a navigation aid, never as proof by itself.

The repository tree contained 1,749 entries. The local audit corpus included all ADRs and 1,123 relevant Go/JS/JSON/Markdown files, including 567 Go test files. A runtime test sweep could not be executed in the audit environment because the Go toolchain was not installed (`go: command not found`). Consequently, “implemented” below means the production path and its checked-in tests are present and internally consistent; it is not a fresh CI result.

## 3. Highest-priority gaps

| Priority | ADRs | Gap in current code | Evidence | Recommended closure |
|---|---|---|---|---|
| P0 | 0164, 0168, 0165 | The no-in-process direction is only partially reached. `DefaultOffloadedKinds()` contains `csv`, `mail`, `script`, `webscrape`. The built-in worker supports 11 kinds, but seven relevant offloadable integration kinds still have no built-in worker implementation: `temis`, `clio`, `sharepoint`, `remedy`, `scim`, `ldap`, `soap`. SOAP was added in-process despite the worker-first direction, with the gap documented in ADR-0165. | `api/connectorkinds.go`, `worker/connectors.go`, `api/handlers.go`, `api/offload_internal_test.go` | Create one authoritative job-kind capability matrix and a build guard. Move SCIM/LDAP/SOAP first, then clio/SharePoint/Remedy/temis. Promote a kind to default offload only when payload resolution, credential provisioning, worker execution and fallback tests all exist. Keep local DMN and the run-loop-owned user connector as explicit, documented engine-only exceptions. |
| P0 | 0027, 0081 | Repository installation persists an element-template package, and the gallery says it lands in the palette, but no Modeler path reads `/api/v1/repository/installed` or applies template bindings to a BPMN element. The install is therefore discoverable and durable, but not usable through the ADR-0027 “Template -> Select” contract. | `api/repository.go`, `api/repositorystore.go`, `api/web/app.js`; no corresponding consumer in `api/web/editor.js` | Add installed-template loading to the Modeler, a template picker, the supported binding applier, compile validation and round-trip/e2e tests. Correct the current UI copy until the path is complete. |
| P1 | 0007, 0142 | The lease protocol is implemented (`JobActivated`, lease epoch fencing, `JobTimedOut`), but metrics still count only created/completed/failed/canceled jobs. `engine/metrics.go` and ADR-0142 incorrectly say the lease protocol does not exist. `atlas_open_incidents` is also absent because one incident deletion path has no explicit resolution event. | `model/record.go`, `engine/behavior.go`, `engine/metrics.go`, `api/metrics.go`, ADR-0142 | Add durable activation and lease-timeout counters from committed records. Decide whether to add an explicit incident-cleared fact so the open-incident gauge is replay-safe; if not, document the deliberate omission. Remove the stale comments. |
| P1 | 0167 | No enforcement test or package generation exists for “an Accepted connector ships in the Repository”. There are 19 Modeler service-task kinds but only 8 bundled packages. Accepted-backed kinds missing from the bundled catalog include at least clio, Remedy, webscrape, user provisioning and mockup. | `api/web/editor.js`, `api/repository_catalog/*.json`, `api/repository_internal_test.go` | Add an explicit `kind -> backing ADR -> release status -> package id` registry. Generate packages where possible and fail `go test ./...` for every Accepted, in-scope kind without exactly one compatible package. |
| P1 | 0176 | The ADR requires a compact public runtime contract before compatibility claims, but `docs/runtime-contract.md` does not exist. | ADR-0176 and repository tree | Publish the document and link it from architecture, README and release checks. Make compatibility claims reference a versioned contract, not scattered ADR prose. |
| P1 | 0037 | Structured JSON variables have no enforced value-size limit. A single variable can therefore create unbounded state/WAL/API pressure. | ADR-0037 follow-up; no compiler/API/engine limit found | Decide the limit and rejection boundary, expose a stable error, and test command, API and replay behavior. |
| P1/P2 | 0047 | Polyglot scripts use workers, but true OS-level sandboxing remains open. Worker separation limits engine blocking; it is not a security sandbox for untrusted code. | ADR-0047 and script worker execution path | Define the supported trust posture. If untrusted scripts are a goal, add a constrained runtime/container profile; otherwise make “trusted code only” explicit in UI and operator docs. |
| P2 | 0006, 0175 | ADR-0006 describes partition routing/cross-partition communication as Accepted architecture, while the runtime is still a single partition and ADR-0175's replicated partition cells are not implemented. | engine/API topology, ADR-0002, ADR-0006, ADR-0175 | Treat scale-out as a separate architecture programme. Amend ADR-0006 to distinguish current single-node routing contracts from future cluster behavior before implementing replication. |
| P2 | 0154 | LDAP's main connector and later hardening are implemented, but sync/delta-cookie behavior remains absent. The older “no paging/pooling/per-value modify” text is stale against the amendment and code. | `connector/ldap`, `compiler/ldap_connector_test.go`, ADR-0154 amendment and consequences | Add delta synchronization only if required by product use cases; immediately reconcile the contradictory ADR prose. |
| P2 | 0165 | SOAP core behavior is implemented, but WSDL import, response attributes/namespaces and WS-Security are not. More importantly, it is not worker-backed. | SOAP compiler/connector sources and tests; ADR-0165 | Move it to the worker before expanding protocol breadth. Then prioritize WS-Security or WSDL assistance from concrete integration demand. |
| P3 | 0147 | The accepted pilot (`api/runloop`, `api/httpapi`, `api/processdoc`) shipped, but the broader Server split is intentionally opportunistic. The current tree still has 286 methods on `*Server`. | ADR-0147 and `api/*.go` | Do not start a migration-only epic. Require new or materially changed API areas to use a narrow service; revisit the method count periodically. |

## 4. Formal status drift: all 43 Proposed ADRs

| ADR | Code assessment | Concrete result / gap |
|---|---|---|
| 0025 | Partial | The hand-written panel strategy and documentation retention are in use; several groups arrived through later ADRs. Execution listeners and Play-mode example data are not a complete vertical slice. |
| 0026 | Implemented | `/api/v1/validate`, engine-version-aware structured problems, debounced/stale-safe Problems UI and compiler dry-run tests exist. |
| 0027 | Partial | Package/storage substrate exists through Repository, but no Modeler template select/apply path consumes installed templates. |
| 0028 | Implemented | User tasks, forms, Tasks app, assignment and completion paths exist. |
| 0030 | Not implemented | No ephemeral in-Modeler engine simulation/play session. Token simulation is a different, client-side feature. |
| 0031 | Not implemented | No Modeler diagram version-history surface. Git-backed applications do not implement this ADR's UX/contract. |
| 0032 | Not implemented | MCP and collaboration infrastructure exist, but no in-Modeler AI copilot feature. |
| 0051 | Implemented | Timer start events for duration/date/cycle are compiled, scheduled, replayed and tested. The ADR's draft wording is stale. |
| 0054 | Implemented | Date/cycle schedules for catch and boundary events exist. |
| 0055 | Implemented | FEEL schedules for catch and boundary timers exist. |
| 0056 | Implemented | FEEL cycles and FEEL timer starts exist. |
| 0057 | Implemented | First-class FEEL temporal values are used for timer schedules. |
| 0061 | Implemented | Durable incidents, retries, resolve/resume and Operations/API surfaces exist. |
| 0064 | Implemented | Timer FEEL failures park work and raise incidents. |
| 0068 | Implemented | Compiler I/O mappings, activity-local scopes, scope-chain reads, promotion/drop events, engine/UI/worker paths and recovery tests exist. ADR-0174 closes the connector-payload remainder. ROADMAP is stale. |
| 0081 | Partial MVP | Curated bundled catalog, manifest/checksum/no-secret validation, trust split, gallery and durable install/uninstall exist. Modeler template application, remote registry, signing and community publishing remain open. |
| 0082 | Implemented | Event subprocess phases are present, including later error/signal/escalation extensions. The opening scope text is stale. |
| 0084 | Implemented | CSV upload/batch validation and correction flow exists with engine-native DMN/scope behavior. |
| 0086 | Implemented | Gateway conditions resolve through the variable scope chain. |
| 0117 | Not implemented | No managed AI agent task/job implementation. |
| 0128 | Implemented | Applications, releases, promotion/export/import and related UI/API paths exist. |
| 0129 | Implemented | Remote targets, deploy tokens and publish/promote paths exist. |
| 0130 | Not implemented | No separate process-version drain/deprecation lifecycle. |
| 0134 | Implemented | Git-backed application source, import/sync/store and tests exist. The ADR's “Phase 4 still open” statement is stale. |
| 0140 | Implemented | Live collaboration sessions, HTTP/API, Modeler UI, MCP integration, tracing and tests exist. |
| 0152 | Implemented | REST OAuth2 client-credentials compilation and connector behavior exist. |
| 0153 | Implemented | SCIM connector and tests exist. It remains part of the worker-migration gap. |
| 0154 | Partial / core landed | LDAP search/write/password operations plus paging, bounds, per-value changes, certificate bind and pooling exist. Sync/delta cookie remains open; ADR prose contradicts its amendment. |
| 0156 | Superseded in substance | ADR-0164 explicitly revises its recommendation. It should not remain a plain `Proposed` peer. |
| 0157 | Implemented | Global job type registry, type-keyed long-poll, lease fencing, Workers view, `atlas worker`, off-loop handlers and supervision exist. Its early “step 1 partial” wording is stale. |
| 0162 | Implemented | Durable migration event/fold, validation, API, UI, MCP, version-aware timeline and recovery tests exist. ROADMAP still calls it designed/unstarted. |
| 0165 | Partial / core landed | Generic SOAP connector is implemented and tested, but is still in-process and lacks the optional protocol extensions named by the ADR. |
| 0166 | Implemented | Active Directory connector and worker path exist. |
| 0167 | Not implemented | No completeness registry, generation or failing build guard; actual Repository catalog is incomplete. |
| 0171 | Implemented | LDIF/directory-file compiler, connector and worker path exist. |
| 0172 | Implemented | Entra connector, worker implementation, paging/list users and advanced query support exist. |
| 0173 | Implemented | SQL Server, MariaDB and PostgreSQL connectors are worker-only and tested. |
| 0174 | Implemented | Task input mappings become the outbound connector payload; scope-chain visibility is shared by the relevant connector paths. |
| 0175 | Not implemented | No replicated partition cells, consensus, placement or horizontal routing implementation. |
| 0176 | Not implemented | The required runtime-contract document and conformance boundary do not exist. |
| 0177 | Implemented | Reload bypasses deploy-time validation as designed, with BPMN/DMN recovery tests and logging. |
| 0178 | Not implemented | No RACI moddle fields, compiler metadata, matrix UI or validation. |
| 0180 | Implemented | Group CRUD, memberships, scope integration and role snapshots exist. |

### Proposed-status totals

- **Implemented:** 28 — 0026, 0028, 0051, 0054, 0055, 0056, 0057, 0061, 0064, 0068, 0082, 0084, 0086, 0128, 0129, 0134, 0140, 0152, 0153, 0157, 0162, 0166, 0171, 0172, 0173, 0174, 0177, 0180.
- **Partial:** 5 — 0025, 0027, 0081, 0154, 0165.
- **Superseded in substance:** 1 — 0156 by 0164.
- **Not implemented:** 9 — 0030, 0031, 0032, 0117, 0130, 0167, 0175, 0176, 0178.

## 5. Accepted ADRs with limits, residual work or stale wording

These are not all acceptance failures. The table distinguishes a material implementation gap from an intentionally bounded decision and from documentation drift.

| ADR | Classification | Current discrepancy or explicit boundary |
|---|---|---|
| 0006 | Material target gap | Partition routing abstractions exist in a single-partition runtime; cross-partition communication/replication is future ADR-0175 work. |
| 0007 | Core implemented; residual gap | Worker leases, long-poll and fencing exist. Activation/lease-timeout metrics are missing, and the ADR/code comments saying the protocol is not built are stale. |
| 0020 | Implemented with explicit boundary | No message buffering; cross-partition correlation remains outside current scope. |
| 0024 | Implemented with explicit boundary | Acyclic parallel joins are implemented; cyclic/looping joins remain unsupported. |
| 0033 | Implemented with explicit boundary | Inclusive joins are implemented for the documented reconverging shapes; cyclic inclusive joins remain unsupported. |
| 0036 | Implemented with explicit boundary | clio connector exists; the optional WAL audit mirror remains deferred. |
| 0037 | Implemented with material hardening gap | Structured JSON variables work, but size limits are not enforced. |
| 0039 | Implemented with explicit boundary | DMN I/O mappings exist; some evaluation detail is not preserved over the remote temis boundary. |
| 0047 | Implemented with security boundary | Job-worker separation exists; true OS sandboxing is not delivered. |
| 0052 | Implemented; stale ADR text | Message end exists, and later ADR-0102/0112 added receive/send tasks that this ADR still describes as absent. |
| 0060 | Implemented with explicit follow-ups | Field writes exist; collection/member removal/null semantics, schema validation and collection indexes remain open. |
| 0063 | Implemented with explicit boundary | Latest/deployment binding exists; `versionTag` pinning is deferred. |
| 0066 | Implemented with explicit boundary | Durable decision evaluation records exist; dedicated retention remains a follow-up. |
| 0071 | Implemented; stale ADR text | Sharing scopes exist; the statement that groups are reserved/not implemented is superseded by ADR-0180 code. |
| 0072 | Implemented with explicit boundary | Multiple DMN models per deployment exist; independently deployed/versioned DMN binding remains deferred. |
| 0073 | Implemented with explicit boundary | Principals directory exists; universal id-based assignment remains follow-up work. |
| 0079 | Implemented with explicit boundary | Mail connector/providers exist; attachments remain open. |
| 0080 | Core implemented; residual counters | Maintained aggregate runtime views exist; some later job/incident counters remain absent. |
| 0093 | Implemented with explicit boundary | Gmail/Graph providers exist; attachments remain open. |
| 0102 | Implemented with explicit boundary | Receive tasks exist; they intentionally inherit ADR-0020's no-buffering behavior. |
| 0103 | Implemented with explicit boundary | Compensation works; strict sequential chaining/`waitForCompletion` and selective snapshots remain deferred. |
| 0105 | Implemented with explicit boundary | Per-server call overrides exist; disabled/parked targets and finer caller-element granularity remain open. |
| 0110 | Implemented with explicit boundary | Deferred-choice gateways exist; parallel instantiate/all-branches behavior is not included. |
| 0121 | Correctly scoped | Only Layer A is accepted and implemented; assignment defaults/access control are explicitly Layers B/C. |
| 0137 | Implemented with explicit boundary | Conditional events exist; repeatable false-to-true rearming for non-interrupting conditions is deferred. |
| 0138 | Implemented with explicit boundary | Parallel ad-hoc subprocess behavior exists; sequential ordering is deferred. |
| 0142 | Partial observability | Prometheus endpoint and most counters exist; activation/lease-timeout counters and an open-incidents gauge do not. Text about the missing worker protocol is stale. |
| 0147 | Accepted pilot implemented | The pilot packages shipped. The broader Server split is intentionally opportunistic, not complete; 286 `*Server` methods remain. |
| 0164 | Material transition gap, acknowledged by ADR | Deprecation and default offload for four kinds exist; the full connector fleet has not moved. |
| 0168 | Material transition gap, acknowledged by ADR | Payload/config split and per-kind switch exist for a subset; seven relevant connector kinds lack a built-in worker. |

## 6. Accepted ADRs without a material discrepancy

For the following 107 Accepted-family ADRs, the audit found the selected decision or its explicitly accepted layer in production code, with no material discrepancy beyond ordinary follow-up work documented elsewhere:

`0001`, `0002`, `0003`, `0004`, `0005`, `0008`, `0009`, `0010`, `0011`, `0012`, `0013`, `0014`, `0015`, `0016`, `0017`, `0018`, `0019`, `0021`, `0022`, `0023`, `0029`, `0034`, `0035`, `0038`, `0040`, `0041`, `0042`, `0043`, `0044`, `0045`, `0046`, `0048`, `0049`, `0050`, `0053`, `0058`, `0059`, `0062`, `0065`, `0067`, `0069`, `0070`, `0074`, `0075`, `0076`, `0077`, `0078`, `0083`, `0085`, `0087`, `0088`, `0089`, `0090`, `0091`, `0092`, `0094`, `0095`, `0096`, `0097`, `0098`, `0099`, `0100`, `0101`, `0104`, `0106`, `0107`, `0108`, `0109`, `0111`, `0112`, `0113`, `0114`, `0115`, `0116`, `0118`, `0119`, `0120`, `0122`, `0123`, `0124`, `0125`, `0126`, `0127`, `0131`, `0132`, `0133`, `0135`, `0136`, `0139`, `0141`, `0143`, `0144`, `0145`, `0146`, `0148`, `0149`, `0150`, `0151`, `0155`, `0158`, `0159`, `0160`, `0161`, `0163`, `0169`, `0170`, `0179`.

This category means “the chosen decision is visible in the current code”, not that every imaginable extension or future roadmap item is complete.

## 7. Documentation and governance discrepancies

| Location | Discrepancy | Fix |
|---|---|---|
| `docs/adr/README.md` | 28 code-landed ADRs remain `Proposed`; the index cannot answer implementation state. | Separate decision lifecycle from implementation lifecycle. Add `Implementation: Not started / Partial / Landed / Superseded` and render both in the index. |
| ADR-0156 | Still `Proposed`, although ADR-0164 explicitly revises it. | Mark `Superseded by ADR-0164` or add a formal supersession field. |
| `ROADMAP.md` ADR-0068 | Still marked in progress and says REST/clio scope reads remain; ADR-0174 and current connector code close that item. | Mark the delivered scope complete and list only genuinely open extensions. |
| `ROADMAP.md` ADR-0162 | Marked designed/unstarted despite engine/API/UI/MCP/recovery implementation. | Mark complete and link implementation evidence. |
| `ROADMAP.md` link events | Label says ADR-0133 while the link points to ADR-0132. | Correct the label to ADR-0132. |
| ADR-0051, ADR-0134, ADR-0157 | Draft/partial/phase-open wording no longer matches shipped code. | Replace historical status prose with dated implementation notes; retain history in amendments. |
| ADR-0052, ADR-0071, ADR-0082 | Later ADRs implemented capabilities that their context still calls absent or out of scope. | Add short amendment notes pointing to the later ADRs. |
| ADR-0154 | Amendment says paging, per-value modify, certificate bind and pooling are done, while the old Consequences section says they are not. | Reconcile the body while preserving the original decision history in an explicit “original limitation” note. |
| `engine/metrics.go`, ADR-0142 | Both say the lease worker protocol is not built; records and behavior prove it is. | Update comments and implement or explicitly defer the now-measurable counters. |
| Repository UI copy | Says install lands a template in the palette, but no Modeler consumer exists. | Change copy now; restore the claim when ADR-0027 apply flow lands. |

## 8. Recommended execution plan

### Phase 0 — establish one truthful status model

Deliver as a documentation/governance-only change.

1. Add a distinct implementation field to every ADR: `Not started`, `Partial`, `Landed`, or `Superseded`.
2. Ratify the 28 implemented Proposed ADRs as `Accepted` where the decision is genuinely settled; otherwise leave `Proposed` but explicitly mark the implementation as landed/experimental.
3. Mark ADR-0156 superseded by ADR-0164.
4. Fix the roadmap, ADR amendments, link typo, metrics comments and Repository UI claim listed above.
5. Add a drift test that regenerates/checks the ADR index from ADR headers. Do not infer “implemented” merely from an `ADR-xxxx` comment.

**Exit criterion:** ADR index and ROADMAP can be used as an honest implementation view without reading source first.

### Phase 1 — close correctness and operability guard rails

1. Publish ADR-0176's versioned `docs/runtime-contract.md`.
2. Add ADR-0007/0142 activation and lease-timeout metrics from committed records.
3. Decide the event/log shape needed for a replay-safe open-incidents gauge.
4. Set and enforce the ADR-0037 variable-size boundary.
5. Add ADR-0167's catalog completeness registry/test, initially against Accepted in-scope kinds.

**Exit criterion:** missing release metadata, catalog entries and critical runtime bounds fail CI instead of relying on memory.

### Phase 2 — finish the worker architecture in vertical slices

First create a checked table with one row per reserved job kind and these columns: side-effecting, engine-only exception, resolved payload, built-in worker, credential owner, supervised configuration, default offload, in-process fallback.

Recommended order:

1. SCIM, LDAP, SOAP — current identity-management surface and clearest ADR-0164 conflict.
2. clio, SharePoint, Remedy, temis — existing managed integrations.
3. Reassess whether any in-process fallback can be removed; preserve compatibility until migration and operator diagnostics are proven.

Each slice must include compiler payload freeze, no secret in job payload, worker execution, fencing/retry/recovery, supervision configuration, Operations visibility and a test proving disabling the in-process handler does not strand jobs.

**Exit criterion:** every side-effecting connector is either served by the built-in worker or carries an explicit, reviewed exception; the default path cannot execute external I/O in the engine process.

### Phase 3 — complete Repository and element templates

1. Expose installed templates to the Modeler.
2. Implement `Template -> Select`, supported property bindings and ordinary BPMN serialization.
3. Compile-validate the applied model and add round-trip/e2e coverage.
4. Generate the missing Accepted connector packages and enable the ADR-0167 build gate.
5. Only then consider remote registry, signing, popularity and community publishing.

**Exit criterion:** installing a package creates an element the user can select, configure, save, deploy and execute; the Repository catalog is complete by construction.

### Phase 4 — separate product epics from architecture epics

Do not mix the remaining unimplemented ADRs into the cleanup changes.

- **Modeler/product:** ADR-0030 Play mode, ADR-0031 history, ADR-0032 copilot, ADR-0178 RACI.
- **Automation/AI:** ADR-0117 agent task, after worker isolation and script trust posture are settled.
- **Lifecycle:** ADR-0130 process-version drain/deprecation.
- **Platform architecture:** ADR-0175 replicated partition cells, preceded by an ADR-0006 amendment that states the current single-partition boundary precisely.

Prioritize these by product demand. Their current `Proposed` status is accurate; they should not block the correctness/governance work above.

## 9. Suggested PR sequence

1. `docs(adr): separate decision and implementation status; reconcile roadmap`
2. `docs(runtime): publish the Atlas runtime contract`
3. `metrics: count job activations and lease timeouts after durability`
4. `repository: enforce an Accepted connector package for every released kind`
5. `modeler: consume and apply installed element templates`
6. `worker: offload SCIM end to end`
7. `worker: offload LDAP end to end`
8. `worker: offload SOAP end to end`
9. Follow-on worker slices for clio, SharePoint, Remedy and temis
10. Separate product epics for the nine genuinely unimplemented Proposed ADRs

This order makes the repository truthful first, then adds guards, then changes runtime placement one recoverable connector slice at a time.
