# ADR-0514: Mobile-first Tasks, Process Start and incident triage share one adaptive experience

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-10-30
- **Decision accepted:** 2026-10-30 by programme owner pblumer
- **Deciders:** atlas maintainers
- **Programme:** [MFE](../planning/mobile-field-experience-program.md), MFE0 (architecture baseline)
- **Open question:** Real iOS Safari/Android touch and keyboard measurements; form-js grid layouts on narrow screens; mobile incident-role boundary; whether the existing Console app shell or a reduced shared navigation surface is the entry point. Alarm acknowledgement and offline completion are **not** decided by this record.
- **Question checked:** 2026-10

## Context and problem statement

Atlas has three increasingly related ways for a person away from their desk to work:
the Tasks inbox and process-start view; a human form on an active user task; and
Operations incidents that may demand immediate authorized action. Smartphone,
tablet and foldable screens cannot be treated as squeezed versions of a full
multi-pane desktop Console.

The already accepted [ADR-0417](0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md)
makes Tasks and Shop one column below 860 CSS px and has Playwright Chromium coverage
at 390 px. It deliberately leaves form-js's multi-column layout untouched, other
Operations screens desktop-oriented, and real iOS/touch behaviour unverified.
This record must extend, not silently break, those guarantees.

The engine and API already provide [form-js Task and Start flows](0028-forms-and-the-tasks-app.md),
[durable incidents](0061-incident-model.md), [repair forms](0169-incident-repair-forms.md),
[cause-grouped incident reading](0337-incident-floods.md) and [durable incident facts
over CloudEvents](0435-one-catalogue-of-the-events-atlas-emits.md). An alarm is not
the incident itself; a user acknowledging an alarm must not resolve the incident.

MFE0's [repository baseline](../planning/mfe0-device-and-contract-baseline.md)
records what has and has not been measured.

## Decision drivers

- **Primary outcome:** Tasks and Process Start are equally prominent on a phone.
  Incident triage is a **role-gated** additional destination, not a full mobile Operations clone.
- **Same semantics and API:** form-js forms, BPMN task completion, deployment
  semantics, incident authorization and incident resolution remain authoritative.
- **Available space, not device names:** layout responds to actual content width,
  input/keyboard space and optional foldable viewport segments.
- **No hidden actions:** a missing button, sideways-scrolling last table column
  or obscured validation error must never make an essential action inaccessible.
- **Accurate states:** locally staged work, server-accepted completion,
  alarm delivery, human acknowledgement and incident repair are distinct.
- **Accessibility/privacy:** usable with touch, keyboard, screen reader, zoom,
  high contrast and reduced motion; no sensitive incident details on lock screens.
- **Invariants:** no new engine hot-path work, no side effect before WAL fsync,
  no UI-only authorization, no duplicate runtime state machine.

## Considered options

1. **Responsive extension of the existing UI and APIs**, with a
   role-aware mobile entry and shared form-js/incident components.
2. **Separate native mobile applications or a parallel task renderer**, exposing
   independently maintained forms, authorization and command paths.
3. **Simply apply a smaller CSS breakpoint to every Console surface**, including
   Operations and the full desktop BPMN Modeler.

## Decision outcome

**Chosen option: 1.** One authoritative set of task, form and incident commands,
with an adaptive presentation. **Accepted on 2026-10-30** by the programme owner.
**Implementation is Partial:** MFE1 navigation and the mobile server-confirmed completion receipt have landed. A deployable safety-inspection example is under review. MFE0 device checks, incident-alarm
acknowledgement, evidence and offline completion require their separate gates.

### Product boundary

- **Phone:** primary destinations **My Tasks**, **Start Process**; an optional
  **Incidents** destination is exposed only to users with the required server-
  verified operator permissions. Show meaningful states and deep links, not
  full process administration.
- **Tablet / unfolded foldable:** the same destinations; display context beside
  the current task or incident when width genuinely permits it. Preserve data
  and focus on resize, keyboard open/close, and hinge/posture changes.
- **Large-tablet Modeler:** initial read-only BPMN inspection; optional later
  bpmn-js touch/pen authoring only after its own gate (MFE6).
- **The Shop:** retain ADR-0417's existing narrow-screen contract; mobile work
  does not redesign its catalogue as part of MFE1.
- **Desktop:** preserve desktop layout and semantics; no second mobile XML,
  form schema or process definition is required.

### Tasks and Process Start

- Retain [ADR-0028](0028-forms-and-the-tasks-app.md): form-js is the
  canonical form schema/viewer; the server's existing task and start routes
  remain the completion/start authority.
- A multi-step phone *presentation* is not multiple BPMN user tasks. Existing
  BPMN wizard semantics continue to represent **actual modeled activities**,
  not visual pagination.
- The mobile flow retains draft values and errors during navigation,
  with field-level feedback and a separate review/confirmation step where
  business risk warrants it.
- Never show *Task completed* until a confirmed server completion. Ambiguous
  timeouts and offline drafts are not success states; their durable protocol
  requires a separately reviewed ADR under MFE5.
