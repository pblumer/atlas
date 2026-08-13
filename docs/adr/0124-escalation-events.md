# ADR-0124: Escalation events (non-interrupting, propagating throw/catch)

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered (all five phases). An **escalation** is a named, coded signal that a process
> raises to say "something needs attention up the chain" — thrown by an **escalation intermediate
> throw event** (which raises the escalation and then **continues on its own outgoing flow**) or an
> **escalation end event** (which raises it and ends its path), and caught by the **nearest enclosing**
> matching handler: an **escalation boundary event** on the containing activity/subprocess/call
> activity, or an **escalation event subprocess** in an enclosing scope. Like an error it propagates
> **structurally up the scope chain** to the first matching catch (no subscription, no broadcast);
> **unlike** an error it may be caught **non-interruptingly** — the handler runs *alongside* the still-
> running activity — and an **uncaught escalation is not a failure**: the throw's own flow semantics
> apply (an intermediate throw continues, an end event ends its path) and the instance runs on. It is
> the direct sibling of ADR-0089 error events with two twists — a **non-interrupting** catch and a
> **benign** uncaught terminal — and reuses `propagateError`'s scope walk, the boundary/event-sub
> arm-fire lifecycle (ADR-0040/0082, including the existing **non-interrupting** fire path), and the
> scope-teardown primitives (ADR-0074). **No new subscription, value type, or recovery path.**

## Context and problem statement

An **escalation** (`<escalationEventDefinition>`) is BPMN's construct for *raising a matter to a
higher level while work proceeds*. The canonical example: an order-handling subprocess reaches a
delay threshold and **escalates to a manager** — a notification branch runs, but the order keeps
being processed. That "notify and keep going" shape is exactly what Atlas cannot express today: an
**error** aborts (always interrupting) and, uncaught, fails the instance; a **signal** broadcasts to
every subscriber regardless of scope. Escalation is the missing third throw/catch variant —
**nearest-enclosing like an error, but optionally non-interrupting and benign when uncaught.**

Concretely, escalation has two properties no existing Atlas construct combines:

1. **Non-interrupting catch.** An escalation boundary or escalation event subprocess may be
   *non-interrupting* (`cancelActivity="false"` / `isInterrupting="false"`): it fires its handler
   **without** tearing down the host activity/scope, which continues running. (An error catch is
   *always* interrupting.) This is the defining feature and the whole point of escalation.
2. **An intermediate throw that continues.** Besides an escalation **end** event, escalation has an
   **intermediate throw** event: it raises the escalation and then **proceeds on its outgoing
   sequence flow**. (Error has no intermediate throw — an error always ends its path.) The throwing
   token lives on unless an *interrupting* catch on an enclosing scope tears it down.

And one property that makes it *simpler* than an error:

3. **Uncaught is benign.** An escalation that reaches the process root with no matching catch is
   **not** an incident and does **not** fail the instance (BPMN/Camunda 8: an unhandled escalation is
   ignored). The throw's own flow semantics simply apply — the intermediate throw's token continues,
   the end event's token ends its path. There is no failure terminal to invent.

Atlas cannot express one today. `<escalationEventDefinition>` and top-level `<bpmn:escalation>` are
not parsed, and there is no compiled escalation type or boundary kind. The modeler blocks it outright
(`UNSUPPORTED_EVENT_DEFS`, `api/web/editor.js:600`), and a raw XML deploy mostly *rejects* it — an
escalation boundary, intermediate throw/catch, or event-subprocess start hits the "only … supported
yet" `default:` in each `compiler/scope_compile.go` switch. The one dangerous exception is the
**escalation end event**: the end-event branch is an `if`-ladder that checks error/compensation/cancel
and then falls through to a plain `AddEndEvent`, so an `escalationEventDefinition` on an end event is
**silently dropped to a none end** — the escalation is lost with no diagnostic. That silent-degrade is
exactly the failure mode ADR-0116 called out for terminate ends: it must become either a real
escalation throw (this ADR) or a loud rejection, never a quiet none-end. The differential/conformance
harness explicitly excludes escalation scenarios "until the engine supports
`escalationEventDefinition`" (`ROADMAP.md:152`).

