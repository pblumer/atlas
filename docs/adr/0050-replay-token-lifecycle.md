# ADR-0050: Reconstruct the live token set for a concurrency-aware replay

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

The single-process replay (ADR-0046) walks an instance through its diagram from the
element-step history — the ordered *activations* a token walked through. That log is
linear, so it serializes concurrency: a parallel fork's branches appear one after
another, and a parallel join appears once per arriving token (twice for a two-branch
join). To an operator that reads as "the token was multiplied and never joined,"
even though the engine is correct — the parallel join *does* synchronize (confirmed
by `TestParallelForkJoin`) and produces a single continuation.

The engine's gateway semantics are already BPMN 2.x-correct:
- a **parallel fork** puts a token on every outgoing flow;
- a **parallel/inclusive join** waits for all its incoming tokens, then fires once —
  back to one token;
- an **exclusive gateway** fires on every activation, so as a merge it passes each
  incoming token through independently (no synchronization).

The gap is purely in the *replay*: with only activations recorded, it cannot know
when a token *left* an element, so it cannot show how many tokens are live and where
at a given moment. It needs the other half of the token lifecycle.

## Decision drivers

- **Show BPMN concurrency honestly.** Fork → concurrent tokens; parallel join →
  fold back to one; exclusive merge → tokens pass individually.
- **Determinism / recovery (invariant I4).** Whatever we record must rebuild
  identically on replay, from the event alone.
- **Hot path (invariant I1).** Recording must not allocate per command.
- **Reuse the established pattern.** The element-step history (ADR-0046) records
  activations from `applyToState`; completions should mirror it exactly.
- **Don't re-implement the engine in the query layer.** The replay must read facts,
  not re-simulate gateway logic to guess when tokens merge.

## Considered options

1. **Reconstruct concurrency in the query layer** from activations plus the compiled
   graph topology, re-simulating gateway semantics to infer merges.
2. **Record element completions** alongside activations, and fold activations minus
   completions by log position into a live-token set at each step.
3. **Collapse the display** — dedupe a join's repeated activations in the step list —
   without modelling tokens at all.

## Decision outcome

Chosen option: **"Record element completions and fold a live-token set"**, because it
reads authoritative facts (mirroring ADR-0046), reuses the history-family pattern,
and represents any topology — parallel, inclusive, exclusive — without re-simulating
the engine.

- A new column family `cfElementCompletion` keys each completion by
  `(processInstanceKey, timestamp, position)` — the same shape as the element-step
  (activation) key — with the completed element's compiled-graph index as the value.
  `applyToState`, on `IntentCompleted`/`IntentTerminated` for an element instance,
  issues the completion write (`Tx.RecordElementCompletion`) right after deleting the
  active record and decrementing the scope's child counter. Derived only from the
  event, so replay rebuilds it identically (I4); one extra `Set`, no read, so no
  hot-path allocation (I1).
- The instance-timeline endpoint (ADR-0046) now scans activations and completions,
  merges them by log position, and folds a per-node live-token counter: a completion
  before an activation releases its token, the activation adds one. At each
  activation step it snapshots the result — `tokens` (the total live token count) and
  `activeTokens` (the distinct diagram elements holding one). A node can hold more
  than one token (a join mid-synchronization), so `tokens` can exceed
  `len(activeTokens)`.
- The replay view draws the token state at the playhead: every element in
  `activeTokens` is green (a fork lights up both branches at once; a join collapses
  them back to one), elements a token has left are gray, and a token counter shows
  the live total. The step list is unchanged — one row per activation, an honest
  arrival log — but the overlay and counter now make the synchronization visible.

### Consequences

- **Positive:** The replay shows BPMN concurrency faithfully — fork, parallel/
  inclusive join, and exclusive merge each render as they behave. The feature reuses
  ADR-0046's recording and read shapes; recording is event-sourced, so it survives
  recovery. No engine behavior changed (none was wrong).
- **Negative / trade-offs accepted:** Retention is unbounded for now, as with the
  other history families (ADR-0017/0022/0038/0046/0048). One extra `Set` per element
  completion — roughly doubling this instance's step-history writes. The step list
  still shows a join once per arrival (chosen over collapsing, to keep an honest
  event log); the overlay/counter carry the synchronization instead.
- **Follow-ups / risks to watch:** The shared retention/compaction policy now spans
  six history families. Optionally collapsing join arrivals in the step list, or
  animating the token dot across concurrent branches, if operators want more.

## Pros and cons of the options

### Option 1 — reconstruct in the query layer from topology
- Good: no new stored state.
- Bad: re-implements gateway semantics (parallel vs inclusive vs exclusive join
  conditions) in the query layer — duplicated logic that must track the engine, and
  fragile for inclusive joins whose firing depends on runtime token reachability.

### Option 2 — record completions, fold a live-token set (chosen)
- Good: authoritative; mirrors ADR-0046; deterministic; represents any topology
  without re-simulation; cheap position-merge read.
- Bad: adds a column family and one hot-path `Set` per completion; unbounded
  retention until a shared compaction policy lands.

### Option 3 — collapse the display only
- Good: no engine/state change.
- Bad: hides the symptom without modelling tokens, so it still can't show concurrent
  branches or an exclusive merge's independent pass-through — the actual ask.

## Links

- extends ADR-0046 (single-process step replay — adds the completion half of the
  token lifecycle, same key shape and recording point)
- relates to ADR-0024 (parallel gateway join) and ADR-0033 (inclusive gateway join),
  whose correct synchronization this makes visible
- shares the unbounded-retention concern with ADR-0017/0022/0038/0046/0048
