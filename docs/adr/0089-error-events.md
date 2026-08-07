# ADR-0089: Error events (scoped propagation to the nearest handler)

- **Status:** Accepted
- **Date:** 2026-07-30
- **Deciders:** Atlas engine team

> **Implementation status.** All phases (1–5) delivered. Each phase
> lands test-first with a recovery test (ADR-0018). Error events build on the subprocess scope
> lifecycle and `terminateScope`/`scopeContains` (ADR-0074), the boundary arm/fire
> machinery (ADR-0040), event subprocesses (ADR-0082), the incident model (ADR-0061), and
> the call-activity child→caller link (ADR-0076). They introduce no subscription, timer,
> value type, or recovery path — an error is thrown synchronously and propagated
> **structurally** up the scope chain, resolved from committed state.
>
> **Delivered (Phase 1, compiler):** `<errorEventDefinition errorRef="…">` (an `Error`
> pointer on the end/boundary structs and the event-sub start) and top-level `<bpmn:error id
> errorCode name>` (`buildErrorResolver`, mirroring `buildMessageResolver`, returning the
> **error code** — matching is by code, not id or name; an empty/absent code is legal, a
> non-empty ref naming no declared error is a deploy error) parse into a new
> `TypeErrorEndEvent` (mirroring `TypeMessageEndEvent`, with an `ErrorEndDetail{ErrorCode}`
> table) and a `BoundaryError` `BoundaryEventKind` with an `ErrorCode` on
> `BoundaryEventDetail` / `EventSubProcessDetail` (so an error boundary and an error event
> subprocess reuse the boundary/event-sub detail). An error boundary and an error event
> subprocess are **forced interrupting** regardless of the XML `cancelActivity` /
> `isInterrupting` attribute. A `checkErrorHandling` validation step raises a
> `SeverityWarning` (never a deploy error) for an error end whose code no enclosing error
> boundary or error event subprocess in the same process statically catches — the
> compile-time shadow of the runtime `propagateError` walk (a matching code, or a code-less
> catch-all). Verified: an error end's code and an error boundary's code + forced-
> interrupting; a code-less catch-all; an error event subprocess trigger + forced-
> interrupting; an unknown `errorRef` is a deploy error in every position (end, boundary,
> event-sub start); the uncaught warning fires for an unhandled throw and stays silent when
> a matching-code, catch-all, boundary, or event-subprocess handler is in scope. No runtime
> yet — `propagateError`, the error end / boundary behaviors, and `FailJobWithError` are
> Phases 2–3.
>
> **Delivered (Phase 2, throw + catch core):** the runnable error-end → error-boundary path.
> `propagateError(c, fromKey, code)` walks up the live `FlowScopeKey` chain (bounded by
> `maxScopeDepth`); at each enclosing activity it scans that activity's armed error
> boundaries (`findErrorBoundary`, the `AttachedToKey` lookup `interruptHost` uses) for a
> code match (`errorCodeMatches`: equal code, or a code-less catch-all) and drives the first
> match to `Completing` — the boundary is always interrupting, so the existing
> `interruptHost` tears the activity and the scope below the throw down and the boundary
> takes its recovery flow. It reads only committed state and the compiled codes (a pure
> function of committed state, I6) and runs only on the command path. `errorEndEventBehavior`
> hops to `Completing` like a none end, then throws via `propagateError` instead of completing
> its scope (so the throwing element is torn down by the interrupt, never double-counted).
> The `BoundaryError` boundary arms **inert** — a `case` in `boundaryEventBehavior.OnActivated`
> that opens no subscription/timer, waiting only to be *found*. Reaching the process root with
> no match raises an incident on the throwing element and parks — the ADR-0061 terminal,
> pulled forward from Phase 3 because it is `propagateError`'s natural uncaught branch (its
> dedicated worker-error and event-subprocess siblings remain in Phase 3). Verified: an error
> end inside a subprocess caught by an error boundary on that subprocess routes to recovery
> (the normal flow is not taken); a nested error propagates *past* an inner subprocess whose
> boundary catches a different code to the outer boundary that matches; an uncaught error
> raises an incident and parks; and an armed error boundary rebuilds on recovery so a throw
> after crash+replay still finds it. Worker errors (`FailJobWithError`), error event
> subprocesses, and call-activity caller propagation remain for Phases 3–4.
>
> **Delivered (Phase 3, worker errors + error event subprocess):** the two remaining throw
> sources and catch targets. A `ThrowJobError(jobKey, code)` processor command (the "throw
> BPMN error" verb, sibling of `FailJob`) rides a command-only `IntentJobErrorThrown`; its
> handler cancels the job and calls `propagateError` from the job's element — so a service
> task with an error boundary catches a worker-thrown error (the code rides in the command's
> transient `incident.Message`, never persisted). `propagateError` now also checks each
> scope's armed **error event subprocesses** (`findErrorEventSub`, a `TypeEventSubProcessStart`
> scoped by the level, reusing ADR-0082): at each scope an error event subprocess is checked
> *before* a boundary on that scope's activity (nearer — it catches within the scope, the
> boundary catches on the way out), and the process root's event subprocesses are checked
> after the element-scope walk (they key off the instance scope, not an element). A found
> event-sub trigger is driven to `Completing` by the shared `fireErrorCatch`, running the
> existing interrupting `eventSubProcessStartBehavior` path (`terminateScope` + activate the
> handler); the `BoundaryError` trigger arms **inert** like the boundary. Verified: a worker
> error caught by the task's boundary (job canceled, recovery flow taken); a worker error
> with no handler raising an incident; an error event subprocess (in a subprocess, and at the
> root) handling a scope error and running its handler; and the worker-error path surviving
> crash+replay. Call-activity caller propagation and the code-matching / same-scope tie-break
> tests remain for Phase 4.
>
> **Delivered (Phase 4, call-activity propagation + code matching):** cross-process error
> propagation. `propagateError` is split into a per-instance scope walk (`errorCaughtInInstance`)
> and an outer caller-hop loop: when an error reaches an instance root uncaught and the instance
> is a call-activity child (`ProcessInstanceValue.ParentElementInstanceKey != 0`), the child is
> terminated (the error aborts it) and propagation continues from the caller's call-activity
> element in the parent instance (ADR-0076); a top-level instance raises the incident. So an
> error boundary on a call activity catches a child's unhandled error, and the caller takes its
> recovery flow. Code matching (`errorCodeMatches`, delivered in Phase 2) is now exercised
> directly: the nearest *matching* code wins and a code-less boundary is a catch-all. Verified:
> two boundaries with different codes on one subprocess route a thrown code to the matching
> one; a catch-all boundary catches a coded error; a child error caught by the caller's error
> boundary (child aborted, caller recovered); a child error no caller catches raising an
> incident at the caller with the child torn down; and the child→caller propagation surviving
> crash+replay. The same-scope boundary-vs-event-subprocess tie-break stays as the ADR chose
> (event subprocess nearer); only the Modeler (Phase 5) remains.
>
> **Delivered (Phase 5, Modeler):** error authoring in the editor's Implement panel
> (`api/web/editor.js`), mirroring the message/signal pattern but keyed on the error **code**.
> An `errorFieldsHTML` picker (a dropdown of the model's shared `<bpmn:error>` declarations
> plus "＋ New error", and the chosen error's `errorCode`) appears on error end events, error
> boundaries, and error event-subprocess start events — driven by an `errorDefOf` dispatch arm
> in each. Because an error catch is always interrupting, the error boundary and error
> event-subprocess arms drop the interrupting/`cancelActivity` toggle and show a fixed
> "always interrupting" note. A central "Errors" manager on the process/collaboration root
> adds, edits (code), and deletes errors (`errorsManagerHTML`/`wireErrorsManager`); helpers
> `listErrors`/`createError`/`linkError`/`deleteError` create `bpmn:Error` root elements and
> set `errorRef` (deleting clears dangling refs), producing exactly the `<bpmn:error id
> errorCode>` + `<errorEventDefinition errorRef>` shape the Phase-1 compiler parses. bpmn-js
> already draws the error marker and offers the error end/boundary/start variants via the
> wrench menu, and the `bpmn:Error` / `bpmn:ErrorEventDefinition` moddle types are native, so
> no diagram-rendering or moddle change was needed.

## Context and problem statement

An **error** is a named, coded failure that a process handles. It is **thrown** — by an
**error end event** (which ends its enclosing subprocess/process abnormally) or by a
**worker** that fails a job with an error code (Zeebe's "throw BPMN error") — and
**caught** by the **nearest enclosing** matching handler: an **error boundary event** on
the containing activity/subprocess/call activity, or an **error event subprocess** in an
enclosing scope. Unlike a message or signal, an error is **not** broadcast and uses **no
subscription**: it propagates **up the scope chain** from the throw point to the *first*
matching catch, terminating the work below it, and if nothing catches it the instance
fails with an incident. It is the construct that turns a failure into control flow —
"if the payment errors, cancel the reservation and refund" — and the substrate a future
compensation/BPMN-transaction story reuses.

Atlas cannot express one today. `<errorEventDefinition>` and top-level `<bpmn:error>` are
not parsed — flow-event structs carry only `Message *`/`Timer *` pointers
(`compiler/parse.go:854-917`), `xmlDefinitions` declares only `Messages`
(`compiler/parse.go:645`), and an error event falls into the `default:` "only … supported
yet" branches (`compiler/scope_compile.go:384,389,505`). A worker can already **fail** a
job to an incident (ADR-0061), but not fail it *to an error code* that a boundary catches.
There is no compiled error type, no propagation, and no runtime behavior.

The question this ADR answers: **how do we route a thrown error to the nearest catching
handler without inventing a subscription/correlation subsystem** — reusing the scope
chain, `terminateScope`, and the boundary/event-subprocess arm-fire lifecycle, since an
error's catch structure is **static** (resolved by scope nesting, not runtime correlation).

What already exists, and is load-bearing:

- **The scope chain is the propagation path.** `ElementInstanceValue.FlowScopeKey`
  (`model/value.go:29`) links an element to its enclosing scope; `scopeContains`
  (`engine/behavior.go:877`) and `ResolveVariable`/`resolveInChain` (`engine/scope.go:19,41`)
  already walk it upward, bounded by `maxScopeDepth = 64`. An error walks the *same* chain
  from the throw point, testing each enclosing activity/scope for a matching error catch.
- **Tearing down the caught scope is solved.** `interruptHost` (`engine/behavior.go:944`)
  cancels a host activity (its job, its inner scope via `terminateScope`, its boundary
  siblings) and lets the firing boundary take its flow; `terminateScope`
  (`engine/behavior.go:916`) tears down every element in a scope. An error boundary catching
  reuses `interruptHost`; an error event subprocess reuses `terminateScope` + the handler.
- **Boundaries and event-sub triggers already arm as catch targets.** `armBoundaryEvents`
  (`engine/behavior.go:840`) and `armEventSubprocesses` (`engine/behavior.go:1978`) activate
  a waiting element instance per boundary/handler; `boundaryEventBehavior` /
  `eventSubProcessStartBehavior` (`engine/behavior.go:1874,2041`) fire it to `Completing`.
  An error catch arms the same way but opens **no** subscription/timer — it waits purely to
  be *found* by propagation and driven to `Completing`.
- **A worker can already fail a job.** `handleJobFailed` (ADR-0061) sets retries and raises
  an incident when they run out. "Fail with an error code" is a sibling command that, instead
  of an incident, propagates an error from the job's element.
- **Unhandled → incident is a solved terminal.** ADR-0061's incident model is exactly what an
  error that reaches the root uncaught becomes.
- **A child→caller link exists.** A call activity records its caller
  (`ParentElementInstanceKey`, ADR-0076); an error unhandled in a child instance propagates to
  the caller's call-activity element — the intra-instance walk continued across the boundary.

So the chain walk, scope teardown, boundary/handler arming and firing, the
incident terminal, and the caller link already accommodate an error. What is missing is
(a) the compiled error metadata (an error boundary/handler kind and an error end event),
(b) a `propagateError` that walks the scope chain to the nearest matching catch, and (c)
a "fail job to an error code" command.

## Decision drivers

- **Reuse, don't reinvent.** Build on the scope chain (ADR-0074), `interruptHost`/
  `terminateScope`, the boundary/event-sub arm-fire lifecycle (ADR-0040/0082), and the
  incident terminal (ADR-0061). An error catch is a boundary/handler that waits to be *found*,
  not correlated.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2);
  a single `applyToState` live and on recovery (I4); the catch structure resolved at compile
  (I5); deterministic replay — propagation is a **pure function of committed scope state** and
  the thrown code, like `completeScope` (I6).
- **Structural, not correlated.** An error's handler is fixed by scope nesting and error code
  at deploy time; propagation is a bounded walk over live scope instances, not a subscription
  scan.
- **Faithful BPMN.** Nearest-enclosing catch wins; an error boundary is **always
  interrupting**; matching is by **error code**, with a code-less catch as a catch-all; an
  uncaught error fails the instance (an incident); an error unhandled in a called process
  propagates to the caller.
- **Ship the runnable core first.** An **error end event inside a subprocess**, caught by an
  **error boundary on that subprocess**, is the first runnable phase — "the handler subprocess
  hit an error end; abort it and take the boundary's recovery flow." Worker errors, event
  subprocesses, the incident terminal, and call-activity propagation layer on.

## Considered options

1. **Structural propagation up the scope chain to an armed, subscription-less catch
   (chosen).** An error boundary / error event-subprocess arms as a waiting element instance
   (reusing `armBoundaryEvents`/`armEventSubprocesses`) but opens no subscription/timer — it
   is inert until *found*. On a throw (error end event, or a job failed to an error code),
   `propagateError(c, fromScope, code)` walks up the `FlowScopeKey` chain; at each enclosing
   activity it checks that activity's armed error boundaries for a code match, and at each
   scope it checks the scope's armed error event-subprocess triggers; the **first** match is
   driven to `Completing` (which `interruptHost`s the activity / `terminateScope`s the scope
   and runs the handler). Reaching the root uncaught raises an incident; reaching a
   call-activity child's root propagates to the caller. No subscription, no new record.
2. **Model errors as scope-keyed subscriptions like messages/signals.** Open an
   "error subscription" per boundary/handler keyed by code and "correlate" on throw. Rejected:
   an error is *nearest-enclosing*, not broadcast or 1:1-by-key — a subscription scan finds
   *all* matching catches regardless of scope distance, so it would still need the scope-chain
   walk to pick the nearest, plus a subscription record that adds a write/recovery path for
   information already implied by the static scope structure.
3. **Resolve the catch entirely at compile time (bake a throw→handler edge).** For each error
   throw, compute its catching node at deploy and jump straight to it at runtime. Rejected:
   the *handler element instance* to fire and the *scope instances* to terminate are runtime
   (which live subprocess instance, which armed boundary); and a worker error's throw point is
   a running activity whose enclosing live scopes must be walked anyway. Compile-time resolves
   *which node type* catches, but the live walk is unavoidable — option 1 does the static part
   at deploy (the boundary/handler exists and its code) and the instance part at runtime.

## Decision outcome

Chosen: **option 1 — an error is thrown and propagated structurally up the live scope
chain to the nearest armed, subscription-less error catch.** The genuinely new logic is
(a) compiled error metadata (a `BoundaryError` kind, an error end event type, an error
event-sub kind, each carrying an error code), (b) `propagateError` — the scope-chain walk
to the nearest match, (c) an error catch behavior that arms inertly and fires when found,
and (d) a "fail job to an error code" command that calls `propagateError` from the job's
element.

### Propagation

`propagateError(c, fromElementKey uint64, code string)`:

1. Walk up from `fromElementKey` following `FlowScopeKey` (the `scopeContains` traversal,
   `engine/behavior.go:877`), bounded by `maxScopeDepth`.
2. At each step, the current scope is either an **activity element instance** (a subprocess,
   call activity, or — for a worker error — the service task itself) or the **process root**:
   - Check the activity's armed **error boundaries** (its boundary instances whose compiled
     `BoundaryEventKind == BoundaryError`) for a **code match** (equal code, or a code-less
     catch-all). On a match: drive that boundary instance to `Completing` — `interruptHost`
     tears the activity down and the boundary takes its recovery flow. Done.
   - Check the scope's armed **error event-subprocess** triggers for a code match. On a match:
     drive that trigger to `Completing` — it `terminateScope`s the scope and runs the handler.
     Done.