The question this ADR answers: **how do we route a thrown escalation to the nearest catching handler,
supporting a non-interrupting catch and a benign uncaught terminal, without inventing anything new** —
given that error events (ADR-0089) already built the exact scope-walk, the boundary/event-sub
lifecycle already supports non-interrupting firing (ADR-0040/0082), and the intermediate-throw event
shape already exists (`TypeMessageThrowEvent`).

What already exists, and is load-bearing:

- **The scope-chain walk to the nearest catch is solved — for errors.** `propagateError`
  (`engine/behavior.go:2163`) walks up the `FlowScopeKey` chain (bounded by `maxScopeDepth = 64`,
  `engine/scope.go:8`), at each enclosing activity scanning its armed error boundaries
  (`findErrorBoundary`, `engine/behavior.go:2227`) and at each scope its error event subprocesses
  (`findErrorEventSub`, `engine/behavior.go:2251`), matching by code (`errorCodeMatches`,
  `engine/behavior.go:2273`, where a code-less catch is a catch-all), driving the first match to
  `Completing`. Escalation propagation is the **same walk** against escalation boundaries/handlers —
  it differs only in what "fire the match" does (interrupting *or* not) and in what happens at the
  root (nothing, vs. an incident).
- **Non-interrupting firing is solved.** The boundary/event-sub arm-fire lifecycle already
  distinguishes interrupting from non-interrupting: a non-interrupting boundary "just take[s] the
  outgoing flow, leaving the host running" (`engine/behavior.go:2879`), and non-interrupting
  event-subprocess triggers re-arm after firing (`engine/behavior.go:3022`). An escalation catch
  reuses this wholesale — an *interrupting* escalation catch behaves like an error catch
  (`interruptHost`, `engine/behavior.go:1221` / `terminateScope`, `:1183`); a *non-interrupting* one
  activates the handler and leaves the host running.
- **The intermediate throw event shape exists.** `TypeMessageThrowEvent` (`compiler/process.go:32`)
  is a throw event that fires an effect and then continues on its outgoing flow (the send-task
  message kind, ADR-0112). An escalation intermediate throw is the same shape with a different effect
  (raise an escalation instead of publish a message).
- **Boundaries and event-sub triggers already arm as inert catch targets.** ADR-0089's `BoundaryError`
  boundary arms **inert** — it opens no subscription/timer and waits only to be *found* by
  propagation. `BoundaryEscalation` arms the same way.
- **bpmn-js already draws escalation and the moddle is native.** The escalation markers
  (`start-event-escalation`, `intermediate-event-catch-escalation`, throw/end variants) ship in the
  vendored bpmn-font, and `bpmn:Escalation` / `bpmn:EscalationEventDefinition` are native moddle
  types (`api/web/vendor/bpmn/zeebe.json:557`). Only the `UNSUPPORTED_EVENT_DEFS` gate and an
  authoring panel are missing — no diagram-rendering or moddle change.

So the scope walk, the interrupting *and* non-interrupting fire paths, the intermediate-throw shape,
the inert-catch arming, and the diagram all already exist. What is missing is (a) the compiled
escalation metadata (an escalation boundary/handler kind and escalation throw/end event types, each
carrying an escalation code), (b) a `propagateEscalation` that reuses the error walk but honors the
non-interrupting fire and the benign root, and (c) the throw behaviors.

## Decision drivers

- **Reuse ADR-0089, don't fork it.** Escalation is error propagation with a non-interrupting fire and
  a benign uncaught terminal. Factor the shared scope-walk so escalation is a thin variant, not a
  copy — ideally `findErrorBoundary`/`findErrorEventSub`/`errorCodeMatches` generalize to "find a
  boundary/handler of kind K matching code", and `propagateEscalation` differs from `propagateError`
  only in the fire step and the root terminal.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2); a single
  `applyToState` live and on recovery (I4); the catch structure resolved at compile (I5); propagation
  a **pure function of committed scope state** and the thrown code, exactly like `propagateError` (I6).
- **Structural, not correlated.** An escalation's handler is fixed by scope nesting and escalation
  code at deploy time; propagation is a bounded walk over live scope instances, not a subscription
  scan. (The signal-events broadcast substrate, ADR-0088, is deliberately *not* reused — escalation
  is nearest-enclosing, not broadcast.)
