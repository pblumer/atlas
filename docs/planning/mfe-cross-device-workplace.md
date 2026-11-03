# MFE cross-device workplace vision

**Status:** Product direction / proposed experience contract, **not** a shipped feature
or an amendment to accepted ADR-0514.  
**Programme:** [Mobile & Field Experience (MFE)](mobile-field-experience-program.md);
MFE0–MFE7. **Owner:** pblumer. **Roadmap:** Milestone S (primary), Milestone A
(bpmn-js/tablet authoring), Milestone 4 (operations and incident semantics).

## North star

**One atlas. Every device. The right experience.**

atlas should become the place where people *want* to manage their daily work:
their tasks, new process requests, approvals, exceptions and process outcomes,
without needing to switch to an external task system. The product must make
the **next safe action** obvious at the device in hand, not merely compress a
desktop Console into a smaller rectangle.

The same principal, published BPMN/DMN definitions, form-js schemas, server-side
authorization, command API, audit facts and durable process state remain
authoritative everywhere. This is an **adaptive product** with progressive
disclosure, not four competing clients with divergent business semantics.

This ambition does **not** mean every desktop capability belongs on a watch.
The expectation is that the *work* remains continuous as a person changes
devices: see a concise signal on a watch, decide or continue on a phone, get
context on a tablet and model or investigate deeply at a desktop.

## Device posture and progressive disclosure

| Capability | Desktop / large screen | Tablet / foldable | Phone | Watch (proposed) |
| --- | --- | --- | --- | --- |
| Personal work home | Full work overview, filters and context | Touch-first overview, adaptive panels | Ranked tasks, starts and important notices | Private, bounded summary only |
| BPMN user tasks | Full form-js task workspace | Form + context where width allows; one pane when it does not | Guided, explicit completion with confirmation | Read status; simple safe responses only if separately approved |
| Start process | Published permitted start forms | First-class, touch-accessible | First-class, role-scoped | Open an authenticated handoff; do not imply a form can run on the watch |
| Incidents | Full authorized operations and remediation | Cause-first triage and appropriate repair actions | Role-gated cause-first summary, minimal context | Privacy-safe urgent signal + deep link; no automatic repair |
| Monitoring and Insights | Full charts, heatmap, history, filters | Adaptive KPI and drill-down | Concise health and exception cards | Critical status counts only |
| BPMN modelling | Full bpmn-js authoring and publishing | Read-only first; touch/pen editing later (MFE6) | View a minimal context/diagram only where readable | No Modeler |
| Field evidence / offline | Server or desktop workflows | Camera/file as permitted, staged drafts (MFE4/5) | Camera/QR and offline draft only after MFE4/5 gates | No unreviewed evidence capture or offline completion |

**Important:** Each cell is a product *target*, not an implementation claim.
MFE1 currently provides only limited mobile navigation, server-confirmed
completion feedback and example resources; MFE0 physical device evidence is
still open. MFE2/MFE3 and MFE4/MFE5 remain separately gated. A watch client
has not been chosen or implemented.

## A single 'My work' mental model

1. **Attention:** Work due now, tasks assigned to me or claimable, newly
   startable processes, and incidents only when authorized; distinguish
   required action from information. Each item identifies its source and
   last confirmed freshness, with no fabricated delivery assurance.
2. **Action:** Open the exact task, fill the same form-js definition, review
   the consequences and submit through the existing API. User-facing
   success requires a durable server acknowledgement. Incidents are
   distinct from business findings; alarm acknowledgement is distinct from
   engine remediation.
3. **Context:** On more space, progressively reveal a BPMN diagram, the
   process and current state, related documents, history and authorized
   remediation. Small screens never hide a required validation error or
   final action behind overflow.
4. **Continuity:** Preserve the *intent and context* of a deep link or item
   selection between devices. Until MFE5 implements a secure, server-backed
   draft/unknown-outcome protocol, a partially filled form **must not be
   presented as synchronized across devices**. Switching devices may open
   the same task but does not promise to recover local unsaved answers.
5. **Trust:** No new user privilege from using a phone or watch; policies
   and grants are checked on the server for every command. The client is
   an interface, not a workflow engine or authorization authority.

## Key interaction principles

- **Task-first, role-aware:** a Task user should not need the desktop
  Console navigation to get work done; an operator sees Incidents when
  authorized. No dead controls, surprise vertical scroll to reach a
  confirmation, or duplicate start destinations.
- **Consistent information architecture:** 'My work' and role-filtered
  destinations are conceptually shared even when navigation differs:
  desktop horizontal/drawer, tablet split-or-single, phone compact/bottom
  destinations, watch cards and handoff. Do not create an alternate BPMN
  start/completion path.
- **Adaptive by usable content width and input modality:** distinguish a
  landscape tablet with a virtual keyboard from a full desktop, and do
  not assume that portrait/landscape maps to a fixed device category.
  A phone can expose a task list and detail sequentially; a wide tablet
  can show them beside each other if neither pane clips.