3. If the root is reached with no match: **the error is unhandled** → raise an incident on the
   throwing element (ADR-0061) and park, *unless* the instance is a call-activity child
   (`ParentElementInstanceKey != 0`), in which case continue `propagateError` from the caller's
   call-activity element in the parent instance (ADR-0076).

The walk reads only `GetElementInstance`/`BoundaryEvents`/`EventSubprocesses` and the
compiled codes — a **pure function of committed state**, so recovery reconstructs the same
decision (I6); it runs only live (a throw is a command), never during replay.

### Throwing

- **Error end event** (`TypeErrorEndEvent`): its `OnCompleting` calls
  `propagateError(c, key, code)` instead of ending the instance. It does **not** complete
  normally — the propagation terminates its scope. (An error end reaching the process root
  uncaught fails the instance via the incident terminal.)
- **Worker error**: a new `FailJobWithError(jobKey, code)` command (a sibling of the ADR-0061
  `FailJob`) whose handler, instead of decrementing retries, calls
  `propagateError(c, job.ElementInstanceKey, code)` — so a service task with an error boundary
  catches a worker-thrown error. The job is consumed (canceled), the boundary fires.

### Compiler

- Add `TypeErrorEndEvent` (mirroring `TypeMessageEndEvent`, `compiler/process.go:38`) to the
  enum + `String()`; grow `numBpmnTypes`. Add a `BoundaryError` value to `BoundaryEventKind`
  (`compiler/process.go:422`) and an `ErrorCode` field to `BoundaryEventDetail` /
  `EventSubProcessDetail` (a code-less catch is a catch-all). An `ErrorEndDetail{ErrorCode}` table.
