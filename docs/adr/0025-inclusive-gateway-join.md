# ADR-0025: Inclusive gateway join synchronization

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas engine maintainers

## Context and problem statement

The inclusive (OR) gateway forks a token onto every outgoing flow whose condition
holds (like an exclusive gateway, but taking *all* matches, not the first), and
joins by waiting for exactly the branches that actually received a token. The
split is a small variation on the exclusive split. The join is the hard part: it
cannot wait for a fixed count like a parallel join (ADR-0024), because the number
of live branches depends on which conditions the split took. It must wait until
*no more tokens can still arrive* at the gateway.

In Atlas a token is a live element instance, and control flow advances in batches
of commands. So two subtleties collide: (1) deciding "can any token still reach
this join?" is a graph-reachability question over the current token positions,
and (2) a token can momentarily be *in flight* — a queued command between one
element completing and the next activating — where it is neither a live element
instance nor yet arrived. Ignoring in-flight tokens makes two pass-through
branches each fire the join separately (a double fire).

## Decision drivers

- **Correct for the common reconverging split/join**, including when branches are
  pass-through (both arrivals land in one batch).
- **Deterministic replay (I4/I6).** The decision must be captured by events on the
  log, so recovery reproduces it without re-deciding.
- **No new persistent state.** Prefer deriving the answer from live element
  instances and the compiled graph over a new column family.

## Considered options

1. **"No token can still arrive" via reachability over live *and* in-flight
   tokens.** Compile the join's ancestor set. A join arrival parks; the join fires
   when no active element instance sits on an ancestor, and no element-activating
   command in the rest of this batch's queue or its followups targets an ancestor
   or the join itself. On fire it consumes every token parked on the join and
   takes the outgoing flow(s) once.
2. **Pair each split with its join** and have the split tell the join how many
   tokens it produced.
3. **Restrict to structured diagrams** and reject general inclusive gateways.

## Decision outcome

Chosen option: **"No token can still arrive," counting in-flight tokens.**

- The compiler exposes `CompiledProcess.NodesReaching(target)` — the set of nodes
  from which `target` is reachable (its ancestors), by a reverse walk of the
  flow graph.
- The inclusive **split** (one incoming flow) fires immediately, taking every
  outgoing flow whose FEEL condition holds, or the default flow if none do.
- The inclusive **join** (several incoming flows): each arrival parks as a live
  element instance on the gateway. On each arrival the behavior asks
  `ProcessingContext.TokenCanStillReach`, which is true if any *live* element
  instance sits on an ancestor of the join, **or** any *in-flight*
  element-activating command — the not-yet-processed remainder of this batch's
  queue plus the followups generated so far — targets an ancestor or the join
  itself. While that holds, the arrival waits. When it is false, the join fires:
  it emits `Completed` for every token parked on it and takes the outgoing
  flow(s) once (with inclusive-split semantics, since the gateway may also fork).

Counting in-flight commands is what makes two pass-through branches correct: when
the first join token is processed, the second branch's join-activation command is
still in the batch queue, so the first token waits; the second token then finds
nothing pending and fires once for both. The decision is expressed purely as
element-lifecycle events on the log, so replay — which only applies events — never
re-runs the reachability logic, and a half-arrived join comes back waiting after a
crash.

### Consequences

- **Positive:** Correct for reconverging inclusive splits, including pass-through
  branches; no new persistent state; deterministic replay and crash recovery for
  free; a deadlocked join is a cancellable instance (ADR-0017). The split/join
  and fork behaviors share one flow-taking primitive.
- **Negative / trade-offs accepted:** Reading the processor's command queue from
  the behavior couples the join to a batch-internal detail (guarded behind a
  `ProcessingContext` method). Each join arrival computes the ancestor set and
  scans live element instances and the pending queue — off the token hot path,
  but not free. Correctness targets **acyclic** flow: an inclusive join on a
  cycle (a loop back through the gateway) is not yet modeled.
- **Follow-ups / risks to watch:** Cyclic inclusive joins; a deadlock detector to
  warn a modeler; caching the ancestor set on the compiled process if join-heavy
  models show it in profiles.

## Pros and cons of the options

### Option 1 — reachability over live and in-flight tokens (chosen)
- Good: correct including pass-through branches; no new state; deterministic;
  reuses the element-instance lifecycle.
- Bad: peeks at the command queue; a scan per arrival; acyclic only.

### Option 2 — split tells the join a count
- Good: O(1) join check.
- Bad: needs split/join pairing, which general BPMN does not give; a join can be
  reached from several splits. More machinery than option 1.

### Option 3 — reject inclusive gateways
- Good: no engine change.
- Bad: fails real models; the editor can already draw them.

## Links

- builds on ADR-0024 (parallel gateway — the fixed-count sibling of this join)
- relates to ADR-0004 (compile BPMN to an indexed graph — reachability is compiled)
- relates to ADR-0017 (a deadlocked join is a cancellable instance)
