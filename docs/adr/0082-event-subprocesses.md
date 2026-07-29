# ADR-0082: Event subprocesses (message- and timer-triggered, interrupting and non-interrupting)

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

> **Implementation status.** Not started. This ADR plans the work; each phase will land
> test-first with a recovery test (ADR-0018). Event subprocesses build on the
> embedded-subprocess scope lifecycle (ADR-0074), the boundary-event arming/firing/
> interruption machinery (ADR-0040), and message/timer correlation (ADR-0020, ADR-0051).
> They introduce no new value type or recovery path — the trigger reuses the existing
> subscription/timer records, and the handler is an ordinary subprocess scope. Only
> **message** and **timer** triggers are in scope; error/signal/escalation-triggered
> event subprocesses depend on error and signal events (ROADMAP Milestone 2, not built)
> and are deferred.

## Context and problem statement

An **event subprocess** is a subprocess nested in a parent scope (the process, or an
embedded subprocess) that is **not** entered by a sequence flow. It has no incoming or
outgoing flows; instead its **start event carries an event definition** (a message,
a timer, and later an error/signal), and that trigger is *armed while the parent scope
runs*. When the trigger fires, the event subprocess executes — either **interrupting**
the parent scope (terminating its other work, then handling the event — a cancellation,
a timeout that aborts the whole scope, an error handler) or **non-interrupting**
(handling the event in parallel with the ongoing scope, and able to fire again — a
deadline reminder, a progress notification). It is the scope-level analog of a boundary
event (ADR-0040): a boundary attaches a trigger to *one activity*; an event subprocess
attaches a trigger to a *whole scope*, and its handler is a full subprocess.

Atlas cannot express one today. `<subProcess triggeredByEvent="true">` is parsed as an
*ordinary* embedded subprocess — the `triggeredByEvent` attribute is dropped
(`compiler/parse.go:772`), and a start event's `isInterrupting` attribute is dropped
(`compiler/parse.go:850`) — so the container would be treated as a normal scope with no
inbound flow (hence never entered), and its message/timer start would be miscounted as a
*process* entry point by the deploy-time scans (`CompiledProcess.MessageStartEvents`
`compiler/process.go:618`, `TimerStartEvents` `:646`, which walk every node without a
`FlowScope` filter). There is no arming of a scope-level trigger and no runtime behavior.

The question this ADR answers: **how do we arm a scope-level trigger and run its handler
without inventing a parallel wait/scope/recovery subsystem** — reusing the boundary-event
trigger machinery (ADR-0040) and the subprocess scope lifecycle (ADR-0074).

What already exists, and is load-bearing:

- **Arming a waiting trigger is a solved primitive.** `armBoundaryEvents`
  (`engine/behavior.go:785`) activates one waiting element instance per boundary when a
  host activates; that instance's `boundaryEventBehavior.OnActivated`
  (`engine/behavior.go:1783`) opens a message subscription (`AppendMessageSubscriptionEvent`)
  or arms a one-shot timer (`armOneShotTimer` `:1027`), keyed to the instance. Firing
  drives it to `Completing`, where `d.Interrupting` decides whether `interruptHost`
  (`:889`) tears the host down. An event-subprocess trigger is the same shape, armed at
  *scope entry* rather than *activity entry*, and firing *activates a subprocess* rather
  than taking a flow.
- **The subprocess scope lifecycle is exactly the handler.** `subProcessBehavior.OnActivated`
  (`engine/behavior.go:1832`) seeds a subprocess's inner start events scoped by the
  container (`FlowScopeKey = <container key>`, `:1848`), each incrementing the container's
  `activeChildren` counter; `completeScope` (`:731`) completes the container when the
  counter drains. An event subprocess, once triggered, runs as an ordinary subprocess.
- **Interruption reuses `terminateScope`/`scopeContains` verbatim** (`engine/behavior.go:861`/
  `:822`): terminating every element whose flow-scope chain leads to a scope is what an
  interrupting boundary already does to a subprocess's inner tokens — an interrupting
  event subprocess does it to the parent scope's tokens.
