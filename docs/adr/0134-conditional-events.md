# ADR-0134: Conditional events (data-triggered catch/boundary)

- **Status:** Proposed
- **Date:** 2026-08-17
- **Deciders:** Atlas engine team

> **Implementation status.** Proposed. A **conditional event** (`<conditionalEventDefinition><condition>`)
> is a catch that fires when a **boolean FEEL condition over the process's variables becomes true** —
> not on a message, a timer, or a signal, but on **data change**. Three forms: a **conditional
> intermediate catch** (waits until the condition holds, then flows on), a **conditional boundary
> event** (interrupting or non-interrupting, fires while its host activity runs), and a **conditional
> event subprocess** (interrupting or non-interrupting, fires while its scope runs). The condition is
> compiled to FEEL at deploy (like a gateway condition) and **re-evaluated whenever a variable it reads
> changes** — every committed write funnels through one chokepoint (`AppendVariableEvent`), so the
> engine schedules a **conditional re-check** (a transient, command-path-only follow-up) for the
> touched instance, evaluates each armed conditional over its scope chain, and drives the ones now true
> to `Completing` — reusing the **inert-armed catch** + `AppendElementCommand(IntentCompleting)` pattern
> that error (ADR-0089) and escalation (ADR-0125) boundaries already established. Unlike a message it
> opens **no subscription**; unlike those two it is triggered by **data**, not by a throw. It is the
> last unimplemented BPMN intermediate-event trigger and the first that reacts to variable state.

## Context and problem statement

A **conditional event** lets a process **react to its own data**: "when the order total exceeds the
approval limit, route to a manager"; "while the shipment is in transit, if its temperature leaves the
safe range, run the alert handler"; "hold this step until stock is replenished." The trigger is
neither a message nor a timer — it is a **boolean condition over process variables** that the engine
watches and fires when it **becomes true**. It is the one event family whose trigger is *internal
state*, which is exactly why it is the last one Atlas lacks: every other catch (message, signal,
timer, error, escalation) is driven by an external arrival or a structural throw, and Atlas already
has the machinery for those; a conditional event needs the engine to **notice a variable changed and
re-test a predicate**.

Atlas cannot express one today, and Camunda 8 / Zeebe **doesn't implement conditional events either**,
so there is no reference engine to mirror — this ADR designs to the BPMN 2.0 spec. Concretely:
`<conditionalEventDefinition>` is not parsed — the intermediate-catch and boundary structs carry no
`Conditional` field — and it falls into the `default:` "only … supported yet" branches of the
intermediate-catch (`compiler/scope_compile.go:371`) and boundary (`compiler/scope_compile.go:684`)
switches. The Modeler blocks it: `bpmn:ConditionalEventDefinition` is in `UNSUPPORTED_EVENT_DEFS`
(`api/web/editor.js:601`).

The question this ADR answers: **when, and on what trigger, does the engine re-evaluate a conditional
event's predicate** — since a naive "check continuously" has no place in a batch-processing,
event-sourced engine, and the answer must stay deterministic on replay (I6).

What already exists, and is load-bearing:

- **FEEL knows which variables it reads.** A compiled FEEL expression exposes `Inputs()` — the exact
  set of free variable names (`expr/expr.go:94`). A gateway condition is compiled with
  `expr.CompileAuto` (`compiler/scope_compile.go:822`) and evaluated at runtime as
  `cond.Eval(bindInputsChain(c, cond.Inputs(), ei.FlowScopeKey))` guarded by `expr.IsTrue`
  (`selectExclusiveFlow`, `engine/behavior.go:2919`). A conditional event's predicate is **the same
  compiled-FEEL-over-the-scope-chain** a gateway condition already is — only the *trigger* differs.
- **Every committed variable write funnels through one chokepoint.** `AppendVariableEvent`
  (`engine/context.go:304`) is the single call every variable change goes through — external
  `SetVariables` (ADR-0095), output I/O mappings (ADR-0068), script/DMN results, start variables,
  multi-instance locals — and it knows the `(scope, name)` written and runs **live only** (replay
  applies variable events straight through `applyToState`, never via this path). That is the natural,
  deterministic hook: "a variable just changed in scope S."