- **Faithful BPMN / Camunda 8.** Nearest-enclosing catch wins; matching is by **escalation code**
  with a code-less catch as a catch-all; a catch may be **interrupting or non-interrupting**; an
  **uncaught escalation is ignored** (no incident, the instance runs on); an escalation unhandled in
  a called process **propagates to the caller** (ADR-0076).
- **Ship the interrupting core first.** An **escalation end event inside a subprocess**, caught by an
  **interrupting escalation boundary** on that subprocess, is the smallest runnable phase and is
  almost exactly ADR-0089 Phase 2. The non-interrupting catch and the intermediate throw — the parts
  that are genuinely new — layer on top.

## Considered options

1. **Generalize ADR-0089's structural walk to a code-and-kind catch, with an interrupting-or-not fire
   and a benign root (chosen).** Add `TypeEscalationThrowEvent` (intermediate) and
   `TypeEscalationEndEvent`, a `BoundaryEscalation` boundary kind, and an `EscalationCode` on the
   boundary/event-sub detail. Refactor the error walk's helpers to match "a boundary/handler of the
   requested kind whose code matches", and add `propagateEscalation(c, fromKey, code)` that runs the
   same walk but (a) fires an *interrupting* match via `interruptHost`/`terminateScope` and a
   *non-interrupting* match by activating the handler while leaving the host running, and (b) at the
   root simply returns (uncaught escalation is benign). The escalation end throws then ends its path;
   the intermediate throw throws then continues on its outgoing flow (unless an interrupting catch
   tore it down). No subscription, no new record, no failure terminal.
2. **Model escalation on the signal broadcast substrate (ADR-0088).** Treat an escalation like a
   scoped signal. Rejected: escalation is **nearest-enclosing**, not broadcast — a broadcast finds
   *every* matching catch regardless of scope distance, so it would still need the scope walk to pick
   the nearest, plus the subscription record/recovery path the static scope structure already implies.
   It also gets the non-interrupting/uncaught semantics wrong (a signal has no "propagate to the
   caller", no "nearest," and no benign-if-unheard-within-scope notion).
3. **A standalone `propagateEscalation` copied from `propagateError`.** Duplicate the walk verbatim
   and edit the fire/root steps. Rejected: two near-identical scope walks drift apart under future
   change (a bug fixed in one silently survives in the other); the walk is exactly the shared part,
   the fire/root are exactly the varying part, so a small generalization (option 1) is cleaner and
   safer than a fork.

## Decision outcome

Chosen: **option 1 — escalation is thrown and propagated structurally up the live scope chain to the
nearest armed escalation catch, firing interruptingly or non-interruptingly, and benign if uncaught.**
The genuinely new logic is (a) compiled escalation metadata (a `BoundaryEscalation` kind, escalation
throw/end event types, each carrying an escalation code), (b) `propagateEscalation` — the reused
scope-chain walk with a non-interrupting fire branch and a return-on-root terminal, and (c) the two
throw behaviors (end: throw then end its path; intermediate: throw then continue).

### Propagation

`propagateEscalation(c, fromElementKey uint64, code string)` — the ADR-0089 walk, two steps changed:

1. Walk up from `fromElementKey` following `FlowScopeKey` (`scopeContains`, `engine/behavior.go:1144`),
   bounded by `maxScopeDepth`.
2. At each step check the current scope's armed **escalation event subprocesses** (nearer — catches
   within the scope) and the enclosing **activity's escalation boundaries** (catches on the way out)
   for a code match (`errorCodeMatches` generalized: equal code, or a code-less catch-all), same
   ordering as errors. On the **first** match:
   - **Interrupting** catch → drive it to `Completing`: `interruptHost` tears the activity down /
     `terminateScope` tears the scope down, and the boundary/handler takes over. (Identical to an
     error catch.)
   - **Non-interrupting** catch → activate the handler **without** tearing anything down — the host
     activity/scope keeps running and the handler runs in parallel, exactly the existing
     non-interrupting boundary/event-sub fire path (`engine/behavior.go:2879`, `:3022`). The throwing
     token is untouched.
   Done in either case. **Nearest-enclosing, not fan-out:** the *first* matching catch consumes the
   escalation and the walk stops — an escalation is caught by exactly one handler (the innermost that
   matches), never re-caught by outer scopes. (This is the BPMN rule and matches errors; it is *not*
   the signal broadcast model, where every subscriber fires. A non-interrupting catch lets the
   *throwing token* continue — but the escalation itself is consumed at the first match.)
