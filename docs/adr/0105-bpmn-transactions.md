# ADR-0105: BPMN transactions (cancel end event, cancel boundary, transactional compensation)

- **Status:** Accepted
- **Date:** 2026-08-07
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered (phases 1–3); `go test -race ./...` green, repo
> coverage floor held. A BPMN **transaction subprocess**
> (`<transaction>`) is an embedded subprocess (ADR-0074) with one added outcome: it can be
> **cancelled**. A **cancel end event** (`<endEvent><cancelEventDefinition/>`) inside the
> transaction rolls the transaction back — it **compensates** every completed compensable
> activity in the transaction scope (ADR-0103, reverse completion order), then the
> transaction is torn down and a **cancel boundary event**
> (`<boundaryEvent><cancelEventDefinition/>`) on the transaction takes its recovery flow.
> This reuses the subprocess scope lifecycle, the `compensate` scope walk, the boundary
> arm/fire machinery, and `interruptHost`/`terminateScope` wholesale; the genuinely new
> parts are (a) parsing `<transaction>` and `<cancelEventDefinition>`, (b) a
> `TypeCancelEndEvent` and a `BoundaryCancel` boundary kind, (c) a small event-derived
> **canceling marker** that links the asynchronous compensation drain to the boundary
> firing, and (d) `cancelEndEventBehavior` + a cancel branch in the subprocess completion.
> Closes Milestone 3's final item, "BPMN transactions (with cancel/compensation)."

## Context and problem statement

A **transaction subprocess** groups activities that must succeed or roll back **as a unit**.
It has three outcomes: it **completes** normally (commits) and flows on; it **cancels** —
its completed work is compensated and it routes out a cancel boundary; or it fails with an
**error** (ADR-0089), which is ordinary error propagation and needs nothing new here.

The distinctive construct is **cancellation**. A **cancel end event**, reachable only inside
a transaction, means "abort this transaction and undo what it did." Per BPMN its effect is,
in order: (1) any still-running activities in the transaction are terminated; (2) every
successfully **completed** activity that has a compensation handler is **compensated**, in
reverse completion order; (3) the **cancel boundary event** attached to the transaction is
triggered, and the enclosing flow continues from it. A cancel boundary event may be attached
**only** to a transaction, and is **always interrupting**.

Atlas cannot express one today. `<transaction>` is not parsed at all (a grep for
`transaction` across `compiler/` returns nothing), so a modeled transaction is silently
dropped by the XML decoder; `<cancelEventDefinition>` is unparsed; there is no cancel end
event type and no cancel boundary kind. The Modeler explicitly flags `bpmn:Transaction` as
"aren't supported yet" (`api/web/editor.js`).

The question this ADR answers: **how do we run transactional cancellation — terminate,
compensate in reverse order, then continue from the cancel boundary — reusing the subprocess
scope, the compensation walk, and the boundary machinery, while keeping the compensate →
then → continue ordering correct and deterministic on replay.**

What already exists, and is load-bearing:

- **A transaction *is* a subprocess scope.** `TypeSubProcess` already runs a
  `start → … → end` in a child scope keyed by its element instance, completes when the scope
  drains (`completeScope`, `engine/behavior.go`), supports boundary events, I/O mappings,
  event subprocesses, and nesting (ADR-0074). A transaction reuses all of it — it is a
  subprocess that additionally *may be cancelled*. So the container is a `TypeSubProcess`
  node marked **`IsTransaction`**, not a new element type; every scope-semantics site
  (`completeScope`, `terminateScope`, `scopeContains`, the compensable-index cleanup in
  `engine/apply.go`, reachability roots in `compiler/validation.go`) then applies unchanged.
- **Compensation is solved.** `compensate(c, ei, activityRef)` (ADR-0103,
  `engine/behavior.go`) scans the `cfCompensable` index of `ei.FlowScopeKey` newest-first,
  activates each completed activity's handler in the scope, and consumes the record. A
  transaction's compensable activities record under the transaction's own scope key, so a
  cancel end event inside the transaction has exactly that scope as its `FlowScopeKey`:
  cancellation's rollback step is literally `compensate(c, ei, -1)` — the whole-scope,
  reverse-order compensation the compensation **end** event already performs. Handlers are
  counted in the scope's active children, so the scope does not finish until they do.
- **Boundary arm/fire, and the inert-catch pattern, are solved.** An error boundary
  (ADR-0089) arms as an element instance but opens no subscription/timer — it waits purely
  to be *found* and driven to `Completing`, which runs `interruptHost`. A cancel boundary is
  the same inert, always-interrupting shape; it is armed at transaction activation and, on
  cancel, driven to `Completing`.