- **The scope chain resolves a condition exactly where it is armed.** `bindInputsChain`
  (`engine/behavior.go:1432`) walks a FEEL expression's inputs up the `FlowScopeKey` chain
  (`resolveInChain`, `engine/scope.go:19`), so a conditional boundary on a subprocess, or a catch
  inside a multi-instance body, reads its enclosing scope's variables (ADR-0086) with no extra work.
- **Inert-armed catches driven to `Completing` are a solved pattern.** `BoundaryError` /
  `BoundaryEscalation` / `BoundaryCancel` boundaries **open nothing** in `boundaryEventBehavior.OnActivated`
  (`engine/behavior.go:3123-3131`) — they arm and wait to be *found* — and are driven to `Completing`
  by a command-path scan (`findErrorBoundary` → `fireErrorCatch` →
  `AppendElementCommand(catchKey, IntentCompleting)`, `engine/behavior.go:2227`). The
  `OnCompleting` fire path already splits interrupting (`interruptHost`) vs non-interrupting
  (take-the-flow-alongside) on `d.Interrupting` (`engine/behavior.go:3141`). A conditional boundary
  arms inert the same way; the conditional re-check plays the role `findErrorBoundary` plays for an
  error.
- **A detail can already carry a compiled expression.** `BoundaryEventDetail.CorrelationKey
  *expr.Compiled` (`compiler/process.go:656`) is the precedent — a conditional detail carries a
  `Condition *expr.Compiled` the same way.
- **Follow-ups run in the next batch, live only.** `processBatch` (`engine/processor.go:594`)
  processes commands in Phase 1, and commands queued via `appendCommand` (`engine/context.go:458`)
  become the next batch — never re-derived on `Recover`. A conditional re-check scheduled as a
  follow-up is therefore deterministic and off replay (I6), exactly like `propagateError`.

So the FEEL predicate, the scope-chain eval, the inert-armed catch, the interrupting/non-interrupting
fire, and the live-only follow-up all already exist. What is missing is (a) parsing the condition, (b)
a `BoundaryConditional` kind + a conditional-catch type carrying the compiled predicate, and (c) the
**re-check trigger**: on a variable write, schedule a re-evaluation of the touched instance's armed
conditionals and fire those now true.

## Decision drivers

- **Data-driven, not polled.** The trigger is a variable changing, which the engine already observes
  at one chokepoint. Re-evaluate *because data changed*, never on a wall-clock tick.
- **Reuse the eval and the fire.** The predicate is a gateway condition; the fire is an inert-armed
  boundary driven to `Completing`. Both exist — a conditional event is their composition.
- **Invariants hold.** The re-check runs **off the hot path** as a follow-up, so no per-command
  allocation on the critical path (I1); durable-before-visible is unchanged — the *fire* is an ordinary
  event (I2); `applyToState` is untouched — evaluation is a command-path read, like `propagateError`
  (I4); the predicate is compiled at deploy (I5); the re-check runs **live only** and its firing is a
  persisted `Completing`→`Completed`/`Terminated` chain that replays identically (I6).
- **Faithful BPMN.** A conditional event fires when its condition **becomes true**; evaluated when the
  event is armed (firing at once if already true) and again on each relevant data change. Interrupting
  cancels the host/scope; non-interrupting runs a handler alongside and can fire again — but only on a
  fresh **false→true transition**, so it does not re-fire while the condition merely stays true.
- **Ship the fire-once core first.** An **interrupting conditional boundary** and a **conditional
  intermediate catch** both fire exactly once — no transition-tracking — so they are the first,
  smallest runnable phase. The **non-interrupting** forms, which need edge-triggering, layer on.

## Considered options

