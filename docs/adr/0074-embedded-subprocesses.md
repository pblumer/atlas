# ADR-0074: Embedded subprocesses (scope lifecycle via child counters)

- **Status:** Proposed
- **Date:** 2026-07-28
- **Deciders:** Atlas engine team

> **Implementation status.** Phase 1 delivered (compiler); Phases 2–4 pending. The
> work is phased, each phase test-first with a recovery test (ADR-0018): compiler
> parse + `FlowScope` → runtime scope lifecycle (the happy path that makes a plain
> embedded subprocess run) → termination/interruption and boundary events on the
> subprocess → nesting depth, empty-scope, and subprocess-level variables/I/O
> mappings. Event subprocesses, multi-instance activities, and call activities are
> **out of scope** here — they are separate ROADMAP Milestone-3 items that build
> on this one.
>
> **Delivered (Phase 1):** the `TypeSubProcess` element type; recursive parsing of
> `<subProcess>` (a shared `xmlFlowContent` embedded in both the process and each
> subprocess; `registerScope`/`connectScope` compile the root and every nested
> scope); a Builder scope cursor (`PushScope`/`PopScope`) so a subprocess's children
> carry it as their `FlowScope`; root-scoped process-entry start-event collection
> (a start nested in a subprocess is that scope's entry, not the process's); the
> now-load-bearing cross-scope sequence-flow rejection in `checkScopes`; reachability
> that enters a subprocess through its own start; and `TypeSubProcess` counted as an
> activity (a valid boundary-event host). **Runtime boundary:** a subprocess model
> now compiles and deploys, but no `subProcessBehavior` is registered yet, so an
> instance must not be *run* until Phase 2 — the token would reach a nil behavior.

## Context and problem statement

An embedded `<subProcess>` groups a fragment of a process — its own start
event(s), inner flow nodes, and end event(s) — inside a container node that
itself sits in the parent flow. A token entering the subprocess spawns a nested
execution; the subprocess node completes and its outgoing flow is taken only when
that nested execution has run to its end. This is the substrate for the rest of
Milestone 3 (event subprocesses, boundary events on activities-with-scope,
multi-instance, compensation) and the one structural BPMN construct Atlas cannot
execute today.

Concretely, the engine is **flat**: every flow node compiles at the process root,
`CompiledNode.FlowScope == -1` for all of them (`compiler/builder.go:179`), and
`<subProcess>` is not parsed at all — it is absent from the `xmlProcess` struct
(`compiler/parse.go:946-977`), so Go's `encoding/xml` silently drops the container
and its children, and the parent flow into it fails deploy with
`unknown targetRef` (`compiler/parse.go:640`). There is no `TypeSubProcess` BPMN
type (`compiler/process.go:18-42`, `numBpmnTypes = 21`) and no runtime behavior.

The question this ADR answers: **how do we execute an embedded subprocess without
inventing a parallel token/scope/recovery subsystem** — reusing the scope
machinery ADR-0068 (activity-local variable scopes) already put in place.

What already exists, and is load-bearing for the answer:

- **Scopes are keys, not structs.** `ElementInstanceValue.FlowScopeKey uint64`
  already means "parent scope (subprocess instance), 0 = root"
  (`model/value.go:29`). Scope-chain variable resolution walks it
  (`engine/scope.go:19-49`, `resolveInChain`/`ResolveVariable`).
- **A per-scope child counter already exists and was written for this.**
  `cfActiveChildren` keys `activeChildren:<scopeKey> → int32` (`state/keys.go:20`);
  its doc says verbatim: *"Each scope (a process instance **or a subprocess
  instance**) tracks how many child element instances are active. A scope
  completes when its counter hits zero."* (`state/tx.go:390-408`). `applyToState`
  already increments it on `VTElementInstance/Activated` and decrements on
  `Completed`/`Terminated`, keyed by `FlowScopeKey`
  (`engine/apply.go:46,76`) — for *any* scope, not just the root.
- **The counter is recovery-safe by construction.** It is a composing Pebble
  merge counter (`state/merge.go:12-24`), so replay rebuilds it deterministically
  (I6) with no special-casing.