- **Tearing a scope down is solved.** `interruptHost(c, hostKey, selfKey)` terminates the
  host's inner scope (`terminateScope`), cancels its job, retires the host, and disarms the
  host's other boundary siblings, then the firing boundary takes its flow. On cancel the
  transaction scope is already drained (compensation finished), so `interruptHost`'s
  `terminateScope` is a no-op and it cleanly retires the transaction shell and its sibling
  boundaries.

What is missing: `<transaction>` + `<cancelEventDefinition>` parsing, the `IsTransaction`
mark, a `TypeCancelEndEvent` and a `BoundaryCancel` kind, the behavior that compensates then
routes to the boundary, and the durable link that defers the boundary firing until
compensation drains.

## Decision drivers

- **Reuse, don't reinvent.** Build the container on `TypeSubProcess`, rollback on
  `compensate`, the cancel boundary on the inert-error-boundary shape, and teardown on
  `interruptHost`/`terminateScope`. Add only what a transaction genuinely needs.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2);
  a single `applyToState` live and on recovery (I4); the transaction/cancel structure
  resolved at compile (I5); **deterministic replay** — the cancel routing is a pure function
  of committed state, the canceling marker derived from the cancel end event's committed
  `Completed` event, never wall-clock or map iteration (I6).
- **Faithful BPMN.** Cancel terminates running work, compensates completed work in reverse
  order, then continues from an always-interrupting cancel boundary that may attach only to a
  transaction; a cancel end event is valid only inside a transaction.
- **Correct ordering across async compensation.** Compensation handlers are activities
  (service-task jobs, subprocesses) that span batches; the cancel boundary must fire only
  **after** they all complete. The design must defer the boundary firing to the scope-drain
  point without a busy wait or a synchronous assumption.

## Considered options

1. **Transaction = `TypeSubProcess` + `IsTransaction`; cancel = `compensate` then route the
   drained scope to an inert cancel boundary, linked by an event-derived canceling marker
   (chosen).** `<transaction>` compiles to a `TypeSubProcess` node marked `IsTransaction`, so
   all scope machinery is inherited. `cancelEndEventBehavior.OnActivated` terminates the
   transaction's other live tokens (keeping the compensable index), calls
   `compensate(c, ei, -1)` to activate the handlers, and hops to `Completing`; its
   `OnCompleting` emits `Completed` (which, in `applyToState`, sets a per-transaction
   **canceling** marker on its scope) and calls `completeScope`. When the compensation
   handlers drain the transaction scope, `completeScope` drives the transaction's
   `Completing`; the subprocess completion, seeing the canceling marker, drives the armed
   `BoundaryCancel` to `Completing` instead of taking the transaction's normal outgoing flow.
   The boundary's `interruptHost` retires the (already-drained) transaction shell and its
   sibling boundaries, then takes the recovery flow. The marker is dropped when the
   transaction tears down. No new recovery path — the marker is derived from a committed
   event and the routing is a pure function of committed state.
2. **A distinct `TypeTransaction` element type with its own behavior.** Rejected: a
   transaction is behaviorally a subprocess for everything except the cancel outcome, so a
   new type would force every scope-semantics site (`apply.go` compensable cleanup,
   `validation.go` reachability roots and scope checks, the builder's scope-tree passes,
   `completeScope`/`terminateScope` type checks) to special-case a second "is this a scope
   container" type — more surface for no behavioral gain. A one-bit `IsTransaction` mark on
   the existing subprocess node captures the only real difference.
3. **Fire the cancel boundary immediately and run compensation as part of the boundary
   flow.** Rejected: `interruptHost` tears the transaction scope down (`terminateScope`),
   which would kill compensation handlers the instant they were activated. BPMN requires
   compensation to *complete* before the boundary continuation runs, and handlers span
   batches, so the boundary firing must be deferred to the scope-drain point — exactly what
   the canceling marker + `completeScope` provide.
4. **A synchronous compensation model (run all handlers inline before continuing).**
   Rejected: compensation handlers are ordinary activities (jobs, subprocesses) whose
   lifecycle spans batches and external workers; they cannot run to completion inside one
   command. The asynchronous scope-drain is the only faithful model.

## Decision outcome

Chosen: **option 1.** The genuinely new logic is (a) parsing `<transaction>` and
`<cancelEventDefinition>`; (b) the `IsTransaction` mark, a `TypeCancelEndEvent`, and a
`BoundaryCancel` boundary kind; (c) the canceling marker in `applyToState`; and (d)
`cancelEndEventBehavior` plus the cancel branch in the subprocess completion.