1. **Re-evaluate on variable change, scheduled as a command-path follow-up (chosen).** Compile the
   condition to FEEL at deploy. A conditional catch/boundary/event-sub arms **inert** (opens nothing).
   When `AppendVariableEvent` commits a write, mark the touched instance dirty; at the end of the
   writing command's processing, enqueue a **transient `ConditionRecheck` follow-up** for each dirty
   instance (deduplicated). Its handler scans the instance's armed conditional elements, evaluates each
   over its scope chain (`cond.Eval(bindInputsChain(cond.Inputs(), scope))`), and drives those now true
   to `Completing` — interrupting via `interruptHost`, non-interrupting alongside. A conditional is
   also evaluated **at arm** (fires at once if already true). Non-interrupting forms store a
   `conditionMet` flag and fire only on false→true. No subscription, no timer, no new value type — the
   re-check is a transient command, the fire is the existing event chain.
2. **Poll: a periodic timer re-evaluates every armed conditional.** Arm a recurring internal timer per
   instance (or globally) that re-tests all conditions. Rejected: it decouples firing from the data
   change (latency between the write and the next tick), burns work re-testing unchanged conditions,
   and ties firing to the wall clock — a poor fit for an event-sourced engine and awkward to replay
   deterministically. The engine already *sees* every write; polling ignores that.
3. **Evaluate only at activity-completion checkpoints** (re-test conditions when an activity completes,
   piggybacking on `handleElementCompleting`). Rejected: it **misses** a condition that becomes true
   via an **external `SetVariables`** (ADR-0095) — a running process whose data an operator or an API
   client changes, with no activity completing — which is a first-class BPMN use of conditional events
   (react to externally-updated state). It also misses a change made by a *parallel* token's activity
   that a waiting conditional in another branch depends on. The variable-write trigger (option 1)
   catches all of these because it hooks the write itself, not the activity boundary.

## Decision outcome

Chosen: **option 1 — a conditional event arms inert and is re-evaluated when a variable it reads
changes, via a command-path follow-up, firing when its predicate becomes true.** The genuinely new
logic is (a) parsing `<conditionalEventDefinition><condition>` and compiling the FEEL predicate, (b) a
`BoundaryConditional` kind + a `TypeConditionalCatchEvent` type, each carrying `Condition
*expr.Compiled`, and (c) the **re-check trigger**: dirty-tracking on `AppendVariableEvent`, a transient
`ConditionRecheck` follow-up, and an evaluator that fires armed conditionals now true.

### The re-check trigger

- **Dirty-tracking.** `AppendVariableEvent` (`context.go:304`) records the written variable's
  **process instance** as dirty in the `ProcessingContext` (a small set, cleared per batch). This is
  the one central hook — every write path already funnels through it, so no per-write-site change.
- **Scheduling.** After a command finishes processing in Phase 1, for each newly-dirty instance the
  engine enqueues one **transient `ConditionRecheck` command** (deduplicated per instance per batch).
  It is a *command*, not an event — like `ThrowJobError` (ADR-0089) — so it is never persisted and
  never replayed; it runs in the next batch on the live command path only (I6).
- **Evaluation.** `ConditionRecheck`'s handler scans the instance's live element instances for armed
  conditionals (`TypeConditionalCatchEvent`, or a `TypeBoundaryEvent`/`TypeEventSubProcessStart` whose
  `Kind == BoundaryConditional`) and evaluates each: `v, err := d.Condition.Eval(bindInputsChain(c,
  d.Condition.Inputs(), scope))`; a true `expr.IsTrue(v)` fires it. Firing drives the element to
  `Completing` with `AppendElementCommand` — the exact `fireErrorCatch` move. (Optimization: skip a
  conditional whose `Inputs()` do not intersect the batch's changed names; the baseline may re-test
  all armed conditionals in the instance, since evaluation is a pure read and a fire-once event that
  was already true would have fired at arm.)
- **At arm.** A conditional's `OnActivated` performs an **immediate self-evaluation** (the same eval);
  if already true it fires at once — a catch the token arrives at with the condition already satisfied
  passes straight through; a boundary true the moment its host activates interrupts immediately. This
  is BPMN-faithful (evaluate on entry).

### Interrupting vs non-interrupting

- **Interrupting boundary / intermediate catch (fire-once).** Evaluate at arm and on each relevant
  change; the first true fires. Interrupting `interruptHost`s the activity and routes the recovery
  flow; the catch `completeAndTakeFlows`. No transition-tracking is needed — it fires exactly once.