- **Polled updates are not alarms:** MFE1 can discover changed work while
  visible (candidate 10s), with pause/foreground refresh and preserved
  forms. MFE3 alone will decide authenticated notification delivery,
  grouping, escalation, acknowledgement, quiet hours, retries and
  privacy. A watch must not rely on foreground browser polling as a
  safety-critical alarm channel.
- **Accessible by design:** touch targets at least 44 CSS px where
  applicable, readable zoom, form-js focus/errors, keyboard and
  assistive technology, sensible safe areas and reduced motion. Test
  actual iPhone Safari, Android, tablets and eventually watch hardware,
  not just browser viewport emulation.
- **No copy of sensitive data:** deep links are scoped and re-authorized;
  lock-screen/watch previews expose no incident messages, personal data
  or task field values by default.

## Four end-to-end journeys to prove the value

1. **Field technician:** start an authorized safety inspection on a phone,
   complete the user task, see the server-confirmed receipt, switch to a
   tablet to read process context, and verify the same durable result on
   desktop. Separate raw evidence capture (MFE4) and offline/loss-of-response
   protocols (MFE5) from the initial live journey.
2. **Manager:** review upcoming work at desktop, open an assigned approval
   from a phone, explicitly confirm the business decision, and see it
   reflected on tablet without double completion. A watch may later
   prompt the manager to continue on an authenticated phone; it may not
   silently 'approve' when opening a notification.
3. **On-call operator:** an incident is raised as a durable engine fact,
   MFE3 securely delivers a deduplicated alert, the watch shows only a
   minimal indicator, and the operator opens a role-checked deep link
   on phone/tablet for cause-first triage. Alarm acknowledgement and
   engine resolve/retry remain different audited actions.
4. **Modeler:** sketch and validate a BPMN process at desktop, inspect a
   live overlay on a tablet, and—only after MFE6's own acceptance—edit
   with touch/pen using **the same bpmn-js modeler**. A watch has no
   diagram editing capability.

## Proposed MFE7 – Wearable companion and cross-device handoff

**State:** planned research and ADR gate only; **not approved for engineering**
and not active under the programme's two-slice WIP cap.

- **Dependency:** MFE1 for authenticated task/Start deep links and MFE3
  for delivered, auditable alarm facts. MFE5 is required for claims of
  shared *unsent draft* continuity; the first watch proof must not depend
  on such a claim.
- **First vertical proof:** one real, authorized and privacy-safe urgent
  incident notification, seen on watch hardware and continued through
  a bound, authenticated phone deep link to read-only incident context.
  A lost/unreachable watch, signed-out phone, revoked access and
  duplicate delivery must have explicit safe outcomes.
- **Evaluate separately:** whether watchOS/Wear OS native companion
  adapters are needed, watch-to-phone deep-link handoff, authentication
  lifetime, re-authentication for risky actions, data minimization,
  identity/device revocation, delivery monitoring and offline behavior.
  **Do not** claim a CSS-responsive web UI is a viable watch app.
- **No write path until a new ADR is accepted.** A future optional
  low-risk 'acknowledge responsibility' is only possible once MFE3
  specifies a durable, attributed alarm lifecycle. It is never the same
  as task completion, engine incident resolution or process start.
  No one-tap approval of financially or safety-sensitive actions by
  default.
- **Acceptance:** paired and unpaired watch tests, accessibility and
  privacy, background/notification delivery and revoke/logout cases,
  no success before fsync, and replay equivalence for any persisted
  new acknowledgement facts.

## Recommended order

1. **MFE0 + MFE1 now:** fix actual tablet landscape clipping,
   test portrait/landscape/keyboard/200% zoom on real devices, finish
   the compact phone navigation and foreground 10-second work hints.
   Preserve task input and keep ordinary user Start denied until its
   scoped authorization ADR is approved.
2. **MFE2 then MFE3:** make mobile incident triage genuinely actionable,
   then design authoritative alarm identity, secure delivery,
   acknowledgement and escalation. These are P0 for the 'manage
   everything' promise; the watch should not leapfrog them.
3. **MFE4/MFE5, MFE6 as applicable:** field capture and safe
   multi-device continuation; optional pen/tablet authoring.
4. **MFE7 only after contracts are ready:** implement the smallest
   verified watch companion slice, not a parallel client/engine.

## Architecture and governance

This document adds a **product target** beyond the narrower mobile
scope of [ADR-0514](../adr/0514-mobile-first-tasks-start-and-incident-triage.md).
It does not supersede ADR-0514, ADR-0209's role boundaries,
[ADR-0417](../adr/0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md)
or the invariants in [docs/architecture/invariants.md](../architecture/invariants.md).
A new or amended ADR must accept watch notification/action security and any
new start-grant or cross-device draft protocol before implementation.

No independent business-rule interpreter, BPMN renderer or client-side
runtime state machine. Preserve compiled BPMN/FEEL/DMN, server-side
authorization, WAL-before-visible ordering, deterministic recovery and
single-writer partition ownership.

**Success measure:** people can find the next task/incident in a few
interactions on the device they are using, finish a permitted action with
clear server confirmation and trust that they will see the same factual
business outcome later on a larger screen.
