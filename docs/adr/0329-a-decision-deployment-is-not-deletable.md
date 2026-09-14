# ADR-0329: A decision deployment is not deletable, and what has to be true before it is

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0319](0319-durable-versioned-decision-deployments.md) made a decision a
durable, versioned runtime artifact, and
[ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md) then made a
process able to deploy against one with **nothing bundled for it**: a
`latest`-bound business rule task is resolved at deploy time to the newest decision
deployment providing its id, and that key is written into the process deployment's
own record (`decisionBindings`) and into its compiled form (`PinnedDecisionKey`).

That resolution is permanent by design — it is what makes the binding survive a
restart and a later deployment of the same decision. It also means a deployed
process can now hold a pointer to a record that is not its own and that it carries
no copy of.

Today that is safe for a reason nobody wrote down: **nothing deletes a decision
deployment.** There is no route in the HTTP surface, no MCP tool, no control in the
Console, and deleting an application explicitly leaves deployed definitions alone.
The store is append-only because nothing ever asked it not to be.

An unwritten invariant is one that gets broken by somebody reasonable. The
symmetric feature is obvious and will be asked for — a process deployment is
deletable (`DELETE /api/v1/processes/{key}`), decision deployments accumulate a
version on every editor Deploy, and an operator has no way to clean up. The person
who builds it will be looking at the process delete as the model, and that model is
missing the half that matters here.

The question: **what has to be true before a decision deployment can be deleted,
and how is that stated so it is read at the right moment?**

## Decision drivers

- **Durable before visible (I2).** A pinned key that no longer resolves is a
  business rule task whose job cannot evaluate — discovered at task activation, in
  a running instance, which is the worst place to discover anything.
- **The process delete already has the shape.** It refuses while instances are
  running, and refuses outright for a platform process. The decision delete needs
  the same kind of gate, against a different fact.
- **There is no fallback to fall back to.** A `deployment`-bound task evaluates the
  model under its own process key, which its own record carries. A `latest`-bound
  task pinned *away* from its own key has nothing of its own to return to.
- **A record is only as good as the moment it is read.** An ADR nobody opens while
  writing the route is decoration.
- **Do not build the route speculatively.** Writing a delete nobody asked for, to
  guard a rule nobody has broken, is inventing the risk in order to manage it.

## Considered options

1. **Record the rule now, enforce the absence with a guard test**, and leave the
   route unbuilt.
2. **Build the delete route now, with the guard.**
3. **Record the rule as prose only** — an ADR and a comment.
4. **Decide nothing** — decision deployments are append-only because they are.

## Decision outcome

Chosen option: **1 — the rule is recorded, and a test makes the route impossible to
add without reading it.**

The rule, stated so it can be implemented without re-deriving it:

> A decision deployment may not be deleted while any deployed process definition is
> pinned to its key. A definition is pinned to key *K* when
> `PinnedDecisionKey(decisionId)` returns *K* for any of its latest-bound decision
> references — equivalently, when its persisted record's `decisionBindings` names
> *K*. The refusal is a 409 naming the definitions, exactly as the process delete
> refuses while instances are running.

Two things about that rule are worth stating explicitly, because both are easy to
get wrong from the outside:

- **Superseded versions are as pinned as current ones.** A definition deployed
  while v2 was newest is pinned to v2's key forever; deleting v2 because v3 exists
  would break exactly the instances the versioning was built to protect.
- **Running instances are not the test.** A definition with no instances today can
  be started tomorrow. The process delete can use a live count because deleting the
  definition removes the thing that would be started; here the definition survives
  and keeps its pin.

`api/decisiondelete_guard_test.go` walks the route table and fails if any `DELETE`
appears under `/api/v1/decision-deployments`. Its failure message is the rule and a
pointer to this record. It is not a prohibition — the route may be built — it is a
way of making sure the half that is not obvious is read by the person building it.

### Why not build the delete now

Option 2 is tempting: the guard is the hard part and it is specified above. It is
rejected because the route has no caller. Nobody has asked to delete a decision
deployment; the operational pressure that would shape it — is it one version or a
whole decision, does it need a dry run, does Operations need a "what is pinned to
this" view first — does not exist yet, so the route would be designed against a
guess. The rule is what has to exist now, because it is what a future author would
not think of. The route is what should wait for a reason.

The honest cost is that the rule is unexercised: no test proves the refusal works,
because there is no refusal. The guard test is therefore about the *route's
absence*, which is a weaker thing to assert, and it says so.

### Consequences

- **Positive:** ADR-0327's load-bearing assumption is written down where it will be
  read, instead of holding by accident.
- **Positive:** The rule is specific enough to implement from: the exact accessor,
  the exact record field, the status code, and the two traps.
- **Positive:** The guard test fails on the commit that adds the route, not on the
  incident six months later.
- **Negative / trade-offs accepted:** The rule is untested, because what it governs
  does not exist. A test asserting an absence is a reminder, not a proof.
- **Negative:** An operator still cannot remove a decision deployment, so the store
  grows with every editor Deploy. That is a real gap; it is the reason somebody will
  eventually build the route, and this record is what they should read first.
- **Follow-ups / risks to watch:** A "what is pinned to this decision deployment"
  view would make the gap visible before it is painful, and is most of the guard's
  query. It belongs with the route, not before it.

## Pros and cons of the options

### Option 1 — record the rule, guard the absence *(chosen)*
- Good: the non-obvious half is stated while it is fresh, and delivered to whoever
  adds the route.
- Good: nothing speculative is built.
- Bad: the rule is unexercised until somebody implements it.

### Option 2 — build the delete now
- Good: the rule would be tested rather than described, and the operational gap
  would close.
- Bad: no caller, so its shape (one version? a whole decision? a dry run?) would be
  guessed, and a guessed destructive route is the wrong kind of guess.

### Option 3 — prose only
- Good: cheapest.
- Bad: an ADR is found by someone who goes looking. The person adding a delete
  route is looking at the *process* delete, not at this.

### Option 4 — decide nothing
- Good: nothing to write.
- Bad: it is the current state, and the current state is an invariant that holds by
  accident. ADR-0327 made it load-bearing; leaving it unwritten is the defect.

## Links

- extends [ADR-0319](0319-durable-versioned-decision-deployments.md) — the record this governs
- extends [ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md) — the pin that made the store's append-only nature load-bearing
- relates to [ADR-0063](0063-dmn-decision-binding.md) — the binding that does the pinning
- relates to [ADR-0019](0019-durable-deployments.md) — the sidecar the process delete removes from
- relates to [ADR-0002](0002-single-writer-partition-model.md) — the turn any such delete would happen in
