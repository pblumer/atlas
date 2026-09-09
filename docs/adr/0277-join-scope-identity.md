# ADR-0277: A join synchronizes within its own execution scope

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine maintainers

## Context and problem statement

[ADR-0024](0024-parallel-gateway-join.md) settled how a parallel join
synchronizes: each arriving branch parks as a live element instance on the join
node, and the behavior counts the live element instances *on that node within the
process instance*. When the count reaches the node's `IncomingCount`, it consumes
them all and fires once. No counter, no new event type — the arrival count is
derived from state the engine already keeps, so replay reconstructs it for free.

The derivation is sound. Its **key** is not. `(process instance, node)` is not the
identity of a join; it is the identity of a *place in the diagram*. A
multi-instance subprocess runs the same diagram several times over, concurrently,
inside one process instance: each iteration is its own execution scope, with its
own tokens, its own variables, and its own copy of every node inside it. Two
iterations therefore have two joins that happen to share a node id — and the
count did not distinguish them.

An external architecture review reproduced the consequence. A multi-instance
subprocess with two iterations, each forking at an AND gateway into branches A and
B and re-joining: completing only the two A jobs — one from each iteration, with
neither B anywhere near done — put two element instances on the join node, which
is `IncomingCount`, so the join fired. It consumed a token belonging to the other
iteration and let the process continue past a synchronization point that had never
actually been reached.

The inclusive (OR) join reads the same state through
`TokenCanStillReach`/`ElementInstancesOnNode` and has both halves of the same
confusion: it waits for tokens in sibling iterations that can never arrive, and
when it does fire it consumes every token parked on the node across all
iterations and continues **once**, so the sibling iterations lose their token
outright.

Nothing about this is specific to multi-instance. Any two concurrent executions of
one embedded subprocess — a subprocess reached twice, an event subprocess, a
compensation handler — put two live tokens on the same node ids in the same
process instance. Multi-instance is simply the case that is trivial to construct.

## Decision drivers

- **Correctness of synchronization first.** A join that fires early is worse than
  a join that never fires: an early fire silently continues a process on work that
  did not happen, and consumes a token that belonged to someone else.
- **Determinism (I4/I6).** The decision must stay a function of facts already on
  the log, re-derivable in the same order on replay. No new counter, no new event.
- **No hot-path cost (I1).** The join already scans the instance's element
  instances; the fix must not add an allocation or a second scan.
- **The change must not over-correct.** An inclusive join must still wait for a
  token running *inside* a subprocess on one of its branches.

## Considered options

1. Key the join on `(process instance, flow scope, node)` — add the scope the
   arriving token already carries to the match.
2. Key the join on the multi-instance iteration index when there is one, leaving
   every other case as it is.
3. Go straight to per-incoming-flow counting (OMG BPMN 2.0.2 §13.4), which needs a
   per-flow arrival record and would answer scope identity along the way.

## Decision outcome

Chosen option: **option 1.** `ElementInstancesOnNode` and `TokenCanStillReach`
take the caller's `FlowScopeKey` and match on it alongside the node id — in the
store scan and, for the inclusive join, in the in-flight command scan too. The
three call sites pass `ei.FlowScopeKey`, which is the scope the arriving token has
carried since it was created.

`FlowScopeKey` is already the engine's name for "which execution does this token
belong to": elements at the top level carry the process-instance key, elements
inside an embedded subprocess carry that subprocess's element-instance key, and
elements inside a multi-instance iteration carry the iteration's key (ADR-0077).
Using it here does not introduce a concept — it stops discarding one.

Option 2 was rejected as a special case for a general defect: it would fix the
reproduction and leave the same bug standing for two concurrent activations of an
ordinary subprocess. Option 3 is the right eventual answer to a *different*
question — how many tokens arrived on each flow — and is deliberately kept
separate below; it is a semantics change with its own risk, and this one is a
bug fix that should not wait for it.

**Narrowing does not blind the inclusive join to nested work.** `NodesReaching`
walks sequence flows, and a sequence flow never crosses a scope boundary, so a
node *inside* a subprocess is not in the join's reachability set to begin with.
What keeps the join waiting for it is the subprocess's own element instance, which
sits in the join's scope on a node that does reach the join. The narrowing removes
sibling scopes, which can never arrive, and keeps ancestors-of-the-token, which
can. There is a test for exactly this, because the risk of over-correcting here is
higher than the risk of under-correcting.

### Consequences

- **Positive:** concurrent executions of one subprocess synchronize
  independently. An AND join fires when *its* branches arrive; an OR join waits
  only for its own scope and no longer swallows a sibling iteration's token.
- **Positive:** no new state, no new event, no extra scan — one more field
  compared per row in a scan that already ran, so replay and cost are unchanged.
- **Negative / trade-offs accepted:** ADR-0024's stated key is amended. Anything
  that relied on cross-scope synchronization was relying on a defect, but the
  change is observable: a model whose multi-instance iterations previously
  "worked" by accidentally releasing each other will now park until each iteration
  really completes — which is the correct behaviour and may look like a
  regression to someone who tuned around the old one.
- **Negative:** `ElementInstancesOnNode` and `TokenCanStillReach` gain a
  parameter. They are exported on `ProcessingContext`; both are engine-internal in
  practice (only the gateway behaviors call them), but the signature change is a
  break for anyone embedding a custom behavior.
- **Follow-ups / risks to watch:** per-incoming-flow counting is still owed. Until
  then a join reached twice over the *same* incoming flow — a loop back through a
  join — still counts two arrivals as two different flows, exactly the limitation
  ADR-0024 recorded. Scope identity is necessary for that work and not sufficient
  for it.

## Pros and cons of the options

### Option 1 — key on (instance, flow scope, node) (chosen)
- Good: uses identity the token already carries; no new state; fixes the general
  case, not just multi-instance; one comparison per scanned row.
- Bad: amends an accepted ADR's key; changes two exported signatures.

### Option 2 — special-case the multi-instance iteration
- Good: smallest possible diff for the reported reproduction.
- Bad: leaves the same defect for every other concurrent subprocess execution, and
  adds a rule that has to be remembered rather than one that follows from the
  data.

### Option 3 — per-incoming-flow counting now
- Good: the OMG-correct answer; would subsume this one.
- Bad: a persistent per-flow arrival record and a revision of ADR-0024's core
  decision, carrying real risk, in exchange for delaying a fix for a defect that
  silently continues processes today.

## Links

- amends [ADR-0024](0024-parallel-gateway-join.md) (the join's key, not its
  derivation)
- amends [ADR-0033](0033-inclusive-gateway-join.md) (the same key, in the
  "can a token still arrive?" question)
- relates to [ADR-0077](0077-multi-instance-activities.md) (flow scopes, and the
  body/iteration roles that give each iteration its own key)
- relates to [ADR-0074](0074-embedded-subprocesses.md) (embedded subprocesses and
  the flow scope each one opens)
- relates to [ADR-0086](0086-gateway-conditions-resolve-over-scope-chain.md) (the
  same scope a gateway condition already resolves its variables over)
- reported as F06 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
