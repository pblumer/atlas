# ADR status reconciliation

**Repository:** `pblumer/atlas`
**Branch:** `claude/modest-hypatia-2ytnmd`
**Inspected commit:** `77c2e20` (`main`)
**Date:** 2026-09-09
**Scope:** all 295 numbered records, `docs/adr/README.md`, `ROADMAP.md`, and the
production packages the records describe.

This is Phase 0 of [the ADR implementation audit of
2026-08-25](adr-implementation-audit-2026-08-25.md), applied. The decision it
implements is recorded in `ADR-draft-two-states-for-a-record`; this document is the
evidence behind the classification it applied, so that a later reader can check the
call rather than take it.

## 1. What changed

| | Before | After |
|---|---:|---:|
| Numbered records | 295 | 295 |
| `Status: Accepted` | 162 | 279 |
| `Status: Proposed` | 131 | 15 |
| `Status: Draft` | 2 | 0 |
| `Status: Superseded by …` | 0 | 1 |
| Records carrying an `Implementation` field | 0 | 295 |

The new field distributes as `Landed` 277, `Not started` 11, `Partial` 6,
`Superseded` 1.

No record's argument was edited. The change to each record is its two front-matter
state lines. Three documentation defects the August audit named were fixed with it, and
they are listed in §5.

## 2. Method, and what it does not establish

Three signals were used, in this order.

1. **Citation from non-test production code.** 113 of the 131 `Proposed` records were
   cited by at least one non-test source file. Following the August audit's own rule,
   this was never treated as proof — a citation is a navigation aid — only as a way to
   rank records by how likely they were to be worth looking at.
2. **Identifier presence.** For each record, the backticked identifiers it names were
   extracted and looked for across the Go and JavaScript sources. 115 of 133
   `Proposed`/`Draft` records had at least four in five present.
3. **Direct inspection.** Every record where the first two signals disagreed, every
   record the August audit classified as anything other than "Implemented", and a
   sample of roughly twenty otherwise, were read against the code they describe.

For the 180 records the August audit covered, its per-record classification was carried
forward except where §4 records a re-check. For records 0181–0295 the classification is
this pass's own.

**What this does not establish.** Records classified `Landed` on signals 1 and 2 without
direct inspection — the bulk of the 277 — are supported by evidence that the code
exists, not by a reading that the code does what the record decided. The August audit
had the same boundary and said so. A record can also carry `Landed` and be wrong in a
way no test can catch, because no test reads prose and finds the code it means. The
periodic re-audit is what closes that, and nothing here replaces it.

The Go toolchain was available for this pass; `go test ./docs/adr` and `gofmt -l .` are
green, and the full sweep is reported in §6.

## 3. Records that are not `Landed`

These 18 are the whole of the non-`Landed` set. Each was inspected against the code,
except 0242 and 0243, which rest on the UI audit cited in their rows.

| ADR | Title | Implementation | Evidence |
|---|---|---|---|
| 0006 | Partition routing and cross-partition communication | Partial | The routing half is built: `model/record.go` bakes the partition into a key's high bits (`partitionShift = 48`), so resolving an owner is a bit-shift. The cross-partition half — a message emitted into another partition's command queue — has nothing, because there is one partition; ADR-0175 is where a second would come from. |
| 0025 | Extend the hand-written properties panel | Partial | The panel strategy is in use and documentation is carried. No execution listeners in `editor.js` or the compiler, which the record includes. |
| 0027 | Element templates for pre-configured, reusable elements | Partial | `api/repository.go` and `api/repositorystore.go` persist installed packages. No Modeler path reads `/api/v1/repository/installed` or applies a binding, so the "select and apply" half of the decision does not exist. |
| 0031 | Diagram version history in the Modeler | Not started | No snapshot store beside the draft store, no Versions control. `api/snapshot.go` is the engine-state snapshot of ADR-0109/0131, unrelated. |
| 0032 | In-Modeler AI copilot over the MCP/HTTP surface | Partial | The MCP authoring tools the record specifies exist (`atlas_get_draft_xml`, `atlas_save_draft`, `atlas_get_process_xml`). The copilot panel does not. |
| 0081 | A community marketplace for connectors, service tasks, script tasks | Partial | Curated catalog, manifest validation with checksums and the no-secret rule, the trust split, the gallery and durable install all exist. What is missing is the sentence the decision turns on: installing an artifact writes a template "which produces ordinary" elements. It does not, because the ADR-0027 path it names has no Modeler consumer. The remote registry and signing are absent too, but those the record lists as its own follow-ups and they are not what makes it partial. |
| 0130 | Deprecating a process version | Not started | No drain state. The record's own status line called it a sketch for discussion. |
| 0142 | Operational metrics over a Prometheus endpoint | Partial | The endpoint and most counters exist. `atlas_jobs_*` covers created/completed/failed/canceled only; activation and lease-timeout counters and an open-incidents gauge are absent, though the facts to count them are durable. |
| 0167 | A released connector ships in the marketplace | Not started | 10 catalog packages against 19 Modeler service-task kinds, and no registry or failing guard test. The mechanism the record decides is the guard, and it does not exist. |
| 0175 | Replicated partition cells for horizontal scale-out | Not started | No consensus implementation in the tree. `grep -w raft` over the Go sources returns nothing. |
| 0176 | Standards boundary and the Atlas runtime contract | Not started | `docs/runtime-contract.md` does not exist. |
| 0178 | Responsibility metadata — RACI on the element | Not started | No moddle fields, compiler metadata, matrix UI or validation. |
| 0187 | Database change events | Not started | `api/inboundsource.go` imports clio, Discord, Google Sheets and Jira. There is no SQL or Postgres source, and no outbox reader on the worker. |
| 0204 | Hosted apps — user HTML/JS served from an isolated origin | Not started | No separate origin, no configuration for one. |
| 0212 | The element-template applier | Not started | Neither half. `validatePackage` checks no extension element, and the Console still says an install "lands in your palette ready to…" — the misleading copy the record calls owed immediately. |
| 0242 | One route table describes the shell | Not started | Confirmed by [the UI ADR impact audit of 2026-09-04](ui-adr-impact-audit-2026-09-04.md): five lists still describe the same 41 routes. |
| 0243 | The views are built from shared parts | Not started | Same audit: 22 hand-built dialogs across 10 files, two button-size conventions, four "way back" shapes. |
| 0156 | In-process vs. out-of-process service tasks | Superseded | ADR-0164 revises its recommendation explicitly and ADR-0233 closes it out. Status is now `Superseded by ADR-0164`. |

