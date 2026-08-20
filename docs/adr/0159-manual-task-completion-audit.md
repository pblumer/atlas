# ADR-0159: Auditable manual task completion

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

An instance can park on a task that will never succeed here: a connector refuses the
call, an external system is unreachable, or the work was simply carried out
out-of-band — the account really was created, the mail really was sent. Retrying
(ADR-0061's *Resolve & retry*) only repeats whatever cannot work, and correcting the
variables (ADR-0095/0098) does not move the token. The operator's remaining option is
to finish the task by hand so the process can continue.

`POST /api/v1/jobs/{key}/complete` already did that. It was, however, indistinguishable
from a worker completing the job: the completion produced exactly the same events, so
nothing in the timeline, the replay, or the log said that a *person* had forced the
step, let alone why. ADR-0098 made an operator's variable corrections attributable;
the act of forcing a step was not.

That is the gap this ADR closes. Manual completion is the most consequential
intervention Atlas offers — it asserts that work the engine could not verify did in
fact happen — so it is exactly the one that must be accountable.

## Decision drivers

- **Accountability.** Forcing a step must leave a durable, inspectable "who / when /
  which element / why" record. A completed step that a person forced must never read
  as one the engine drove.
- **A reason is not optional.** "Who and when" without "why" does not answer the
  question asked months later. The justification is what makes the record useful.
- **Invariant integrity.** Attribution must be a persisted fact rebuilt by replay
  (invariants I4/I6), never a side write, and must not touch the token-movement hot
  path (I1). Element ids stay interned and are never written to the log as text (I5).
- **Reuse the shape that works.** ADR-0098 already solved the sibling problem for
  variables; this should be recognisably the same mechanism, not a second idiom.

## Decision

Add `VTOperatorAction` / `IntentOperatorActed` and a matching `OperatorActionValue`:
an append-only history record keyed under its process instance, exactly like the
ADR-0098 variable audit.

```go
type OperatorActionValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64 // the element acted on; the id is resolved from it (I5)
	JobKey             uint64
	Kind               OperatorActionKind // "completeJob" today, a byte on the log
	Actor              string             // "" when auth is off / unidentified
	Reason             string             // required by the surfaces that mint these
}
```

- `Processor.CompleteJobManually(jobKey, actor, reason, outputs...)` completes the job
  exactly as `CompleteJob` does and additionally emits the audit event, gated by an
  explicit `Manual` flag on the command — never inferred from a non-empty reason, so a
  worker's completion can never mint an operator-action record and a manual one is
  attributed even if the reason were somehow empty.
- `POST /api/v1/jobs/{key}/complete` **requires** a non-blank `reason` (400 otherwise)
  and takes the actor from the authenticated principal. The Operations UI offers
  *✓ Complete manually…* on every incident row, with the reason mandatory in the dialog
  as well, so the requirement is visible before the request rather than as a rejection
  after it. Optional output variables entered there are written as the job's result.
- The instance timeline attaches the record to its element's step (`manual`), and the
  replay renders it as a *Completed manually* block on the step's Details tab.

The action kind is a byte with a closed vocabulary rather than free text, leaving room
for the other interventions (cancel, resolve) to join the same record later without a
second mechanism.

### Variables before and after a step

The same change adds `variablesAfter` to a timeline step: the variable fold at the
element's *completion* position, where `variables` is the fold at its activation.
A task that writes its result on completion (a job's outputs, an output mapping) showed
nothing under "as of activation", so the replay reported "the element has no variables"
for an element that plainly produced some. The Variables tab now offers **Input** and
**Output** for a finished element and marks the values the element itself wrote.

This is not attribution, but it is the same question in a different register — *what
did this step actually do* — and it is what makes a manually completed step reviewable:
the reason says why it was forced, the output says what was asserted.

## Consequences

**Positive.** A forced step is permanently distinguishable from an engine-driven one,
with who, when, and why, rebuilt from the log on recovery like every other fact.
Manual completion becomes a safe, reviewable operator tool rather than an untracked
back door. The Variables tab stops reporting "no variables" for productive elements.

**Negative / accepted.**

- **Breaking API change.** `POST /jobs/{key}/complete` now rejects a body without a
  reason (400). Existing callers — the `atlas_complete_job` MCP tool included — must
  pass one. This is deliberate: an unexplained manual completion is precisely what
  this ADR exists to prevent, and pre-1.0 is the moment to make it required.
- **`Actor` is empty when auth is off.** Single-user mode has no identity to record;
  the reason and the timestamp still are. Same limitation as ADR-0098.
- **One more value type and column family.** Justified by keeping the audit a first-
  class replayable fact instead of a side channel.
- The record covers manual *completion* only. Cancel/terminate and incident resolution
  are not yet audited; the `Kind` byte is the seam for them.
