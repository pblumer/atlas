# ADR-DRAFT: At most once for irreversible operations — a lease that runs out raises «outcome unknown» instead of redelivering

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-06
- **Deciders:** pblumer (maintainer)
- **Open question:** whether the engine should accept an optional «started» report from
  the worker, so a lease that runs out before the worker began the side effect can still
  be redelivered safely. It halves the false alarms and adds a message and an event to
  the protocol; whether that is worth it depends on how often a lease runs out before a
  worker starts, which is not measured.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-draft-operations-declare-idempotency-and-effect](draft-operations-declare-idempotency-and-effect.md)
makes every operation declare its idempotency and effect, and names two things atlas can
do when the lease of a job runs out on an operation declared `none`: warn and redeliver,
or hold. Holding changes what the engine does with a job, so it is decided here.

Today a job is delivered **at least once** ([ADR-0007](0007-job-worker-protocol.md)): a
lease that runs out puts the job back on offer. For an operation that cannot be repeated
safely — a payment without an idempotency key, a final deletion — a lease that runs out
is exactly the case in which the effect may already have happened. Redelivering then
risks the one outcome the operation must not have.

## Decision drivers

- A duplicate of an irreversible effect is worse than a manual check.
- The engine stays deterministic: what happens on a lease expiry is an event, replayed
  as a fact, never recomputed ([invariants](../architecture/invariants.md) I4, I6).
- No new queue, no second state machine beside the job and the incident
  ([ADR-0061](0061-incident-model.md)).
- An operation that is idempotent loses nothing: the mode applies only where declared.

## Considered options

1. Keep at least once for every job, and only warn (variant (a) of the declaration
   record).
2. **At most once, per operation:** for a job whose operation is declared `none` and
   `irreversible` — or whose operator chose this mode for it — a lease that runs out is
   not redelivered; the job waits on an incident «outcome unknown».
3. Option 2, plus a «started» report: a lease that runs out before the worker reported
   «started» is redelivered as today; only one that ran out after it raises the
   incident.

## Decision outcome (proposed)

**Option 2 now, option 3 left open** (see the open question).

### Semantics

- The mode is fixed **at deploy**, per job-backed task, from the operation's declaration
  and the operator's choice, and compiled into the process ([invariant](../architecture/invariants.md)
  I5). A running instance never changes mode because a manifest or a setting changed
  later.
- When the lease of such a job runs out, the engine does not make the job activatable
  again. It records a fact — the job's lease expired with its outcome unknown — and
  raises an incident on the job with a cause that says so. The token waits on it.
- A report the worker sends after the lease ran out is refused, as today, by the lease
  token. It is not lost evidence: the incident tells the operator to look at the target
  system.
- **Resolving** the incident offers two decisions, and the operator states which:
  - «it did not happen»: the job is offered again under the **same** job key, so the
    same `idempotencyKey`, and the retry count the operator gives;
  - «it happened»: the job is completed by hand with the variables the operator
    supplies, with the reason recorded, as a manual completion already is.
- A job that fails normally — the worker reports a failure — keeps today's retry and
  incident behaviour. Only the silent case, the lease running out, changes.

### Durability and replay

The lease expiry is detected on the run loop as it is today. What is new is the event it
produces: one record (`JobOutcomeUnknown`, or an incident intent carrying that cause) is
appended, fsynced and committed before the incident is visible
([invariant](../architecture/invariants.md) I2), and `applyToState` applies it the one
way, live and on recovery (I4). Recovery never re-decides whether a lease ran out; it
reads that it did (I6). The change needs the repository's recovery test pattern: write
through the processor, close, reopen, and assert the same incident.

### Alerting

An incident «outcome unknown» is an event of the event catalogue
([ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md)) with its own type, so an
installation can route it to a person at any hour. Without that, the mode would turn a
duplicate into a stalled process nobody sees, which is not better.

### Consequences

- **Positive:** an irreversible operation is never repeated by a lease expiry; the one
  person who can know — someone who looks at the target system — decides.
- **Negative / trade-offs accepted:** every lease expiry on such a job is a manual case,
  including the ones where the worker crashed before acting (until option 3); a new event
  and incident cause in the engine; a deploy-time setting per task.
- **Follow-ups:** the Console's two-way resolve; the event type and its catalogue entry;
  the operator setting and its compilation; the recovery tests; option 3 once there is a
  measurement.

## Pros and cons of the options

### Option 1 — warn only

- Good: no engine change; nothing waits on a person.
- Bad: the duplicate happens; the warning only says afterwards that it may have.

### Option 2 — hold on lease expiry (proposed)

- Good: no duplicate of an irreversible effect; reuses job, lease and incident.
- Bad: false alarms when the worker died before acting.

### Option 3 — hold only after «started»

- Good: fewer false alarms.
- Bad: one more message and event; a worker that never sends «started» falls back to
  option 2 anyway; worth it only if the false alarms are frequent.

## Links

- [ADR-draft-operations-declare-idempotency-and-effect](draft-operations-declare-idempotency-and-effect.md)
- [ADR-0007](0007-job-worker-protocol.md), [ADR-0061](0061-incident-model.md),
  [ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md)
- [Engine invariants](../architecture/invariants.md)
