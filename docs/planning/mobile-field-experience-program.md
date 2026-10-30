# Mobile & Field Experience (MFE) delivery programme

**Programme ID:** MFE.  
**Baseline:** Public `pblumer/atlas` main, reviewed 2026-10-30.  
**Strategic tracks:** Milestone S (single-binary server and web UI); Milestone A (Forms and Modeler); Milestone 4 (operability).  
**Owner:** pblumer (accepted 2026-10-30); slice coordinators remain unassigned until explicitly appointed.  
**Operational status:** [programme manifest](../programs/mfe/program.json). MFE0 is active as a source/architecture audit; MFE1–MFE6 remain planned. No MFE slice is delivered.

## Outcome

People can start business processes, work assigned BPMN user tasks and safely triage actionable incidents on a phone, foldable or tablet without using a cramped desktop Console. An operator can receive an incident alert, identify the affected process and cause, acknowledge responsibility, and choose an authorized remediation or escalation. Larger tablets may optionally support BPMN authoring via the existing bpmn-js modeler. Mobile work never promises that an offline draft or an unconfirmed command is durably completed.

**End-to-end proof:** An operator uses a phone to start a safety-inspection process, completes a field checklist with evidence, and watches a deliberately failing downstream worker raise an incident. The on-call operator receives one correctly routed alert for the grouped cause, authenticates via a deep link, acknowledges it, inspects the failure and carries out an authorized resolution or escalation. Verify both offline and lost-response variants; test a foldable/tablet layout and real devices. A business safety finding is process data, **not** automatically an engine incident.

## MFE0 source audit and proposal

- [Mobile contract and device baseline](mfe0-device-and-contract-baseline.md) — code/ADR audit only; physical-device checks remain open.
- [ADR-0514](../adr/0514-mobile-first-tasks-start-and-incident-triage.md) — **Proposed**, mobile Tasks/Start/Incidents presentation boundary; not approved and not implemented.
- MFE0 is **active**, coordinated by pblumer. Programme integration remains gated on architectural review and real-device validation.

## Existing baseline and gaps

| Surface | Current implementation | Gap / not yet demonstrated |
| --- | --- | --- |
| Tasks and Process Start | BPMN user tasks, form-js renderer, task inbox, start forms, grouped applications and favourites (ADR-0028, ADR-0466) | Field-focused mobile navigation and validation, adaptive multi-column forms and hardware acceptance |
| Narrow-screen UX | ADR-0417 accepted/landed; 860px one-column Tasks/Shop, larger touch targets; Chromium E2E at 390px/768px | iOS Safari, virtual keyboards, form-js multi-column schemes, real touch devices and foldable segments |
| Incidents | Durable incidents and resolve API (ADR-0061), Operations incident views, summary and bulk resolution (ADR-0337), incident repair forms (ADR-0169) | Mobile triage and restricted repair UX; acknowledgement/ownership is NOT the same as incident resolution |
| Incident facts and delivery | `atlas.incident.raised` / `atlas.incident.resolved` in CloudEvents feed, at-least-once subscribed push delivery (ADR-0433/0435) | Human-facing push notifications, recipients, policy, acknowledgement, deduplicated pages and monitored escalation |
| Device capture | form-js file picker and specialized logo handling | General evidence storage/reference contract, camera capture, barcode/QR entry, privacy constraints |
| Offline | No verified end-to-end offline completion protocol | Protected drafts, attachment staging, server-side idempotency, conflict detection and unknown-result reconciliation |
| Tablet modelling | Desktop bpmn-js Modeler | Optional supported touch/pen authoring and responsive property panels |

## Scope and explicit non-goals

**In scope:** Mobile Tasks and process initiation as equal primary destinations; accessible touch interactions; incident read/triage and alarm response for authorized operators; reliable evidence handling; optional offline drafts and synchronized completion; adaptable layouts on tablets/foldables; a later optional authoring mode.

**Out of scope:** A second process execution engine; bespoke BPMN renderer; full smartphone Operations/Insights/administration/Modeler; an independent notification subsystem inside the processor; a promise of guaranteed Web Push delivery; an offline process-execution runtime. No client-side UI decision may substitute for BPMN/DMN routing or server-side authorization.

## Architecture authority and unresolved decisions

- [ADR-0417](../adr/0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md) (Accepted/Landed) governs today's breakpoint. A new adaptive layout decision must explicitly extend/supersede its one-column rule where necessary.
- [ADR-0028](../adr/0028-forms-and-the-tasks-app.md) (Accepted/Landed) is the shared form-js/task contract. Keep one form model and one task completion path.
- [ADR-0061](../adr/0061-incident-model.md), [ADR-0169](../adr/0169-incident-repair-forms.md), and [ADR-0337](../adr/0337-incident-floods.md) are the incident sources of truth; do not conflate acknowledgement, incident resolution and root-cause repair.
- [ADR-0435](../adr/0435-one-catalogue-of-the-events-atlas-emits.md) and [ADR-0433](../adr/0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md) define durable platform facts and delivery. Alarm routing can consume these facts, but direct notifications must not escape the post-fsync boundary.
- [ADR-0343](../adr/0343-pending-work.md) and modelled notifications require alert channel choice to remain an explicit architecture decision; avoid another hard-coded delivery path in the engine.
- [ADR-0381](../adr/0381-a-problem-aggregates-incidents.md) is Proposed/Not started. Do not assume its problem lifecycle exists.
- A **new ADR** is required for durable alarm acknowledgements/ownership and escalation policy; another for offline completion idempotency, secure local state and loss-of-response reconciliation. These are proposals, not accepted design.
- A Web Push gateway is at best a transport adapter, not the authority for incident state. Delivery receipts, human acknowledgement and engine resolution must be separately observable and authorized. Do not expose sensitive incident payloads in lock-screen notifications.

