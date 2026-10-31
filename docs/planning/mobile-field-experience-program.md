# Mobile & Field Experience (MFE) delivery programme

**Programme ID:** MFE.  
**Baseline:** Public `pblumer/atlas` main, reviewed 2026-10-31.  
**Strategic tracks:** Milestone S (single-binary server and web UI); Milestone A (Forms and Modeler); Milestone 4 (operability).  
**Owner:** pblumer (accepted 2026-10-30); slice coordinators remain unassigned until explicitly appointed.  
**Operational status:** [programme manifest](../programs/mfe/program.json). MFE1 browser UI changes, including PR #1472, are merged (squash commit `9a2e911828286b628613ff86c0126aa246981fe1`), with green CI for that PR. MFE0 physical-device acceptance #1451 remains open, so no iOS/Android release acceptance is claimed. MFE1 process-start permission expansion is separately gated on ADR review #1471. MFE2–MFE7 are not delivered.

## Outcome

People can start business processes, work assigned BPMN user tasks and safely triage actionable incidents on a phone, foldable or tablet without using a cramped desktop Console. An operator can receive an incident alert, identify the affected process and cause, acknowledge responsibility, and choose an authorized remediation or escalation. Larger tablets may optionally support BPMN authoring via the existing bpmn-js modeler. Mobile work never promises that an offline draft or an unconfirmed command is durably completed.

### Cross-device product ambition (proposed expansion)

**One atlas. Every device. The right experience.** The north star is a personal, role-aware workplace where Tasks, process starts, decisions, Incidents, monitoring and authorized follow-up can be managed comfortably from desktop, tablet and phone, with a **future watch companion for minimal urgent signals and authenticated handoff**. The intent is to make atlas the natural place to manage daily work, not clone every desktop feature on tiny screens. Same BPMN definitions, forms, APIs and permissions; the presentation is adaptive to available width, input, context and risk.

[Cross-device experience and MFE7 proposal](mfe-cross-device-workplace.md) records a device/capability matrix, experience principles, four end-to-end journeys, acceptance tests, watch risks and phased priorities. This is **product direction**, not implementation or an amendment to accepted ADR-0514; watch actions and notifications require a separate ADR and MFE3 authoritative alarm design. MFE0 real tablet-landscape and iPhone evidence remain open.

**End-to-end proof:** An operator uses a phone to start a safety-inspection process, completes a field checklist with evidence, and watches a deliberately failing downstream worker raise an incident. The on-call operator receives one correctly routed alert for the grouped cause, authenticates via a deep link, acknowledges it, inspects the failure and carries out an authorized resolution or escalation. Verify both offline and lost-response variants; test a foldable/tablet layout and real devices. A business safety finding is process data, **not** automatically an engine incident.

### Candidate showcase — QR-linked work order acknowledgement ("Scan & Confirm")

**Status:** Proposed use case, not a delivered feature or committed integration slice. It complements the safety-inspection/alarming proof above; it does not replace the MFE0 hardware and architecture gate.

**Business journey:** A dispatcher starts a BPMN work-order process. Once its order data is durable, a configured Mail Worker sends the recipient an email with order details, optionally attaching a PDF work order. The document contains a QR code plus a human-readable URL; email readers on the same phone can tap the link instead of trying to scan their own screen. At the job site, workshop, delivery point or customer premises, a person scans the printed QR code (or follows the link), reviews the identified order on a responsive mobile Tasks surface, explicitly acknowledges receipt, handover or completion, and optionally records a remark or evidence. The ordinary BPMN user-task completion command advances the process only after the server confirms durability. A following gateway can route acknowledgement, rejection or a discrepancy to the right business handling.

