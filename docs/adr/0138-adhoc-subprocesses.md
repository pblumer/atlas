# ADR-0138: Ad-hoc subprocesses (on-demand, unordered contained activities)

- **Status:** Proposed
- **Date:** 2026-08-18
- **Deciders:** Atlas engine team

> **Implementation status.** Proposed. An **ad-hoc subprocess** (`<adHocSubProcess>`) is a container
> scope whose contained activities are **not driven by sequence flow from a start event** but run
> **on demand, in any order, zero or more times** — the BPMN construct for flexible / case-management
> work ("do any of these, in whatever order, until we're done"). It carries an optional boolean FEEL
> **completion condition** and an **ordering** (parallel or sequential). Atlas ships the faithful
> executable core: on entry it activates every **entry activity** (a contained flow node with no
> incoming sequence flow) at once (parallel ordering), each an independent token scoped by the
> container; contained activities may still be wired by sequence flows, so a token flows on normally
> within the scope. After each contained activity completes, the **completion condition** is
> re-evaluated over the ad-hoc's scope chain; the first time it holds the engine cancels the remaining
> active work (`cancelRemainingInstances`, default true) and completes the ad-hoc, taking its outgoing
> flow. With no completion condition the ad-hoc completes when its scope drains — the plain
> subprocess scope-drain (ADR-0074). It reuses the **subprocess scope machinery** (container is a
> scope; `completeScope` drives its `Completing`; boundary events and recovery come for free) and the
> **completion-condition eval + `terminateScope` cancel** pattern from multi-instance (ADR-0077).
> Sequential ordering and `cancelRemainingInstances="false"` are documented follow-ups.

## Context and problem statement

Every executable element Atlas runs today is **flow-driven**: a token enters a node through a sequence
flow (or a start/boundary/event trigger), the node runs, and the token leaves through an outgoing
flow. An **ad-hoc subprocess** breaks that model deliberately. Its contained activities have **no
required sequence flow between them**; they are a *bag of things that may be done*, in any order, each
zero or more times, until the work is judged complete. This is BPMN's construct for **flexible,
case-management-style processes** — "gather documents, call the customer, request approval — in
whatever order the case worker sees fit, until the completion condition says stop." It is the last
major **structural** BPMN element Atlas lacks: the event palette (message/timer/signal/error/
escalation/conditional/link/compensation), the gateways (exclusive/parallel/inclusive/event-based),
and the scoped containers (embedded subprocess, event subprocess, transaction, call activity) are all
implemented, but the ad-hoc container — the one whose activities are *not* sequenced — is not.

Atlas cannot express one today: `<adHocSubProcess>` is parsed only to raise a clear **deploy error**
(`compiler/scope_compile.go`, the unsupported-node table names the element and points at the supported
set), and the Modeler blocks it (`bpmn:AdHocSubProcess` in `UNSUPPORTED_TYPES`, `api/web/editor.js`).
Camunda 8 / Zeebe implements ad-hoc subprocesses (activating the no-incoming-flow inner elements, with
a completion condition), so there is a reference model to align with rather than design from scratch.

The question this ADR answers: **how does the engine start and complete an ad-hoc subprocess** — since
there is no start event to seed and no single outgoing flow inside to follow — while staying within
the event-sourced, single-writer model and reusing what already exists.

What already exists, and is load-bearing:

- **A subprocess is already a scope.** `subProcessBehavior` (`engine/behavior.go`) makes an embedded
  subprocess a child scope: its inner nodes are scoped by the container's key, it is **not** driven to
  `Completing` by a token flowing through it but by its scope **draining** (`completeScope`, ADR-0074),
  and boundary events, interrupts (`terminateScope`), and recovery all treat it uniformly. An ad-hoc
  subprocess is *the same scope* with a different way of being **entered** (activate the entry
  activities instead of seeding a start event) and an extra way of **completing** (a completion
  condition, not only scope-drain).
- **The compiler knows each node's in-degree.** `CompiledNode.IncomingCount` (`compiler/process.go`)
  is the number of sequence flows targeting a node (a parallel join already reads it). The ad-hoc's
  **entry activities** are exactly its contained flow nodes with `IncomingCount == 0` (excluding
  boundary events and event-sub triggers, which arm rather than start) — computable at compile into a
  per-container index, the ad-hoc analog of `ScopeStartEvents` (ADR-0074).
