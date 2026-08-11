# ADR-0114: Terminate end events

- **Status:** Accepted
- **Date:** 2026-08-11
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. A `<endEvent><terminateEventDefinition/></endEvent>` ends its
> **enclosing flow scope** at once — every other live token in that scope is terminated (its jobs
> cancelled), then the scope completes. At the process root that ends the instance; inside an
> embedded subprocess it ends that subprocess and the parent continues on the subprocess's outgoing
> flow. It reuses `terminateScopeExcept` and `completeScope` **wholesale** — it is
> `cancelEndEventBehavior` (ADR-0108) minus compensation and minus the cancel boundary. One new
> element type and a two-method behavior; **no new subscription, value type, or recovery path**.

## Context and problem statement

A **terminate end event** (`<endEvent><terminateEventDefinition/></endEvent>`) is the BPMN "abort"
end: reaching it ends the enclosing process (or subprocess) **immediately**, consuming every other
running token — not just the one that arrived, the way a plain end event does. It is the last
unimplemented standard end-event type (none, message, signal, error, and cancel ends all run), and a
common one: "if this branch decides to stop, kill everything else and end."

Atlas cannot express one today. A `<terminateEventDefinition>` is **parsed** but **rejected** at
deploy: `xmlEndEvent` collects `Terminate *xmlTerminateEventDefinition` (`compiler/parse.go`), and
`registerScope` fails any model containing one with "…which Atlas can't execute yet — a terminate
end would abort the whole instance; remove it or use a plain end event"
(`compiler/scope_compile.go:570`). The rejection is deliberately loud because the tempting stopgap —
silently dropping the `<terminateEventDefinition>` and deploying a plain end — is **wrong**: a plain
end completes only the one arriving token, so the modeled abort never happens and other branches keep
running. The Modeler mirrors the gap: `bpmn:TerminateEventDefinition` is in `UNSUPPORTED_EVENT_DEFS`
("Terminate end events can't run yet", `api/web/editor.js`).

The question this ADR answers: **what exactly does a terminate end event terminate, and how do we
run it without a new teardown mechanism** — given that Atlas already tears scopes down for cancel
end events (ADR-0108), interrupting boundaries, and instance cancellation, all through the same
primitives.

What already exists, and is load-bearing:

- **Scoped teardown is solved.** `terminateScopeExcept(c, procKey, scopeKey, exceptKey)`
  (`engine/behavior.go:1152`) terminates every live element instance inside a scope **recursively**
  — cancelling each job, emitting a `Terminated` event per victim (which `apply.go` applies and
  recovery replays) — while sparing one instance. `terminateScope` is the same with nothing spared.
  These already power interrupting boundaries and cancel ends.
- **Scope completion is solved and already root/subprocess-aware.** `completeScope(c, scope)`
  (`engine/behavior.go:999`) fires once a scope's active-child count hits zero: for a **subprocess**
  scope it disarms event-subprocess triggers and drives the container to `Completing` (so the
  subprocess takes its outgoing flow); for the **process-instance root** it completes the instance
  and resumes a call-activity caller if there is one (ADR-0076). A terminate end needs *exactly*
  this dual behavior and gets it for free.
- **The structural precedent is `cancelEndEventBehavior`.** A cancel end (ADR-0108,
  `engine/behavior.go:2227`) already does "terminate the enclosing scope's other tokens sparing
  myself → complete the scope": `terminateScopeExcept(…, key)`, then on completing
  `completeScope(FlowScopeKey)`. A terminate end is that behavior **with two things removed** — no
  `compensate()` (a terminate runs no compensation or event handling, by spec) and no cancel-boundary
  routing (a terminate completes its scope normally, not out a cancel boundary).
- **Recovery is inherited.** A terminate emits only `Terminated` (per victim) and the scope's normal
  `Completed`/completion events — all already durable and replayed. Termination is a pure function of
  committed scope state; nothing new is logged.

So the teardown, the job cancellation, the root-vs-subprocess completion, and the recovery already
accommodate a terminate end. What is missing is the compiled type, the (small) behavior, and dropping
the two rejections.

## Decision drivers

- **Reuse, don't reinvent.** A terminate end *is* a cancel end without compensation — build it on the
  same `terminateScopeExcept` + `completeScope` path, not a parallel teardown.
- **Faithful BPMN.** A terminate ends the **enclosing flow scope**, matching Camunda/Zeebe: at the
  root the instance; inside a subprocess that subprocess, after which the parent continues. It runs
  **no compensation and no event handling** (BPMN 13.4.6).
