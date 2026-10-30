# ADR-0514: Mobile-first Tasks, Process Start and incident triage share one adaptive experience

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-30
- **Deciders:** atlas maintainers
- **Programme:** [MFE](../planning/mobile-field-experience-program.md), MFE0 (architecture baseline)
- **Open questions:** Real iOS Safari/Android touch and keyboard measurements; form-js grid layouts on narrow screens; mobile incident-role boundary; whether the existing Console app shell or a reduced shared navigation surface is the entry point. Alarm acknowledgement and offline completion are **not** decided by this record.

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

## Proposed decision

**Option 1**: One authoritative set of task, form and incident commands, with
an adaptive presentation. This is **proposed**, not approved or landed.

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

## Links

- [MFE programme](../planning/mobile-field-experience-program.md)
- [MFE0 baseline](../planning/mfe0-device-and-contract-baseline.md)
- ADR-0028, ADR-0417, ADR-0061, ADR-0169, ADR-0337, ADR-0343,
  ADR-0433, ADR-0435
