# ADR-0102: Receive tasks

- **Status:** Accepted
- **Date:** 2026-08-07
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. A `<receiveTask>` is an **activity** that waits for
> a correlating message — the message intermediate catch event's semantics in task form — so
> it reuses the ADR-0020 subscription/correlate machinery wholesale. It introduces one new
> element type and behavior, no new subscription, value type, or recovery path.

## Context and problem statement

A **receive task** (`<bpmn:receiveTask messageRef="…">`) parks a token and waits until a
matching message arrives, then continues. It is the activity-shaped sibling of the
**message intermediate catch event** (ADR-0020): same "subscribe on a message and wait,
continue on correlate" semantics, but as a **task** rather than an event — so, unlike a
catch event, it is an *activity* and therefore accepts **boundary events** (the common
"wait for a reply, but time out after 2 days" pattern), **I/O variable mappings**
(ADR-0068), **data associations** (ADR-0058/0059), and **multi-instance** looping
(ADR-0077). It is the last unstarted item in Milestone 2's event set.

Atlas cannot express one today. `<receiveTask>` is parsed only to be **rejected**:
`xmlDefinitions` collects `ReceiveTasks []xmlNode` (`compiler/parse.go:813`) with no
`messageRef`, and `registerScope` fails any model containing one with "…which Atlas can't
execute yet" (`compiler/scope_compile.go:608`). There is no compiled type and no behavior.

The question this ADR answers: **how do we make a receive task wait on a message without a
second wait/correlate mechanism** — given that message catch events already subscribe,
wait, and are woken 1:1 by a correlating publish/throw through one shared path
(`correlateMessage`), and given that boundary arming, I/O mappings, data associations, and
multi-instance are already generic over *activities*.

What already exists, and is load-bearing:

- **The wait/wake path is solved.** `messageCatchEventBehavior` (`engine/behavior.go`)
  opens a `MessageSubscriptionValue` on `(name, correlationKey)` on activation and stays
  Activated; `correlateMessage` drives every matching subscription's element instance to
  `Completing`, writing the payload into its scope (ADR-0020). A receive task subscribes and
  is woken *identically* — the only difference is that on completion it is an activity taking
  its outgoing flow, which `completeAndTakeFlows` already does (running I/O output mappings
  and data associations on the way, ADR-0068/0058).
- **Activity machinery is generic.** Boundary events attach by `attachedToRef` and arm via
  `armBoundaryEvents` for any host; the compiler only gates attachment on `isActivity`
  (`compiler/validation.go`). I/O mappings (`wireIO`), data associations (`wireDataOut`/
  `wireDataIn`), and multi-instance (`wireMI`) are per-element helpers already applied to
  every task type (`compiler/parse.go`). Adding the receive task to `isActivity` and those
  wiring loops makes all of it work with no new mechanism.
- **Recovery is inherited.** A receive task's only durable state is its message
  subscription, an existing recoverable record; a parked receive task rebuilds from the log
  exactly as a parked message catch does.

So the wait, the wake, the payload, boundary arming, I/O, data, multi-instance, and recovery
already accommodate a receive task. What is missing is the compiled type, the (trivial)
behavior, and the parse wiring.

## Decision drivers

- **Reuse, don't reinvent.** A receive task *is* a message catch that is an activity — build
  it on the same subscription/correlate path, not a parallel one.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2);
  a single `applyToState` live and on recovery (I4); the message resolved at compile (I5);
  the correlation key frozen into the subscription event so replay never re-evaluates it (I6)
  — all inherited unchanged from the message catch.
- **Faithful BPMN.** A receive task is an activity: it accepts boundary events, I/O and data
  mappings, and multi-instance, matching how modelers use "wait for a reply, else time out".
- **Keep it compile-gated.** A receive task with no (or an unknown) `messageRef` cannot
  execute, so it stays a deploy error rather than a silent no-op.

## Considered options

1. **A `TypeReceiveTask` element with a behavior that reuses the message-catch
   subscription/correlate path (chosen).** Parse `<receiveTask messageRef>` to a
   `TypeReceiveTask` node carrying a `MessageDetail` (name + compiled correlation key),
   resolved via the same `buildMessageResolver` the catch event uses. Its `receiveTaskBehavior`
   opens a message subscription on activation and completes-and-takes-flows on correlate —
   the message-catch behavior, minus nothing. Mark it an activity so boundary/I/O/data/MI
   machinery applies. No new subscription family, value type, or recovery path.
2. **Compile a receive task down to a message catch event (a plain event, not an activity).**
   Rejected: a receive task is an *activity* in BPMN — it can host boundary events and I/O
   mappings, which a catch event (compiled as a plain event, not in `isActivity`) does not.
   Collapsing the two loses the boundary-timeout pattern that is the receive task's main use.
