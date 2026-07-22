# ADR-0024: Parallel gateway join synchronization

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

The parallel (AND) gateway forks a token onto every outgoing flow and joins by
waiting until a token has arrived on *each* incoming flow, then firing the
outgoing flow once. The fork is trivial in Atlas — it is `takeOutgoingFlows`,
which already activates every outgoing target. The join is the real question: it
must *synchronize* several concurrent branches, which means remembering how many
of its incoming flows have been taken so far, across batches, and doing so in a
way that replays deterministically (invariants I4/I6) and survives a crash.

Atlas has no explicit "token on a flow" entity; a token is an active element
instance. When a branch reaches the join, the engine activates an element
instance on the join node — so N branches reaching a join produce N element
instances on it. The join must collapse those N into one continuation, exactly
once.

## Decision drivers

- **Deterministic replay (I4/I6).** The synchronization decision must be captured
  by facts already on the log (which element instances exist, and the
  Completed/Activating events emitted), so recovery reconstructs the same state
  without re-counting.
- **No new persistent counter to keep in sync.** Prefer deriving arrival count
  from state Atlas already maintains over adding a counter column family with its
  own lifecycle (increment, read, reset, delete) driven by new event types.
- **Crash safety mid-join.** A join half-arrived when the process crashes must
  come back still waiting, and fire when the remaining branches arrive.

## Considered options

1. **Count live element instances on the join node.** Each arriving branch parks
   as an element instance on the join (stays Activated). On each arrival, count
   how many instances currently sit on the node (through the in-flight
   transaction, so the just-arrived one is included). When the count equals the
   join's incoming-flow count, consume them all (emit Completed for each) and fire
   the outgoing flow once.
2. **A persistent arrival counter** in a new column family, keyed by (process
   instance, join element), incremented via a dedicated join-arrival event and
   reset on fire.
3. **Tokens as first-class entities** on sequence flows, consumed/produced by the
   join.

## Decision outcome

Chosen option: **"Count live element instances on the join node."**

- The compiler records each node's incoming-flow count (`CompiledNode.IncomingCount`).
- A parallel gateway with one incoming flow is a fork: it fires immediately,
  producing a token on every outgoing flow.
- A parallel gateway with several incoming flows is a join. Each arriving branch
  activates an element instance on the node and *waits* (stays Activated). The
  behavior counts the live element instances on the node within the instance,
  read through the in-flight transaction (`Tx.ElementInstancesOfProcess`), so the
  arrival being processed is counted alongside its already-committed siblings.
  While fewer than `IncomingCount` have arrived, the branch simply parks. When the
  last arrives, the behavior emits `Completed` for every parked instance on the
  node (consuming all the tokens) and fires the outgoing flow(s) once.

Because the decision is expressed entirely as element-lifecycle events already on
the log, replay reconstructs the identical state without ever re-running the
counting logic (`applyToState` only applies events). A crash mid-join leaves the
arrived branches as parked element instances on the log, so recovery brings the
join back still waiting.

### Consequences

- **Positive:** No new column family or event type; synchronization rides the
  existing element-instance lifecycle and replays deterministically. Fork, join,
  and a combined join+fork gateway all fall out of one behavior. A deadlocked join
  (e.g. one placed after an exclusive split) is a live instance the operator can
  now cancel (ADR-0017 termination).
- **Negative / trade-offs accepted:** Each arrival at a join does a prefix scan of
  the instance's element instances (plus a decode per row) — off the hot token
  path, but not free. A join that is reached more times than it has incoming flows
  (e.g. a loop back through it) is not yet modeled correctly; today's target is
  acyclic fork/join. No token-count-per-flow, so a single incoming flow feeding
  two tokens (uncommon) is not distinguished.
- **Follow-ups / risks to watch:** Cyclic/looping joins; inclusive (OR) join
  semantics, which need upstream reachability analysis; deadlock detection to warn
  a modeler rather than silently parking forever.

## Pros and cons of the options

### Option 1 — count live element instances (chosen)
- Good: reuses the element-instance lifecycle; deterministic replay for free;
  crash-safe; one behavior for fork and join.
- Bad: a prefix scan per arrival; no per-flow token multiplicity.

### Option 2 — persistent arrival counter
- Good: O(1) arrival update; explicit.
- Bad: a new column family and a new event type to increment/reset it, more state
  to keep consistent on replay for no functional gain over option 1.

### Option 3 — first-class tokens on flows
- Good: models BPMN token semantics directly, including multiplicity and loops.
- Bad: a large new runtime concept (tokens, their identity and storage) that the
  element-instance model already covers for every other element. Disproportionate.

## Links

- relates to ADR-0004 (compile BPMN to an indexed graph — incoming count is compiled)
- relates to ADR-0002 (single-writer partition — the counting reads the batch's own tx)
- relates to ADR-0017 (a deadlocked join is a cancellable instance)
