# ADR-0086: Gateway conditions resolve over the scope chain

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas resolves a variable name up the **scope chain**: an activity nested in an
embedded subprocess (or a multi-instance body) sees its enclosing scope's
variables first, falling back outward to the process root, nearest scope winning
([ADR-0068](0068-task-io-variable-mappings.md), [ADR-0074](0074-embedded-subprocesses.md),
[ADR-0077](0077-multi-instance-activities.md)). Script tasks, I/O mappings, and —
since [ADR-0084](0084-csv-batch-validation.md) — the DMN business rule worker all
read this way, via `bindInputsChain` / `ResolveVariable`.

**Exclusive and inclusive gateways were the exception.** Their flow conditions
were evaluated against the **process-instance root scope only**
(`bindInputs(c, cond.Inputs(), ei.ProcessInstanceKey)` in `selectExclusiveFlow`
and `takeInclusiveOutgoing`). A gateway placed *inside* a subprocess therefore
could not branch on a variable produced inside that subprocess — the variable
lives in the subprocess/iteration scope, not the root, so the condition read FEEL
null and the gateway always fell to its default.

This surfaced building the CSV batch-validation correction loop (ADR-0084): a
per-row `businessRuleTask` inside a multi-instance subprocess writes a `verdict`
into its iteration scope, and the natural next step — an `exclusiveGateway` that
routes valid rows onward and invalid rows to a correction user task on `=verdict.valid`
— could not read `verdict`. The question: **should a gateway condition see the
scope it lives in, like every other expression in the engine?**

## Decision drivers

- **Consistency.** Every other FEEL expression in the engine resolves up the scope
  chain; a gateway condition reading only the root is a surprising exception.
- **Correctness (Camunda-faithful).** A gateway inside a subprocess should branch
  on that subprocess's data — the standard BPMN/Camunda semantics.
- **Enables in-scope loops.** "Repeat this activity until a locally-computed
  condition holds" (the correction loop) needs the gateway to see the local result.
- **Determinism / replay.** The change must not affect how conditions replay.

## Considered options

1. **Resolve gateway conditions over the scope chain** from the gateway's own flow
   scope (`bindInputsChain(c, cond.Inputs(), ei.FlowScopeKey)`). (Chosen.)
2. **Leave gateways root-only; work around it in the model** — compute a
   root-scope flag and loop at the process root, reshaping collections in FEEL.
3. **Add explicit input mappings to gateways** — a per-gateway `zeebe:ioMapping`
   that lifts local variables the condition needs.

## Decision outcome

Chosen option: **Option 1.** `selectExclusiveFlow` and `takeInclusiveOutgoing` now
bind a condition's inputs with `bindInputsChain(c, cond.Inputs(), ei.FlowScopeKey)`
— the same scope-chain resolver script tasks and I/O mappings use — instead of the
single-scope `bindInputs(..., ei.ProcessInstanceKey)`.

For a **top-level** gateway this is a no-op: its `FlowScopeKey` *is* the
process-instance key, and the chain from there reads exactly the root scope, as
before. For a gateway **inside a subprocess or a multi-instance body**, the
condition now resolves its names starting at that scope and walking outward
(nearest wins), so it can branch on locally-produced data.

### Consequences

- **Positive:** gateways read the scope they live in, matching every other engine
  expression and Camunda semantics; a per-row "validate → gateway → correct → loop"
  inside a multi-instance subprocess is now expressible (ADR-0084 Slice 3).
- **Negative / trade-offs accepted:** a gateway inside a subprocess that references
  a name which *also* exists at the root now reads the **inner** binding where it
  previously read the root one. This is the correct shadowing rule and matches
  every other read path, but it is a behavior change for that (contrived) overlap;
  no existing test or example relied on it.
- **Neutral (invariants intact):** conditions are still pure evaluations of durable
  variable state; which branch a token takes is captured by which element
  activates (never re-evaluated on replay, invariant I6), so recovery is unchanged.
  The resolver runs on the processor goroutine over live state exactly as before —
  no new allocation pattern, no cross-scope write.

## Pros and cons of the options

### Option 1 — resolve over the scope chain
- Good: consistent with the whole engine; correct BPMN semantics; unlocks in-scope
  loops; a two-line change reusing the existing resolver; no replay impact.
- Bad: the inner-shadows-outer behavior change for a name present at both scopes.

### Option 2 — root-only, work around in the model
- Good: no engine change.
- Bad: pushes intricate index-tracking / collection-reshaping FEEL into every model
  that wants an in-subprocess decision; the gateway stays inconsistent with the
  rest of the engine; the "surprise" that bit us stays for the next author.

### Option 3 — explicit input mappings on gateways
- Good: opt-in, no change to existing gateways.
- Bad: a new authoring surface and compile path for what should be automatic; the
  gateway *still* wouldn't read its scope without ceremony; more moving parts than
  the one-line resolver swap.

## Links

- extends ADR-0068 (task I/O variable mappings — scope-chain resolution) to gateways
- unblocks ADR-0084 (CSV batch validation — the per-row correction loop, Slice 3)
- relates to ADR-0074 (embedded subprocesses), ADR-0077 (multi-instance activities)