### Compiler

- Parse `<transaction>` as `xmlTransaction` (the `xmlSubProcess` shape, embedding
  `xmlFlowContent` so it nests identically) collected on `xmlFlowContent.Transactions`. It
  compiles through the **same** registration path as an embedded subprocess
  (`AddSubProcess` + `PushScope`/`registerScope`/`PopScope`), then is marked `IsTransaction`
  on its `CompiledNode`. A transaction is never `triggeredByEvent`.
- Parse `<cancelEventDefinition/>` as a `Cancel *xmlCancelEventDefinition` pointer on the end
  and boundary event structs (presence only — a cancel carries no ref or payload).
- Add `TypeCancelEndEvent` to the `BpmnType` enum + `String()`, grow `numBpmnTypes`. Add a
  `BoundaryCancel` value to `BoundaryEventKind`. A cancel end event and a cancel boundary
  carry no detail (a cancel always compensates the whole transaction; the boundary's
  continuation is its own outgoing flow). A `BoundaryCancel` is **forced interrupting**.
- End-event dispatch gains a `case e.Cancel != nil → AddCancelEndEvent()`; boundary dispatch
  gains a `case ev.Cancel != nil → AddBoundaryCancelEvent(host)`.
- **Validation** (`checkTransactions`, a new `Validate` step): a `TypeCancelEndEvent` whose
  enclosing `FlowScope` is not an `IsTransaction` subprocess is a deploy error (a cancel end
  is valid only directly inside a transaction); a `BoundaryCancel` whose host is not an
  `IsTransaction` subprocess is a deploy error (a cancel boundary attaches only to a
  transaction); a transaction that has a cancel end event but no cancel boundary raises a
  `SeverityWarning` (the cancellation would tear the transaction down with no recovery route
  — legal, but usually a modeling mistake).

### Runtime

- **Container.** `TypeSubProcess` (marked `IsTransaction`) reuses `subProcessBehavior` — same
  scope seeding, event-subprocess arming, and normal completion. The cancel boundary arms
  inert like an error boundary (`BoundaryCancel` opens nothing in
  `boundaryEventBehavior.OnActivated`); `armBoundaryEvents` arms it (only `BoundaryCompensation`
  is skipped).
- **Cancel end event** (`cancelEndEventBehavior`):
  - `OnActivated`: let `txKey := ei.FlowScopeKey` (validation guarantees this is the
    transaction). Terminate the transaction's **other** live tokens
    (`terminateScopeExcept(procKey, txKey, selfKey)` — a `terminateScope` that spares the
    cancel end event; the completed compensable activities are not live instances, so their
    `cfCompensable` records survive, which are dropped only when the transaction element
    itself tears down). Then `compensate(c, ei, -1)` activates the handlers in reverse
    completion order, counted as scope children. Hop to `Completing`.
  - `OnCompleting`: emit `Completed` (decrementing the transaction scope; `applyToState`
    sets the canceling marker on `ei.FlowScopeKey`), then `completeScope(ei.FlowScopeKey)` —
    which fires the transaction's `Completing` once the compensation handlers have drained
    (immediately if there was nothing to compensate).