3. If the root is reached with no match: **the escalation is unhandled and benign** — `propagateEscalation`
   simply returns (no incident, unlike `propagateError`), *unless* the instance is a call-activity
   child (`ParentElementInstanceKey != 0`), in which case continue from the caller's call-activity
   element in the parent instance (ADR-0076), same as errors. The throw's own flow semantics (below)
   then carry the instance on.

The walk reads only committed element/boundary/handler state and the compiled codes — a **pure
function of committed state** (I6), reconstructed identically on recovery; it runs only live (a throw
is a command), never during replay.

### Throwing

- **Escalation end event** (`TypeEscalationEndEvent`): `OnCompleting` calls `propagateEscalation`,
  then **ends its own path** like a none end (decrement the scope, possibly `completeScope`). Because
  the catch is *nearest-enclosing above* the throw, a non-interrupting or uncaught catch leaves the
  end event's scope intact, so the end simply completes its token; an interrupting catch will already
  have torn the throwing token down via `terminateScope`, so the end's completion must be guarded
  against a scope that no longer exists (the ADR-0089 "torn down by the interrupt, never
  double-counted" concern — sharpened here because non-interrupting means the token often *does*
  survive).
- **Escalation intermediate throw event** (`TypeEscalationThrowEvent`, mirroring
  `TypeMessageThrowEvent`): `OnActivated`/`OnCompleting` calls `propagateEscalation`, then **takes its
  outgoing sequence flow** — the token continues. Again guarded: if an interrupting catch on an
  enclosing scope fired, this token was inside the terminated scope and is already gone, so "continue"
  must no-op in that case. This throw-then-maybe-continue ordering is the subtlest part of the feature
  and gets an explicit test (see risks).

There is **no worker-thrown escalation** (unlike `FailJobWithError`): escalation is a modeled
control-flow construct, not a job outcome. A worker that wants to escalate completes its job and the
model routes to an escalation throw.

### Compiler

- Add `TypeEscalationEndEvent` (mirroring `TypeErrorEndEvent`, `compiler/process.go:57`) and
  `TypeEscalationThrowEvent` (mirroring `TypeMessageThrowEvent`, `:32`) to the enum + `String()`;
  grow `numBpmnTypes` (36 → 38). Add a `BoundaryEscalation` value to `BoundaryEventKind` and an
  `EscalationCode` + `Interrupting` on `BoundaryEventDetail` / `EventSubProcessDetail` (escalation
  **honors** interrupting/non-interrupting — do **not** force interrupting the way errors do). An
  `EscalationEndDetail{EscalationCode}` / throw detail table.
- Parse `<escalationEventDefinition escalationRef="…">` (an `Escalation *xmlEscalationEventDefinition`
  pointer on the end/intermediate-throw/boundary structs and the event-sub start) and top-level
  `<bpmn:escalation id escalationCode name>`; a `buildEscalationResolver` mirroring
  `buildErrorResolver`/`buildMessageResolver` returning the **escalation code** (matching is by code,
  not id or name; an empty/absent code is a legal catch-all, a non-empty ref naming no declared
  escalation is a deploy error). Add the escalation arms to the end-event, intermediate-throw,
  boundary, and event-sub-trigger switches in `compiler/scope_compile.go`.
- Validation: the escalation boundary/event-sub `Interrupting` flag is taken **straight from**
  `cancelActivity`/`isInterrupting` (no forced-interrupting override). An **optional**
  `SeverityWarning` when an escalation throw has no statically matching enclosing escalation catch —
  but **lower value than the error one**, because an uncaught escalation is legal by design (a
  fire-and-forget notification), so this may be `SeverityInfo` or omitted. (Proposed: omit in Phase 1,
  reconsider after the runtime lands.)

### Runtime

- `escalationEndEventBehavior`: `OnActivated` like an end event; `OnCompleting` calls
  `propagateEscalation` then ends its path (guarded).
- `escalationThrowEventBehavior`: throw via `propagateEscalation`, then take the outgoing flow
  (guarded), mirroring the message-throw behavior.