- Parse `<errorEventDefinition errorRef="…">` (an `Error *xmlErrorEventDefinition` pointer on
  the end/boundary structs and the event-sub start) and top-level `<bpmn:error id
  errorCode name>`; a `buildErrorResolver` mirroring `buildMessageResolver`
  (`compiler/parse.go:234`) returning the **error code** (matching is by code, not id). Add
  `case e.Error != nil:` to the end-event switch, `case ev.Error != nil:` to the boundary
  switch, and the error arm to the event-sub trigger switch (`compiler/scope_compile.go`).
- Validation: an error boundary is **forced interrupting** (override the `CancelActivity !=
  "false"` derivation for the error case, `compiler/scope_compile.go:483`). Optionally a
  **`SeverityWarning`** (`checkErrorHandling`, a new `Validate` step) when an error end/throw
  has no statically matching enclosing error boundary or event subprocess in the same process
  — a warning, not a deploy error, because the catch may live at the call-activity caller
  (ADR-0076) which one process can't see; the runtime incident is the real terminal.

### Runtime

- `errorEndEventBehavior`: `OnActivated` runs like an end event; `OnCompleting` calls
  `propagateError`.
- Error **boundary** / **event subprocess**: a `BoundaryError` `case` in
  `boundaryEventBehavior.OnActivated` / `eventSubProcessStartBehavior.OnActivated` that opens
  **nothing** (the catch is inert, waiting to be found); firing is via `propagateError`
  driving it to `Completing`, then the existing `interruptHost` / `terminateScope` + handler
  paths. An error boundary is always interrupting.
