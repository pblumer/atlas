# ADR-0109: Event-based gateways (deferred choice)

- **Status:** Accepted
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered (phases 1–3); `go test -race ./...` green, repo
> coverage floor held. An **event-based gateway**
> (`<eventBasedGateway>`) is a **deferred choice**: a token reaching it does not pick a branch
> on data (that is the exclusive gateway) — it **arms every target catch event at once**
> (message / timer / signal intermediate catch), waits, and the **first event to fire wins**.
> The winning branch continues; every other armed catch is **cancelled**. This reuses the
> intermediate-catch behaviors, subscriptions, timers, and the correlate/fire paths wholesale;
> the genuinely new parts are (a) a `TypeEventBasedGateway` and its behavior that arms the
> targets in a shared **race group**, (b) one appended `EventGatewayKey` field on the element
> instance to label the group, and (c) a sibling-cancel — the `interruptHost` "one fires,
> terminate the rest" loop — run when a grouped catch completes.

## Context and problem statement

BPMN's **event-based gateway** models "wait for whichever of these happens first." The
canonical use is a **request with a timeout**: after sending a request, a token waits at an
event gateway whose two branches are a *message catch* (the reply) and a *timer catch* (the
deadline) — whichever fires first is taken, and the other is discarded. It is also how a
process waits for one of several possible messages/signals and reacts to the first.

Unlike an exclusive gateway (which evaluates data conditions and routes **immediately**), an
event gateway **defers** the choice to runtime events: it opens all the waits, then lets the
world decide. Exactly one branch is taken; the losers are cancelled so their subscriptions and
timers do not linger or fire later.

Atlas cannot express one today. `<eventBasedGateway>` is **not parsed** — it is absent from
`xmlFlowContent`, so `encoding/xml` silently drops it, and any `<sequenceFlow>` out of it then
fails id resolution with a confusing "unknown sourceRef". There is no compiled type and no
behavior. The Modeler flags `bpmn:EventBasedGateway` as "aren't supported yet"
(`api/web/editor.js`).

The question this ADR answers: **how do we run a deferred choice — arm N catch events, take
the first to fire, cancel the rest — reusing the existing catch-event, subscription, timer, and
correlate machinery, deterministically and recovery-safely.**

What already exists, and is load-bearing:

- **The catch events are already complete.** A message/timer/signal intermediate catch event
  (`messageCatchEventBehavior` / `timerCatchEventBehavior` / `signalCatchEventBehavior`,
  `engine/behavior.go`) opens its subscription/timer in `OnActivated`, stays `Activated`, and
  fires by being driven to `Completing` — `correlateMessage`, `handleTimerTriggered`, and
  `broadcastSignal` each look up the waiting element instance by key and
  `AppendElementCommand(…, IntentCompleting, …)`. An event gateway arms **the same catch
  instances**; nothing about a subscription or a timer changes.
- **"One fires, cancel the siblings" is solved.** `interruptHost` (`engine/behavior.go`)
  terminates every boundary sibling of a host **except the one that fired** (`selfKey`), by
  scanning element instances grouped by `AttachedToKey`. An event gateway needs exactly that
  loop — winner skipped, losers `Terminated` — grouped by a gateway key instead of a host key.
- **Losing waits self-retire.** Terminating a catch instance does **not** eagerly delete its
  subscription/timer; the engine relies on lazy self-retirement — a later correlate/fire finds
  `GetElementInstance == nil` and drops the stale entry (the documented boundary-disarm
  pattern). Cancelling an event gateway's losers reuses this exactly.
- **The fork primitive exists.** `activateElement` mints a target element instance from an
  outgoing flow, assigning token ids; `takeOutgoingFlows` forks across multiple flows. An event
  gateway arms its targets the same way, adding only the group label.

What is missing: `<eventBasedGateway>` parsing, a `TypeEventBasedGateway` + behavior, a group
key to link the armed catches, and the sibling-cancel on fire.

## Decision drivers

- **Reuse, don't reinvent.** Arm the existing catch instances; fire them through the existing
  correlate/timer/signal paths; cancel losers with the existing sibling-terminate loop.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2); a
  single `applyToState` live and on recovery (I4); the gateway's targets resolved at compile
  (I5); **deterministic replay** — the race group is a field on the committed element-instance
  events, so recovery rebuilds the armed catches and their group identically, and the fire +
  sibling-cancel is a pure command-path function of committed state (I6).