- **A scope-owner dispatch predicate already exists.**
  `processInstanceKeyOfScope` returns the owning PI for an activity-local scope,
  or the scope itself when it is the root (`engine/behavior.go:932-937`) — exactly
  the "is this scope a subprocess element instance or the process root?" test the
  runtime needs.

So the scope model, the counter, `applyToState`, key generation, and the recovery
path already accommodate a subprocess scope. What is missing is the element type,
a behavior that *enters* the scope, and the fix that makes an inner end event
complete *its* scope rather than the whole instance.

## Decision drivers

- **Reuse, don't reinvent.** ADR-0068 explicitly built activity-local scopes as
  "the same machinery a subprocess scope needs later" and states it "unblocks a
  future embedded-subprocess ADR." Building on it keeps one scope model, one
  counter, one `applyToState`, one recovery path.
- **Invariants hold.** No per-command allocation on the hot path (I1); durable
  before visible (I2); a single `applyToState` used live and on recovery (I4);
  structure resolved at compile time (I5); deterministic replay with frozen keys
  and composing counters (I6).
- **Recovery correctness is the hard part.** The scope-completion decision runs on
  the behavior/command path (not persisted, not replayed). It must be a *pure
  function of replayed state* so recovery reconstructs the identical decision.
- **Faithful BPMN.** A token enters via the subprocess's none-start event; the
  subprocess completes when its nested execution reaches its end event(s); an
  interrupting boundary on the subprocess terminates everything inside it.
- **Ship the runnable core first.** The plain expanded embedded subprocess (one
  none-start, inner nodes, none-end, no boundary) is Phase 1 — it makes the
  modeled "Arbeit-01" scenario deploy and run. Everything else layers on.

## Considered options

1. **Reuse the activity-local scope substrate (chosen).** Model the subprocess as
   an `ElementInstanceValue` that is *itself a scope*: its element-instance key is
   the `FlowScopeKey` its inner nodes carry and the scope key its child counter and
   any local variables are keyed under. No new `ValueType`, no new record, no new
   recovery path.
2. **Flatten the subprocess at compile time.** Rewrite `<subProcess>` into plain
   root-level nodes, splicing the inner start/end into the parent flow. Rejected:
   it discards the scope boundary, so boundary events on the subprocess,
   subprocess-local variables, scoped interruption, and (later) multi-instance
   cannot be expressed; and two inner end events would wrongly complete the whole
   instance. It "works" only for the most trivial case and is a dead end for the
   rest of Milestone 3.
3. **A dedicated subprocess-instance subsystem.** New `VTSubprocessInstance`
   value type, its own counter, its own apply/recovery handling. Rejected:
   duplicates the `activeChildren` counter and the element-instance lifecycle
   ADR-0068 already built, adds a second recovery path (against the spirit of I4),
   and buys nothing the element-instance-as-scope model doesn't already give.

## Decision outcome

Chosen: **option 1 — a subprocess is an element instance that is its own scope,
reusing `FlowScopeKey`, the `activeChildren` counter, `applyToState`, and the
existing recovery path.** The only genuinely new logic is (a) a behavior that
seeds the inner start when the subprocess activates, and (b) generalising the
end-event "scope reached zero" branch to complete the *owning scope* rather than
always the process instance.

### The scope lifecycle

1. **Enter.** A token reaches the subprocess node; it is `Activating → Activated`
   like any element. On `Activated`, `applyToState` increments the **parent**
   scope's `activeChildren` (the subprocess is a child of its parent) — existing
   code, `apply.go:46`.
2. **Seed the inner start.** `subProcessBehavior.OnActivated` seeds the
   subprocess's none-start event(s) as element instances with
   `FlowScopeKey = <subprocess element-instance key>`, using the same
   `activateElement` primitive that `handleProcessInstanceActivating` uses to seed
   process start events (`behavior.go:112-124`, `activateElement` `:659-677`).
   Each inner activation increments the **subprocess's** counter.
3. **Run.** Inner nodes execute exactly as at the root — the token machinery is
   scope-agnostic; `takeOutgoingFlows` copies `FlowScopeKey` from source to target
   (`behavior.go:671`), so a token stays in the subprocess scope as it moves.
4. **Complete.** When an inner end event completes and the subprocess's
   `activeChildren` hits zero, the subprocess element instance is completed
   (`IntentCompleted` → decrements the parent scope) and its outgoing flows are
   taken. Only when the **root** scope empties does the process instance complete.