- Escalation **boundary** / **event subprocess**: a `BoundaryEscalation` `case` in
  `boundaryEventBehavior.OnActivated` / `eventSubProcessStartBehavior.OnActivated` that opens
  **nothing** (inert, waiting to be found); firing is via `propagateEscalation`, then the existing
  interrupting (`interruptHost`/`terminateScope`) **or** non-interrupting (activate-and-continue) fire
  path per the compiled `Interrupting` flag.
- Uncaught → **nothing** (return). No `AppendIncidentEvent`. A call-activity child's unhandled
  escalation continues from the caller.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** Parse `<escalationEventDefinition>` + `<bpmn:escalation>`; the resolver;
  `TypeEscalationEndEvent` + `TypeEscalationThrowEvent` + details; `BoundaryEscalation` kind +
  `EscalationCode` + `Interrupting` (honored, not forced). *Tests:* a parse test asserting an
  escalation end/throw's code, an escalation boundary's code and that a `cancelActivity="false"`
  boundary is recorded **non-interrupting** (the key divergence from errors); a deploy accepting a
  subprocess with an escalation end + escalation boundary; an unknown `escalationRef` is a deploy
  error in every position.
- **Phase 2 — Interrupting throw + catch core.** Generalize the error walk helpers; add
  `propagateEscalation` (interrupting fire + benign root); `escalationEndEventBehavior`; the inert
  `BoundaryEscalation` boundary. *Tests:* `start → subProcess{start → escalationEnd} → …` with an
  **interrupting** escalation boundary on the subprocess routing to a recovery path (the subprocess
  aborts, the boundary fires, recovery runs) — the ADR-0089 Phase-2 shape; a **nested** escalation
  propagating past an inner subprocess to the outer boundary; an **uncaught** escalation end that
  simply completes its scope and lets the instance finish normally (**no incident** — the divergence
  from errors); a **recovery test** (throw after crash+replay finds the same boundary).
- **Phase 3 — Non-interrupting + intermediate throw + event subprocess.** The genuinely new
  semantics. A **non-interrupting** escalation boundary fires its handler while the host activity/
  subprocess **keeps running**; the **escalation intermediate throw** raises the escalation and then
  **continues on its outgoing flow**; an **escalation event subprocess** (interrupting and
  non-interrupting) catches a scope escalation (reusing ADR-0082). *Tests:* a non-interrupting
  boundary handler runs to completion **and** the host job later completes normally (both paths
  observed); an intermediate throw whose escalation is caught non-interruptingly, with the throwing
  token continuing past the throw; a non-interrupting escalation event subprocess re-arming; recovery.
- **Phase 4 — Code matching + call-activity propagation.** Code-specific vs catch-all matching (the
  nearest *matching* code wins, a catch-all catches any); an escalation unhandled in a call-activity
  child propagates to the caller's escalation boundary (ADR-0076); the interrupting-vs-non-interrupting
  choice preserved across the child→caller hop. *Tests:* two boundaries with different codes route
  differently and a catch-all catches an unmatched code; a child-process escalation caught
  (non-interruptingly) by the caller's boundary while the caller continues; an uncaught child
  escalation leaving both child and caller to finish normally; recovery across the hop.
- **Phase 5 — Modeler.** The Implement panel authors an escalation's code (an escalation-code field,
  reusing the error-code shape) on escalation throw/end events and escalation boundaries/event
  subprocesses; a central "Escalations" manager mirroring the "Errors" manager creates/edits/deletes
  `bpmn:Escalation` root elements. **Unlike errors, the escalation boundary/event-sub keeps the
  interrupting toggle** (non-interrupting is a first-class choice). Drop
  `bpmn:EscalationEventDefinition` from `UNSUPPORTED_EVENT_DEFS` (`api/web/editor.js:600`). bpmn-js
  already draws the markers and offers the throw/end/boundary/start variants via the wrench menu, and
  the moddle types are native — no diagram or moddle change.

### Consequences

- **Positive:** the engine gains "raise a matter and (optionally) keep working" — the non-interrupting
  handler that neither a signal nor an error can express — on the ADR-0089 scope walk and the
  ADR-0040/0082 non-interrupting fire path, both already built and tested. No subscription, value type,
  or recovery path; propagation is a pure function of committed scope state. It unblocks the escalation
  scenarios the conformance/differential harness currently excludes (`ROADMAP.md:152`) and completes
  the throw/catch event family (message, error, signal, escalation).