3. **A generic "waitpoint" abstraction unifying catch events and receive tasks.** Rejected:
   over-engineering — the two already share `correlateMessage`; a shared *behavior struct*
   saves a dozen lines at the cost of an indirection, and the element-type distinction
   (event vs activity) is real and must survive anyway.

## Decision outcome

Chosen: **option 1 — a `TypeReceiveTask` activity whose behavior reuses the message
subscription/correlate path.** The genuinely new logic is (a) a compiled `TypeReceiveTask`
with a `MessageDetail` table and `messageRef` resolution, (b) adding the receive task to
`isActivity` and the data/I/O/multi-instance wiring loops so it is a first-class activity,
and (c) `receiveTaskBehavior`, which is `messageCatchEventBehavior`'s two methods over the
receive-task detail table.

### Compiler

- Parse `<receiveTask id messageRef>` into an `xmlReceiveTask` (mirroring `xmlServiceTask`'s
  activity shape: `IOMapping`, `MultiInstance`, `DataOut`, `DataIn`), replacing the
  `[]xmlNode` placeholder. Add `TypeReceiveTask` to the `BpmnType` enum + `String()` and grow
  `numBpmnTypes`; a `receiveTasks []MessageDetail` table with an `AddReceiveTask(name,
  correlationKey)` builder and a `ReceiveTask(detail)` accessor (mirroring `messageCatches`).
- In `registerScope`, resolve the task's `messageRef` through `resolveMessage` (a receive task
  with an empty or unknown ref is a deploy error, like a message catch) and register the node;
  drop `receiveTask` from the "unsupported element" list. Add `TypeReceiveTask` to `isActivity`
  and call `wireDataOut`/`wireDataIn`/`wireIO`/`wireMI` for the receive tasks, so boundary
  attachment validation, data associations, I/O mappings, and multi-instance all apply.

### Runtime

- `receiveTaskBehavior`: `OnActivated` opens a `MessageSubscriptionValue` on the task's
  resolved `(name, correlationKey)` and waits (stays Activated); `OnCompleting` is
  `completeAndTakeFlows`. It is `messageCatchEventBehavior` verbatim over the receive-task
  detail table — a correlating publish/throw drives it to `Completing` through the existing
  `correlateMessage`, and an attached boundary event arms and fires unchanged.

### Modeler

- The Implement panel offers the shared-message picker (`messageFieldsHTML`) on a
  `bpmn:ReceiveTask`, exactly as on a message catch event; bpmn-js already draws the receive
  task and offers it in the palette and replace menu.

### Consequences

- **Positive:** Atlas gains the wait-for-message *activity* — with boundary timeouts, I/O and
  data mappings, and multi-instance — on the message machinery ADR-0020 already built. One
  new element type and a two-method behavior; no new subscription, value type, or recovery
  path. Completes Milestone 2's event set.
- **Negative / trade-offs accepted:** a second element type (`TypeReceiveTask`) whose behavior
  duplicates the message catch's two methods over a separate detail table — the cost of
  keeping the event-vs-activity distinction explicit rather than folding them.
- **Follow-ups / risks to watch:** message **buffering** (a message that arrives before the
  receive task subscribes is dropped, as for a catch event) is still deferred to ADR-0020's
  follow-up; a receive task as a **message-throw's** local counterpart (send/receive pairs) is
  a modeling convention, not new engine work.

## Pros and cons of the options

### Option 1 — TypeReceiveTask activity reusing the message-catch path (chosen)
- Good: reuses subscription/correlate, boundary/I/O/data/MI machinery, and recovery; the
  event-vs-activity distinction is preserved; minimal new code.
- Bad: a behavior that duplicates the message catch's two methods over its own detail table.

### Option 2 — compile a receive task to a message catch event (rejected)
- Good: zero new element type.
- Bad: a catch event is not an activity, so boundary events and I/O mappings — the receive
  task's whole point — would not attach.

### Option 3 — a unified waitpoint abstraction (rejected)
- Good: one wait primitive.
- Bad: over-engineering; the two already share `correlateMessage`, and the element-type
  distinction must persist regardless.

## Links

- builds directly on ADR-0020 (message correlation and subscriptions — the wait/wake path a
  receive task reuses), and reuses ADR-0040 (boundary arm/fire), ADR-0068 (I/O mappings),
  ADR-0058/0059 (data associations), and ADR-0077 (multi-instance) — all generic over
  activities
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery test up front)
- ROADMAP Milestone 2 "Receive tasks"; message buffering remains an ADR-0020 follow-up
