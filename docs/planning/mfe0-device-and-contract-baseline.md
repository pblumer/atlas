# MFE0: Repository and device-contract baseline

**Status:** Repository/code audit completed 2026-10-30; browser and physical-device measurements **not executed**.  
**Programme:** [Mobile & Field Experience](mobile-field-experience-program.md), slice MFE0.  
**Architecture proposal:** [ADR-draft-mobile-first-tasks-start-and-incident-triage](../adr/draft-mobile-first-tasks-start-and-incident-triage.md) (Proposed, not accepted).

## Exact evidence on `main`

| Question | Evidence | Verified status |
| --- | --- | --- |
| Can a user work Tasks on a narrow phone? | [ADR-0417](../adr/0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md), [tasks-responsive.spec.mjs](../../e2e/tasks-responsive.spec.mjs) | Implemented single-column list/detail with Back button at <860px. Browser geometry tested at 390px; not tested on physical device here. |
| Can a user start a process on a phone? | [tasks-responsive.spec.mjs](../../e2e/tasks-responsive.spec.mjs) | Implemented Start list/detail, separate mobile selection; test checks layout at 390px. |
| Can form-js render a multi-column schema on 320/390px? | [ADR-0417](../adr/0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md) explicitly leaves viewer layout untouched | **Unverified.** Existing page width is not evidence of a complex form's fit. |
| Are date/select inputs and software keyboard usable on real iOS/Android? | ADR-0417 open question | **Unverified**; no device lab was run. |
| Do tasks submit through the engine? | [ADR-0028](../adr/0028-forms-and-the-tasks-app.md), [task completion handler](../../api/handlers.go), [form refusal E2E](../../e2e/form-refusal.spec.mjs) | Implemented through `POST /api/v1/tasks/{key}/complete` with form variables; server authorization/revalidation is authoritative. |
| Is there incident support? | [ADR-0061](../adr/0061-incident-model.md), [incident UI](../../api/web/incidents.js), [incident E2E](../../e2e/incidents-ops.spec.mjs) | Durable raise/resolve; operator UI, variable/worker/repair actions. E2E mainly exercises desktop operations; **not** mobile incident UX. |
| Can a mobile incident inbox safely aggregate a flood? | [ADR-0337](../adr/0337-incident-floods.md), `GET /api/v1/incidents/summary`, [incident operations E2E](../../e2e/incidents-ops.spec.mjs) | Implemented grouping by process definition + BPMN element + incident type; API returns `groupsTruncated` and `ungrouped`. Not an alarm state machine. |
| Are incident facts delivered to integrations? | [ADR-0435](../adr/0435-one-catalogue-of-the-events-atlas-emits.md), [ADR-0433](../adr/0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md), [incident feed test](../../api/incidentfeed_internal_test.go) | Implemented CloudEvents per-incident raised/resolved; at-least-once feed push to a subscribed receiver, not guaranteed delivery to a person's handset. |
| Is there durable alarm acknowledgement or push-to-phone? | Existing incident/feed ADRs and source search for `PushManager`, `showNotification` | **No evidence found** of an end-to-end human paging/acknowledgement mechanism. Must be designed, not inferred from the feed. |
| Can users safely complete offline? | Task HTTP completion handler, search for service-worker registration and offline API | **Not demonstrated**: no verified local staging, completion operation status, server dedup or reconnect contract. |
| Can we use device camera for process evidence without WAL payloads? | [tasks-logo-upload.spec.mjs](../../e2e/tasks-logo-upload.spec.mjs) | Specialized logo upload pattern exists; general evidence API, ownership and offline staging are not proven. |
| Is full tablet Modeler editing touch/pen-ready? | bpmn-js embedded Modeler, MFE6 programme scope | **Unverified**, not a release promise. |

**Evidence classification:** An indexed code search yielding no `PushManager` or `serviceWorker.register` result is only an absence of retrieved evidence, not a proof that the entire codebase contains none. No browser was driven and no test executed as part of this document-only audit.

## User journeys to instrument