- **Negative / trade-offs accepted:** a second scope-walk *caller* (mitigated by generalizing the
  error helpers rather than forking them — see risks if the generalization proves awkward); two new
  compiled event types and a boundary kind; the throw-then-continue ordering of the intermediate throw
  is a genuinely new control-flow shape that needs careful teardown guarding.
- **Follow-ups / risks to watch:** (1) **The throw-then-continue guard.** An intermediate throw whose
  escalation is caught *interruptingly* on an enclosing scope has its own token torn down by
  `terminateScope`; "then take the outgoing flow" must no-op in that case and must not double-count —
  the single most important test. (2) **Non-interrupting completion accounting.** A non-interrupting
  handler adds a token to the scope's active-child count; the scope must not complete until *both* the
  host path and the handler path drain — verify the count is incremented on the non-interrupting fire
  and that `completeScope` waits. (3) **The generalization of the error helpers.** If
  `findErrorBoundary`/`errorCodeMatches` don't generalize cleanly to a kind parameter, fall back to
  thin escalation-specific finders that share `errorCodeMatches` only — decide during Phase 2 and
  record it. (4) **Same-scope tie-break** (a boundary and an event subprocess both match at one scope)
  follows the ADR-0089 rule (event subprocess nearer); pin it with a test. (5) **Escalation vs. a
  simultaneously-firing timer/message boundary** needs an explicit ordering test, as errors do.

## Pros and cons of the options

### Option 1 — generalize the error walk; interrupting-or-not fire; benign root (chosen)
- Good: reuses the ADR-0089 scope walk, the ADR-0040/0082 non-interrupting fire path, the
  intermediate-throw shape, and the inert-catch arming; the catch structure is static (I5) and
  propagation a pure function of committed state (I6); no subscription, value type, or recovery path;
  the caller link handles cross-process escalation; no failure terminal to invent.
- Bad: a small refactor of the error helpers to admit a kind parameter; the throw-then-continue guard
  is a new control-flow subtlety; two new event types plus a boundary kind.

### Option 2 — escalation on the signal broadcast substrate (rejected)
- Good: reuses the subscription plumbing.
- Bad: escalation is nearest-enclosing, not broadcast — a scan finds all matches regardless of scope
  distance, so the scope walk is still needed to pick the nearest, plus a subscription record and
  recovery path for information the static scope structure already implies; the caller-propagation and
  benign-uncaught semantics don't fit the broadcast model.

### Option 3 — a standalone `propagateEscalation` copied from `propagateError` (rejected)
- Good: no refactor of the error path; escalation changes can't regress errors.
- Bad: two near-identical scope walks drift apart under future change; the walk is exactly the shared
  part and the fire/root exactly the varying part, so a small generalization is cleaner and less
  bug-prone than a fork.

## Links

- direct sibling of and builds on **ADR-0089** (error events — the scope walk `propagateError`,
  `findErrorBoundary`/`findErrorEventSub`/`errorCodeMatches`, the inert armed catch, the compiled
  error metadata this ADR mirrors); escalation is that design with a non-interrupting fire and a
  benign uncaught terminal
- builds on **ADR-0074** (subprocess scope lifecycle — `FlowScopeKey`, `scopeContains`,
  `terminateScope`), **ADR-0040** (boundary arm/fire, `interruptHost`, **and the non-interrupting fire
  path**), **ADR-0082** (event subprocesses — an escalation event subprocess, interrupting and
  non-interrupting), and **ADR-0076** (call-activity child→caller link — cross-process escalation)
- reuses the intermediate-throw event shape from **ADR-0112** (`TypeMessageThrowEvent`)
- deliberately **not** built on **ADR-0088** (signal events) — escalation is nearest-enclosing, not
  broadcast; the two are opposite delivery models
- honors I1, I2, I4, I5, I6 and **ADR-0018** (test-first, recovery tests up front)
- ROADMAP Milestone 2; completes the throw/catch event family and unblocks the conformance/differential
  escalation scenarios currently excluded (`ROADMAP.md:152`)