## 4. Where the August audit is superseded by the current tree

Seven of the audit's classifications no longer hold. Each was re-inspected.

| ADR | August 2026-08-25 | Now | What changed |
|---|---|---|---|
| 0030 | Not implemented | Landed | The `playground` package runs a model on the real engine in a throwaway sandbox on a virtual clock, and `/api/v1/playground/sessions` serves the Modeler's Playground tab. |
| 0037 | Material hardening gap | Landed | The variable-size boundary the audit asked for exists: `engine.DefaultMaxVariable` and the budgets in `engine/budget.go`. |
| 0117 | Not implemented | Landed | `connector/agent` and `worker/agentconnector.go` exist; `connectorKindAgent` is a supervised worker-only kind. |
| 0154 | Partial / core landed | Landed | Unchanged in code. Reclassified under the definition in §2 of the new record: the delta cookie is listed in the record's own follow-ups, and a deferred extension does not make a record partial. |
| 0164 | Material transition gap | Landed | `DefaultOffloadedKinds()` now returns 17 kinds including every one the audit named as missing — temis, clio, SharePoint, Remedy, SCIM, LDAP, SOAP. |
| 0165 | Partial / core landed | Landed | SOAP is worker-backed (`worker/connectors.go`). WSDL binding is not missing work: the record chose the generic connector over it and rejected option 2 by name. |
| 0168 | Material transition gap | Landed | Same evidence as 0164; `api/connectorpayload_internal_test.go` describes ADR-0233's table as empty. |

The audit's P0 and several P1 items are therefore closed by work that happened after it
was written. The ones that remain open are 0167, 0176, and the 0007/0142 counters.

## 5. Documentation defects fixed with this pass

| Location | Was | Now |
|---|---|---|
| `ROADMAP.md` | ADR-0068 marked in progress, with connector scope reads and embedded subprocess scopes listed as remaining | Marked complete; both items are closed by ADR-0174 and ADR-0074 respectively |
| `ROADMAP.md` | ADR-0162 marked designed and unstarted | Marked complete; the record's implementation sequence is delivered end to end |
| `ROADMAP.md` | Link-events entry labelled ADR-0133, linking to `0132-link-events.md` | Label corrected to ADR-0132 |
| `engine/metrics.go` | A comment stating the lease-based worker protocol is not built | Corrected; the protocol is durable fact, and the missing counters are named as ADR-0142's open item |

One defect the audit named is deliberately **not** fixed here: the Repository UI copy
claiming an installed template lands in the palette. It is wrong, and correcting it is
the immediate half of ADR-0212 — product behaviour, not documentation. It is left for
that record so this change stays a governance change.

## 6. Verification

- `go test ./docs/adr` — green, including the new vocabulary, cross-field and index
  tests in `state_test.go`.
- `gofmt -l .` — no output.
- `go test -race -timeout=25m ./...` — 63 packages pass. Two fail, and both fail
  identically on unmodified `origin/main` at the same commit: `TestDateTimeBinding`
  in `expr` and `TestMatcherMatches/instance_not_older_than_five_days` in
  `api/taskfolder`. Both compare a fixture timestamp against FEEL's `now()`, which
  reads the wall clock, so they were correct until the wall clock moved past their
  fixtures — `api/taskfolder`'s case pins an instance 72 hours before 2026-09-07 and
  asks whether it is older than five days, which became true on 2026-09-09. This is
  the wall-clock dependence `AGENTS.md` forbids under Testing conventions. It is a
  pre-existing defect, unrelated to this change, and is deliberately not fixed here:
  the fix is a behavioural one (inject the clock into FEEL evaluation, or make the
  fixtures relative) and does not belong in a governance change.

## 7. What is still owed

Phases 1 through 4 of the August audit are untouched by this change, minus the items
§4 shows as closed. What remains, in the audit's own priority order:

1. Publish `docs/runtime-contract.md` (ADR-0176).
2. Add activation and lease-timeout counters, and decide the event shape for a
   replay-safe open-incidents gauge (ADR-0007/0142).
3. Add the catalog completeness registry and its failing guard (ADR-0167).
4. Correct the Repository UI copy, then build the applier (ADR-0212, ADR-0027).
5. Treat 0031, 0130, 0175, 0178, 0187, 0204, 0242 and 0243 as product and architecture
   epics, prioritised by demand. Their `Proposed` / `Not started` pair is now accurate,
   and they block nothing above.
