# ADR-0103: Compensation and compensation handlers

- **Status:** Proposed
- **Date:** 2026-08-07
- **Deciders:** Atlas engine team

> **Implementation status.** Not started. This ADR plans the work; each phase lands
> test-first with a recovery test (ADR-0018). Compensation builds on the subprocess scope
> lifecycle (ADR-0074), the boundary machinery (ADR-0040), the throw/end event precedent
> (ADR-0052/0088/0089), and the structural scope-walk of error propagation (ADR-0089). It
> introduces one genuinely new durable record — a per-scope index of completed
> **compensable** activities in completion order — and parses BPMN `<association>` links,
> but no new recovery path: the throw is a command-path side effect, and the index is
> derived purely from the `Completed` event in `applyToState`.

## Context and problem statement

**Compensation** undoes the effect of an activity that already **completed successfully**,
when a later step decides the work must be rolled back — "the payment went through and the
reservation was made, but the order was cancelled, so refund the payment and release the
reservation." It is not error handling (ADR-0089 catches a *failure* before an activity
finishes); compensation runs the **compensation handler** of an activity that *succeeded*.

In BPMN a compensable activity carries a **compensation boundary event**
(`<boundaryEvent><compensateEventDefinition/>`) linked by a BPMN `<association>` to a
**compensation handler** — an activity marked `isForCompensation="true"` that sits **off the
normal sequence flow** (no incoming/outgoing flow drives it; it runs only when compensation
is triggered). Compensation is **thrown** by an intermediate **compensation throw event** or
a **compensation end event** (`<compensateEventDefinition activityRef="…"?>`): with an
`activityRef` it compensates that one activity; without one it compensates **every**
completed compensable activity **in the current scope, in reverse completion order**.

Atlas cannot express one today. `<compensateEventDefinition>` is unparsed; `<association>`
(the boundary→handler link) is unparsed (only `dataInput/OutputAssociation` exist);
`isForCompensation` appears nowhere in the engine; and — the load-bearing gap — **there is
no record of which activities completed, or in what order**: a completed element instance is
deleted in `applyToState` (`engine/apply.go`), `ElementInstanceValue` has no completion
timestamp, and the history indices (`RecordElementStep`/`RecordElementReplay`) record
activation/replay order with **no scope key**, so "the completed compensable activities of
scope X, newest first" cannot be answered from existing state.

The question this ADR answers: **how do we run the handlers of already-completed activities,
in reverse completion order, scoped correctly, and deterministically on replay** — reusing
the boundary/scope machinery and the error-style command-path scope walk, while adding the
minimum new durable state (a completed-compensable index) and honoring the invariants.

What already exists, and is load-bearing:

- **The throw is the error-throw shape.** A compensation throw is `errorEndEventBehavior` /
  `propagateError`'s structural twin (`engine/behavior.go`): a command-path side effect that
  finds targets in committed scope state and drives them with `AppendElementCommand(…
  Completing)` — no new recovery path. `signalEndEventBehavior` / `messageEndEventBehavior`
  are the precedent for an *end* event that does a side effect then ends its scope.
- **The boundary carries the host→handler link.** A `<boundaryEvent>` already resolves its
  host via `attachedToRef` and compiles into `BoundaryEventDetail` with a
  `BoundaryEventKind` (`compiler/process.go`); a `BoundaryCompensation` kind can carry the
  handler node id. Unlike every other kind it is **inert**: it is never armed as an element
  instance (`armBoundaryEvents` skips it) — compensation availability *begins* at host
  completion, the exact moment `disarmBoundaryEvents` would otherwise retire a boundary.
- **Scope confinement is solved.** `FlowScopeKey`, `scopeContains`, `completeScope`, and
  `ForEachElementInstance` filtered by scope (`engine/behavior.go`) are how error
  propagation and event subprocesses confine work to a scope; compensation confines the
  same way.
- **The new-element-type seam is uniform.** A `BpmnType` + a detail table + a behavior +
  one registration line — the signal/error precedent (ADR-0088/0089) this session followed.
- **The Modeler already authors it.** Stock bpmn-js (vendored) offers compensation
  boundary/throw/end and the `isForCompensation` marker in its replace menu — no Modeler
  work is needed to draw compensation models.

What is missing: `<compensateEventDefinition>` + `<association>` + `isForCompensation`
parsing, a `BoundaryCompensation` kind and compensation throw/end types, a **durable
completed-compensable index**, and the throw behavior that walks it.

## Decision drivers

- **Reuse, don't reinvent.** Build the throw on the error-throw command-path walk, the
  host→handler link on the boundary detail, and scope confinement on `FlowScopeKey` —
  add only the one record existing state cannot supply.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2);
  a single `applyToState` live and on recovery (I4); the handler links and compensable
  marking resolved at compile (I5); **deterministic replay** — the completed-compensable
  index and the reverse order are a pure function of the committed `Completed` events, keyed
  by a position-derived sequence, never wall-clock or map iteration (I6).
