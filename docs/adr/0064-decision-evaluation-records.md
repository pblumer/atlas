# ADR-0064: Durable decision-evaluation records for debugging

- **Status:** Accepted
- **Date:** 2026-07-25
- **Deciders:** Core team

## Context and problem statement

A business rule task delegates a decision to DMN (ADR-0014), reads its inputs from
process variables and writes its result back as one (ADR-0039), against a bound
model version (ADR-0063). What the engine keeps of that evaluation is only the
result: the DMN worker builds the input context, evaluates the decision off the
processor goroutine, and writes the output variable back on completion — and then
discards everything else. The input context is never stored, and the temis engine's
**trace** (which decision tables ran, which rules matched, and why — temis produces
it on demand via `WithTrace`) is never even requested.

That makes decisions hard to use and to test. An operator looking at an instance in
Operations sees the *output* variable a decision produced but cannot see **what
inputs it was given** or **how it reached that answer** — neither while the instance
runs nor, more importantly, after it has finished. When a decision routes a process
the "wrong" way, there is no way to inspect whether the inputs were wrong or the
rules were. The question this ADR answers is **how to capture a decision's inputs,
outputs, and reasoning as durable, inspectable history** without violating the
engine's invariants — in particular without evaluating DMN or serialising a trace on
the single-writer processor path (I1), and without a side effect or re-evaluation
inside `applyToState` (I4/I6).

## Decision drivers

- Reuse the job path and its durability/recovery properties, exactly as ADR-0039
  did for output variables — don't invent a second completion mechanism.
- Keep DMN evaluation and trace serialisation off the processor hot path (I1); keep
  `applyToState` a deterministic function of already-decided data (I4/I6).
- Survive recovery: a decision recorded before a crash must reappear after replay,
  rebuilt from the log alone, without re-running the decision.
- Work live and post-mortem through the same surface: the record must be queryable
  while the instance runs and after it has completed.

## Considered options

1. **Re-evaluate on demand.** Store nothing; when an operator asks, re-run the
   decision through temis with `WithTrace` against the instance's current (or a
   step's) variables.
2. **Record the evaluation as a durable event on completion.** The worker requests
   the trace during its off-path evaluation and rides the inputs/outputs/trace back
   on the job completion; the processor freezes them into a new history record when
   it folds the completion, exactly as it already freezes output variables.
3. **Write the evaluation into a process variable.** Reuse the existing variable
   machinery by storing the inputs/outputs/trace under a reserved variable name.

## Decision outcome

Chosen option: **option 2 — a durable decision-evaluation record on completion.**

- **New record type.** `VTDecisionEvaluation` / `IntentDecisionEvaluated` carry a
  `DecisionEvaluationValue`: the owning process instance and business rule task
  element, the process definition and element id (to map back onto the diagram), the
  decision id, and the input context, outputs, and trace as canonical JSON. It is
  append-only history keyed under the process instance, in the same `(scope, ts,
  pos)` shape as the variable-snapshot timeline (ADR-0048) — one record per
  evaluation, never overwritten, so a scope-wide scan yields every decision an
  instance made in order.
- **Capture off the hot path.** The DMN worker already evaluates off the processor
  goroutine; it now requests temis's trace there (temis's `WithTrace` is opt-in and
  allocating, its default path being allocation-free — but off-path here so it costs
  nothing on the run loop) and builds the record from the inputs it assembled and the
  outputs and trace it got back.
- **Freeze on completion.** The job completion protocol, already widened once from
  "a bare signal" to "carries output variables" (ADR-0039), is widened again to
  "may carry a decision evaluation." `CompleteJobWithDecision` rides the record on
  the completion command; when `handleJobCompleted` folds it, it re-stamps the
  record's instance/element keys from the authoritative job and appends an
  `IntentDecisionEvaluated` event **before** the element completes. The value is
  frozen into the event, so replay re-applies it without re-running the worker
  (I4/I6) — identical to how output variables replay. Because the record rides the
  completion, at-least-once job execution records it exactly once (a re-run's second
  completion is a no-op on an already-completed job).
- **Surfaced in Operations.** `GET /api/v1/instances/{key}/decisions` serves an
  instance's evaluations — inputs, outputs, trace, and diagram element — live and
  after completion. The live viewer badges each decided business rule task and, on
  click, shows the inputs it saw, the outputs it produced, and a rules-fired view of
  the trace.

The channel is general (a completion may carry outputs, a decision, or both), but
only the DMN/temis workers set the decision; every other worker is unchanged.

### Consequences

- **Positive:** A decision is now debuggable end to end — an operator sees what a
  business rule task was given and how it decided, both live and long after the
  instance finished, backed by durable history rather than a re-run. No change to
  the WAL format's framing or `applyToState`'s contract: the record reuses the same
  event/apply/history machinery as variable snapshots and message-flow history.
  Recovery is inherited — the evaluation replays like any other event. Remote
  decisions (ADR-0050) record inputs and outputs with an empty trace, since the
  connector returns none.
- **Negative / trade-offs accepted:** A per-decision history record adds write
  volume and unbounded retention, like the other history families (ADR-0017/0022) —
  a retention policy is future work. The trace is captured at evaluation time, so it
  reflects the inputs as of when the job ran, consistent with ADR-0039. Failed
  evaluations are not recorded here — a failure raises an incident (ADR-0061), which
  is the existing surface for "why did this not complete."
- **Follow-ups / risks to watch:** retention/compaction for the new family;
  surfacing the record in the step-by-step replay timeline (ADR-0046) alongside the
  variable snapshots it already lines up with by log position; a trace channel for
  remote temis decisions if the connector protocol grows one.

## Pros and cons of the options

### Re-evaluate on demand
- Good: stores nothing; no new record or write volume.
- Bad: re-evaluation is a side effect and non-trivial work; the inputs at evaluation
  time are gone (variables may have changed), so it cannot faithfully reproduce what
  actually happened — it shows what *would* happen now. Fails the "look up what
  happened" requirement. Rejected.

### Record on completion (chosen)
- Good: keeps all DMN and trace work off the hot path; reuses the job path and its
  recovery; the record is a faithful, frozen fact; queryable live and post-mortem.
- Bad: a small widening of the completion protocol; a new history family with
  unbounded retention for now.

### Write into a process variable
- Good: reuses the variable store and its UI with zero new machinery.
- Bad: pollutes the business variable namespace with debug data a downstream FEEL
  expression could accidentally read; a trace is far richer than a scalar and
  deserves its own shape and rendering. Conflates diagnostics with process data.
  Rejected.

## Links

- extends the job worker protocol of [ADR-0007](0007-job-worker-protocol.md) and the
  output-carrying completion of [ADR-0039](0039-dmn-io-variable-mappings.md)
  (completion may now also carry a decision evaluation)
- builds on the DMN integration of [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
  and the binding of [ADR-0063](0063-dmn-decision-binding.md); remote decisions per
  [ADR-0050](0050-temis-decision-connector.md) record an empty trace
- keys the history in the `(scope, ts, pos)` shape of [ADR-0048](0048-per-step-variable-snapshots.md);
  respects I1/I4/I6