- **Completion-condition eval + cancel-the-rest is a solved pattern.** A multi-instance loop compiles
  a `CompletionCondition *expr.Compiled` and, in `finishMultiInstanceIteration` (`engine/behavior.go`),
  evaluates it over the scope chain after each iteration completes; when it holds it calls
  `terminateScope(pi, bodyKey)` to cancel the still-running iterations and then `completeScope(bodyKey)`
  (ADR-0077). An ad-hoc subprocess's completion condition is **the same eval and the same cancel**,
  checked after each *contained activity* completes rather than each iteration.
- **A detail can carry a compiled FEEL expression + flags.** `MultiInstanceDetail.CompletionCondition
  *expr.Compiled` and `EventSubProcessDetail` are the precedents — an `AdHocDetail{CompletionCondition
  *expr.Compiled; CancelRemaining, Sequential bool}` carries the ad-hoc's configuration the same way.
- **`terminateScope` / `completeScope` already tear down and finish a scope.** The cancel-remaining and
  scope-drain paths the ad-hoc needs are the same primitives boundary interrupts, transaction cancel,
  and MI early-exit use — no new teardown or completion machinery.

So the scope, the entry-node index, the completion-condition eval, the cancel-the-rest, and the
scope-drain completion all already exist. What is missing is (a) parsing `<adHocSubProcess>` and its
`<completionCondition>` / `ordering` / `cancelRemainingInstances`, (b) a `TypeAdHocSubProcess` type
with an `AdHocDetail` and a compiled entry-activity index, and (c) a behavior that **activates the
entry activities on entry** and **checks the completion condition after each contained activity
completes**.

## Decision drivers

- **Reuse the scope, the eval, and the teardown.** An ad-hoc subprocess is an embedded-subprocess
  scope entered differently and completed with an extra condition. Both the scope machinery (ADR-0074)
  and the completion-condition-plus-`terminateScope` pattern (ADR-0077) exist — the ad-hoc is their
  composition, not new infrastructure.
- **Faithful BPMN, tractable subset.** The **parallel** ordering (activate all entry activities at
  once) with an optional completion condition and the default `cancelRemainingInstances="true"` is the
  core, most-used shape and matches Zeebe. Sequential ordering and `cancelRemainingInstances="false"`
  layer on later without changing the model.
- **Invariants hold.** Entry activation and the completion-condition check run **on the command path**
  as ordinary activation/completion processing (no hot-path allocation, I1); the fire is the existing
  durable `Terminated`/`Completed`/`Completing` chain (durable-before-visible, I2); `applyToState` is
  untouched — the completion check is a command-path read like the MI one (I4); the condition and the
  entry index are resolved at **compile** (I5); the check runs **live only** and its effect (cancel +
  complete) is a persisted event chain that replays identically (I6).
- **A clear diagnostic today becomes a runnable element.** The compiler's existing "Atlas can't
  execute an `<adHocSubProcess>` yet" error is replaced by real execution; the Modeler's block is
  lifted and the completion condition / ordering become authorable.

## Considered options

1. **A `TypeAdHocSubProcess` scope that activates its entry activities on entry and checks a
   completion condition after each contained activity completes (chosen).** Parse `<adHocSubProcess>`
   as a scope (like `<subProcess>`), plus its `<completionCondition>` (compiled to boolean FEEL),
   `ordering`, and `cancelRemainingInstances`. Compile a per-container **entry-activity index** (inner
   flow nodes with `IncomingCount == 0`, excluding boundary events). `adHocSubProcessBehavior.OnActivated`
   arms the scope's event subprocesses (as a subprocess does) and activates every entry activity at
   once (parallel ordering), each an independent token scoped by the container; an ad-hoc with no entry
   activity completes at once. A contained activity that has outgoing flows carries its token on within
   the scope like any node; a dead-end drains it. **After each contained activity completes**, a
   command-path checkpoint evaluates the completion condition over the ad-hoc's scope chain; the first
   time it holds, `terminateScope` cancels the remaining active work (default `cancelRemainingInstances`)
   and `completeScope` finishes the ad-hoc, whose `OnCompleting` drops its local scope and takes its
   outgoing flow. With no completion condition the ad-hoc completes on plain scope-drain. Boundary
   events on the ad-hoc, interrupts, and recovery come from the shared scope machinery unchanged.
2. **Keep it unsupported (status quo).** Continue to reject `<adHocSubProcess>` at deploy. Rejected:
   it permanently omits a whole BPMN element family — flexible / case-management processes have no
   representation, and a model drawn with the standard ad-hoc shape cannot run at all.