- `FailJobWithError` command + handler calling `propagateError`.
- Unhandled → `AppendIncidentEvent` (ADR-0061) on the throwing element; a call-activity child's
  unhandled error continues from the caller.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<errorEventDefinition>` + `<bpmn:error>`; the resolver;
  `TypeErrorEndEvent` + `ErrorEndDetail`; `BoundaryError` kind + `ErrorCode`; the forced-
  interrupting rule; the optional uncaught warning. *Tests:* a parse test asserting the error
  end's code and an error boundary's code + forced-interrupting; a deploy that accepts a
  subprocess with an error end + error boundary; the uncaught warning.
- **Phase 2 — Throw + catch core.** `propagateError`; `errorEndEventBehavior`; the inert
  `BoundaryError` boundary. *Tests:* `start → subProcess{start → errorEnd} → …` with an error
  boundary on the subprocess routing to a recovery path: the error end aborts the subprocess,
  the boundary fires, the recovery flow runs; a **nested** error propagates past an inner
  subprocess with no matching boundary to the outer one that has it; a **recovery test** —
  throw after crash+replay finds the same boundary.
- **Phase 3 — Worker errors + event subprocess + unhandled.** `FailJobWithError` → an error
  boundary on a service task; an error **event subprocess** catches a scope error (reusing
  ADR-0082); an error that reaches the root uncaught raises an incident and parks. *Tests:* a
  worker fails a job to a code caught by the task's boundary; an error event subprocess handles
  a subprocess error; an uncaught error raises an incident; recovery.
- **Phase 4 — Code matching + call-activity propagation + nesting.** Code-specific vs catch-all
  matching (the nearest *matching* code wins, a catch-all catches any); an error unhandled in a
  call-activity child propagates to the caller's call-activity error boundary (ADR-0076); an
  error boundary on a call activity. *Tests:* two boundaries with different codes route
  differently and a catch-all catches an unmatched code; a child-process error caught by the
  caller's boundary; recovery across the child→caller propagation.
- **Phase 5 — Modeler.** The Implement panel authors an error's code (an error-code field,
  reusing the message-name shape) on error end events and error boundaries/event subprocesses;
  bpmn-js already draws the error marker and the end/boundary variants via the wrench menu, and
  an error boundary is always interrupting (no cancelActivity toggle).

### Consequences

- **Positive:** the engine gains failure-as-control-flow — error handlers, scoped abort, and
  worker-thrown errors — on the scope-chain and teardown machinery ADR-0074/0040/0082 already
  built, plus the ADR-0061 incident terminal. No subscription, value type, or recovery path;
  propagation is a pure function of committed scope state. It is the substrate a future
  compensation / BPMN-transaction story reuses, and it closes the error-triggered event
  subprocess ADR-0082 deferred.
- **Negative / trade-offs accepted:** a scope-chain walk on each throw (bounded by
  `maxScopeDepth`, the same bound variable resolution and `scopeContains` already use); a new
  `FailJobWithError` command beside `FailJob`; an error catch that arms as an element instance
  yet opens no subscription (an inert waiter — a small asymmetry with message/timer boundaries).
- **Follow-ups / risks to watch:** the exact ordering when an error boundary and an error event
  subprocess both match at the same scope (BPMN: the boundary on the *inner* activity is nearer;
  define the tie precisely with a test). Error propagation *through* a multi-instance body (an
  error in one iteration) and interaction with a simultaneously-firing timer/message boundary
  need explicit ordering tests. Signal-vs-error is deliberately opposite (broadcast vs nearest);
  a future `bpmn:escalation` (non-interrupting, propagating) is a third variant deferred with
  compensation.

## Pros and cons of the options

### Option 1 — structural propagation to an inert armed catch (chosen)
- Good: reuses the scope chain, `interruptHost`/`terminateScope`, and the boundary/event-sub
  arm-fire lifecycle; the catch structure is static (I5) and propagation a pure function of
  committed state (I6); no subscription, value type, or recovery path; the caller link handles
  cross-process errors.
- Bad: a per-throw scope walk; a new fail-to-error command; an inert armed catch (opens no
  subscription), a small asymmetry with message boundaries.

### Option 2 — error subscriptions like messages/signals (rejected)
- Good: reuses the subscription plumbing.
- Bad: an error is nearest-enclosing, not broadcast/1:1 — a subscription scan finds all matches
  regardless of scope distance, so the scope walk is still needed to pick the nearest, plus a
  record and a recovery path for information the static scope structure already implies.

### Option 3 — bake the throw→handler edge at compile time (rejected)
- Good: no runtime walk.
- Bad: the handler *instance* to fire and the scope *instances* to terminate are runtime; a
  worker error's throw point is a live activity whose enclosing scopes must be walked anyway.
  Compile-time gives *which type* catches; the live walk is unavoidable.

## Links

- builds directly on ADR-0074 (subprocess scope lifecycle — `FlowScopeKey`, `scopeContains`,
  `terminateScope`), ADR-0040 (boundary arm/fire, `interruptHost`), ADR-0082 (event
  subprocesses — an error event subprocess; closes its deferred error-trigger), ADR-0061
  (incident model — the uncaught-error terminal and the `FailJob` sibling), and ADR-0076
  (call-activity child→caller link — cross-process error propagation)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 2 "Error events and error propagation"; the substrate for a future
  compensation / BPMN-transaction story (Milestone 3)
- sibling to ADR-0088 (signal events) — the other outstanding Milestone-2 event type, with the
  opposite delivery model (broadcast to all, not nearest-enclosing to one)