1. **MFE1 — Field safety check:** Open a task from a mobile link, read all field instructions, mark a problem, trigger conditional validation, stage photo evidence, review and submit. Show completion only on a confirmed server response; model a safety finding as business data rather than an engine incident.
2. **MFE1 — Start:** From mobile landing, choose an application/favourite, open a start form, validate/submit, return to a durable process-instance reference; verify accessibility and duplicated tap handling.
3. **MFE2 — Operational failure:** Worker retries exhaust, one incident is raised, operator opens grouped overview on a phone, sees context, optionally uses the existing repair form, then uses *Resolve & retry* with server authorization. Check that resolving does not imply successful recovery.
4. **MFE3 — Alarm:** A hundred incidents sharing one cause generate per-incident feed events. A downstream, authorized alert coordinator deduplicates an incident wave, sends minimal-content notifications, records a separate human acknowledgement and escalates if none arrives. This is **proposed**, not an implemented flow.
5. **MFE5 — Interrupted work:** Offline local draft; photo upload pending; reconnection with changed task assignment; timeout after server fsync; duplicate manual resend. The UI must never turn an unknown result into a success.

## Browser/device acceptance matrix

| Device class | Viewport or environment | Required examination |
| --- | --- | --- |
| Small phone | 320px and 360px CSS viewport | Complete fields, labels, errors and footer; smallest practical touch targets |
| Typical phone | 390px and 412px | Tasks/Start home, one-hand flow, rotated device and screenshot comparison |
| Android Chrome hardware | Portrait/landscape | Back gesture, camera/file chooser, select/date, text scaling, virtual keyboard |
| iPhone Safari hardware | Portrait/landscape | Dynamic bars, safe-area insets, focus scrolling, camera/file chooser, virtual keyboard |
| Small tablet | 600px and 768px | Context visibility, readable cards, orientation switching |
| Large tablet | 1024px and 1280px | Task + context layout, operator detail pane, pointer and touch |
| Foldable | Folded and unfolded; segmented posture when available | Content never crosses hinge; preserve form data and focus |
| Accessibility | 200% zoom, screen reader, keyboard, high contrast/reduced motion | Named actions, visible errors/focus, no essential gestures |
| Connectivity | Offline, slow response, response lost, session expired | Honest saved/queued/uncertain/confirmed/conflict states; no duplicate business action |

Do not label this matrix **passed** until the corresponding instrumented run and device/model/browser versions are documented with screenshots or test output.

## Known specification hazards

### Responsive breakpoints

ADR-0417 uses a fixed 860px threshold. Its existing tests rely on 390px, 900px and
1280px behaviour; changing the breakpoint without an explicit amendment is a
regression risk. Use actual container width for multi-pane layouts while retaining
the existing externally tested navigation semantics until an accepted ADR replaces them.

### Form meaning and validation

The form-js renderer is the schema source; do not introduce an independent
mobile form language. A mobile presentation step must not create an extra BPMN
user task. Remote options and task assignment must be revalidated by the server.
The UI may highlight local errors but never bypass engine authority.

### Incident feeds and alarm floods

ADR-0435 deliberately prohibits throwing a platform incident as an engine
signal to self-start an incident listener. Its CloudEvents are durable per
incident but may be delivered at least once. ADR-0337 groups by a tuple, not a
persisted stable alarm wave ID. Any human paging coordinator requires its own
verified idempotency/grouping, escalation ownership and privacy model.
Treat `acknowledged` (a person's promise to act) differently from
`resolved` (the engine unparked its failed element), and from verified recovery.

### Privacy and offline

An unlocked browser storage surface is not automatically an encrypted
offline vault. Device data retention, sign-out cleanup, sensitive incident
fields in notifications, attachment authorization and a lost-response
completion protocol require explicit policy and ADR review.

## Architecture and security decisions to review in MFE0

1. Accept/revise [proposed ADR-draft-mobile-first-tasks-start-and-incident-triage](../adr/draft-mobile-first-tasks-start-and-incident-triage.md) on mobile navigation and component reuse; decide role-gated Incidents entry and desktop compatibility.
2. For MFE3, choose modelled BPMN human acknowledgement tasks vs a new durable operations-side acknowledgement record; define stable alarm episode identity, deduplication, authorised recipients, escalation/fallback and retention. Separate ADR mandatory.
3. For MFE5, choose a server-enforced idempotent completion contract with outcome lookup and local storage threat model. Separate ADR mandatory.
4. For MFE4, agree durable attachment store/API/reference format and retention/authorization; never place photographs in process variables.
5. Run device and E2E matrix before closing MFE0. Do not mark the slice complete based on a documentation PR.

## First executable follow-up

After architecture acceptance, implement **MFE1 Task/Start mobile navigation** as one
small vertical TDD slice on the existing API/UI: tests for 320/390px and tablet
visibility, role-independent Tasks/Start entry, no overflow, valid and refused
submissions, and stable navigation across orientation changes. In parallel,
prepare MFE2's **read-only, cause-first incident view** using existing
`/api/v1/incidents/summary`; do not yet implement alarm acknowledgement.
