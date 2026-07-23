# ADR-0039: Input/output variable mappings for business rule tasks

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Core team

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) landed business rule tasks
as a vertical slice: a DMN decision is compiled at deploy time, evaluated off the
processor goroutine by an in-process worker through the job path, and the token
advances on completion. But because the variable subsystem did not exist yet, that
slice left two deliberate stand-ins:

- **Inputs** were a *static* JSON context recorded at deploy time — a decision
  could not read the running instance's data.
- **Outputs** were handed to a caller-supplied *sink*; in the server the sink was
  `nil`, so a decision's result was evaluated and then **discarded**. A downstream
  gateway could not route on it.

ADR-0014 named this the explicit seam Milestone 1 would replace once variables
existed. [ADR-0037](0037-structured-json-variables.md) delivered the variable
subsystem (scalars and structured JSON, read by FEEL, written by script tasks).
The question this ADR answers is **how a business rule task reads its inputs from
process variables and writes its result back as one**, without violating the
engine's invariants — in particular without evaluating DMN or FEEL-input mappings
on the single-writer processor path (I1), and without a side effect inside
`applyToState` (I4).

## Decision drivers

- Reuse the existing job path and its durability/recovery properties (ADR-0007),
  as ADR-0014 already does — don't invent a second completion mechanism.
- Keep DMN and its input-expression evaluation off the processor hot path (I1),
  and keep `applyToState` a deterministic function of already-decided data (I4/I6).
- Make the wiring authorable in standard executable BPMN (Zeebe extensions), so a
  model expresses it without Atlas-specific ceremony.

## Considered options

1. **Evaluate input mappings in the behavior at activation** (on the processor),
   serialize the resulting context, and carry it on the job.
2. **Evaluate input mappings in the worker** off the processor goroutine, reading
   the instance's committed variables; **carry the decision result back as output
   variables on the `CompleteJob` command**, written by the processor when the
   completion is folded.

## Decision outcome

Chosen option: **option 2 — evaluate in the worker, complete with output
variables.**

- **Inputs (read side).** A business rule task's decision inputs come from two
  layers the worker merges: constant `<atlas:decisionInput>` values as a base, and
  Zeebe io-mapping inputs (`<zeebe:ioMapping><zeebe:input source="=…" target="…"/>`)
  as the variable-driven layer. Each mapping's `source` is a FEEL expression
  **compiled once at deploy time** (I5); the DMN worker evaluates it against the
  instance's committed variables when the job runs — off the processor goroutine
  (I1) — and a mapping overrides a static input of the same name.

- **Outputs (write side).** `CompleteJob` gains an optional list of output
  variables. The DMN worker returns the decision's result as the process variable
  named by `<zeebe:calledDecision resultVariable="…">`; the runner rides those
  variables along on the completion command. When the processor folds the
  completion (`handleJobCompleted`), it writes them into the job's process instance
  scope as `VariableCreated` events **before** the element completes, so a
  downstream gateway can route on the result. The values are frozen into events, so
  replay re-applies them without re-running the worker (I6) — exactly how instance
  creation seeds its start variables.

This extends the ADR-0007 job protocol from "completion is a bare signal" to
"completion may carry the worker's output variables," the in-process analogue of
Zeebe's `CompleteJob(variables)`. The job harness exposes it as a distinct
`HandleWithOutput` registration so output-less workers (service tasks, the clio
connector) are unchanged.

### Consequences

- **Positive:** A business rule task now participates in data flow: it reads live
  instance variables and its result drives downstream routing. No change to the WAL
  format, the record set, or `applyToState`'s contract — output variables reuse the
  existing `VariableCreated` event. Crash recovery is inherited: the completion's
  variable events replay like any other. The output-variable channel is general
  (not DMN-specific), so service tasks can adopt it when an external worker returns
  variables.
- **Negative / trade-offs accepted:** The worker reads variables from committed
  state, so a decision sees the instance as of job execution, not a frozen
  activation snapshot — acceptable and consistent with how any job worker fetches
  variables. Numbers routed through a decision input arrive as JSON numbers
  (float64), matching the existing static-input behavior; high-precision decimals
  are not yet preserved across the temis boundary.
- **Follow-ups / risks to watch:** explicit `<zeebe:output>` mappings (beyond a
  single `resultVariable`); output variables for service-task workers over the
  future gRPC transport (ADR-0007 Milestone 4); preserving decimal precision
  through temis.

## Pros and cons of the options

### Evaluate input mappings in the behavior at activation
- Good: inputs frozen at a single activation instant.
- Bad: runs FEEL input evaluation on the single writer and would have to carry an
  arbitrary context on the job record, growing the durable format — against I1 and
  the minimal-record goal. Rejected.

### Evaluate in the worker, complete with output variables
- Good: keeps all DMN/FEEL-input work off the hot path; reuses the job path and its
  recovery; output variables are a general, durable extension over existing events.
- Bad: a small widening of the job-completion protocol; the decision reads
  committed (not activation-snapshot) variables.

## Links

- completes the input/output seam left open by [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
- extends the job worker protocol of [ADR-0007](0007-job-worker-protocol.md) (completion may carry output variables)
- builds on the variable subsystem of [ADR-0037](0037-structured-json-variables.md); respects I1/I4/I5/I6
- relates to ADR-0008/0015 (FEEL compiled at deploy time)
