# ADR-0279: Topology is compiled, including the join's ancestors

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine maintainers

## Context and problem statement

An inclusive join fires when no token can still arrive at it. Answering that needs
the join's *ancestors* — the nodes from which it is reachable by sequence flows —
and the engine asked for them like this, on every arrival at every such join:

```go
c.TokenCanStillReach(..., cp.NodesReaching(ei.ElementId))
```

`NodesReaching` built a reverse adjacency over the **whole** graph (`[][]int32`,
one slice per node), then walked it with a map and a stack, and returned the map.
Three allocations and an O(V+E) walk per token movement, to recover something that
cannot change: a compiled process is immutable, so its ancestors were the same the
last thousand times somebody asked.

That is both invariants at once. I1 says the token path does not allocate. I5 says
Atlas compiles rather than interprets — topology is exactly what "compile" means
here, and rebuilding it at runtime is interpretation with extra steps. An external
review flagged it as such.

## Decision drivers

- **No allocation on the token path (I1).**
- **Compile it once (I5).** The information is a property of the model.
- **Memory in proportion to what needs it.** A set per node would be O(V²); the
  runtime only ever asks about inclusive joins, and most processes have none.
- **A missing set must not be a quiet wrong answer.** An empty ancestor set at a
  real join reads as "nothing upstream", which fires the join early — the worst
  failure this code has.

## Considered options

1. Precompute a `map[int32]bool` per inclusive join and hand back the stored map.
2. Precompute a **bitset** per inclusive join. (chosen)
3. Memoise lazily on the `CompiledProcess`, on first ask.
4. Precompute for every node.

## Decision outcome

Chosen option: **option 2.** `Builder.Build` computes, once, the ancestor set of
every inclusive gateway with more than one incoming flow, and stores it as a
`NodeSet` — a bitset with a single `Has` operation. `CompiledProcess.NodesReaching`
is replaced by `InclusiveJoinReach`, which is a map lookup returning that set, and
`TokenCanStillReach` takes a `NodeSet` instead of a map.

**Why a bitset over a stored map.** For a process with J joins and V nodes, maps
cost J×V entries at roughly fifty bytes each — half a megabyte for ten joins in a
thousand-node process, per deployed version, held for the life of the deployment. A
bitset is V bits: the same case is about 1.2 KB. Membership is an index and a mask,
which is also faster than hashing an int32. The review's own suggestion was bitsets
weighed against query frequency, and the frequency is per token movement.

**Why not lazily.** A `CompiledProcess` is shared across every instance of a
definition and read from the processor goroutine, so a lazily filled field needs a
mutex or an atomic — a lock on the token path to avoid an allocation on the token
path. Building at compile time costs one pass over the graph per deployment.

**Why the name changed.** `NodesReaching` sounded like a general graph query and
answered for any node. The set is now computed for exactly the nodes that need it,
so a call about anything else returns the empty set — and an empty set at a real
join is the early-fire failure above. The name says which nodes it answers for, and
a compiler test asserts that every inclusive join in a built process has one.

Option 4 was rejected on memory: O(V²) bits for a query the runtime makes about a
handful of nodes.

### Consequences

- **Positive:** an arrival at an inclusive join allocates nothing for reachability
  and does no graph walk, pinned by an allocation test alongside the engine's other
  I1 tests.
- **Positive:** the memory is bounded and paid at deploy: nothing at all for a
  process with no inclusive join, which is most of them.
- **Negative / trade-offs accepted:** `NodesReaching` is gone and its replacement
  has a different signature and a narrower contract. It was engine-internal in
  practice — one call site — but it was exported, so this is a break.
- **Negative:** the set is keyed by node id in a map on the CompiledProcess. A map
  lookup per arrival is not nothing; it is one hash of an int32 against three
  allocations and a graph walk, and it stays off the *per-node* path because the
  lookup happens once per arrival, not once per candidate.
- **Follow-ups / risks to watch:** `NodeSet` has no exported constructor, so it can
  only come from a compiled process. That is deliberate — a set built at runtime
  would be the thing this record removes — but it means a test that wants one builds
  a process, which the engine's own tests now do.

## Pros and cons of the options

### Option 1 — a stored map per join
- Good: no change to the call site's types.
- Bad: J×V map entries held for the life of the deployment, and hashing per query.

### Option 2 — a bitset per join (chosen)
- Good: V bits per join; membership is an index and a mask; immutable and shareable.
- Bad: a new type crossing the compiler/engine boundary, and a signature change.

### Option 3 — lazy memoisation
- Good: pays only for joins actually reached.
- Bad: a shared, concurrently read structure needs synchronisation — a lock on the
  path whose allocation we came to remove.

### Option 4 — precompute for every node
- Good: one rule, no "which nodes have a set" question.
- Bad: O(V²) bits for a query about a handful of nodes.

## Links

- honours [ADR-0004](0004-compile-bpmn-to-indexed-graph.md) (topology lives in the
  compiled form) at a place that had drifted from it
- relates to [ADR-0033](0033-inclusive-gateway-join.md) (the join whose wait
  condition this feeds)
- relates to [`invariants.md`](../architecture/invariants.md) I1 and I5
- reported as F14 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