**Boundary and UX rules:**
- Scanning is **navigation**, never consent, authentication, task claim, or completion by itself. Show a concise order summary, intended acknowledgement, any mandatory fields and one explicit confirmation step. No QR image can confer broader process access by exposing a raw instance or task key.
- Start with the **authenticated, assignment-checked** path using the existing Tasks/task API and a deep link that resolves a permitted context. A public-start token from [ADR-0029](../adr/0029-public-process-start-links.md) authorizes starting a *new* instance, **not** confirming an existing task.
- If external recipients must acknowledge **without an atlas account**, decide that as a separate security/authorization ADR before implementation: one order/action scope, short expiry, revocation, replay/duplicate handling, rate limits, payload limits and a deliberate audit/identity-assurance level. Possession of a forwarded email or photographed QR code must not silently equal verified identity; do not imply legal/electronic-signature assurance.
- Generate and deliver mail/PDF/QR artifacts **outside the processor**, through job-backed Workers after durable state transitions. Existing outbound Mail Worker support is not proof of a complete QR/PDF generation or public task-completion flow; inventory and test those components before committing a delivery slice.
- Evidence files live outside WAL process variables. Only validated durable references and integrity metadata travel with completion. An offline device may save a clearly labelled local draft, never display a completed receipt until server confirmation; queued and unknown outcomes belong to MFE5.

**MFE slice ownership:** MFE0 validates the QR/link threat model and device matrix; **MFE1** owns the authenticated mobile task detail, accessible explicit confirmation and deep-link navigation; **MFE4** owns QR/barcode entry and optional field evidence; **MFE5** adds safe offline draft/sync and duplicate/unknown-result reconciliation. Mail/PDF composition is a Worker-side integration dependency, not a new engine execution path. MFE2/MFE3 incident alerting remain separate priorities.

**Acceptance ideas:** Test a QR printed in a PDF and an email link on real phone/tablet/foldable devices; authorized versus wrong-user access; expired/revoked/reused links; cancelled/already-completed work orders; duplicate submissions and a lost response after fsync; deliberate refusal or discrepancy; evidence upload/restart; browser close/reopen and offline reconnect once MFE5 exists. Verify live/replay equivalence for any new persisted acknowledgement fact, and ensure no external mail is sent before the originating event is durable.

## MFE0 source audit and accepted architecture

- [Mobile contract and device baseline](mfe0-device-and-contract-baseline.md) — source/ADR audit plus merged Chromium Tasks/Start/form-js regression tests; physical-device checks remain open.
- [MFE0 real-device acceptance runbook](mfe0-device-validation-runbook.md) — hardware test matrix, evidence requirements, accessibility/security cases, and handoff; all hardware outcomes remain unverified.
- [ADR-0514](../adr/0514-mobile-first-tasks-start-and-incident-triage.md) — **Accepted on 2026-10-30 / partially implemented**. Shared adaptive mobile Tasks/Start/authorized Incidents UX is approved as the architecture contract; it is not yet implemented.
- MFE0 is **active**, coordinated by pblumer. The architecture decision is accepted; merged PRs #1453/#1454 prove Chromium regressions only. Real-device validation and implementation handoff remain open, including the separate MFE3/MFE4/MFE5 ADR gates. Initial MFE1/MFE2 scoped issues are [#1456](https://github.com/pblumer/atlas/issues/1456) and [#1457](https://github.com/pblumer/atlas/issues/1457).

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

**Out of scope (current committed MFE1–MFE6 work, not the long-term product ambition):** A second process execution engine; bespoke BPMN renderer; a complete desktop Operations/Insights/administration/Modeler mirrored on a phone; an independent notification subsystem inside the processor; a promise of guaranteed Web Push delivery; an offline process-execution runtime. No client-side UI decision may substitute for BPMN/DMN routing or server-side authorization.

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