3. **Rewrite an ad-hoc subprocess as a plain subprocess with a start event fanning into a parallel
   gateway.** Desugar the ad-hoc into a start → parallel-split → activities → parallel-join → end at
   compile. Rejected: it is **not faithful** — a parallel gateway runs each branch **exactly once** and
   **joins by waiting for all**, losing the ad-hoc's *zero-or-more* and *any-order* semantics and its
   ability to **cancel the remaining activities** when a completion condition fires; it also cannot
   represent "do this one twice, that one never," and the Modeler's ad-hoc shape would not round-trip
   to the desugared graph. The completion condition has no home in that shape.

## Decision outcome

Chosen: **option 1 — an ad-hoc subprocess is a scope that activates its entry activities on entry and
completes on its completion condition (cancelling the rest) or on scope-drain.** The genuinely new
logic is (a) parsing `<adHocSubProcess>` + `<completionCondition>` / `ordering` /
`cancelRemainingInstances`, (b) a `TypeAdHocSubProcess` type with an `AdHocDetail` and a compiled
entry-activity index, and (c) `adHocSubProcessBehavior` plus the after-each-activity completion
checkpoint.

### Compiler

- Parse `<adHocSubProcess>` as a scope: it embeds `xmlFlowContent` exactly as `<subProcess>` does, so
  its inner `<serviceTask>`, `<userTask>`, `<sequenceFlow>`, boundary events, and nested subprocesses
  register into the flat graph under a pushed scope (ADR-0074). Additionally parse `ordering`
  (`Parallel` default / `Sequential`), `cancelRemainingInstances` (default `true`), and an optional
  `<completionCondition>` compiled with `expr.CompileAuto` (an empty/absent condition means "complete
  on scope-drain", not an error — unlike a conditional event, an ad-hoc without a condition is valid).
- Add `TypeAdHocSubProcess` to the `BpmnType` enum + `String()`; grow `numBpmnTypes`.
- Add `AdHocDetail{CompletionCondition *expr.Compiled; CancelRemaining bool; Sequential bool}` and,
  per container, an **entry-activity index** — the contained flow nodes with `IncomingCount == 0`,
  excluding boundary events and event-sub trigger handlers — stored the way `ScopeStartEvents` is
  (a slice into a shared topology array, no per-activation allocation). Builder methods
  `AddAdHocSubProcess` / `SetAdHoc(nodeID, detail)` mirror `AddSubProcess` / `SetEventSubProcess`.
- Remove `<adHocSubProcess>` from the unsupported-node table in `registerScope`.

### Runtime

- `adHocSubProcessBehavior.OnActivated`: arm the scope's event subprocesses (as `subProcessBehavior`
  does), then activate **every entry activity** at once (parallel ordering), each a fresh element
  instance scoped by the container with its own token; an ad-hoc with **no** entry activity is an
  empty scope and completes immediately (schedule its `Completing`).
- **Completion checkpoint.** After a contained activity completes (the same point `completeAndTakeFlows`
  finishes an inner node), if its flow scope is an ad-hoc container with a completion condition,
  evaluate the condition over the ad-hoc's scope chain (`cond.Eval(bindInputsChain(cond.Inputs(),
  adhocKey))` guarded by `expr.IsTrue`, the ADR-0137 / MI eval). If it holds: `terminateScope` the
  ad-hoc (cancelling the remaining active activities and their jobs — `cancelRemainingInstances`
  default true) and `completeScope` it. If it does not hold, the token continues (it had outgoing
  flows) or drains (dead-end), and plain scope-drain will complete the ad-hoc once all work is done.
- `adHocSubProcessBehavior.OnCompleting`: drop the local scope and take the ad-hoc's outgoing flow —
  the subprocess completion path (`dropLocalScope` + `completeAndTakeFlows`), so an ad-hoc is an
  ordinary activity to the flow that contains it.
- Boundary events on the ad-hoc, `terminateScope` interrupts, and **recovery** are inherited from the
  scope machinery: the container and its inner instances replay through `Activated` events, and the
  completion checkpoint is a live-only command-path read whose effect (a persisted `Terminated` /
  `Completing` chain) replays identically (I6).

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<adHocSubProcess>` (scope content + `ordering` +
  `cancelRemainingInstances` + `<completionCondition>`); `TypeAdHocSubProcess` + `AdHocDetail` + the
  entry-activity index; remove the deploy rejection. *Tests:* an ad-hoc with two unconnected tasks
  compiles with both as entry activities; a completion condition compiles to FEEL with the right
  inputs; `ordering`/`cancelRemainingInstances` flags parse (defaults parallel + cancel); an inner
  chain (a→b) makes only `a` an entry; a boundary event on an inner task is not an entry.
