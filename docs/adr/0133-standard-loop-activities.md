# ADR-0133: Standard loop activities (the ↻ marker)

- **Status:** Accepted
- **Date:** 2026-08-17
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered end to end: `<standardLoopCharacteristics>` is
> parsed and compiled, runs on the ADR-0077 multi-instance body/iteration machinery,
> and is authored in the Modeler's Implement panel — where the loop **marker on the
> shape and the Mode property are one and the same fact**, in both directions.
>
> **Compiler.** The marker parses on exactly the activities multi-instance already
> covers (service, script, user, receive and job-kind send tasks, call activities,
> subprocesses) and compiles into the existing `MultiInstanceDetail` table, marked
> `Standard`, with `LoopCondition` (compiled once at deploy, I5), `TestBefore`, and
> `LoopMaximum`. The node keeps its real activity type and its single `MultiInstance`
> index, so no new compiled field, node type, or table was added. Refused at deploy: a
> loop with neither a condition nor a maximum (it could never end), a non-positive or
> unparseable `loopMaximum`, an uncompilable condition, and an activity carrying *both*
> loop markers.
>
> **Engine.** A standard loop is a **sequential** loop whose iteration set is a
> condition rather than a collection, so it reuses the ADR-0077 runtime unchanged: the
> activation seeds a body, the body seeds one inner iteration, and each completed
> iteration decides whether the next one runs. `seedMultiInstance` checks the condition
> before iteration 1 when `testBefore` is set (a while loop, possibly zero runs) and
> otherwise seeds it unconditionally (BPMN's repeat-until, always at least one run);
> `finishMultiInstanceIteration` evaluates the condition over the finished iteration's
> own scope chain — so it reads the 1-based `loopCounter` and everything that iteration
> wrote — and seeds the next while the condition holds and the cap is not reached. No
> new record, intent, counter, or recovery path; recovery is inherited, and verified by
> a crash-and-replay test that parks mid-loop and finishes afterwards.
>
> **Iteration results escape.** Unlike a multi-instance iteration (whose result is its
> own, aggregated into the output collection), a standard loop's iterations are
> successive runs of one activity: their results land on the body scope, so the next
> run and the loop condition read them, and the body promotes them to the enclosing
> scope when the loop ends. A looping activity therefore leaves behind exactly what the
> same activity would have left running once.
>
> **Modeler and simulation.** The Implement panel's section is now **Loop**, with one
> Mode select covering all four states (none / loop / multi-instance parallel /
> multi-instance sequential) — reading whichever characteristics the element carries
> and writing the element bpmn-js draws the marker from. An element with a loop marker
> Atlas does not run says so in the panel instead of letting the icon imply behavior.
> The Design-view token simulation counts a standard loop like a sequential
> multi-instance, badged ↻, bounded by the modelled `loopMaximum`. Verified in a real
> browser (Playwright, the vendored bpmn-js): the panel reads an imported loop, setting
> the mode draws the marker and exports the expected XML, clearing it removes both, and
> switching to a multi-instance replaces one marker with the other.

## Context and problem statement

BPMN has **two** loop markers, and Atlas only ran one of them.
`<multiInstanceLoopCharacteristics>` (ADR-0077) runs an activity once per element of a
collection, drawn ∥ or ≡. `<standardLoopCharacteristics>` — the **↻** marker — repeats
an activity *while a condition holds*: check the goods again until they pass, call the
customer again until someone answers, retry the reconciliation until it balances. It is
the natural way to model "do this until it works", and bpmn-js draws it from the
palette's marker toggle like any other.

Atlas could not execute one. `<standardLoopCharacteristics>` was absent from the
activity `xml*` structs, so `encoding/xml` silently dropped it: the activity deployed
and ran **once**, with the ↻ icon on the diagram claiming otherwise. Worse, the
Modeler's Implement panel only knew the multi-instance element, so a task carrying the
loop marker showed **Mode: None — runs once** while displaying ↻ — the property and the
icon describing two different processes, with the property telling the truth and the
icon getting the attention.

Two questions, then: **how do we execute a condition-driven loop without a second loop
runtime**, and **how does the panel stop disagreeing with the shape**?

## Decision drivers

- **Reuse, don't reinvent.** ADR-0077 already built everything a repeated activity
  needs: a body element instance that is its own scope, inner iterations seeded under
  it, `loopCounter` bindings, an `activeChildren` counter that drains, `completeScope`,
  and interruption via `terminateScope`. A standard loop differs from a *sequential*
  multi-instance in exactly one respect — how the next iteration is decided.
- **Invariants hold.** No per-command hot-path allocation (I1); one `applyToState` live
  and on recovery (I4); the condition compiles at deploy, never at runtime (I5);
  seeding decisions are pure functions of replayed state, and the iterations that
  happened are reconstructed from their persisted `Activated`/variable events rather
  than re-evaluated (I6).
- **The icon is a property, not decoration.** Whatever a reader sees on the shape must
  be what the engine does. That means the panel reads and writes the same
  `loopCharacteristics` bpmn-js renders from, in one control, and says so plainly where
  Atlas cannot honour a marker.
- **A loop must be able to end.** A condition-driven loop is the one construct in the
  model that can run forever by itself. The author needs a bound they can reason about.

## Considered options

1. **A standard loop as a sequential multi-instance with a condition-driven iteration
   set (chosen).** The compiled `MultiInstanceDetail` gains `Standard`, `LoopCondition`,
   `TestBefore`, `LoopMaximum`; the runtime keeps its body/inner structure and swaps the
   "is there a next item?" question for "does the condition still hold?".
2. **A separate loop detail, node type, and behavior.** A `TypeLoopActivity` with its
   own body, its own seeding, its own completion path.
3. **Rewrite a standard loop into a cyclic subgraph at compile time.** Desugar ↻ into an
   exclusive gateway looping back into the activity.
4. **Refuse the marker at deploy.** Keep the engine as it is and make the model fail to
   deploy, so at least the icon never lies.

## Decision outcome

Chosen option: **"a standard loop is a sequential multi-instance whose iteration set is
a condition"**, because the two constructs differ in one decision and agree on
everything else — scope, counter, key generation, variable bindings, interruption,
recovery. The engine change is a branch in `seedMultiInstance` (seed the first
iteration, or skip it when `testBefore` says the condition already fails) and a branch
in `finishMultiInstanceIteration` (seed the next while the condition holds and the cap
is not reached), plus the result-scoping rule below. Everything else — the body owning
the outgoing flow, an interrupting boundary tearing every iteration down, a crash
rebuilding the live iteration from the log — is inherited, not rewritten.

The one place the two markers genuinely disagree is **what an iteration's result
means**. Multi-instance iterations are independent, so ADR-0077 scopes each result to
its own iteration and aggregates them into the output collection. A standard loop's
iterations are the *same* activity run again: the run that just finished is how the
loop makes progress towards its own exit. So a standard loop's iteration results are
written to the **body scope** (the enclosing activity's own scope, one level below the
process), where the next iteration and the loop condition read them up the chain, and
the body **promotes them to the enclosing scope** when the loop ends. The observable
rule is: *a looping activity leaves behind what the same activity would have left
running once.*

The bound is `loopMaximum`, an optional hard cap; a loop with neither a condition nor a
maximum is refused at deploy. Atlas does **not** impose a hidden global iteration
ceiling: a cyclic sequence flow can already loop forever and BPMN allows it, so a
silent cut-off would be a surprise of a different kind. The cap is the author's, stated
in the model.

For the Modeler, the section became one **Mode** select over all four states, reading
`loopCharacteristics` by `$type`. Because bpmn-js draws the marker from exactly the
element the panel writes, and the panel re-renders on `element.changed`, the two stay
in sync by construction: setting Mode draws the icon, and toggling the marker anywhere
else (the context pad, an imported file) reads back as that mode. Where Atlas cannot
run a marker at all, the panel says so rather than leaving the icon to imply behavior.

### Consequences

- **Positive:** the ↻ marker executes, on every activity kind that already supported
  multi-instance, with no new value type, intent, counter, or recovery path — the
  existing recovery guarantees cover it unchanged. The Modeler, the token simulation,
  and the Operations call-activity list all name the two markers apart, so the icon,
  the property, and the runtime agree.
- **Negative / trade-offs accepted:** the two markers share one compiled struct, so
  half its fields are meaningless for each — readable only because the doc comment says
  which belong to which. A condition that never turns false and has no `loopMaximum`
  loops until the instance is cancelled; for an activity with no external wait (a script
  task) that is a busy loop on the partition, the same exposure a cyclic sequence flow
  already carries. An activity cannot carry both markers — a deploy-time refusal, not a
  silent pick.
- **Follow-ups / risks to watch:** the supported activity set is inherited from
  ADR-0077, so business rule, manual and undefined tasks still ignore *both* markers —
  the panel now says so, but running them there is future work. The token simulation
  counts a standard loop by `loopMaximum` (or the configurable default) because it does
  not evaluate FEEL; that is a teaching aid's approximation, labelled as such in the
  badge tooltip.

## Pros and cons of the options

### Option 1 — sequential multi-instance with a condition-driven iteration set (chosen)
- Good: one loop runtime, one recovery path, one scope model; the engine delta is two
  branches and a scoping rule.
- Good: composes for free with what ADR-0077 composed with — boundary interruption,
  nesting in subprocesses, call activities, I/O mappings.
- Bad: one struct carries two markers' fields.

### Option 2 — a separate loop node type, detail, and behavior
- Good: each marker's data structure says only what it means.
- Bad: a second body/iteration lifecycle to keep correct under crash, interruption, and
  nesting — the expensive half, duplicated for a one-decision difference. Every future
  scope change would have to be made twice.

### Option 3 — desugar into a cyclic subgraph at compile time
- Good: no runtime change whatsoever.
- Bad: the compiled graph stops matching the diagram — element ids, visit history, the
  Operations timeline and the token simulation would all show synthetic nodes the author
  never drew. `loopCounter` and the testBefore/repeat-until distinction would need
  synthesizing anyway, and an interrupting boundary on the activity would attach to the
  wrong thing.

### Option 4 — refuse the marker at deploy
- Good: cheapest, and it does stop the icon from lying.
- Bad: it answers "the diagram claims something Atlas won't do" by removing the
  diagram, not by doing the thing. The construct is common and the runtime for it
  already existed.

## Links

- builds on [ADR-0077](0077-multi-instance-activities.md) (multi-instance activities —
  the body/iteration runtime this reuses)
- builds on [ADR-0074](0074-embedded-subprocesses.md) (the element-as-scope lifecycle
  and `activeChildren` counter underneath both)
- relates to [ADR-0068](0068-task-io-variable-mappings.md) (activity-local scopes
  and the result-scoping rule this refines for a loop)
- relates to [ADR-0100](0100-token-simulation-configurable-multi-instance-count.md)
  (the simulation's counted-repetition visualisation, extended to the ↻ marker)
