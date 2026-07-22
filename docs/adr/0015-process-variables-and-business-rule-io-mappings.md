# ADR-0015: Process variables and business-rule I/O mappings

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Core team

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) added business rule tasks that evaluate a DMN decision through temis, but with an explicit seam: a task fed its decision a *static* input context recorded at deploy time, and surfaced outputs through a worker-side sink rather than writing them anywhere. That was deliberate — Atlas had no process-variable subsystem (it was Milestone 1, unstarted), so there was nothing to read inputs from or write outputs to.

To make business rule tasks actually participate in a process's data flow, we need the first slice of that subsystem: a way to hold named values on a running instance, seed them, read them, and write them back — without breaking the engine's invariants. The `VTVariable` value type and `VariableCreated`/`VariableUpdated` intents were already reserved in the data model; this ADR decides how they work and how a business rule task's input/output mappings ride on top.

## Decision drivers

- Variables must be recoverable state, folded from events by the one `applyToState` (I4), never written directly by a worker (I2, I3).
- No regression to the control-flow hot path's allocation-freedom (I1).
- Deliver a working, testable slice; defer scope/FEEL richness that isn't needed yet.
- Keep the DMN dependency and evaluation off the processor goroutine (ADR-0014).

## Considered options

1. **Worker writes variables directly to the state store.** Rejected outright: violates single-writer (I3) and durable-before-visible (I2).
2. **Full Milestone-1 variable system now** — variable scopes (local vs. propagated), copy-on-write, FEEL input/output expressions. Correct end state, but large and not required to make business rule tasks useful.
3. **Minimal instance-scoped variables + name-based mappings**, with variables flowing through the normal event/command path. Chosen.

## Decision outcome

Chosen option: **minimal instance-scoped variables carried through the event/command path.**

**Storage.** A variable is a `model.VariableValue{ProcessInstanceKey, Name, Value}` — a name scoped to a process instance, with an opaque value (JSON today). Names are runtime data (a decision output key is not known at compile time), so they are *not* interned into the compiled process; they live in the state key `var:<piKey>:<name>` in a new column family. `applyToState` folds `VariableCreated`/`VariableUpdated` into an upsert, identically live and on recovery (I4). When an instance completes or terminates, its variables are removed in one deterministic range delete, so finished instances leave no variable state.

**Getting values in and out of the processor.** A worker never touches state directly. Instead a command may carry `[]model.NamedVariable`:

- **Seeding** — `CreateInstanceWithVariables` carries initial variables; the process-instance activation handler emits a `VariableCreated` per variable, scoped to the new instance, before its elements run.
- **Write-back** — a job completion carries the variables the worker produced (`CompleteJobWithVariables`); the completion handler emits a variable event per entry before the element completes. The job worker protocol (ADR-0007) is thus extended so a `Handler` returns the variables its job produced.

The carrier slice is nil for internally generated followup commands, so the control-flow hot path still allocates nothing (I1).

**Business-rule I/O mappings.** `BusinessRuleTaskDetail` gains `InputMappings` (decision-input name ← process-variable name) and `ResultVariable`. At evaluation the DMN worker builds the input context from the static inputs overlaid with the mapped variables (read from state off the hot path, mapping wins), evaluates, and returns the outputs as the job's result variable — which the processor writes back on completion. In BPMN XML, `<zeebe:calledDecision>` gains `resultVariable`, and an `<atlas:decisionInput>` may name a `variable` (a binding) instead of a `value` (a static constant).

### Consequences

- **Positive:** Business rule tasks now read real instance variables and write their decision outputs back durably; both survive crash recovery (a seeded variable is rebuilt by replaying its `VariableCreated` event). No new invariant pressure — variables are ordinary events through `applyToState`; the hot path is unchanged (confirmed by the existing allocation tests). The job protocol now cleanly carries variables, which future service-task output mapping will reuse.
- **Negative / trade-offs accepted:** Variables are **instance-scoped only** — no local/subprocess scopes or copy-on-write yet (Milestone 1). Mappings are **name-based**, not FEEL expressions, so no transformation or nested extraction. The whole outputs map is stored under the result variable as JSON rather than being unwound into per-output variables. Values are JSON, not a typed/MessagePack representation. `inflightValue` grew by a `VariableValue` (a string + a byte slice); copying it still does not allocate, but the per-event value is larger.
- **Follow-ups / risks to watch:** variable scopes and propagation; FEEL-based input/output mappings; unwrapping single-output decisions; a compact typed value encoding; exposing variables through the query API and web UI.

## Pros and cons of the options

### Worker writes state directly
- Good: no round-trip.
- Bad: breaks single-writer and durable-before-visible. Non-starter.

### Full Milestone-1 variable system now
- Good: final semantics (scopes, FEEL) in one step.
- Bad: large; most of it (scoping, copy-on-write, expressions) isn't needed to make business rule tasks carry data. Slower to land, more to get wrong at once.

### Minimal instance-scoped variables (chosen)
- Good: smallest change that makes I/O mappings real; respects every invariant; reuses the event/command and job paths; recoverable.
- Bad: instance scope and name-based mapping only — a deliberate first slice, not the end state.

## Links

- realizes the input/output-mapping follow-up of [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
- extends ADR-0007 (job completion now carries variables)
- respects ADR-0001/I4 (`applyToState`), ADR-0005/I2 (durable before visible), I1 (hot-path allocation)
- first step toward the Milestone 1 variable/scope work in [`ROADMAP.md`](../../ROADMAP.md)