- **Invariants hold.** `applyToState` stays pure — the behavior decides on the command path and emits
  `Terminated`/`Completed` events; apply only applies them (I4). No per-command hot-path allocation
  beyond the existing victim scan (I1); durable-before-visible and single-apply inherited (I2/I4).
- **No silent wrong-doing.** The current loud rejection exists because dropping the terminate would
  change behavior invisibly; running it correctly removes the need for that stopgap.

## Considered options

1. **A `TypeTerminateEndEvent` whose behavior is `terminateScopeExcept` + `completeScope` over its
   enclosing scope (chosen).** Parse the already-collected `Terminate` marker into a
   `TypeTerminateEndEvent` node (no detail); its `terminateEndEventBehavior` terminates the enclosing
   scope's other tokens on activation and completes the scope on completing — `cancelEndEventBehavior`
   minus compensation and minus the cancel boundary. Scoped semantics: root → instance, subprocess →
   subprocess-then-parent, both via the existing `completeScope`.
2. **Terminate always ends the whole instance, even inside a subprocess.** Rejected: simpler mental
   model and it matches the old rejection message's wording, but it is **BPMN-incorrect** — a
   terminate in a subprocess must be scoped to that subprocess (the parent continues), which is the
   pattern that makes terminate useful inside event subprocesses and parallel branches. It would also
   need to bypass the scope machinery (always target the root) rather than reuse it.
3. **Compile a terminate end to a plain end (the current stopgap, made silent).** Rejected outright:
   a plain end completes only the arriving token, so parallel branches keep running — the modeled
   abort never happens. This is precisely the wrong behavior the loud rejection guards against today.

## Decision outcome

Chosen: **option 1 — a `TypeTerminateEndEvent` that terminates its enclosing flow scope and completes
it, reusing `terminateScopeExcept` and `completeScope`.** The genuinely new logic is (a) a compiled
`TypeTerminateEndEvent` (no detail) and its parse wiring, (b) a two-method `terminateEndEventBehavior`,
and (c) dropping the compiler rejection and the Modeler's `UNSUPPORTED_EVENT_DEFS` entry.

### Compiler

- In `registerScope`, an end event whose `Terminate != nil` registers `b.AddTerminateEndEvent()` →
  `TypeTerminateEndEvent` (a detail-less node, like `AddEndEvent`), **before** the plain/message/
  signal/error/cancel end handling; drop the `<terminateEventDefinition>` rejection
  (`compiler/scope_compile.go:570`). Add `TypeTerminateEndEvent` to the `BpmnType` enum + `String()`
  and grow `numBpmnTypes` (`compiler/process.go`). No detail table (a terminate carries no data).
- No new validation is required: a terminate end is legal anywhere a plain end is (root or any
  subprocess scope); its effect is defined by whichever scope it lands in.

### Runtime