- **Non-interrupting boundary / event subprocess (edge-triggered).** A non-interrupting conditional
  may fire many times, but must fire only when the condition **transitions false→true**, not while it
  stays true (else a single true condition would re-fire on every unrelated write). The armed instance
  stores a durable `conditionMet` flag: each evaluation computes `now`, fires only when `now &&
  !conditionMet`, and records `conditionMet = now`. Firing spawns the handler alongside the
  still-running host/scope (reusing the ADR-0040/0082 non-interrupting path) and the trigger re-arms
  with `conditionMet` carried, so it fires again on the next false→true.

### Compiler

- Parse `<conditionalEventDefinition><condition>…FEEL…</condition>` — a `Conditional
  *xmlConditionalEventDefinition` pointer on `xmlIntermediateCatchEvent` (`parse.go:1385`),
  `xmlBoundaryEvent` (`parse.go:1488`), and the event-subprocess start struct. Compile the condition
  body with `expr.CompileAuto` (as gateway conditions do); an empty/absent condition is a **deploy
  error** (a conditional event with no predicate can never fire).
- Add `TypeConditionalCatchEvent` to the `BpmnType` enum + `String()`; grow `numBpmnTypes` (40 → 41).
  Boundary and event-subprocess conditionals reuse `TypeBoundaryEvent` / `TypeEventSubProcessStart`
  (they dispatch on `BoundaryEventKind`), so only the intermediate catch needs a new type.
- Add `BoundaryConditional` to `BoundaryEventKind` and a `Condition *expr.Compiled` to
  `BoundaryEventDetail` / `EventSubProcessDetail` (mirroring `CorrelationKey *expr.Compiled`), plus a
  `ConditionalDetail{Condition *expr.Compiled}` for the intermediate catch. Builder methods
  `AddConditionalCatchEvent`, `AddBoundaryConditionalEvent(host, cond, interrupting)`, and a
  `SetEventSubProcess` conditional path, following `AddBoundaryEscalationEvent`
  (`builder.go:1395`). A conditional boundary/event-sub **honors** `cancelActivity`/`isInterrupting`
  (like escalation, unlike error).

### Runtime

- `conditionalCatchBehavior`: `OnActivated` arms inert and self-evaluates (fire if already true);
  otherwise stays `Activated` until a re-check fires it; `OnCompleting` `completeAndTakeFlows`.
- `BoundaryConditional` cases in `boundaryEventBehavior.OnActivated` / `eventSubProcessStartBehavior.OnActivated`
  that open nothing and self-evaluate; the existing `OnCompleting` honors `d.Interrupting`.
- Dirty-tracking in `AppendVariableEvent`; the `ConditionRecheck` transient command + handler
  (`recheckConditionals`), mirroring `propagateError`'s command-path scan; the `conditionMet` flag on
  the armed instance for non-interrupting edge-triggering.

### Phased implementation plan (test-first)

- **Phase 1 — Compile + the re-check core + interrupting.** Parse `<condition>`; the types/kind/detail;
  the dirty-track → `ConditionRecheck` → `recheckConditionals` path; **interrupting conditional
  boundary** and **conditional intermediate catch** (fire-once). *Tests:* a catch that passes straight
  through when its condition is already true at arrival, and one that waits until a later
  `SetVariables` makes it true; an interrupting boundary on a user task that fires when a variable
  crosses a threshold (host cancelled, recovery flow taken) and one that fires **immediately** because
  the condition holds at arm; an empty condition is a deploy error; a **recovery test** — a parked
  conditional re-arms on replay and fires on a post-recovery variable change.
- **Phase 2 — Non-interrupting + event subprocess.** Edge-triggering (`conditionMet`); a
  **non-interrupting** conditional boundary whose handler runs while the host keeps going and fires
  again only on a fresh false→true; a **conditional event subprocess** (interrupting and
  non-interrupting). *Tests:* a non-interrupting boundary that fires once per false→true and **not**
  on unrelated writes while the condition stays true; a non-interrupting event subprocess re-arming;
  recovery of the `conditionMet` flag.