- **Transaction completion** (the cancel branch in `subProcessBehavior.OnCompleting`, guarded
  by `IsTransaction`): if the transaction's scope carries the **canceling marker**, drive the
  armed `BoundaryCancel` to `Completing` (its `interruptHost` sees the drained scope —
  `terminateScope` a no-op — retires the transaction shell as `Terminated`, disarms sibling
  boundaries, and takes the boundary's recovery flow) rather than taking the transaction's
  normal outgoing flow. If there is no cancel boundary, retire the transaction (`Terminated`)
  and drain the parent scope. Otherwise (no marker) complete normally (commit).
- **Canceling marker.** A per-transaction presence marker in a new `cfCanceling` column
  family, keyed by the transaction's scope key. It is **set in `applyToState`** when a
  `TypeCancelEndEvent` element's `Completed` event is applied (keyed by its `FlowScopeKey`),
  and **dropped in `applyToState`** when the transaction element completes or terminates
  (beside the ADR-0103 `DeleteCompensablesOfScope` on the same teardown). It is therefore a
  pure function of committed events — recovery rebuilds it identically, and there is **no new
  recovery path**: the throw is a command-path walk, the marker a derived index.

### Recovery

Cancellation spans batches (terminate → activate handlers → workers complete → scope drains
→ boundary fires), and commands are not replayed (only events are). Every waiting point is a
durable event-derived state: the armed cancel boundary (an element instance), each
compensation handler's job, the `cfCompensable` records, and the `cfCanceling` marker all
rebuild from the log. A crash mid-cancellation (e.g. while a compensation handler's job waits
for a worker) recovers to the same waiting state and continues — the recovery test crashes at
exactly that point.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<transaction>` (→ `IsTransaction` subprocess) and
  `<cancelEventDefinition>`; `TypeCancelEndEvent`; `BoundaryCancel` (forced interrupting);
  the `checkTransactions` validation. *Tests:* a transaction with a cancel end + cancel
  boundary compiles with the boundary interrupting and the cancel end typed; a cancel end
  outside a transaction, and a cancel boundary on a plain activity, are deploy errors; the
  no-boundary warning fires.
- **Phase 2 — Runtime.** `cancelEndEventBehavior`; the inert `BoundaryCancel`; the
  `cfCanceling` marker + `applyToState` set/drop; the cancel branch in the subprocess
  completion; `terminateScopeExcept`. *Tests:* `transaction{ A(compensable) → cancel end }`
  with a cancel boundary compensates A then takes the recovery flow; a transaction that
  commits (normal end) takes its normal flow and does **not** compensate; two compensable
  activities compensate in reverse order before the boundary fires; running siblings are
  terminated; a **recovery test** — crash while a compensation handler's job waits, replay,
  then complete and confirm the boundary fires.
- **Phase 3 — Modeler + polish.** Drop `bpmn:Transaction` from the Modeler's unsupported
  set (bpmn-js already draws transactions, cancel boundaries, and cancel end events via the
  wrench menu, and the moddle types are native); a cancel boundary is always interrupting, so
  drop its `cancelActivity` toggle (an "always interrupting" note, like an error boundary);
  a hand-authored transaction model round-trips through the compiler. Run the full
  `go test -race ./...`, `go vet`, `gofmt` sequence.

### Consequences

- **Positive:** Atlas gains BPMN transactions — commit / cancel-with-compensation — on the
  subprocess scope, the compensation walk, and the boundary/teardown machinery already built,
  plus one small event-derived marker. No new recovery path; the container reuses
  `TypeSubProcess` so every scope-semantics site is inherited unchanged. Closes Milestone 3.
- **Negative / trade-offs accepted:** one new column family (`cfCanceling`) and a cancel
  branch inside `subProcessBehavior.OnCompleting` (a small coupling of the transaction outcome
  into the shared subprocess completion, justified by the type reuse); a `terminateScope`
  variant that spares one element.
- **Follow-ups / risks to watch:** compensation of a **committed** transaction from an
  *outer* compensation throw (a transaction as a compensable unit) is deferred — it needs the
  ADR-0103 "compensation across scopes" follow-up, since a subprocess/transaction completing
  drops its own scope's compensables today. Nested-transaction cancellation, a cancel throw
  from an event subprocess, and BPMN **compensation data** (a handler that needs the
  compensated activity's inputs) remain open, as they do for ADR-0103.

## Pros and cons of the options

### Option 1 — `TypeSubProcess` + `IsTransaction`, cancel via compensate-then-boundary (chosen)
- Good: reuses the subprocess scope, `compensate`, the inert-boundary shape, and
  `interruptHost`; every scope-semantics site is inherited; the canceling marker is derived
  from a committed event (I6) with no new recovery path; correct compensate → then →
  continue ordering via the existing scope-drain.
- Bad: a new column family; a cancel branch in the shared subprocess completion.

### Option 2 — a distinct `TypeTransaction` type (rejected)
- Good: clean behavior dispatch and a trivial "is transaction" check.
- Bad: forces every scope-container site to recognize a second type for no behavioral gain;
  more surface, more places to drift.

### Option 3 — fire the boundary immediately, compensate in its flow (rejected)
- Good: no marker.
- Bad: `interruptHost` tears the scope down and kills the just-activated handlers;
  compensation must complete before the boundary continuation, and handlers span batches.

### Option 4 — synchronous compensation (rejected)
- Good: no deferred routing.
- Bad: handlers are activities/jobs spanning batches and external workers; they cannot run
  inline.

## Links

- builds directly on ADR-0074 (subprocess scope lifecycle — `TypeSubProcess`, `completeScope`,
  `terminateScope`, `scopeContains`), ADR-0103 (compensation — `compensate`, the
  `cfCompensable` index, reverse completion order), ADR-0040 (boundary arm/fire,
  `interruptHost`), and ADR-0089 (error events — the inert armed catch and the
  command-path structural walk this mirrors)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 3 "BPMN transactions (with cancel/compensation)" — the milestone's final
  item; completes the compensation substrate ADR-0089/0103 anticipated