- `terminateEndEventBehavior`:
  - `OnActivated`: `terminateScopeExcept(c, ei.ProcessInstanceKey, ei.FlowScopeKey, key)` — terminate
    every other live token in the enclosing scope (recursively, cancelling their jobs), sparing this
    event; then append `IntentCompleting`.
  - `OnCompleting`: append `IntentCompleted` (decrements the now-drained scope's last child), then
    `completeScope(c, ei.FlowScopeKey)` — which completes the subprocess (outgoing flow, parent
    continues) or the instance root (instance ends, caller resumed), exactly as for a cancel end but
    with **no** compensation and **no** cancel-boundary hop.
  - Register `p.behaviors[compiler.TypeTerminateEndEvent] = terminateEndEventBehavior{}`.
- Multi-instance iterations, nested subprocesses, and armed event-subprocess/boundary triggers inside
  the terminated scope are all torn down by the existing `terminateScopeExcept`/`completeScope` walk —
  no special-casing. Compensation is deliberately **not** triggered (unlike a cancel end).
- **One latent inconsistency surfaced and fixed.** A terminate reached in the *same batch* as a
  parallel sibling's activation (the common `fork → terminate` shape) could not see the sibling: the
  scope-teardown scan `ForEachElementInstance` read **committed** state, while every other teardown
  read (`GetElementInstance`, `GetJob`, `ActiveChildren`, `scopeContains`) reads the in-flight
  transaction — so the not-yet-committed sibling was invisible and survived. `ForEachElementInstance`
  now reads the tx too, aligning it with the rest; the full race suite (cancel, interrupting
  boundary, event-gateway loser cancellation, compensation — the other callers of the shared scan)
  stays green, confirming the alignment is a strict consistency fix, not a behavior change for them.
  A cancel end never hit this because a transaction's tokens are always committed from prior batches.

### Modeler

- Drop `"bpmn:TerminateEventDefinition"` from `UNSUPPORTED_EVENT_DEFS` (`api/web/editor.js`) so the
  badge and Problems-bar warning clear. bpmn-js already draws a terminate end (the filled-circle
  marker) and offers it in the replace menu; a terminate carries no configuration, so no Implement
  panel is needed.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** `TypeTerminateEndEvent` enum + `String()` + `numBpmnTypes`;
  `AddTerminateEndEvent`; `registerScope` registering it and dropping the rejection. *Tests:* an end
  event with a `<terminateEventDefinition>` compiles to `TypeTerminateEndEvent` (replacing the
  current "rejected" test); a plain/message end is unaffected.
- **Phase 2 — Runtime.** `terminateEndEventBehavior` + dispatch. *Tests:* a terminate end on one
  branch of a parallel split **ends the instance**, cancelling the other branch's waiting job/token
  (element/instance counts drain to zero; the job is `Canceled`); a terminate **inside an embedded
  subprocess** ends only that subprocess and the parent **continues on the subprocess's outgoing
  flow**; a terminate in a scope with a **multi-instance** activity ends all its iterations; a
  terminate does **not** run a compensation handler that a cancel would; and a **recovery test** —
  replaying the log rebuilds the identical terminated/completed history (I4). Confirm no new
  per-command allocation (I1).
- **Phase 3 — Modeler + docs.** Drop the `UNSUPPORTED_EVENT_DEFS` entry; a round-trip check (a
  terminate end deploys). Update this ADR to **Accepted/Delivered**, `docs/adr/README.md`, and the
  ROADMAP end-event note. Full sequence green: `go test -race ./...`, `go vet ./...`, `gofmt -l .`,
  and `./scripts/check-coverage.sh 95`.

### Consequences

- **Positive:** Atlas runs the last standard BPMN end-event type — the "abort" end — on the teardown
  and completion machinery it already has. One element type and a two-method behavior; no new
  subscription, value type, or recovery path. The loud compile rejection (and its footgun stopgap)
  goes away. Scoped semantics make terminate useful both as a whole-instance abort and as an
  "end this subprocess branch" inside event subprocesses and parallel flows.
- **Negative / trade-offs accepted:** a terminate end is a **hard** stop — it runs no compensation
  and no event handling (BPMN-correct, but a modeler wanting cleanup on abort must use a cancel end /
  a transaction instead; the ADR documents the distinction). The behavior duplicates the cancel end's
  two-method shape over its own (compensation-free) path rather than sharing a struct — the small
  cost of keeping "abort" and "cancel-with-compensation" explicitly separate.
- **Follow-ups / risks to watch:** a terminate inside a **call activity's** child process ends the
  child and resumes the caller through the existing `completeScope`→`resumeCaller` path (ADR-0076) —
  covered by the root-scope case, but worth an explicit test; **terminate on a token-simulation**
  (Play mode, ADR-0030) should mirror the engine's scoped teardown if/when simulation grows to it.

## Pros and cons of the options

### Option 1 — `TypeTerminateEndEvent` reusing `terminateScopeExcept` + `completeScope` (chosen)
- Good: reuses the scope teardown, job cancellation, root/subprocess completion, and recovery; BPMN-
  faithful scoped semantics; `cancelEndEventBehavior` minus two lines; removes the rejection footgun.
- Bad: a two-method behavior that mirrors the cancel end rather than sharing a struct.

### Option 2 — always terminate the whole instance (rejected)
- Good: one simple mental model ("terminate = kill everything").
- Bad: BPMN-incorrect inside a subprocess; loses the scoped "end this branch" pattern; bypasses the
  scope machinery instead of reusing it.

### Option 3 — compile terminate to a plain end (rejected)
- Good: zero new code.
- Bad: silently wrong — parallel branches keep running, the abort never happens; exactly the behavior
  today's loud rejection prevents.

## Links

- reuses ADR-0108 (cancel end events / transactions — the `terminateScopeExcept` + `completeScope`
  precedent this follows, minus compensation) and ADR-0074 (embedded subprocess scopes — the scoped
  semantics), ADR-0076 (call activities — a terminate in a child resumes the caller), ADR-0040
  (boundary/interrupt teardown — the same `Terminated` machinery)
- relates to ADR-0102/0112 (receive/send tasks — the same "distinct element type, reuse the existing
  machinery, no new recovery path" design), and ADR-0089 (error events — the other structural,
  scope-tearing end)
- honors I1, I2, I4 and ADR-0018 (test-first, recovery test up front)
- ROADMAP Milestone 2 end-event set; the last unimplemented standard end-event type