### The one load-bearing runtime change

Today `endEventBehavior.OnCompleting` (and `messageEndEventBehavior`) do:

```go
c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements ei.FlowScopeKey's counter
if c.ActiveChildren(ei.FlowScopeKey) == 0 {
    // hard-wired: complete the *process instance*
    c.AppendProcessInstanceEvent(ei.ProcessInstanceKey, model.IntentCompleted, *pi)
}
```

The decrement is already scope-correct (`ei.FlowScopeKey`, not `piKey`). Only the
completion *action* is instance-hardwired. Generalise it to dispatch on the scope
owner, a **pure function of state** (so recovery reconstructs it identically):

- If `GetElementInstance(ei.FlowScopeKey) == nil` → the scope is the process-instance
  root → complete the process instance (today's behavior).
- Else → the scope is a subprocess element instance → drive *that* element to
  `Completing` (complete it and take its outgoing flows).

This is the same present/absent test `processInstanceKeyOfScope` already uses
(`behavior.go:932-937`).

### Compiler

- Add `TypeSubProcess` to the `BpmnType` enum, its `String()` case, and grow
  `numBpmnTypes` (`compiler/process.go:18-45`).
- Add a `flowScope int32` cursor to `Builder` (default -1); `addNode` writes the
  cursor instead of the `-1` literal (`builder.go:179`); add `PushScope`/`PopScope`
  and an `AddSubProcess` that creates the container node first (so it has an
  ElementId) and returns it.
- Add `SubProcesses []xmlSubProcess` to `xmlProcess` and define `xmlSubProcess`
  with the same element slices recursively (including nested `SubProcesses` and its
  own `sequenceFlow`s). `compileProcess` recurses: register the subprocess node in
  the flat `ids` map, `PushScope(id)`, enumerate/register its children and inner
  flows into the *same* map and Builder, `PopScope`. Nesting is expressed only via
  `FlowScope`; the node/flow arrays stay flat, so linearization
  (`Builder.Build`) needs no change.
- Validation activates for free: `checkScopes`'s cross-scope flow rule
  (`validation.go:264-270`) fires the moment two connected nodes have different
  `FlowScope`. Add `TypeSubProcess` to `isActivity` (`validation.go:299-307`) so a
  boundary event may legally attach to a subprocess (Phase 2), and extend
  reachability (`checkReachability`) to treat entering the subprocess's own start
  as a reachable traversal.

### Termination and interruption (Phase 2)

Terminating a subprocess must terminate every token *inside its scope*, each
`IntentTerminated` decrementing the subprocess counter — the scoped analog of
`handleProcessInstanceTerminating`, which terminates every element instance of the
whole instance (`behavior.go:133-145`). Extend the `interruptHost` primitive
(`behavior.go:717-749`, today: one host + its boundary siblings) with a
"terminate all element instances whose scope chain leads to scope X" scan over
`ForEachElementInstance` (`context.go:104-116`), filtering by `FlowScopeKey` /
walking the chain. This unlocks interrupting boundary events on a subprocess and
is a prerequisite for event subprocesses later.

### Phased implementation plan (test-first)

- **Phase 1 — Compile a subprocess.** Parse `<subProcess>`, assign non-root
  `FlowScope`, activate cross-scope validation. *Tests:* parse test asserting inner
  nodes carry the subprocess `FlowScope`; a deploy-rejection test for a flow that
  illegally crosses the scope boundary. Deploy accepts the model; no runtime yet.
- **Phase 2 — Run the happy path.** `subProcessBehavior` seeds the inner start;
  generalise the end-event completion dispatch; the subprocess completes and takes
  its outgoing flow when its counter empties; the instance completes only when the
  root empties. *Tests:* a `start → subProcess{start → script → end} → end` model
  runs to completion (every element visited once, zero parked tokens); a
  **recovery test** — process to a token parked inside the subprocess, replay,
  assert scope instance + child counter + variables match live. **This phase makes
  the "Arbeit-01" scenario run.**
- **Phase 3 — Terminate/interrupt.** Scope-recursive termination; interrupting and
  non-interrupting boundary events on the subprocess; `isActivity` includes
  `TypeSubProcess`. *Tests:* interrupting timer boundary cancels all inner tokens
  and takes the boundary flow; recovery test across the interrupt.
- **Phase 4 — Depth and data.** Nested subprocesses (subprocess in subprocess);
  the empty-subprocess edge case (a subprocess whose start flows straight to its
  end, or with no content, completes immediately); subprocess-level variables and
  I/O mappings on the subprocess node (reuse ADR-0068 directly). *Tests:* two-level
  nesting recovery test; empty-subprocess completes in one batch; a variable
  written in the subprocess scope resolves for inner nodes and is dropped on scope
  exit.

### Modeler

The modeler is stock bpmn-js and already lets a user draw an expanded embedded
subprocess (`api/web/editor.js`); it is the server-side compiler that rejects it
today. Once Phase 1 lands, deploy accepts it with no UI change. A plain embedded
subprocess needs no Implement-panel fields; subprocess-level I/O mappings (Phase 4)
would reuse the existing ADR-0068 io-mapping editor. The client-side Problems
panel is a separate, unstarted item (ADR-0026) and out of scope here.

### Consequences

- **Positive:** the engine gains real scope nesting on top of the machinery
  ADR-0068 already built — no new value type, no new record, no second recovery
  path, and the child counter (written anticipating exactly this) folds correctly
  live and on replay. Phase 2 is a small, localized change (one behavior + one
  generalized completion branch) that makes the modeled scenario run. It is the
  foundation the rest of Milestone 3 needs.
- **Negative / trade-offs accepted:** end-event completion becomes a dispatch on
  scope owner rather than an unconditional instance-complete — a behavior change to
  a hot, well-tested path that must stay a pure function of state. Termination gains
  a scope-scan cost (bounded by the instance's live element count, the same surface
  parallel/inclusive joins already scan). Scope depth is bounded defensively at
  `maxScopeDepth = 64` (`scope.go:8`).
- **Follow-ups / risks to watch:** the scope-completion decision is not persisted,
  so any state it reads must be fully rebuilt by replay before it can run again —
  keep it a pure function of `GetElementInstance`/`ActiveChildren`. Guard the
  empty-subprocess and "inner start completes within the same batch as entry" cases
  so a scope is never declared complete before its first child is seeded. Event
  subprocesses, multi-instance, and call activities remain separate ROADMAP items
  that build on this scope lifecycle.

## Pros and cons of the options

### Option 1 — element-instance-as-scope, reuse ADR-0068 substrate (chosen)
- Good: one scope model, one counter, one `applyToState`, one recovery path;
  reuses `FlowScopeKey`, `activeChildren`, `processInstanceKeyOfScope`; small
  runtime delta; the counter was designed for this.
- Bad: generalizes a hot, well-tested completion path; adds a scoped-termination
  scan.

### Option 2 — compile-time flattening (rejected)
- Good: no runtime change; trivial cases "just work."
- Bad: discards the scope boundary — no boundary-on-subprocess, no scoped
  variables, no scoped interruption, wrong completion with multiple inner ends;
  a dead end for the rest of Milestone 3.

### Option 3 — dedicated subprocess-instance subsystem (rejected)
- Good: conceptually separate.
- Bad: duplicates the counter and element-instance lifecycle; a second recovery
  path; against the single-`applyToState` grain (I4); no added capability.

## Links

- builds directly on ADR-0068 (task I/O variable mappings with activity-local
  scopes), which introduced `FlowScopeKey` scope-chain resolution, the
  activity-local scope pattern (scope key = element-instance key), and states it
  "unblocks a future embedded-subprocess ADR"
- reuses the `activeChildren` per-scope counter (`state/keys.go:20`,
  `state/tx.go:390-408`, `state/merge.go:12-24`) and the scope-owner dispatch
  `processInstanceKeyOfScope` (`engine/behavior.go:932-937`)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 3: "Embedded subprocesses (scope lifecycle via child
  counters)"; precedes and is depended on by event subprocesses, multi-instance
  activities, and call activities (all separate Milestone-3 items)
- the interruption template is `interruptHost` (`engine/behavior.go:717-749`) and
  the whole-instance terminate `handleProcessInstanceTerminating` (`:133-145`)
