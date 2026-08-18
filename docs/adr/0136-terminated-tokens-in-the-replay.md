# ADR-0136: Terminated tokens in the step-by-step replay

- **Status:** Accepted
- **Date:** 2026-08-18
- **Deciders:** Atlas maintainers

## Context and problem statement

The single-process replay (ADR-0046) folds a retained history of causal token
facts — one record per element activation and per element consumption — into the
multi-token frames the Operations replay transport scrubs through. To keep a token
from flickering out of existence between the log position where it leaves one
element and the position where it enters the next, a *non-leaf* consumption is
**deferred**: the token stays drawn on the consumed element until the activation it
caused appears, and only then moves. A leaf consumption (an end event, nothing
outgoing) removes its token at once.

That fold assumes every consumption hands the token to a successor. It does not.
An element torn down by an **interrupting boundary event**, by a cancelled
instance, or by a terminate end event never takes its outgoing flow — the boundary's
own flow is taken instead, or nothing is. The history recorded both endings with
the same action code (`2`, "left the element"), so the fold could not tell them
apart and waited forever for an activation that would never come.

The visible defect: a process whose user task is interrupted by an attached timer
finishes — the instance reads `completed`, the task carries an end timestamp — and
the replay's final frame still shows a live token parked on the interrupted task.
A frame after the end of the instance that still holds a token contradicts the
engine, which deleted that element instance (and decremented its live-token
counter) the moment it was terminated.

A second, smaller defect sat in the same fold. The link from a consumption to its
successor is the *graph*: an activation arriving over flow `F` is the successor of
whatever was consumed on `F`'s source node. Elements that are activated **without
taking a flow** — an armed boundary event, an armed event-subprocess trigger, a
compensation handler — left `SourceFlowId` at its zero value, and zero is a valid
flow index. So such an activation claimed to have arrived over flow `0` and could
retire a live token parked on that flow's source, besides making the frontend
animate the token along an edge that does not exist.

## Decision drivers

- **The fold must reflect the engine, not approximate it.** A finished instance has
  no element instances; its replay must show no tokens.
- **Determinism / recovery (invariant I4).** Whatever distinguishes the two endings
  must be derived from the event alone, in `applyToState`, so replay rebuilds it.
- **No new state.** The distinction already exists in the log as the event intent;
  it was being discarded on the way into the history.
- **Old history stays readable.** Records written before this change cannot be
  rewritten; the reader has to stay correct on them.

## Considered options

1. **Record termination as its own action code** and stop deferring it in the fold.
2. **Infer it in the reader** — treat a deferral that no activation ever resolves as
   a termination, in a pre-pass over the history.
3. **Never defer** — remove every token at its consumption and accept the flicker.

## Decision outcome

Chosen option: **"Record termination as its own action code"**, because the fact
that an element was terminated rather than completed is a real, already-durable
distinction (the event intent), and the replay history is derived state whose whole
job is to carry the facts a later fold needs. Inferring it (option 2) reconstructs
by absence what the log already states, and gets the answer wrong for a token whose
successor simply has not activated yet; option 3 reintroduces the flicker ADR-0046
deliberately removed and would make a parallel join appear to lose an arrival.

Concretely:

- `applyToState` writes action `3` (terminated) for `IntentTerminated` and keeps `2`
  (completed) for `IntentCompleted`. The codes are named in the `state` package
  (`ReplayActivated` / `ReplayCompleted` / `ReplayTerminated`) so writer and reader
  share one definition; being persisted, their numeric values are now part of the
  on-disk history format and are never reused.
- The fold removes a terminated element's token immediately, exactly as it does a
  leaf's. No flicker follows: an interrupt emits the host's termination *before* the
  boundary's own completion and continuation, so the boundary's token is on the
  diagram throughout.
- Every activation that does not take a sequence flow now records `SourceFlowId =
  -1`, the convention start events already used, so "no incoming flow" is
  representable instead of colliding with flow index 0.
- Belt and braces for history already written: if the fold ends with tokens still
  deferred on an instance that is no longer active, they are dropped and the empty
  terminal frame is emitted. A live fold never reaches this — every deferral is
  resolved — so it only repairs the terminal frame of instances recorded before this
  change.

### Consequences

- **Positive:** The replay's frames now agree with the engine's own live-token
  counters: an interrupted activity's token dies with it, and a finished instance
  ends with an empty frame. The interrupt is *visible* — the token moves to the
  boundary's route instead of being duplicated.
- **Positive:** An armed boundary event, event-subprocess trigger, or compensation
  handler no longer claims a phantom predecessor, so the replay animates it in place
  and the fold cannot retire an unrelated live token.
- **Negative / trade-offs accepted:** A third action code is a persisted-format
  addition. Old records are still read (2 means "consumed"), but the intermediate
  frames of instances that finished before this change keep their ghost token; only
  their final frame is repaired. Rebuilding state from the WAL restores them fully.
- **Follow-ups / risks to watch:** An armed boundary event is recorded with token id
  `0` (it is activated with no token of its own), so the replay labels it "Token 0".
  Giving an armed boundary a token identity of its own is a separate question —
  it changes what token the boundary's continuation carries — and is left open.

## Pros and cons of the options

### Option 1 — a distinct action code
- Good: derived from the event intent alone; deterministic on replay (I4).
- Good: the reader stays a single forward fold, no pre-pass, no heuristics.
- Bad: adds a value to a persisted format; old records keep the old meaning.

### Option 2 — infer in the reader
- Good: no format change, repairs old history too.
- Bad: reconstructs by absence what the log already states; a deferral awaiting a
  successor in a still-running instance is indistinguishable from a termination.
- Bad: needs a pre-pass over the whole history before the fold can emit a frame.

### Option 3 — never defer
- Good: trivially correct about terminations.
- Bad: reintroduces the flicker and the lost join arrival ADR-0046 removed.

## Links

- relates to ADR-0046 (single-process step-by-step replay)
- relates to ADR-0040 (boundary events), ADR-0116 (terminate end event)
- relates to ADR-0080 (runtime token counters), which was always correct here