**Controlled early integration (2026-10-31):** The programme owner requested continuation with MFE1 despite MFE0 device acceptance still open. The first test-first PR changed only responsive Tasks/Start *navigation and translations* plus browser regression tests; it has merged. The second MFE1 UI-only slice (#1469) is merged: it adds a completion receipt **after server acknowledgement** while preserving the server-owned completion command and existing form-js validation. The next test-first example is [safety inspection](../programs/mfe/safety-inspection/README.md), a deployable BPMN/user-task/form-js pair that branches on a business finding without introducing an engine incident or requiring camera/QR/offline features. It must preserve the existing operator-only Start permission and the server-owned task/instance mutation paths. This is a documented narrow **implementation exception**, not an MFE0 acceptance, device release authorisation, or permission to implement unreviewed security changes. MFE0 #1451 stays active; two active slices (MFE0/MFE1) respect the programme WIP cap.

**Security gate:** The present `POST /api/v1/processes/{key}/instances` requires `operator` (ADR-0209). That role also grants operational privileges; simply offering Start to `user` would violate least privilege. [Issue #1467](https://github.com/pblumer/atlas/issues/1467) tracks a new ADR for scoped self-service process starts. Until then only permitted operators see both mobile destinations; ordinary task users see Tasks without a misleading Start action.

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

### MFE7 — Wearable companion and cross-device handoff (proposed)

- **Planned only:** a future watchOS/Wear OS companion should deliver a privacy-safe, role-authorized urgent signal and hand off to the already authenticated phone Tasks/Incidents experience. This is not a shrinking of the web Console onto a watch.
- MFE1 provides authenticated deep links; MFE3 must first settle durable alarms, acknowledgement/ownership, recipients, delivery fallback and security. Do not mistake a polled count for a delivered alarm. Shared unsent drafts would depend on the separately accepted MFE5 design.
- A new ADR must decide native/watch delivery adapters, risk-limited actions, device pairing, explicit consent, account/permission revocation, audit attribution, lock-screen privacy and failure modes before implementing any write operation.
- **First proof:** a watch displays a minimal urgent incident indicator, then launches an authenticated, server-authorized phone deep link to a read-only cause summary. Test actual watch hardware, lost delivery, logout and duplicate alert cases. No engine incident resolve or task completion from a notification.
- **Exit:** owner-approved security/notification ADR, real-device evidence, loss/fallback and post-fsync visibility tests. Detailed [experience contract](mfe-cross-device-workplace.md).

## Delivery dependencies and priorities

MFE0 gates every implementation slice. MFE1 and MFE2 can proceed as separate vertical products after the architecture review, with a default limit of two active integration slices. MFE3 depends on MFE2's actionable, authorized incident view; MFE4 depends on MFE1; MFE5 depends on MFE1/MFE4; MFE6 is optional after MFE1. MFE7 depends on MFE1/MFE3 and remains a future research/ADR slice; full unsent-draft continuity additionally depends on MFE5. MFE2 and MFE3 are **P0**, not after offline, tablet modelling or watch companion work.

Strategic ownership: [Milestone S](../../ROADMAP.md#milestone-s--single-binary-server--web-ui-) is the primary roadmap home; [Milestone A](../../ROADMAP.md#milestone-a--modeler--authoring-experience-) provides the shared form/editor design dependency; [Milestone 4](../../ROADMAP.md#milestone-4--operability-) owns existing incident runtime functionality. This programme coordinates across those tracks; it is not another execution subsystem.

## Invariants and delivery protocol

The processor hot path stays allocation-free; never send alerts or acknowledge completion before WAL fsync; single-writer partition ownership, one deterministic `applyToState` and immutable events continue to govern live and replay. Client UI state cannot become an independent execution authority. New durable acknowledgements and completion idempotency need separately reviewed state-placement decisions. Follow [AGENTS.md](../../AGENTS.md) and the [programme delivery protocol](../development/program-delivery.md): regression tests first, recovery equivalence for state-changing paths, complete Go build/test/race/vet/gofmt gates and browser E2E/device acceptance.

## Immediate next action

Review the [MFE1 deployable safety-inspection example](../programs/mfe/safety-inspection/README.md), verify its BPMN/form tests and CI, and run both business outcomes against an atlas instance. Keep [MFE0 real-device acceptance #1451](https://github.com/pblumer/atlas/issues/1451) open and collect actual iOS/Android, accessibility and 200% zoom evidence before claiming a mobile release. [MFE1 Tasks/Start #1456](https://github.com/pblumer/atlas/issues/1456) remains active; [MFE2 read-only incident triage #1457](https://github.com/pblumer/atlas/issues/1457) remains planned, and non-operator start authorisation still requires [#1467](https://github.com/pblumer/atlas/issues/1467). MFE3 alarm acknowledgement, MFE4 field evidence and MFE5 offline completion need separate accepted decisions.
