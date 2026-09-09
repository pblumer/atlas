# ADR-0290: A join counts tokens per incoming flow

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-08
- **Deciders:** Atlas engine maintainers

## Context and problem statement

[ADR-0024](0024-parallel-gateway-join.md) chose to derive a parallel join's arrival
count from the live element instances parked on the join node, rather than keeping a
counter. The derivation is what makes the join replay for free, and it stays. What it
counted was an approximation, and ADR-0024 said so in its own consequences:

> No token-count-per-flow, so a single incoming flow feeding two tokens (uncommon)
> is not distinguished.

It is not uncommon. Any fork whose branches rejoin through a merge before the join
produces it, and so does a loop back through the same join. When it happens the join
fires on two tokens from *one* branch while the other has not arrived — the one thing
a parallel join exists to prevent — and it then consumes every token on the node, so
the surplus disappears with it. Half the modelled work vanishes and the instance
reports success.

[ADR-0277](0277-join-scope-identity.md) corrected the join's *key* (which execution a
token belongs to). This is the other half of the same finding: the join's *unit of
counting*.

OMG BPMN 2.0.2 §13.4 is precise about both:

> The Parallel Gateway is activated if there is at least one token on each incoming
> Sequence Flow.

and it consumes exactly one token from each, leaving the rest.

## Decision drivers

- **Correctness before economy.** A join that fires early continues a process on work
  that has not happened.
- **Keep ADR-0024's derivation.** The decision must stay a function of facts already
  on the log — no counter, no new event, replay unchanged (I4/I6).
- **No hot-path allocation (I1).** The join already scans; the change must not add an
  allocation per arrival.
- **Surplus is not scrap.** A token that cannot be paired belongs to the next firing.
- **A rule an inclusive join can share**, since it has the same shape with a
  different quantifier.

## Considered options

1. Count distinct incoming flows among the parked tokens, using the `SourceFlowId`
   each token already carries. (chosen)
2. Keep a per-flow arrival counter in state, incremented by a new event.
3. Precompute each join's incoming flow ids in the compiler and check them off.
4. Leave it; ADR-0024 recorded the limitation.

## Decision outcome

Chosen option: **option 1.** An element instance already records the sequence flow
that created it — `SourceFlowId`, set by `activateElement` at every flow-taking site.
A join's waiting tokens therefore already say which flow each came in on, and nothing
new has to be written down.

- `ArrivalsOnNode` returns the tokens waiting on the node in one scope, oldest first,
  each with its flow. Oldest-first is the index's own order (element-instance keys
  ascend with minting), which makes the join first-in-first-out and its choice a pure
  function of state rather than of scan order.
- `OldestPerFlow` reduces those to one token per distinct flow — the set a firing
  consumes.
- The **parallel** join fires when that set covers every incoming flow
  (`len(set) == IncomingCount`), consumes exactly it, and leaves the rest parked. A
  set can only be completed by an arrival, so checking once per arrival is both
  sufficient and complete.
- The **inclusive** join fires when nothing more can arrive, and then repeats: one
  token per *occupied* flow per firing, until the node is empty. Its surplus must not
  park, because it would be waiting for an arrival that can never come — the one
  place the two gateways differ, and they differ because their trigger differs. The
  repeat terminates by construction: each firing consumes at least one waiting token
  and nothing can arrive while it runs, because an activation is a queued command and
  not an immediate one, so the tokens waiting at the start bound the firings.
- Both joins hand their outgoing flows a **continuation**: a fresh token whose lineage
  is the token consumed on the flow the arrival came in on. The parallel join already
  did this; the inclusive one now does too, where before it carried the triggering
  arrival's own token id through however many firings it set off. It has to, because
  a second firing consumes tokens that arrival is not among.

**Why not a counter (option 2).** It is the option ADR-0024 rejected, for reasons
that still hold: a new column family and a new event type to increment and reset, and
more state to keep consistent on replay, for something the element instances already
know. Option 3 (compiled incoming-flow ids) would let the join name the flow it is
missing, which is a better *diagnostic*; it is not needed for the decision, because a
token's `SourceFlowId` is by construction one of the node's incoming flows, so
counting distinct ones against `IncomingCount` is exact. It stays available if a
deadlock report ever wants to say *which* branch is missing.

`ElementInstancesOnNode` had no other caller and is gone.

### Consequences

- **Positive:** a parallel join now synchronizes what BPMN says it synchronizes.
  Two tokens on one flow no longer stand in for the other, and the leftover survives
  to be paired with the next arrival.
- **Positive:** no new state, no new event, and no extra scan — the same walk, one
  field more per row and a linear pass over a set the size of the incoming-flow
  count. Pinned by an allocation test alongside the engine's other I1 tests.
- **Positive:** the inclusive join stops swallowing a surplus, which is the same
  defect in its own idiom.
- **Negative / trade-offs accepted:** behaviour changes for existing models, and in
  the direction of *less* progress. A model that quietly relied on a join firing on
  two tokens from one branch will now park instead — a deadlocked join, visible and
  cancellable (ADR-0090), where before it silently continued. That is the correction,
  and it will look like a regression to whoever tuned around the old behaviour.
- **Negative:** the join's decision now depends on `SourceFlowId`, which migration
  rewrites element ids around (ADR-0162). A token migrated onto a join carries the
  flow id it arrived on in the *old* definition. That was already true of the
  element-instance record; it now feeds the join's arithmetic, and it deserves a
  look when migration next changes.
- **Negative:** two more reused buffers on the processor. They exist because an
  inclusive join re-scans the node while still holding the set it is consuming.
- **Negative:** an inclusive join's continuation is a new token id where it used to
  be the arriving one. Lineage and the execution budget (ADR-0272) carry over, and a
  parallel join has always worked this way, but anything reading token identity
  across an OR join sees a different id than before.
- **Follow-ups / risks to watch:** a deadlocked parallel join is now a state a model
  can reach more often than before. Naming the missing branch — which option 3 would
  make cheap — would turn "this instance is stuck" into "this instance is waiting for
  the flow from X", and that is worth doing before an operator meets it.

## Pros and cons of the options

### Option 1 — count distinct flows from SourceFlowId (chosen)
- Good: exact; uses a field every token already carries; no new state or event;
  replay unchanged; one rule serves both gateways.
- Bad: leans on `SourceFlowId` being meaningful, which migration complicates.

### Option 2 — a per-flow counter in state
- Good: O(1) per arrival; explicit; could name the missing flow.
- Bad: a new column family and event type to keep consistent on replay, for
  information the element instances already hold — ADR-0024's own reasoning.

### Option 3 — compiled incoming-flow ids
- Good: lets the join say which branch is missing.
- Bad: not needed for the decision itself; a diagnostic wearing a decision's clothes.

### Option 4 — leave it
- Good: no change; the limitation is documented.
- Bad: the documented limitation is a join that fires without one of its branches and
  discards the tokens it did not use.

## Links

- supersedes the counting rule in [ADR-0024](0024-parallel-gateway-join.md); its
  derivation, its replay argument and its "no new counter" decision are unchanged
- amends [ADR-0033](0033-inclusive-gateway-join.md) the same way, for the gateway
  whose trigger differs but whose consumption does not
- completes [ADR-0277](0277-join-scope-identity.md), which corrected the join's key
- relates to [ADR-0090](0090-bulk-terminate-instances.md) (a deadlocked join is an
  instance an operator can terminate)
- touches [ADR-0162](0162-process-instance-migration.md), which remaps element ids and
  therefore the `SourceFlowId` this rule now reads
- reported as F06 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