- **Faithful BPMN.** Compensation runs the handlers of **successfully completed** activities,
  in **reverse completion order**, confined to the throw's scope (or a single `activityRef`);
  a compensation boundary is inert and its handler is off the normal flow; a handler runs
  once per completed instance and is then consumed.
- **Ship the runnable core first.** A single-activity compensation (`activityRef` → run that
  activity's handler) is the first runnable phase; reverse-order broadcast, nested scopes,
  and the end event layer on.

## Considered options

1. **A durable per-scope completed-compensable index, walked by a command-path throw
   (chosen).** When a **compensable** activity (one bearing a compensation boundary)
   reaches `IntentCompleted`, `applyToState` writes a record into a new `cfCompensable`
   family keyed `scopeKey : seq` (a per-scope sequence incremented in `applyToState`, so a
   reverse prefix scan yields reverse completion order), carrying the completed activity's
   element id, its compensation-handler node id, and the instance key. A compensation throw
   `compensate(c, scopeKey, activityRef)` scans that scope's records (all, or filtered to
   `activityRef`), newest first, and for each **activates the handler node** in the scope
   and consumes the record. Records are dropped when their scope completes or is terminated.
   The throw is a command-path side effect (like `propagateError`); the index is derived
   purely from the `Completed` event, so recovery rebuilds it identically — no new recovery
   path.
2. **Retain completed compensable element instances instead of deleting them.** Mark a
   compensable activity "completed-retained" rather than deleting it in `applyToState`, and
   scan retained instances on a throw. Rejected: it forks the element lifecycle (every
   completion path must special-case retention), bloats the live element-instance index with
   tombstones, and still needs a completion-order key — more invasive than one purpose-built
   index for the same information.
3. **Resolve compensation entirely at compile time (throw → handler edges).** Rejected: which
   *instances* completed, in what order, and how many times (a multi-instance body, a
   subprocess run twice) is inherently runtime; the compiler can resolve *which handler* a
   host has, but not *what to compensate* — the live index is unavoidable.

## Decision outcome

Chosen: **option 1 — a durable per-scope completed-compensable index, walked by a
command-path compensation throw.** The genuinely new logic is (a) parsing
`<compensateEventDefinition>`, `<association>`, and `isForCompensation`; (b) a
`BoundaryCompensation` kind that records the host→handler link and is never armed; (c) the
`cfCompensable` index written on a compensable activity's completion and dropped with its
scope; and (d) `compensate`, the throw behavior that walks the index in reverse completion
order and activates each handler.

### Compiler

- Parse `<compensateEventDefinition activityRef="…"?>` (a `Compensation *xmlCompensate…` on
  the intermediate-throw and end structs) and `<boundaryEvent><compensateEventDefinition/>`.
  Parse top-level/process `<association sourceRef targetRef>` (a new `xmlAssociation`) — the
  boundary→handler link bpmn-js emits. Parse `isForCompensation="true"` on activities.
- Add `TypeCompensationThrowEvent` and `TypeCompensationEndEvent` to the `BpmnType` enum +
  `String()`, grow `numBpmnTypes`, with a `CompensationDetail{ActivityRef int32 (interned),
  -1 = whole scope}` table. Add a `BoundaryCompensation` value to `BoundaryEventKind` with a
  `CompensationHandler int32` (handler node id) on `BoundaryEventDetail`; resolve it from the
  association (boundary id ↔ handler id) at compile. Mark a host activity **compensable**
  (a bool/handler ref on its `CompiledNode`) when it has a compensation boundary, so the
  runtime knows on completion whether to write an index record.
- Exclude a `BoundaryCompensation` boundary from `armBoundaryEvents`/`disarmBoundaryEvents`
  (it is inert metadata). Teach `checkReachability` (`compiler/validation.go`) to treat
  `isForCompensation` handler activities as reachable roots (like event-sub containers), so a
  handler off the normal flow does not raise a spurious dead-code warning. Validate: a
  compensation boundary must link to exactly one `isForCompensation` handler; an `activityRef`
  must name a compensable activity in scope (else a `SeverityWarning`, since the target may be
  in a called process — mirroring ADR-0089's uncaught warning).

### Runtime

- **Recording.** On `IntentCompleted` of a compensable activity, `applyToState` writes a
  `CompensableValue{ScopeKey, ElementId, HandlerNode, ElementInstanceKey, Seq}` into
  `cfCompensable`, keyed `scopeKey : seq` (a per-scope counter kept in committed state and
  incremented here — deterministic on replay, I6). Dropped when the scope completes
  (`completeScope`) or is terminated (`terminateScope`), so it never leaks.
- **Throwing.** `compensationThrowEventBehavior.OnActivated` calls `compensate(c,
  scopeKey, activityRef)` then hops to `Completing` (take outgoing flow);
  `compensationEndEventBehavior` calls it then ends the scope (the message/signal-end shape).
  `compensate` scans the scope's `cfCompensable` records newest-first (reverse completion
  order), skips those whose element id ≠ `activityRef` when one is given, and for each
  activates its handler node in the scope (an ordinary activity activation that runs to
  completion with no outgoing flow, like an event-sub handler) and consumes the record. It
  reads only committed state and the compiled links — a pure function of committed state
  (I6) — and runs only on the command path.