- **The counter and message/timer records recover for free.** The `activeChildren` merge
  counter (`engine/apply.go:46`/`76`), message subscriptions
  (`model.MessageSubscriptionValue`, keyed by `(name, correlationKey, elementKey)`,
  `state/tx.go:281`), and timers (`model.TimerValue`, keyed by due date, `state/keys.go:91`)
  are all durable and replay-reconstructed; a trigger that "fires later and finds its
  element gone" already self-retires (`engine/behavior.go:537`, `:1452`).
- **Message-triggered instantiation shows the create-from-trigger seam.** A message start
  event instantiates a process via `AppendCreateInstanceCommand` inside `correlateMessage`
  (`engine/behavior.go:1463`). An event-subprocess trigger is the *intra-instance* analog:
  fire → activate a subprocess in the running scope.

So the scope model, the counter, the subscription/timer records, correlation, arming,
interruption, and recovery already accommodate an event subprocess. What is missing is
(a) the compiled `triggeredByEvent`/`isInterrupting` metadata and a per-scope list of
event-subprocess triggers, (b) arming those triggers at scope entry, (c) a trigger that,
on firing, activates the handler subprocess (interrupting the scope or not, and re-arming
if non-interrupting), and (d) keeping an armed trigger from blocking scope completion.

## Decision drivers

- **Reuse, don't reinvent.** Build on ADR-0040 (arm/fire/interrupt) and ADR-0074 (scope
  lifecycle). One scope model, one counter, one `applyToState`, one recovery path; the
  trigger is an existing subscription/timer, the handler an existing subprocess.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible
  (I2); a single `applyToState` live and on recovery (I4); structure resolved at compile
  (I5); deterministic replay with frozen keys, correlation keys, and due dates (I6).
- **An armed trigger must not deadlock scope completion.** A boundary event counts as an
  active child of its host's scope and is disarmed when the host completes. An
  event-subprocess trigger is armed for the *whole scope's* lifetime, so it must **not**
  count toward the scope's `activeChildren` — otherwise the scope could never drain — yet
  must still be disarmed when the scope completes.
- **Faithful BPMN.** A trigger armed while the parent scope runs; interrupting terminates
  the scope's other work then runs the handler; non-interrupting runs it in parallel and
  can fire repeatedly; the handler runs to its own end.
- **Ship the runnable core first.** A **message-triggered, interrupting** event subprocess
  at the process root — "on a cancel message, abort the flow and run a compensation
  handler" — is the first runnable phase. Non-interrupting, timers, and nesting layer on.

## Considered options

1. **Scope-level trigger as an *uncounted* waiting element instance; handler is an ordinary
   subprocess (chosen).** For each event subprocess, arm a trigger element instance in the
   parent scope when the scope is entered — reusing the boundary-event arming path (it
   opens the subscription/timer) — but give it a distinct `TypeEventSubProcessStart` type
   that `applyToState` **excludes from the `activeChildren` counter**, so it never blocks
   scope drain. On firing, its behavior activates the handler subprocess (interrupting:
   `terminateScope` the parent's other tokens + disarm sibling triggers first;
   non-interrupting: activate + re-arm). When the scope drains, `completeScope` disarms its
   armed triggers (a bounded scan, like `disarmBoundaryEvents`). No new value type, record,
   counter, or recovery path.
2. **Trigger as a *counted* element instance; teach `completeScope` to ignore armed
   triggers.** Same arming, but count the trigger and, when the counter is non-zero, scan
   the scope's children to decide "only armed triggers remain → complete anyway." Rejected:
   pushes a scan onto the hot, well-tested `completeScope` path on *every* drain check, to
   avoid a one-line apply-time exclusion; option 1's cost is instead a bounded scan only at
   the moment a scope actually completes.