- Remote lookups cannot silently fabricate valid offline choices. Server
  revalidation remains authoritative after reconnect.

### Mobile incidents

- Reuse `GET /api/v1/incidents/summary` for a bounded, cause-first overview,
  including `groupsTruncated`/`ungrouped`, and request selected detail
  separately; do not transfer a flood-sized raw incident list to a phone.
- Display process, affected element, type, message where authorized, age and
  count; make primary actions readable without a desktop table or hover.
- A read-only incident view is not permission to retry, edit variables, alter
  worker configuration or bulk resolve. Check permissions server-side on every
  command and gate controls accordingly.
- `Resolve & retry` is an **engine action**, not `Acknowledge`. A successful
  retry command does not prove the upstream cause was fixed; show fresh state.
- Distinguish the *business safety finding* on a checklist from an *engine
  incident* that pauses execution.

### Alarm handoff (out of scope for this decision)

- Existing `atlas.incident.raised` / `atlas.incident.resolved` CloudEvents
  are durable **per-incident facts**, not a ready-made deduplicated paging or
  human-acknowledgement channel (ADR-0435).
- The grouping tuple in ADR-0337 is a **reading**, not a stable persisted alert
  identity. A flood-aware alarm recipient must define its own authorized,
  durable grouping/window and idempotency policy before sending.
- The subscription/transport of ADR-0433 is at least once. Human notification
  delivery, acknowledgement, ownership, escalation, quiet hours and fallback
  are **not** provided by that guarantee.
- Follow ADR-0343: model/coordinate notifications outside the processor,
  rather than creating an unmodelled second outbound delivery system.
  Do not throw platform incident signals directly into the engine, which
  ADR-0435 explicitly rejects.
- MFE3 requires a separate ADR comparing BPMN user-task acknowledgements with
  a durable operations-side acknowledgement store, including audited identity,
  group lifecycle, retry, replay and escalation. No option is selected here.

### Offline and local evidence (out of scope for this decision)

- MFE4 specifies durable external blob references and access checks, not
  raw photos in process variables/WAL.
- MFE5 specifies secure device storage, sign-out and loss-of-access handling,
  completion idempotency, known-vs-unknown responses, server reconciliation,
  and conflict/fencing semantics in its own ADR.
- Never equate browser local persistence with server durable completion.

## Acceptance and tests

- Chromium E2E at **320/360/390/600/768/1024/1280 CSS px**, portrait and
  landscape, and 200% zoom; no horizontal clipping of controls or form-js
  validation. Keep all ADR-0417 and existing desktop assertions.
- Real iOS Safari and Android Chrome tests for date/select/file inputs,
  virtual keyboard, safe areas, focus recovery and dynamic address bar;
  a foldable with actual hinged/segmented posture if supported.
- Task submit/validation, process start, role-gated incident triage,
  high-cardinality cause summary and denied/expired commands.
- Accessible names, spoken errors, focus order and actionable touch targets;
  test touch and keyboard, not only viewport emulation.
- Failure states for no network, delayed response and lost server response;
  do not claim offline synchronization until MFE5 acceptance.
- Any future mutation of incident/alarm/command state must include deterministic
  live/replay equality tests and post-fsync side-effect checks.

## Consequences

- **Positive:** a coherent field-working UX across Tasks, Start and authorized
  incident triage without a second BPMN engine or duplicate forms.
- **Negative:** shared responsive components require explicit content- and
  device-based tests; some desktop operations are intentionally not portable.
- **Deferred:** alarm lifecycle, push transport, offline write protocol,
  capture/evidence storage and tablet authoring are gated by MFE3–MFE6.

## As built (partial)

- [MFE1 PR #1468](https://github.com/pblumer/atlas/pull/1468) (merged): accessible Tasks/authorised Start navigation on narrow screens with six Chromium E2E scenarios and unchanged server roles.
- [MFE1 PR #1469](https://github.com/pblumer/atlas/pull/1469) (merged): mobile Task-completion receipt that appears only after the existing HTTP command is acknowledged. The browser test uses real form-js with a **mocked** safety-inspection task.
- A separate [MFE1 safety-inspection example](../programs/mfe/safety-inspection/README.md) is now proposed for review as a BPMN model plus form-js schema. It is not installed by default, nor does it demonstrate physical-device acceptance.
- The choice of a full field-worker process-start permission independent of broad `operator` is **not made** in this ADR: see issue [#1467](https://github.com/pblumer/atlas/issues/1467).
- **Under review:** MFE2 read-only mobile incident triage in draft PR #1479, after contract-only merged PR #1477; no accepted device evidence or incident acknowledgement.
- **Not landed:** MFE0 real-device/screen-reader/zoom acceptance, MFE3 alarm acknowledgement, MFE4 QR/evidence and MFE5 offline/idempotency. Marking this ADR Partial does not claim any of those gates.

## Links

- [MFE programme](../planning/mobile-field-experience-program.md)
- [MFE0 baseline](../planning/mfe0-device-and-contract-baseline.md)
- ADR-0028, ADR-0417, ADR-0061, ADR-0169, ADR-0337, ADR-0343,
  ADR-0433, ADR-0435