- **Phase 3 — Modeler + docs.** Drop `bpmn:ConditionalEventDefinition` from `UNSUPPORTED_EVENT_DEFS`;
  a condition (FEEL) field + the interrupting toggle on conditional boundary/catch/event-sub in the
  Implement panel; accept this ADR and update the ROADMAP. bpmn-js draws the conditional marker and
  the moddle type is native — no diagram change.

### Consequences

- **Positive:** the engine gains **data-reactive control flow** — wait-until, threshold boundaries,
  and event subprocesses that watch state — the last unimplemented BPMN intermediate-event trigger, on
  the gateway-condition eval, the scope chain, and the inert-armed-catch fire path that already exist.
  It reacts correctly to *external* `SetVariables`, which the completion-checkpoint alternative would
  miss. No subscription, value type (beyond a small `conditionMet` flag for non-interrupting), or new
  recovery path; the re-check is a live-only follow-up and the fire is the existing event chain.
- **Negative / trade-offs accepted:** a new central hook in `AppendVariableEvent` (dirty-tracking) and
  a `ConditionRecheck` follow-up per instance-with-changed-variables per batch; a durable `conditionMet`
  flag on non-interrupting conditional instances; a re-check that (baseline) re-tests an instance's
  armed conditionals on any variable change, mitigated by the `Inputs()`-intersection optimization.
- **Follow-ups / risks to watch:** (1) **Re-trigger loops** — a conditional handler that writes a
  variable which re-satisfies the same (or another) condition could cascade across follow-up batches;
  fire-once (interrupting/catch) and edge-triggering (non-interrupting) bound this, but a
  handler-writes-its-own-input case needs an explicit test that it does not livelock. (2) **Evaluation
  order** when several conditionals in one instance become true in the same re-check — define it
  (document order) and test it. (3) **Cost** of re-testing on high-frequency variable writes — the
  `Inputs()` filter keeps a write that touches no watched name from evaluating anything; verify with a
  test that an unrelated write fires nothing. (4) **`conditionMet` on recovery** must rebuild from the
  armed instance's persisted state so a post-crash false→true still edge-triggers.

## Pros and cons of the options

### Option 1 — re-evaluate on variable change, command-path follow-up (chosen)
- Good: data-driven off the one write chokepoint; reuses the gateway-condition eval, the scope chain,
  the inert-armed catch, and the interrupting/non-interrupting fire; deterministic and off replay (the
  re-check is a live-only follow-up, the fire is a persisted event); catches external `SetVariables`.
- Bad: a central hook in `AppendVariableEvent`; a `conditionMet` flag for non-interrupting; a re-check
  that re-tests armed conditionals (mitigated by the `Inputs()` filter); re-trigger loops to guard.

### Option 2 — periodic polling (rejected)
- Good: trivially catches every change eventually; no write-path hook.
- Bad: latency between the change and the tick; wasted re-tests of unchanged conditions; firing tied to
  the wall clock, awkward to replay deterministically; ignores that the engine already sees every write.

### Option 3 — evaluate at activity-completion checkpoints (rejected)
- Good: no write-path hook; re-uses `handleElementCompleting`.
- Bad: misses a condition made true by an external `SetVariables` with no activity completing, and by a
  parallel branch's write that a waiting conditional depends on — both first-class conditional-event
  uses; not faithful.

## Links

- builds on the gateway-condition eval and scope chain (`selectExclusiveFlow` /
  `bindInputsChain`, **ADR-0086**), the variable-write chokepoint `AppendVariableEvent` and external
  `SetVariables` (**ADR-0095**), FEEL `Inputs()`/`CompileAuto` (**ADR-0008/0015**), the boundary
  arm/fire lifecycle (**ADR-0040**), event subprocesses (**ADR-0082**), and the inert-armed catch +
  command-path fire pattern from **ADR-0089** (error) and **ADR-0125** (escalation)
- honors I1, I2, I4, I5, I6 and **ADR-0018** (test-first, recovery tests up front)
- ROADMAP Milestone 1/2; the last unimplemented BPMN intermediate-event trigger, and the first event
  triggered by process **data** rather than an arrival or a throw