## Delivery slices and gates

### MFE0 — Architecture and device baseline

- Audit existing Tasks/Start/Incidents/Forms UX on representative iOS Safari, Android Chrome, tablets, folded/unfolded devices and virtual keyboards; record before/after measurements.
- Define responsive content regions, WCAG/touch targets, device-size testing matrix, form-js extension boundaries, principal permissions and offline/alerting threat model.
- Review necessary ADRs before any schema, WAL, notification or completion-contract changes.
- **Exit:** reviewed architectural boundaries, maintainers' programme ownership, chosen first executable user journey and regression baseline. Documentation alone does not complete this slice.

### MFE1 — Mobile Tasks and Process Start

- One accessible mobile entry for **My tasks** and **Start a process**, including search, favourites, application context and authenticated deep links.
- Adaptive forms: step guidance on phones, contextual side-by-side panes at wider sizes, preservation of entered fields on rotation/fold.
- Vertical example: safety inspection with validation and confirmation; completion uses the existing HTTP API and only displays confirmed success after durability.
- **Exit:** device-size and E2E tests for claim, data validation, submit, back navigation, expired session and server refusal; no unsupported form-js features claimed.

### MFE2 — Mobile incident triage

- Operator-scoped incident inbox with grouping by cause using existing `GET /api/v1/incidents/summary` instead of downloading flood-sized row lists.
- Present impact, process, element, latest error and remediation options; provide safe read-only detail for roles without remediation rights.
- The person may inspect, navigate to a repair form and, if authorized, retry/resolve through the existing API after confirmation.
- **Exit:** incident authentication and role tests, stable grouped list at high cardinality, no accidental bulk resolution, distinction between acknowledging responsibility and resolving a technical incident.

### MFE3 — Incident alerting and escalation

- Derive alarms from *durable* incident feed facts, with routing policy, priority, quiet hours exceptions, deduplication by grouped cause, delivery health and a fallback channel for high-criticality cases.
- A human alert includes a minimal, privacy-safe deep link; authenticating and opening it does not count as acknowledging responsibility.
- Specify a durable acknowledgement/ownership record **outside** the incident resolution lifecycle and a monitored escalation when no authorized human acknowledges in time; preserve audit attribution.
- Use the existing CloudEvents/Worker and modelled workflow seams; resolve architectural questions in an ADR before code changes. Web Push alone is not a guaranteed alarm channel.
- **Exit:** simulated delivery loss, duplicated event, offline handset, recipient authorization, no-ack escalation and incident flood tests; no notification before fsync.

### MFE4 — Field capture and evidence

- Camera/file selection, QR/barcode deep links, checklist controls, field validation and explicit permissions. A scan opens a context, never silently completes a task or starts an unauthorized process.
- Evidence blobs live outside process variables/WAL; a validated durable reference and integrity metadata are the only values passed into the task completion.
- **Exit:** attachment size/type validation, unauthorized access, partial uploads/restart and safe resubmission tested end to end.

### MFE5 — Offline drafts and safe synchronization

- Protected, expiration-bounded local drafts and attachments with user/device isolation, sign-out cleanup and unambiguous local-save feedback.
- Introduce stable completion operation IDs, durable deduplication and status lookup so a timeout after server fsync never creates a second business completion.
- Revalidate current task assignment, authorization, form and evidence on reconnect. Conflict is explicit; never display *completed* for a locally queued or uncertain result.
- **Exit:** browser close/reopen, offline/online handover, duplicate submit, restart/replay, expired credentials, remote completion race and privacy threat tests. Full server recovery and race checks required.

### MFE6 — Large-tablet BPMN authoring (optional)

- First make bpmn-js diagrams readable, selectable and zoomable on large touch screens; only then evaluate touch/pen modelling and adaptive properties.
- Keep the same model, compiler validation and deployment semantics; never build a second BPMN renderer or promise full phone-based authoring.
- **Exit:** successful bpmn-js open/edit/save/reopen/deploy on tested tablets with keyboard, pen and touch parity where supported.

## Delivery dependencies and priorities

MFE0 gates every implementation slice. MFE1 and MFE2 can proceed as separate vertical products after the architecture review, with a default limit of two active integration slices. MFE3 depends on MFE2's actionable, authorized incident view; MFE4 depends on MFE1; MFE5 depends on MFE1/MFE4; MFE6 is optional after MFE1. MFE2 and MFE3 are **P0**, not after offline or tablet modelling.

Strategic ownership: [Milestone S](../../ROADMAP.md#milestone-s--single-binary-server--web-ui-) is the primary roadmap home; [Milestone A](../../ROADMAP.md#milestone-a--modeler--authoring-experience-) provides the shared form/editor design dependency; [Milestone 4](../../ROADMAP.md#milestone-4--operability-) owns existing incident runtime functionality. This programme coordinates across those tracks; it is not another execution subsystem.

## Invariants and delivery protocol

The processor hot path stays allocation-free; never send alerts or acknowledge completion before WAL fsync; single-writer partition ownership, one deterministic `applyToState` and immutable events continue to govern live and replay. Client UI state cannot become an independent execution authority. New durable acknowledgements and completion idempotency need separately reviewed state-placement decisions. Follow [AGENTS.md](../../AGENTS.md) and the [programme delivery protocol](../development/program-delivery.md): regression tests first, recovery equivalence for state-changing paths, complete Go build/test/race/vet/gofmt gates and browser E2E/device acceptance.

## Immediate next action

Programme owner pblumer reviews MFE0's existing-UX audit and the alert acknowledgement/notification architecture, appoints slice coordinators, and selects the first executable mobile Tasks/Start journey plus a parallel read-only incident triage spike. No status in this planning document represents shipped functionality.
