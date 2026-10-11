# ADR status reconciliation, 2026-10-11

**Repository:** `pblumer/atlas`
**Based on:** `main` at `2f8e27f4`
**Date:** 2026-10-11
**Scope:** the status findings of [the status report of 2026-10-10](statusbericht-2026-10-10.md),
measure 1.2 — records whose `Implementation` overstates or understates what is built, and
statements in records and docs that no longer match the code.

Every change below was checked against the code before it was made; the report was the
list of suspects, not the evidence. The rules applied are the ones in
[the ADR index](../adr/README.md#the-two-states-of-a-record): a record is `Partial` when part
of what it *decided* is missing, not when a follow-up it defers is; `Landed` requires
`Accepted`.

## Records that said more than is built

| Record | Was | Now | Built | Missing | How the absence was established |
|---|---|---|---|---|---|
| [0041](../adr/0041-connector-management-and-secret-store.md) | Landed | Partial | `credentialsRef` resolved from the vault, then `ATLAS_CONNECTOR_<REF>_TOKEN` (`api/connectors.go`); managed connector records and their Console page | Mounted-secret-file backend (A2), inline config file (A1), external secret manager (A4) | The only `*_FILE` variable is `ATLAS_VAULT_KEY_FILE` (`api/vault/vault.go`); no secret-manager client anywhere |
| [0117](../adr/0117-ai-agent-task.md) | Landed | Partial | The service task via ADR-0256 (`compiler/connector_compile.go`, `connector/agent/aitask.go`); tool use via ADR-0253 | Output schema; `maxIterations`/`timeout`/`tokenBudget` in the model; `VTAgentRun` with token usage; `GET …/agent-runs`; its Operations view | `VTAgentRun` and `agent-runs` occur in no `.go` or `.js` file; the compiler reads only worker, model, prompt, result variable and retries |
| [0128](../adr/0128-process-applications.md) | Landed | Partial | Capabilities 1 and 2: applications, releases, deployment targets and promotion (ADR-0129) | Capability 3, the git source binding | See 0134 |
| [0134](../adr/0134-git-backed-applications.md) | Landed | Partial | Source layout, format version, portable key (`api/appsource.go`); tar.gz export and import (`api/appsource_http.go`) | The git binding: clone, sync, commit, push, fast-forward-or-refuse, repository credential | No git library in `go.mod`; `api/appsource_http.go` names the git slice as separate and future |
| [0207](../adr/0207-worker-type-packaging.md) | Landed | Partial | Step 1: the manifest parser and validator in `workertype/` | Steps 2–6: catalog exposure, Repository install and removal, OCI launch verified by digest, signature policy, cloud-native integration | No package outside `workertype/` imports it |
| [0208](../adr/0208-worker-type-packages.md) | Landed | Partial | Steps 1–3 and 6: runtime modes, `WorkerTypeDefinition`, built-in metadata, `GET /api/v1/worker-types`, `/configured-workers` | Repository packages and the `installed` origin, persisted digest and signature state, type id and version reported by Worker Instances, version pinning, the uninstall refusal | Only `WorkerTypeOriginBuiltIn` is defined (`api/builtinworkertypes.go`) |
| [0216](../adr/0216-mockups-are-one-view.md) | Landed | Not started | — | `MockReport` store, `/api/v1/mocks`, the Mockups view, the Jira mock | No type `MockReport` and no `/api/v1/mocks` route; the Console still has one page per kind (`api/web/app.js`) |

Statements elsewhere that repeated these claims were corrected with them: the README's
"git-backed" process applications; [ADR-0253](../adr/0253-agent-tool-calls-drive-adhoc-activation.md)'s
second audit view in the agent-run record; [ADR-0217](../adr/0217-openapi-mock-server.md) and the
comments in `cmd/atlas/main.go` and `connector/rest/openapimock/`, which said the OpenAPI
mock reports into a Mockups view that does not exist. The changelog entries of earlier
releases are left as they were published.

## Records that said less than is built

| Record | Was | Now | Evidence |
|---|---|---|---|
| [0303](../adr/0303-script-sandbox-isolation.md) | Accepted / Partial | Accepted / Landed | `connector/script/sandbox.go`, `sandbox_linux.go`, `process_unix.go`; `sandbox_internal_test.go`, `sandbox_policy_linux_test.go`. #895–#898 are the record's own follow-ups |
| [0311](../adr/0311-portal-approval-page.md) | Accepted / Partial | Accepted / Landed | `api/approvals.go` and the `genehmigung-*.bpmn` models; the page half was withdrawn for ADR-0394 |
| [0313](../adr/0313-portal-language-follows-the-browser.md) | Accepted / Not started | Accepted / Partial | Browser language and narrowing in `api/web/shop.js`; account language and the missing-key fallback are not built |
| [0316](../adr/0316-portal-theme-per-catalogue.md) | Accepted / Partial | Accepted / Landed | The upload screen it named as missing is in `api/web/catalog-admin.js`, tested in `api/catalogappearance_internal_test.go` |
| [0333](../adr/0333-inventory-commissioning-load.md) | Accepted / Partial | Accepted / Landed | `api/inventoryload*.go`; its stated blocker, reconciliation, is built under ADR-0334 |
| [0335](../adr/0335-starting-an-instance-returns-its-key.md) | Proposed / Not started | Accepted / Partial | The chosen option shipped with ADR-0416 (`Command.Created`, `instanceKey` in the start answer); the CSV start and four hosted apps are not moved yet |
| [0403](../adr/0403-the-estate-graph-is-derived-from-the-log.md) | Accepted / Not started | Accepted / Landed | A rules record; the estate graph of ADR-0402 keeps them (`api/panorama/mesh.go`) |
| [0409](../adr/0409-the-archive-substrate-waits-its-schema-does-not.md) | Proposed / Partial | Accepted / Landed | The column guards run in `go test` (`model/archiveschema_test.go`, `api/archiveschema_test.go`); the refused substrate is the decision |
| [0410](../adr/0410-a-peer-credential-carries-the-reach-a-membership-cannot-give-it.md) | Accepted / Partial | Accepted / Landed | `api/apitokenstore.go`, `api/apitokenscope.go`, `api/reach_internal_test.go`; the estate altitude it awaited is ADR-0402, landed |
| [0425](../adr/0425-a-product-lifecycle-is-one-process-with-message-triggers.md) | Accepted / Partial | Accepted / Landed | `change`, the one missing operation, is started by the action act (`api/orderaction.go`) and delivered by `api/orderchange.go` |

[ADR-0312](../adr/0312-portal-catalogue-order-inventory.md) stays `Partial`, with a
note: reconciliation and the commissioning load are built, but no route lets a person give
up on a failed line, and no deadline escalates its incident.

Moving 0335 and 0409 from `Proposed` to `Accepted` follows the index's rule that a record
whose chosen option has shipped is a record whose decision was taken. Two records the report
also named were **not** moved, because accepting them would be a decision the code cannot
make: [ADR-0299](../adr/0299-worker-type-admission-criteria.md) (admission criteria for a Worker
Type; ADR-0442 applies them, but the record is policy) and
[ADR-0387](../adr/0387-the-catalogue-against-the-standards-boundary.md) (its mapping
document exists, but its open question leaves adoption open).

## Statements that no longer matched the code

Corrected with a dated *As built* note beside the original text, which is kept:

- **Open work that is built:** ADR-0007 (registry, type-keyed pull, long-poll, fencing,
  counters), 0037 (variable-size limits, ADR-0294), 0050 (temis wiring and its Modeler
  option), 0052 (send and receive tasks), 0071 (groups, roles per endpoint group, grant
  audit; runtime access control begun), 0079 (native providers, call budget, implicit TLS),
  0082 (error, signal, escalation and conditional event subprocesses), 0098 (actor in the
  timeline, MCP audit tool, operator role), 0103 (transactions), 0142 (`--log-level`, job
  counters) and 0154 (pooling, paging, per-value modify, client certificate, DirSync).
- **In-process execution described as current:** ADR-0164 and 0165; eighteen kinds run on a
  worker by default since ADR-0168 and ADR-0233.
- **"Not deletable":** ADR-0327, 0329 and 0330; ADR-0336 built both deletes, and 0329's guard
  test was replaced by `api/decisiondelete_internal_test.go`.
- **Wording:** ADR-0419's decision no longer opens with "(Proposed…)"; ADR-0376 notes that its
  table has ten tools; ADR-0429 §9 says which three decisions keep it `Partial`.
- **Identifiers that no longer exist:** ADR-0160 and 0163 (`workerdialog.js` and its
  functions), 0255 (`--supervise-connector`), 0277 (`ArrivalsOnNode`, ADR-0290), 0325 (the
  `dmn` package), 0343 (`examples/erinnerung/erinnerung.bpmn`) and 0428
  (`catalogOwnerOfName`), and a comment in `api/web/infomodel-import.js`.
- **Front matter:** in ADR-0127, 0139, 0154, 0166, 0191, 0200, 0205, 0211, 0221 and 0251 the
  `Implementation` line stood inside the Status line's parenthesis; it now follows it.
- **The index's "(amended)":** 23 rows of amended records lacked the marker. They have it
  now, and `TestTheIndexSaysWhichRecordsWereAmended` holds the index to the records from here
  on.

Outside the records:

- `docs/runtime-contract.md` applies to 0.9.x, and its Jobs row names the BPMN error a worker
  can throw.
- The Console's Repository view no longer says an installed template lands in the palette;
  installing keeps it on the server, and applying it is ADR-0212, not built.
- `docs/compliance/isds-offene-punkte.md` O-02: the task read is gated like the list; the
  route and role counts, which had gone stale, are dropped rather than recounted.
- `docs/comparisons/mim.md` cites ADR-0173 for the SQL kinds, not ADR-0170.
- The package comment of `api/server.go` and the comment on `handleCompleteJob` no longer
  say there are no leases.

## Left open

- `docs/comparisons/mim.md` calls Oracle the remaining database follow-up in two places and
  "deliberately not building" in a third. Which is meant is a product decision.
- [ADR-0354](../adr/0354-approval-list-order.md) decided the sort of an approval
  list on a page ADR-0394 replaced; the inbox now orders approvals by its own rule. Whether the
  record is superseded or still owed is a decision, not a correction.
- The medium findings that are missing code rather than wrong text — ADR-0211's mesh
  completeness test, ADR-0246's process tab for a plain user, ADR-0283's tailer, ADR-0411's
  served types in the Console — are work, not status, and stay with the status report.
