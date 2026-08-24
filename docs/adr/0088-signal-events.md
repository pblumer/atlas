# ADR-0088: Signal events (broadcast throw/catch)

- **Status:** Accepted
- **Date:** 2026-07-30
- **Deciders:** Atlas engine team

> **Implementation status.** All phases (1–5) delivered. Each phase
> lands test-first with a recovery test (ADR-0018). Signal events build on the message
> correlation/subscription substrate (ADR-0020), the boundary arm/fire machinery
> (ADR-0040), message-start instantiation (ADR-0035), and event subprocesses
> (ADR-0082). They introduce a parallel subscription family but no new recovery path.
>
> **Delivered (Phase 1, compiler):** `<signalEventDefinition signalRef="…">` (a `Signal`
> pointer on the start/catch/throw/end/boundary structs) and top-level `<bpmn:signal id
> name>` (`buildSignalResolver`, mirroring `buildMessageResolver`, returning the name — no
> correlation key, no code) parse into four new `BpmnType`s
> (`TypeSignalCatchEvent`/`ThrowEvent`/`EndEvent`/`StartEvent`, a `SignalDetail{SignalName}`
> table shared by catch/throw/end/start; the end reuses the throw table) and a
> `BoundarySignal` `BoundaryEventKind` with a `SignalName` on `BoundaryEventDetail` /
> `EventSubProcessDetail` (so a signal boundary and a signal event subprocess reuse the
> boundary/event-sub detail). A signal start is a process entry point (`isStartEvent`,
> `SignalStartEvents()` mirroring `MessageStartEvents`). No "must be caught" validation —
> a signal is fire-and-forget. Verified: a throw/catch/boundary/start/end and a signal
> event subprocess compile with the resolved name; an unknown or unnamed signal ref is a
> deploy error in every position. No runtime yet.
>
> **Delivered (Phase 2, broadcast core):** the runnable throw→catch path. A
> `SignalSubscriptionValue` (the `MessageSubscriptionValue` shape minus the correlation
> key) in a new `cfSignalSubscription` family keyed `signal-name:elementKey`, with
> `PutSignalSubscription` / `DeleteSignalSubscription` / `SubscribedSignals(name, fn)` tx
> methods (a name-only prefix scan). It reuses the message subscription intents
> (`SubscriptionCreated` / `SubscriptionCorrelated`) over its own value type (`VTSignal`)
> and `applyToState` arm. `broadcastSignal(c, name, vars)` mirrors `correlateMessage`:
> collect every subscription on the name, then for each retire it, write the throw's
> variables into that instance's scope, and enqueue its `Completing` — matches collected
> before any mutation so retiring one cannot disturb the scan. `signalCatchEventBehavior`
> opens the subscription and waits; `signalThrowEventBehavior` gathers the instance's
> variables and broadcasts inline (keeping `applyToState` pure), then takes its outgoing
> flow. Verified: one throw fires two intermediate catches in one instance (1:n via a
> parallel split) and a catch in a second instance (cross-instance), carrying the payload
> into every scope; a throw with no listener is a no-op; a parked catch's subscription
> rebuilds on recovery so a throw after restart still fires it. Signal-start
> instantiation, boundary, end, and event-subprocess catches remain for Phases 3–4.
>
> **Delivered (Phase 3, boundary + end + start):** the remaining plain-event catches and
> the send-and-stop / instantiate throws. A `BoundarySignal` case in
> `boundaryEventBehavior.OnActivated` opens a name-only signal subscription, so a later
> broadcast drives the boundary instance to `Completing` through the shared boundary
> fire path (interrupt-the-host-then-route, or take-the-flow when non-interrupting) —
> no new fire logic. `signalEndEventBehavior` broadcasts inline exactly like the throw
> (reusing the throw detail table) then ends the instance like a none end event, mirroring
> `messageEndEventBehavior`. `broadcastSignal` now also instantiates every deployed
> signal-start definition on the name (a `signalStarts` name→defKeys index the processor
> maintains in `Deploy`/`Undeploy`, mirroring `messageStarts` but without a correlation
> key or singleton state); `TypeSignalStartEvent` runs as a plain `startEventBehavior`
> once instantiated, like a message start. Verified: an interrupting signal boundary
> cancels its host (job canceled) and routes to escalation cross-instance; a
> non-interrupting one escalates while the host keeps running; a signal end event fires a
> waiting catch then stops its own instance; one broadcast both instantiates a fresh
> signal-start instance and fires a waiting boundary in another; undeploy drops a
> signal-start from the index; and an armed boundary subscription rebuilds on recovery so
> a throw after restart still fires it. The signal event subprocess remains for Phase 4.
>
> **Delivered (Phase 4, signal event subprocess):** the signal trigger for an event
> subprocess (ADR-0082), interrupting and non-interrupting. A `BoundarySignal` case in
> `eventSubProcessStartBehavior.OnActivated` opens a name-only signal subscription on the
> armed trigger — the same subscription a signal boundary opens — so a broadcast drives
> the trigger to `Completing` through the shared event-sub fire path (terminate the parent
> scope's other work if interrupting, then activate the handler subprocess); a
> non-interrupting trigger then re-arms a fresh subscription, extending the existing
> message re-arm condition to signals. No new arming, teardown, or recovery path — the
> compiler already records the trigger spec (`EventSubProcessDetail{Kind: BoundarySignal,
> SignalName}`, Phase 1). Verified: an interrupting signal event subprocess tears down the
> main flow (job canceled) and runs the handler cross-instance; a non-interrupting one
> fires twice (re-arming between) while the main flow runs untouched, then disarms when the
> flow completes; and the re-armed subscription rebuilds on recovery so a broadcast after
> restart runs the handler again.
>
> **Delivered (Phase 5, Modeler):** signal authoring in the editor's Implement panel
> (`api/web/editor.js`), mirroring message authoring minus the correlation key. A
> `signalFieldsHTML` picker (a dropdown of the model's shared `<bpmn:signal>` declarations
> plus "＋ New signal", and the chosen signal's name) appears on every signal event —
> intermediate catch/throw, boundary, event-subprocess start, signal start, and signal end
> — driven by a `signalDefOf` dispatch arm in each event type; a central "Signals" manager
> on the process/collaboration root adds, renames, and deletes signals
> (`signalsManagerHTML`/`wireSignalsManager`). Helpers `listSignals`/`createSignal`/
> `linkSignal`/`deleteSignal` create `bpmn:Signal` root elements and set `signalRef` on the
> `bpmn:SignalEventDefinition` (deleting a signal clears dangling refs), producing exactly
> the `<bpmn:signal id name>` + `<signalEventDefinition signalRef>` shape the Phase-1
> compiler parses. bpmn-js already draws the signal triangle marker and offers every signal
> variant (start/end/throw/catch/boundary) via the wrench menu, so no diagram-rendering
> change was needed; `atlas-moddle.json` needs none either (the signal moddle types are
> native).

## Context and problem statement

A **signal** is a named **broadcast** event. A signal throw — an intermediate signal
throw event or a signal end event — is delivered to **every** currently-waiting signal
catch of the same name, **across all instances** (a signal boundary on an activity, a
signal intermediate catch, a signal start event that instantiates, a signal event
subprocess). It differs from a message (ADR-0020), which is **1:1 correlated** by a
correlation key: a signal has **no correlation key** and fans out **1:n** by name
alone — "broadcast that the shipment is cancelled; every order waiting on that signal
reacts." It is the last of the plain event types Milestone 2 lists.

Atlas cannot express one today. `<signalEventDefinition>` and top-level `<bpmn:signal>`
are not parsed — the flow-event structs carry only `Message *` / `Timer *` pointers
(`compiler/parse.go:854-917`), `xmlDefinitions` declares only `Messages`
(`compiler/parse.go:645`), and a signal event falls into the `default:` "only … events
are supported yet" branches in `registerScope` (`compiler/scope_compile.go:384,389,505`).
There is no compiled type and no runtime behavior.

The question this ADR answers: **how do we broadcast a named event to every waiting
catch without inventing a parallel subscription/correlation/recovery subsystem** —
reusing the message machinery, which already delivers 1:n and already reaches across
instances.

What already exists, and is load-bearing:

- **Delivery is already 1:n and cross-instance.** `correlateMessage`
  (`engine/behavior.go:1479`) collects **every** open subscription matching
  `(name, correlationKey)` and drives each waiting element to `Completing`; the
  subscription store is keyed `name:correlationKey:elementKey` with **no process-instance
  key in the key** (`state/keys.go:402`), so one scan reaches subscriptions in every
  instance. A signal is exactly this delivery with the match widened to **name only**.
- **A throw invokes delivery inline.** `messageThrowEventBehavior.OnActivated`
  (`engine/behavior.go:1394`) gathers its instance's variables as payload and calls
  `correlateMessage` directly on the command path (keeping `applyToState` pure), then
  completes; `messageEndEventBehavior` (`engine/behavior.go:1416`) does the same then
  ends the instance. A signal throw / signal end mirror this exactly.
- **Catch lifecycles are solved.** A signal boundary reuses `armBoundaryEvents` /
  `boundaryEventBehavior` (`engine/behavior.go:840`, `:1874`) — a new `case` in
  `OnActivated` that opens a *signal* subscription; a signal intermediate catch reuses
  the message-catch shape; a signal event subprocess reuses
  `eventSubProcessStartBehavior` (`engine/behavior.go:2041`) with a new trigger kind; a
  signal start event instantiates via the message-start precedent
  (`c.p.messageStarts[name]`, `engine/behavior.go:1518` / ADR-0035).
- **The compiler seam is uniform.** Every event kind is `AddXxx(detail) → addNode(Type,
  detail)` (`compiler/builder.go`); the node type is chosen from which event definition
  is present (`compiler/scope_compile.go`), and boundary/event-sub kinds are one
  `BoundaryEventKind` enum (`compiler/process.go:422`). Top-level `<bpmn:message>` is
  indexed and resolved by `buildMessageResolver` (`compiler/parse.go:234`); `<bpmn:signal>`
  mirrors it, returning just a name (no correlation key, no code).

So delivery, cross-instance reach, throw-inline-correlate, the catch lifecycles,
instantiation, and recovery already accommodate a signal. What is missing is a
**name-only** subscription and broadcast, the compiled signal types, and the behaviors.

## Decision drivers

- **Reuse, don't reinvent.** Build on `correlateMessage`'s 1:n cross-instance delivery,
  the boundary/event-sub arm-fire lifecycle, and message-start instantiation. A signal is
  "a message without a correlation key."
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible
  (I2); a single `applyToState` live and on recovery (I4); structure resolved at compile
  (I5); deterministic replay — a broadcast is a pure function of the committed
  subscription set at throw time (I6).
- **Keep signals and messages from crossing wires.** A message publish must never fire a
  signal catch, nor a signal broadcast a message catch. The two subscription kinds must be
  cleanly separable in storage and scan.
- **Faithful BPMN.** Broadcast by name, no correlation key, no buffering (a signal thrown
  with no listener is legal and simply reaches no one — matching the no-buffering message
  semantics of ADR-0020); a signal start instantiates every deployed process with a
  matching signal start; catches fire in every instance.
- **Ship the runnable core first.** A signal **throw** reaching a signal **intermediate
  catch** in the same and other instances is the first runnable phase; boundary, start,
  and event-subprocess catches layer on.

## Considered options

1. **A parallel signal-subscription family, keyed by name; broadcast reuses the message
   delivery shape (chosen).** Signal catches open a `SignalSubscription` in a new
   `cfSignalSubscription` column family keyed `name:elementKey` (no correlation key). A
   signal throw calls `broadcastSignal(name)` — the `correlateMessage` shape but scanning
   the signal family by **name prefix only**, driving every match to `Completing`, plus
   instantiating matching signal-start definitions. Signal throw/catch/end/start get their
   own `BpmnType`s and a light `SignalDetail{Name}`; boundary and event-subprocess catches
   add a `BoundarySignal` kind to the shared enum. No new recovery path — a subscription is
   an existing durable record shape in a new family.
2. **Reuse the message-subscription family with a `Signal bool` discriminator.** Store
   signal subscriptions in `cfMessageSubscription` with an empty correlation key and a flag,
   and filter the scan. Rejected: a name-only prefix scan over the shared family also matches
   real message subscriptions with an empty key, so the delivery paths must both filter by
   the flag on the hot correlate scan — mixing the two waits in one family and one scan to
   avoid a second column family. The parallel family (option 1) keeps message and signal
   correlation physically separate and each scan single-purpose.
3. **A single generic "named-event" subscription unifying messages and signals.** Rejected:
   messages need a correlation key and 1:1 retire-on-correlate semantics; signals need
   name-only 1:n; folding both into one type forces every message call site to reason about
   the broadcast case and vice versa, for no reuse the delivery-shape sharing (option 1)
   doesn't already give.

## Decision outcome

Chosen: **option 1 — signals reuse the message *delivery shape* over a parallel,
name-keyed subscription family.** The genuinely new logic is (a) a `SignalSubscription`
record in `cfSignalSubscription` keyed by name, (b) a `broadcastSignal` that scans it by
name and drives every match plus instantiates signal starts, (c) compiled signal
throw/catch/end/start types and a `BoundarySignal` kind, and (d) the behaviors, most of
which delegate to the existing message/boundary/event-sub lifecycles.

### Subscription and broadcast

- **`SignalSubscription`** (a `model` value): `{ProcessInstanceKey, ElementInstanceKey,
  SignalName, ProcessDefKey, ElementId}` — the `MessageSubscriptionValue` shape
  (`model/value.go:460`) minus the correlation key. Stored in a new `cfSignalSubscription`
  family keyed `signal:<name>:<elementKey>` (mirroring `keyMessageSubscription`,
  `state/keys.go:402`, without the correlation-key component). `PutSignalSubscription` /
  `DeleteSignalSubscription` / `SubscribedSignals(name, fn)` mirror the message tx methods
  (`state/tx.go:281-296`) with a name-only prefix.
- **`broadcastSignal(c, name, vars, senderPI)`** mirrors `correlateMessage`
  (`engine/behavior.go:1479`): collect every `cfSignalSubscription` on `name` into a slice
  first (so retiring one can't disturb the scan), then for each emit a
  `SignalSubscriptionCorrelated` (retire it — a catch fires once), write the payload into
  that instance's scope, and enqueue its `Completing`; afterwards instantiate every deployed
  signal-start definition on `name` (a `c.p.signalStarts[name]` index mirroring
  `c.p.messageStarts`). It is called **inline** from the throw/end behavior, exactly as a
  message throw calls `correlateMessage`.

### Compiler

- Add `TypeSignalCatchEvent`, `TypeSignalThrowEvent`, `TypeSignalEndEvent`, and
  `TypeSignalStartEvent` to the `BpmnType` enum (`compiler/process.go:19-46`), their
  `String()` cases, and grow `numBpmnTypes`. A `SignalDetail{Name int32 (interned)}` table
  (signals carry no correlation key and no code) with `messageStarts`-style tables for
  catch/throw/start.
- Add a `BoundarySignal` value to `BoundaryEventKind` (`compiler/process.go:422`) with a
  `SignalName` on `BoundaryEventDetail` / `EventSubProcessDetail`, so a signal boundary and a
  signal event subprocess reuse the boundary/event-sub detail (the same way `BoundaryMessage`
  does).
- Parse `<signalEventDefinition signalRef="…">` (a `Signal *xmlSignalEventDefinition`
  pointer on each flow-event struct) and top-level `<bpmn:signal id name>` (a `Signals
  []xmlSignal` on `xmlDefinitions`); add a `buildSignalResolver` mirroring
  `buildMessageResolver` (`compiler/parse.go:234`) that returns the signal name. Add
  `case ev.Signal != nil:` arms to the start/catch/throw/end/boundary switches in
  `registerScope` and the event-sub trigger switch (`compiler/scope_compile.go`), and add
  a signal start to `isStartEvent` / the `startEvents` collection (`compiler/builder.go`).
- Validation: **none required** — a signal throw with no listener is legal (broadcast,
  fire-and-forget). At most a `SeverityWarning` for a signal name no catch in the model
  listens on, matching the existing warning pattern (`compiler/validation.go:242`).

### Runtime

- `signalThrowEventBehavior` / `signalEndEventBehavior`: gather the instance's variables as
  payload (`instanceVariables`, `engine/behavior.go:1576`), call `broadcastSignal`, then
  take the outgoing flow (throw) or end the instance (end) — mirroring
  `messageThrowEventBehavior` / `messageEndEventBehavior`.
- `signalCatchEventBehavior`: `OnActivated` opens a `SignalSubscription` and waits;
  `OnCompleting` = `completeAndTakeFlows` — mirroring the message catch.
- Signal **boundary** and **event subprocess**: a `BoundarySignal` `case` in
  `boundaryEventBehavior.OnActivated` (`engine/behavior.go:1876`) and
  `eventSubProcessStartBehavior.OnActivated` (`engine/behavior.go:2043`) that opens the
  signal subscription; the fire paths (`interruptHost` / handler activation) are unchanged.
- Signal **start**: `broadcastSignal` instantiates matching signal-start definitions via a
  `signalStarts` index the processor maintains in `Deploy` (mirroring `messageStarts`).

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<signalEventDefinition>` + `<bpmn:signal>`; the resolver;
  `SignalDetail`; the four signal types + `BoundarySignal` kind; `isStartEvent`/reachability.
  *Tests:* a parse test asserting the compiled detail for a throw, a catch, and a boundary;
  deploy accepts the model. No runtime yet.
- **Phase 2 — Broadcast core.** `SignalSubscription` family + tx methods; `broadcastSignal`;
  `signalThrowEventBehavior` + `signalCatchEventBehavior`. *Tests:* a signal throw fires **two**
  waiting intermediate catches (1:n) in the same instance and, via a second instance with a
  waiting catch, **across instances**; a signal with no listener is a no-op; a **recovery
  test** — a parked signal catch's subscription rebuilds so a throw after restart still fires it.
- **Phase 3 — Boundary + start + end.** Signal boundary (interrupting and non-interrupting);
  signal end event (broadcast then stop); signal **start** events instantiate every deployed
  matching process. *Tests:* an interrupting signal boundary cancels its host and routes out;
  a signal broadcast starts a fresh instance *and* fires a waiting boundary in another; recovery.
- **Phase 4 — Signal event subprocess.** A `BoundarySignal` event-sub trigger, interrupting
  and non-interrupting (re-arming), reusing ADR-0082. *Tests:* a broadcast fires a
  non-interrupting signal event subprocess in a running instance; recovery across a re-arm.
- **Phase 5 — Modeler.** The Implement panel authors a signal event's name (reusing the
  message-name field shape); bpmn-js already draws the signal triangle marker and the
  throw/catch/boundary/start variants via the wrench menu.

### Consequences

- **Positive:** the engine gains broadcast events on the message delivery machinery — one
  new subscription family, one broadcast function, and behaviors that mostly delegate. No new
  recovery path; cross-instance reach is inherited (the subscription store is already
  name-global). It rounds out Milestone 2's event set.
- **Negative / trade-offs accepted:** a second subscription column family and its tx methods
  (parallel to the message family) — the cost of keeping signal and message correlation
  physically separate. Four new `BpmnType`s (throw/catch/end/start) plus a boundary kind.
  Cross-**partition** broadcast is out of scope (the scan is one partition's single writer,
  I3) — deferred to Milestone 5 with cross-partition messaging.
- **Follow-ups / risks to watch:** a signal broadcast that fires very many catches in one
  batch (a widely-listened signal) does bounded work per match but scales with the listener
  count — the same surface a widely-correlated message has. Signal payload semantics
  (broadcast carries the thrower's variables into every catcher) match message payloads;
  revisit if selective payloads are wanted. A signal-start broadcast that starts many
  instances at once is bounded by the number of matching definitions.

## Pros and cons of the options

### Option 1 — parallel name-keyed signal family, reuse the delivery shape (chosen)
- Good: message and signal correlation stay physically separate (each scan single-purpose);
  reuses `correlateMessage`'s 1:n cross-instance delivery shape, the boundary/event-sub
  lifecycle, and message-start instantiation; no new recovery path.
- Bad: a second subscription family + tx methods; four new element types.

### Option 2 — one message family with a `Signal` flag (rejected)
- Good: no second column family.
- Bad: a name-only scan over the shared family also matches empty-key message subscriptions,
  so both delivery paths must filter on the flag — mixing two correlation semantics in one
  hot scan.

### Option 3 — one generic named-event subscription (rejected)
- Good: a single wait primitive.
- Bad: messages (1:1, correlation key, retire-on-correlate) and signals (1:n, name-only)
  have different semantics; unifying forces every call site to handle both — no reuse the
  delivery-shape sharing doesn't already give.

## Links

- builds directly on ADR-0020 (message correlation and subscriptions — the 1:n cross-instance
  delivery `broadcastSignal` mirrors), ADR-0040 (boundary arm/fire — a signal boundary), and
  ADR-0035 (message-start instantiation — signal starts), and reuses ADR-0082 (event
  subprocesses — a signal event subprocess)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 2 "Signal events (broadcast)"; cross-partition broadcast is deferred to
  Milestone 5 (ADR-0006) with cross-partition messaging
- sibling to ADR-0089 (error events) — the other outstanding Milestone-2 event type, with the
  opposite delivery model (scoped propagation to one handler, not broadcast)