3. **Trigger as a bare scope-keyed subscription/timer with no element instance.** Open the
   subscription/timer keyed to the scope, carrying the handler node id, and branch the
   timer/message *firing* paths (`handleTimerTriggered`, `correlateMessage`) on a new
   "activate a node in a scope" mode. Rejected: the firing paths key on
   `ElementInstanceKey` and self-retire when "the element is gone"; a root-scope trigger
   has no element instance at the PI key, so it would need new discriminator fields and new
   branches in two hot correlation paths — more surface than arming an (uncounted) element
   instance whose completion the existing paths already drive.

## Decision outcome

Chosen: **option 1 — an event subprocess is armed as an *uncounted* trigger element
instance in the parent scope (reusing the boundary arming/firing machinery) whose firing
activates an ordinary subprocess handler.** The genuinely new logic is (a) compiled
`triggeredByEvent`/`isInterrupting` metadata and a per-scope trigger list, (b) an
`armEventSubprocesses` step at scope entry, (c) an `eventSubProcessStartBehavior` that
opens the trigger and, on firing, activates the handler (interrupt/parallel/re-arm), (d)
one apply-time exclusion from the counter, and (e) a disarm scan when a scope completes.

### The trigger lifecycle

1. **Arm at scope entry.** When the parent scope is entered — `handleProcessInstanceActivating`
   (`engine/behavior.go:81`) for the process root, `subProcessBehavior.OnActivated`
   (`:1832`) for a subprocess — `armEventSubprocesses(scope)` activates one
   `TypeEventSubProcessStart` trigger instance per event subprocess in that scope, scoped
   by the parent (`FlowScopeKey = <scope key>`), the mirror of `armBoundaryEvents`
   (`:785`). Its `OnActivated` opens a message subscription or arms a one-shot timer —
   the same code `boundaryEventBehavior.OnActivated` (`:1783`) runs — keyed to the trigger
   instance, so correlation/firing reach it unchanged.
2. **Do not count it.** `applyToState` skips `IncrementActiveChildren`/`DecrementActiveChildren`
   for a `TypeEventSubProcessStart` element (`engine/apply.go:46`/`76`), so an armed trigger
   never keeps its scope from draining. (It is still a real element instance — it shows as a
   pending trigger on the Operations overlay and is individually terminable.)
3. **Fire.** The existing timer/message paths drive the trigger to `Completing`
   (`handleTimerTriggered` `:529`, `correlateMessage` `:1424`). `eventSubProcessStartBehavior.OnCompleting`
   then, reading the compiled `Interrupting` flag:
   - **interrupting:** `terminateScope(c, procKey, parentScope)` tears down the parent
     scope's other tokens (`:861`), and the scope's *other* event-subprocess triggers are
     disarmed; then it activates the handler subprocess in the parent scope. The trigger
     does not re-arm.
   - **non-interrupting:** it activates the handler subprocess in the parent scope
     (incrementing the scope counter — the handler is counted, keeping the scope alive
     while it runs) and **re-arms** a fresh trigger, so a second message/timer fires again.
4. **Run the handler.** The handler is an ordinary subprocess instance: `subProcessBehavior`
   seeds its inner start (the event-subprocess start event, which — like every message/timer
   start — flows straight on now that the trigger has fired), and it runs to its end,
   draining its own scope and completing (`completeScope`), decrementing the *parent* scope.
5. **Disarm on scope completion.** When the parent scope drains, `completeScope` (`:731`)
   disarms the scope's still-armed triggers before completing it — a bounded scan over the
   scope's `TypeEventSubProcessStart` instances (mirroring `disarmBoundaryEvents`), whose
   subscriptions/timers self-retire. A `handleProcessInstanceTerminating` cancel already
   terminates them (it terminates every element instance).

### Interrupting at the root vs a subprocess

- **Root scope, interrupting:** terminating the root's other tokens ends the main flow;
  the handler runs; when it drains, the *instance* completes (`completeScope` root branch).
- **Subprocess scope, interrupting:** terminating the enclosing subprocess's other tokens;
  the handler runs; when the subprocess scope drains, the subprocess completes and takes
  its outgoing flow — exactly an interrupting boundary on that subprocess, but the "handler"
  is inside rather than out a boundary flow.