- Handlers run in the enclosing scope against the instance's current variables (no
  per-activity data snapshot — a follow-up if selective compensation data is wanted). A
  handler is counted in its scope's active-child count so the throw's scope completion waits
  for the handlers it started.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<compensateEventDefinition>`, `<association>`,
  `isForCompensation`; the `BoundaryCompensation` kind + host→handler resolution;
  `TypeCompensationThrowEvent`/`EndEvent` + `CompensationDetail`; compensable marking;
  reachability treats handlers as roots. *Tests:* a compensable activity + boundary + handler
  + throw compile with the resolved handler link; the handler does not raise a reachability
  warning; a boundary with no association, or an `activityRef` to a non-compensable activity,
  is caught. No runtime yet.
- **Phase 2 — Record + compensate one.** The `cfCompensable` family + tx methods; recording
  on a compensable activity's completion; `compensationThrowEventBehavior` handling a throw
  with an `activityRef` (run that one activity's handler). *Tests:* `start → A(compensable) →
  throw(activityRef=A) → end` runs A's handler after A completed; a **recovery test** — the
  index rebuilds so a throw after restart still compensates.
- **Phase 3 — Broadcast + reverse order + end event.** A throw with no `activityRef`
  compensates every completed compensable activity in the scope in reverse completion order
  (sequential: the next handler activates when the previous completes, so order is
  observable); the compensation **end** event. *Tests:* two compensable activities compensate
  in reverse order; the compensation end event compensates then ends; recovery.
- **Phase 4 — Scopes + cleanup.** Compensation confined to a subprocess scope; the index
  dropped when a scope completes or is cancelled (no leak, no cross-scope compensation); a
  compensable activity that runs twice (loop / multi-instance) records twice and compensates
  each. *Tests:* a subprocess-scoped throw compensates only its scope; a completed scope's
  records are gone; recovery.
- **Phase 5 — Modeler + polish.** bpmn-js already authors compensation; add only the
  Implement-panel touch the throw needs (the optional `activityRef` selector) and verify a
  hand-drawn compensation model round-trips through the compiler. Update the `<association>`
  export path if the panel must create the boundary→handler link.

### Consequences

- **Positive:** Atlas gains rollback-of-completed-work — the substrate for BPMN transactions
  (ADR-forthcoming) — on the boundary/scope/throw machinery already built, plus one
  purpose-built index. No new recovery path; the throw is a pure command-path walk; the index
  is derived from committed `Completed` events. Closes Milestone 3's compensation item.
- **Negative / trade-offs accepted:** one new durable family (`cfCompensable`) and its
  per-scope sequence counter; `<association>` parsing (new, but small and standard); a
  boundary kind that is compile-time-only metadata (an asymmetry with armed boundaries).
- **Follow-ups / risks to watch:** per-activity compensation **data** (a handler that needs
  the compensated activity's inputs) is deferred — handlers run against instance variables.
  Compensation **across a call activity** (compensating a completed child process) and
  compensation triggered from an **event subprocess** need explicit ordering tests. The
  reverse-order execution is **sequential**; a concurrent variant is a later option. A
  compensation throw for an `activityRef` that completed **multiple** times (a loop) — all
  instances, newest first — is covered by Phase 4's multi-run test.

## Pros and cons of the options

### Option 1 — durable per-scope completed-compensable index (chosen)
- Good: reuses the error-throw command-path walk, the boundary host→handler link, and scope
  confinement; one purpose-built index derived from committed state (I6); no new recovery
  path; reverse order from a position-derived key.
- Bad: a new column family + per-scope sequence; `<association>` parsing; an inert boundary
  kind.

### Option 2 — retain completed compensable element instances (rejected)
- Good: no separate family.
- Bad: forks the element lifecycle with retention/tombstones, bloats the live index, and
  still needs a completion-order key — more invasive for the same information.

### Option 3 — resolve compensation at compile time (rejected)
- Good: no runtime index.
- Bad: which instances completed, in what order, and how many times is inherently runtime;
  the compiler can give *which handler*, not *what to compensate*.

## Links

- builds on ADR-0074 (subprocess scope lifecycle — `FlowScopeKey`, `scopeContains`,
  `completeScope`), ADR-0040 (boundary events — the host→handler link), ADR-0052/0088/0089
  (throw/end event behaviors — the command-path side-effect-then-end shape), and reuses the
  structural scope walk of ADR-0089 (error propagation)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 3 "Compensation and compensation handlers"; the substrate for the
  following "BPMN transactions (with cancel/compensation)" item