- **Faithful BPMN.** A deferred choice: all targets armed simultaneously, the first event wins,
  the rest are cancelled; targets must be catch events (not tasks/gateways).
- **Recovery-safe waits.** The armed catches, their subscriptions, and their timers are all
  event-derived, so a crash mid-wait recovers to the same armed race and the first fire after
  restart still wins and cancels the losers.

## Considered options

1. **Arm the existing catch instances in a race group labelled by a new `EventGatewayKey`
   field; the first to complete cancels the siblings (chosen).** The gateway's `OnActivated`
   mints one waiting instance per outgoing flow — the flow's target catch event — each carrying
   `EventGatewayKey =` the gateway's element key, then the gateway completes. Each catch opens
   its own subscription/timer and waits, unchanged. When one is driven to `Completing` (a
   message correlates, a timer fires, a signal broadcasts), the shared completion path sees its
   `EventGatewayKey`, terminates every other live instance with the same key (losers'
   subscriptions/timers self-retire), and takes the winner's flow. A guard drops a loser whose
   `Completing` was already queued when it was cancelled (a same-event double-fire).
2. **Reuse `AttachedToKey` as the group key instead of a new field.** Rejected:
   `AttachedToKey != 0` is treated as "this is a boundary event" in the boundary disarm /
   interrupt scans (`disarmBoundaryEvents`, `interruptHost`) and its struct doc scopes it to
   boundary-host linkage. Overloading it would make a host's disarm scan mis-select the
   gateway's catch children (and vice-versa). One appended, separately-named field is clean and
   append-compatible.
3. **Make the gateway itself the single waiting element, holding all N subscriptions/timers.**
   Rejected: the correlate/timer/signal paths assume **one** subscription per waiting element
   and drive *that* element to `Completing`; a gateway holding many would force those shared
   paths to special-case "which of my subscriptions fired, pick that branch, retire the rest" —
   invasive to the correlation machinery for no gain over arming the real catch instances,
   which already carry their branch (their single outgoing flow).
4. **A parallel event gateway (`instantiate` / all-branches) variant.** Out of scope: the
   exclusive deferred-choice is the overwhelmingly common form; the parallel event gateway is
   rare and deferred.

## Decision outcome

Chosen: **option 1.** The genuinely new logic is (a) compiling `<eventBasedGateway>` to a
`TypeEventBasedGateway` node and validating its targets are catch events; (b) the appended
`EventGatewayKey` label; (c) `eventBasedGatewayBehavior` arming the targets in the group; and
(d) the sibling-cancel on a grouped catch's completion.

### Compiler

- Parse `<eventBasedGateway>` (an `EventBasedGateways []xmlNode` on `xmlFlowContent`), register
  it via a new `Builder.AddEventBasedGateway()` → `addNode(TypeEventBasedGateway, -1)` (no
  detail), alongside the other gateway loops. Add `TypeEventBasedGateway` to the `BpmnType`
  enum + `String()`, grow `numBpmnTypes`. Remove it from the Modeler's unsupported set.
- **Validation** (`checkGateways`): `isGateway` includes the new type (so the existing
  no-outgoing / no-incoming checks apply). A new `checkEventGatewayTargets` branch: every
  outgoing flow's target must be a **catch event** (`TypeMessageCatchEvent`,
  `TypeTimerCatchEvent`, `TypeSignalCatchEvent`) — else a `SeverityError` (a new
  `RuleEventGatewayTarget`), since a non-catch target cannot participate in the deferred choice.
  Timer targets keep the existing "a catch timer fires once, no cycle" rule.

### Runtime

- **`EventGatewayKey uint64`** on `ElementInstanceValue`, appended after `MultiInstance` and
  decoded under a new length guard (append-compatible: an old record decodes it as 0, I4). It is
  0 for every element except a catch armed by an event gateway, where it is the gateway's
  element-instance key — a stable, unique **group label** (it need not stay live).