- **Phase 2 — Runtime (parallel, cancelRemaining=true).** `adHocSubProcessBehavior`; the after-each-
  activity completion checkpoint; empty-ad-hoc immediate completion; `OnCompleting` drop-scope +
  take-flow. *Tests:* two parallel entry activities both run, then the ad-hoc completes and takes its
  outgoing flow (no condition → scope-drain); a completion condition that becomes true after the first
  activity completes cancels the second (its job gone) and completes the ad-hoc; an inner sequence-flow
  chain runs a→b before completing; an empty ad-hoc passes straight through; a **recovery** test — a
  parked ad-hoc with an active inner job replays and completes on job completion.
- **Phase 3 — Modeler + docs + remaining ordering options.** Drop `bpmn:AdHocSubProcess` from
  `UNSUPPORTED_TYPES`; add a completion-condition (FEEL) field + an ordering select +
  `cancelRemainingInstances` toggle to the ad-hoc in the Implement panel. Accept this ADR and update
  the ROADMAP. If tractable, **sequential ordering** (activate one entry activity at a time, the next
  on each completion) and **`cancelRemainingInstances="false"`** (let active activities finish before
  the ad-hoc completes) land here; otherwise they are documented follow-ups.

### Consequences

- **Positive:** Atlas gains **flexible / case-management control flow** — a bag of on-demand,
  unordered, zero-or-more activities with a completion condition — the last major structural BPMN
  element, built entirely on the existing subprocess scope, the multi-instance completion-condition
  eval, and the `terminateScope`/`completeScope` teardown. No new value type, event, or recovery path;
  boundary events and recovery come for free from the scope machinery. The compiler's "can't execute
  yet" error and the Modeler's block are both lifted.
- **Negative / trade-offs accepted:** a new `TypeAdHocSubProcess` behavior and a completion checkpoint
  after each contained activity completes; the v1 subset ships **parallel ordering** and
  **`cancelRemainingInstances="true"`** only, with sequential ordering and the `false` variant as
  documented follow-ups; the completion condition is checked at **activity-completion checkpoints**
  (the BPMN trigger), so a condition made true purely by an external `SetVariables` with no activity
  completing is not noticed until the next completion — the ADR-0137 variable-change re-check could be
  reused to close that gap later.
- **Follow-ups / risks to watch:** (1) **Sequential ordering** — activate one entry at a time; needs a
  "next entry on completion" driver like a sequential MI loop. (2) **`cancelRemainingInstances="false"`**
  — complete only once the already-active activities finish, rather than cancelling them. (3) **Entry
  activities with incoming flow only from inside the ad-hoc** (a cycle with no in-degree-0 node) —
  define and test that an ad-hoc with no entry activity completes at once rather than deadlocking.
  (4) **Nested markers** — an ad-hoc inner activity that is itself multi-instance or a subprocess must
  compose with the scope machinery unchanged; covered by a test.

## Pros and cons of the options

### Option 1 — a `TypeAdHocSubProcess` scope (chosen)
- Good: reuses the subprocess scope, the MI completion-condition eval, and the `terminateScope`/
  `completeScope` teardown; faithful *any-order, zero-or-more* semantics with a real completion
  condition and cancel-the-rest; boundary events and recovery for free; deterministic and off replay.
- Bad: a new behavior + completion checkpoint; v1 ships parallel + cancel-true only; the condition is
  checked at activity-completion points, not on every external variable write.

### Option 2 — keep it unsupported (rejected)
- Good: no work.
- Bad: a whole BPMN element family remains unrunnable; standard ad-hoc diagrams fail to deploy.

### Option 3 — desugar to start → parallel-split → join (rejected)
- Good: no new type; reuses gateways.
- Bad: not faithful — loses zero-or-more, any-order, and cancel-the-rest; the completion condition has
  no home; the Modeler's ad-hoc shape does not round-trip.

## Links

- builds on the embedded-subprocess **scope machinery** (`subProcessBehavior` / `completeScope` /
  `terminateScope`, **ADR-0074**), the multi-instance **completion-condition eval + cancel-remaining**
  (`finishMultiInstanceIteration`, **ADR-0077**), FEEL `CompileAuto`/`Inputs()` and scope-chain
  resolution (`bindInputsChain`, **ADR-0008/0015/0086**), and event subprocesses arming inside a scope
  (**ADR-0082**); the completion condition's activity-completion checkpoint could later reuse the
  variable-change re-check of conditional events (**ADR-0137**)
- honors I1, I2, I4, I5, I6 and **ADR-0018** (test-first, recovery tests up front)
- ROADMAP Milestone 1; the last major structural BPMN element — a container whose activities are not
  driven by sequence flow