- **Non-interrupting, either level:** the handler is a parallel child of the scope; the
  scope completes only when the main flow *and* every handler instance have drained (the
  counter already models this).

### Compiler

- Parse `triggeredByEvent="true"` on `<subProcess>` (add `TriggeredByEvent` to
  `xmlSubProcess`, `compiler/parse.go:772`) and `isInterrupting` on its start event (add
  `IsInterrupting` to `xmlStartEvent`, `:850`; default interrupting when absent —
  `!= "false"`, mirroring the boundary `CancelActivity != "false"` at
  `compiler/scope_compile.go:444`).
- Compile the container as a subprocess that is **event-triggered**: it keeps
  `TypeSubProcess` but is flagged so it is (a) not a token-entered scope and (b) excluded
  from `startEvents`/the deploy-time message/timer-start scans. For each event subprocess,
  emit a per-**parent-scope** trigger descriptor — the boundary-event precedent
  (`BoundaryEventDetail{HostNode, Interrupting, Kind, Schedule|MessageName/CorrelationKey}`,
  `compiler/process.go:400`) applied with the parent scope as the "host" and the handler
  container as the target — grouped per scope like `boundaryEvents`/`scopeStarts`
  (`compiler/builder.go:916`/`:934`). Message correlation keys and timer schedules compile
  through the existing `resolveMessage`/`parseTimerSchedule` paths (`scope_compile.go:30`/`:40`).
- Validation: `checkReachability` (`compiler/validation.go:127`) must treat an event
  subprocess's inner start as reachable *via its trigger* rather than via a container the
  main flow enters (today's subprocess-start case at `:151` only handles token-entered
  subprocesses); the `MessageStartEvents`/`TimerStartEvents` deploy scans must skip
  event-subprocess starts so they are not registered as process entry points.

### Runtime

- New `TypeEventSubProcessStart` (a trigger that opens a subscription/timer like a boundary
  and, on firing, activates a handler subprocess) and an `eventSubProcessStartBehavior`.
- `armEventSubprocesses(scope)` called at scope entry (root + subprocess), the analog of
  `armBoundaryEvents`.
- One apply-time exclusion from `activeChildren` for `TypeEventSubProcessStart`
  (`engine/apply.go`), and a disarm scan in `completeScope`.
- Interruption reuses `terminateScope`/`scopeContains`; non-interrupting re-arm mirrors
  `fireRecurringBoundary` (`engine/behavior.go:573`).

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `triggeredByEvent` + `isInterrupting`; compile per-scope
  event-subprocess trigger descriptors; flag the container so it is not a process entry
  point; reachability + deploy-scan fixes. *Tests:* a parse test asserting the descriptor
  (kind, interrupting, correlation/schedule, handler node, parent scope) and that the
  handler's start is not in `StartEvents()`; a deploy that accepts the model. No runtime yet.
- **Phase 2 — Message-triggered, interrupting, at the root (the runnable core).**
  `armEventSubprocesses` at instance creation opens the message subscription; a correlating
  message interrupts the main flow (`terminateScope`) and activates the handler; the handler
  runs to its end and the instance completes. Adds `TypeEventSubProcessStart`, the
  behavior, and the counter exclusion. *Tests:* `start → userTask` with a message-triggered
  interrupting event subprocess `{start → script → end}` — publish the message while the
  task waits: the task is terminated, the handler runs, the instance completes; a **recovery
  test** — armed subscription parked, replay, correlate after restart, assert identical
  teardown + handler run.
- **Phase 3 — Non-interrupting + timer triggers.** Non-interrupting handlers run in parallel
  and the trigger re-arms (fires N times); timer-triggered event subprocesses (duration/
  date, and a re-arming cycle for non-interrupting). The uncounted-trigger + disarm-on-scope-
  completion machinery. *Tests:* a non-interrupting message trigger fires twice → two handler
  runs while the main flow continues; a timer event subprocess fires on schedule; the scope
  completes only after the main flow *and* handlers drain; recovery across a re-arm.