- **`eventBasedGatewayBehavior`.** `OnActivated`: for each outgoing flow, mint the flow's target
  catch event as an `IntentActivating` instance carrying `EventGatewayKey =` the gateway key and
  a forked token (`ParentTokenID =` the gateway's token) — the catch then opens its
  subscription/timer via its own unchanged `OnActivated`. Then the gateway hops to `Completing`.
  `OnCompleting`: emit `Completed` (the gateway consumed itself into the armed waits; it takes no
  outgoing flow of its own — the targets were already armed). The armed catches are the scope's
  active children and keep it alive until one wins.
- **Sibling-cancel on fire.** `completeAndTakeFlows` (the shared catch `OnCompleting`) gains a
  guarded prologue: if the completing instance has `EventGatewayKey != 0`, first drop it if the
  instance is already gone (it lost a same-event race), then terminate every **other** live
  instance sharing that key — an `interruptHost`-shaped scan, winner skipped. Each loser's
  `Terminated` retires its element; its subscription/timer self-retires (the boundary pattern).
  Then the winner completes and takes its single outgoing flow normally.

### Recovery

A crash while the race is armed recovers exactly: each armed catch's `Activated` event (carrying
`EventGatewayKey`) rebuilds the instance and its group; its `SubscriptionCreated` / `TimerCreated`
event rebuilds the wait. After restart the first fire drives the winner to `Completing` (command
path) and cancels the losers — a pure function of committed state, no new recovery path. The
recovery test arms a message/timer race, crashes while both wait, replays, then publishes the
message and asserts the winner flows on and the timer branch is terminated.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<eventBasedGateway>`; `TypeEventBasedGateway` + builder;
  `isGateway` inclusion; `checkEventGatewayTargets`. *Tests:* a gateway with message + timer
  catch targets compiles; a gateway targeting a task (non-catch) is a deploy error; a
  namespaced modeler round-trip.
- **Phase 2 — Runtime.** `EventGatewayKey` field + codec; `eventBasedGatewayBehavior`; the
  group-cancel in `completeAndTakeFlows`. *Tests:* message-wins (timer branch terminated, its
  timer retired, flow taken), timer-wins (message branch terminated, its subscription retired),
  signal target, and a **recovery test** — crash mid-race, replay, then fire and confirm the
  winner + cancelled loser.
- **Phase 3 — Modeler + polish.** Drop `bpmn:EventBasedGateway` from the unsupported set (bpmn-js
  draws it natively); a Details-panel note; a hand-authored round-trip. Full `go test -race`,
  `vet`, `gofmt`, coverage.

### Consequences

- **Positive:** Atlas gains the request/timeout and first-of-several-events patterns on the
  catch-event, subscription, timer, and sibling-terminate machinery already built, plus one
  appended field. No change to the correlate/timer/signal paths; no new recovery path.
- **Negative / trade-offs accepted:** one new element-instance field (8 bytes, append-compatible)
  and a guarded prologue on the shared `completeAndTakeFlows` (cheap: skipped when the field is
  0, which is every non-gateway element). Losing waits self-retire lazily rather than eagerly —
  consistent with boundary disarm, but a stale subscription/timer lingers until its next
  (no-op) fire.
- **Follow-ups / risks to watch:** **receive-task** targets (a receive task is a valid
  event-gateway target and opens a message subscription like a catch, but is an activity with
  boundary events / I/O — deferred). The **parallel** event gateway (`instantiate`) variant.
  Eager subscription/timer retirement if a lingering losing wait ever proves observable.
  Conditional catch events (unsupported generally) as a fourth target type.

## Pros and cons of the options

### Option 1 — arm the real catch instances in a keyed race group (chosen)
- Good: reuses the catch behaviors, subscriptions, timers, correlate/fire paths, and the
  interrupt sibling-loop unchanged; the group is an event-derived field so recovery is free
  (I4/I6); the fire + cancel is a pure command-path function of committed state.
- Bad: one appended element-instance field; a guarded branch in the shared completion path.

### Option 2 — reuse `AttachedToKey` as the group key (rejected)
- Good: no new field.
- Bad: `AttachedToKey != 0` means "boundary event" to the disarm/interrupt scans; overloading it
  corrupts host disarm and gateway cancel alike.

### Option 3 — the gateway holds all subscriptions (rejected)
- Good: no per-target instance.
- Bad: forces the shared correlate/timer/signal paths to special-case a multi-subscription
  waiter and pick a branch — invasive for no gain over arming the real catch instances.

### Option 4 — parallel event gateway (rejected/deferred)
- Good: completeness.
- Bad: rare; the exclusive deferred-choice is the real need.

## Links

- builds on the intermediate catch events and their subscriptions/timers (ADR-0020 message
  correlation, ADR-0051/0054 timers, ADR-0088 signals), the boundary arm/fire and
  `interruptHost` sibling-terminate loop (ADR-0040), and the fork primitive `activateElement`
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 2 "Events and timers" — the deferred-choice gateway that completes the
  event-handling control-flow set