- **Phase 4 — Nesting, subprocess-level, teardown.** An event subprocess inside an embedded
  subprocess (armed on the subprocess's activation, disarmed on its completion/interruption);
  interaction with an interrupting boundary on the enclosing subprocess (both tear the scope
  down once); an interrupting event subprocess whose handler itself contains activities.
  *Tests:* subprocess-scoped event subprocess interrupts only its subprocess (the outer flow
  continues); disarm on normal subprocess completion; recovery across a subprocess-level
  interrupt.
- **Phase 5 — Modeler.** The properties panel authors `triggeredByEvent` + the start event's
  trigger (message/timer) and `isInterrupting`; bpmn-js already draws an event subprocess
  (dashed border) and its start-event marker. Reuses the message/timer authoring the catch/
  boundary editors use.

### Consequences

- **Positive:** the engine gains scope-level event handling — cancellation, scoped timeouts,
  and (once error events exist) error handlers — on the machinery ADR-0040 and ADR-0074
  already built. No new value type, record, counter, or recovery path; the trigger is an
  existing subscription/timer, the handler an existing subprocess. It is the substrate a
  future error/compensation story reuses.
- **Negative / trade-offs accepted:** one apply-time branch to exclude the trigger type from
  the counter (a hot path, but a single type check), and a disarm scan at scope completion
  (bounded by the scope's live elements, like `disarmBoundaryEvents`). A new element type and
  behavior. The trigger's arm-and-fire is not persisted as a decision — like `completeScope`,
  everything it reads must be rebuilt by replay first.
- **Follow-ups / risks to watch:** error-, signal-, and escalation-triggered event
  subprocesses wait on error/signal events (Milestone 2). A non-interrupting trigger that
  fires unboundedly (a fast cycle timer) spawns unbounded handler instances — the same
  concern any non-interrupting recurring boundary has. Interaction with multi-instance (an
  event subprocess inside a multi-instance body) and the exact semantics of an interrupting
  event subprocess versus a simultaneously-firing boundary need explicit ordering tests.

## Pros and cons of the options

### Option 1 — uncounted trigger element + subprocess handler (chosen)
- Good: reuses arming/firing/interruption (ADR-0040) and the scope lifecycle (ADR-0074);
  the trigger is an existing subscription/timer, the handler an existing subprocess; root-
  and subprocess-scope triggers both work because the trigger is a real element instance;
  `completeScope` stays a counter check; recovery inherited.
- Bad: a new element type + behavior; one apply-time counter exclusion; a disarm scan at
  scope completion.

### Option 2 — counted trigger, teach `completeScope` (rejected)
- Good: no apply-time change.
- Bad: a scan on every drain check of a hot, well-tested path, versus a one-line exclusion
  and a scan only when a scope actually completes.

### Option 3 — bare scope-keyed subscription/timer, no element instance (rejected)
- Good: nothing counts; no element instance to disarm.
- Bad: new discriminator fields on `TimerValue`/`MessageSubscriptionValue` and new branches
  in the `handleTimerTriggered`/`correlateMessage` firing paths (which key on
  `ElementInstanceKey` and self-retire on "element gone"); a root-scope trigger has no
  element at the PI key.

## Links

- builds directly on ADR-0040 (boundary events — arm a waiting trigger, fire to Completing,
  `interruptHost`/`terminateScope`, interrupting vs non-interrupting) and ADR-0074 (embedded
  subprocesses — the scope-as-element lifecycle, `activeChildren`, `completeScope`)
- reuses ADR-0020 (message correlation and subscriptions), ADR-0035 (message/timer starts
  flow straight on once triggered; create-from-trigger seam), ADR-0051/0054 (timer arming,
  date/cycle timers)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 3 "Event subprocesses (interrupting and non-interrupting)"; error-,
  signal-, and escalation-triggered variants wait on Milestone 2 error/signal events
