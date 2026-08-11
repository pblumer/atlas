# ADR-0111: Completing the incident model — retry backoff and timer-FEEL failure incidents

- **Status:** Accepted
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

> **Implementation status.** Proposed → in delivery. This closes the runtime gaps the incident
> model (ADR-0061/0064) left open, the last of ROADMAP Milestone 2:
> (1) **retry backoff** — a failed job with retries left waits a worker-supplied delay before
> a worker can pull it again, instead of retrying immediately;
> (2) **recurring-timer re-arm FEEL failures** — a repeating boundary/event-subprocess timer
> whose FEEL schedule can't be re-evaluated on a later occurrence raises an incident and parks,
> instead of silently ceasing to recur;
> (3) **start-event timer FEEL failures** — a timer *start* event whose (compiler-constant)
> FEEL schedule can't resolve is caught at **deploy time** as a validation error, instead of
> being silently not armed. Backoff reuses the timer due-date scheduler; the re-arm incident
> reuses the ADR-0064 job-less timer incident; the start-timer check reuses the compiler's
> validation pass. An **Operations incidents view** is a follow-up PR on the existing HTTP
> surface. (Retry-backoff and an operator UI were the named leftovers on this item.)

## Context and problem statement

The incident model (ADR-0061) turns a stuck job or an unresolvable timer into a durable,
operator-resolvable **incident** rather than a hang or an infinite retry. Three gaps remain:

- **No retry backoff.** When a worker fails a job with retries left, the job goes **straight
  back** on the activatable index (`state/tx.go` `PutJob`: on the index iff `Retries > 0`), so a
  worker re-pulls it immediately. A transient fault (a downstream service briefly down) becomes a
  tight retry loop hammering the fault. BPMN engines let a worker say "retry, but not for N
  seconds" — a **backoff**. Atlas has no way to express or honor one (`failJobReq` carries only
  `retries` and `message`; `Processor.FailJob` no delay).
- **Silent recurring-timer re-arm failures.** A recurring (cycle) boundary or event-subprocess
  timer re-evaluates its FEEL schedule on **each** occurrence (ADR-0054/0055). If a later
  re-evaluation fails, the re-arm path (`fireRecurringBoundary` via `recurringBoundarySchedule`,
  `engine/behavior.go`) uses the boolean `resolveSchedule`, gets `ok = false`, and — back in
  `handleTimerTriggered` — silently falls through to *completing* the timer: it just **stops
  recurring**, with no incident. ADR-0064 raises an incident for the *initial* arm's FEEL
  failure but not the re-arm's.
- **Silent start-timer FEEL failures.** A timer start event's schedule may be a **constant**
  FEEL expression (ADR-0056; a start event has no instance, so it can't read variables). At
  deploy the engine resolves it (`handleTimerStartArm`); on failure it `continue`s — the start
  timer is **silently not armed**, with no signal to the operator. A start event has no process
  instance, so the ADR-0064 job-less incident (keyed by element instance) has nothing to attach
  to.

The question this ADR answers: **how do we honor a retry backoff, and surface the two silent
timer-FEEL failures, reusing the timer scheduler, the ADR-0064 incident, and the compiler's
validation — without a new scheduler or a new incident shape for the instance-less start case.**

What already exists, and is load-bearing:

- **The timer due-date index is a "fire when due" scheduler.** `cfTimer` is keyed
  `timer:<dueDate>:<key>` so `DueTimers(now)` is a range scan and `TickTimers` fires everything
  due (`state/store.go`, `engine/processor.go`). A retry backoff is exactly "fire this job's
  re-activation when due" — a timer.
- **The job carries timestamps already.** `JobValue.Deadline` (a unix-nano field) is the
  precedent for a per-job timestamp; a `RetryDueDate` is the same shape.
- **The activatable-index gate is one predicate.** `PutJob` places a job on the worker-visible
  index iff `Retries > 0`. Adding "and not currently backing off" is a one-condition change.
- **The ADR-0064 job-less incident + re-arm already exist.** `armOneShotTimer` raises an
  incident (JobKey = 0) on a FEEL failure and parks; `handleIncidentResolved` routes a
  JobKey-0 incident to `rearmTimerElement`, which re-runs the arm. The recurring re-arm failure
  reuses this exact incident and resolve path.
- **The compiler evaluates FEEL at deploy for other constants.** Validation already runs a
  graph-wide pass; a constant start-timer schedule (no inputs) can be evaluated there.

## Decision drivers

- **Reuse, don't reinvent.** Backoff is a timer; the recurring re-arm incident is the ADR-0064
  incident; the start-timer check is a validation rule. No new scheduler, no new incident shape.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2); a
  single `applyToState` live and on recovery (I4); the backoff *delay* is supplied by the caller
  but the *due date* is read from the clock at command time and frozen into the event, never in
  `applyToState` (I6); a job backing off is off the index by a pure function of its committed
  fields.
- **Recovery-safe.** A job backing off, and its retry timer, are event-derived — a crash during
  backoff recovers to the same parked job and the retry still fires when due.
- **Backward compatible.** A fail with no backoff (delay 0) behaves exactly as today (immediate
  retry); the new `JobValue`/`TimerValue` fields are append-compatible (old records decode 0).

## Considered options

### Retry backoff
1. **A retry timer that re-activates the job when the backoff elapses (chosen).** On a fail with
   `Retries > 0` and a delay, stamp `RetryDueDate = now + delay` on the job (so `PutJob` keeps it
   **off** the activatable index) and arm a **retry timer** (a `TimerValue` carrying the job key)
   for that due date. When the timer fires, clear `RetryDueDate` and re-emit the job (`PutJob`
   puts it back on the index) and notify workers. Reuses the timer scheduler and recovery
   wholesale.
2. **Time-filter the worker's activatable scan.** Keep the failed job on the index but skip it in
   `ActivatableJobs` until `now >= RetryDueDate`. Rejected: the poller is notification-driven, so
   nothing would re-notify when the backoff elapses — it would still need a timer to wake the
   worker, and meanwhile a stale job sits on the worker-visible index. Option 1's off-index-until-
   due is cleaner and keeps the index meaning "pullable now".
3. **Sleep in the worker.** Rejected: not durable (a crash loses the backoff), and it blocks a
   worker thread; backoff must be engine state.

### Start-timer FEEL failure
1. **Deploy-time validation error (chosen).** A start-timer schedule is compiler-constant, so its
   FEEL can be evaluated in the compiler's `Validate` pass; a schedule that won't resolve to a
   valid temporal is a `SeverityError`, refusing the deploy — the operator sees it immediately in
   the Problems panel. No runtime incident with nothing to key on.
2. **A definition-level incident.** Rejected: it needs a new incident attachment (a process-def
   key, not an element instance), a new apply/index/resolve path, and there is no running
   instance for an operator to act on — a deploy-time error is both simpler and more useful.

## Decision outcome

Chosen: **retry backoff via a retry timer; recurring re-arm FEEL failure via the ADR-0064
incident; start-timer FEEL failure via a deploy-time validation error.**

### Retry backoff

- **Model.** Append `RetryDueDate int64` to `JobValue` and `JobKey uint64` to `TimerValue`, both
  append-compatible (an old record decodes them 0).
- **Fail.** `Processor.FailJob(jobKey, retries, message, backoff int64)` gains the backoff (unix-
  nanos delay; 0 = immediate). `handleJobFailed`: if `retries > 0 && backoff > 0`, set
  `job.RetryDueDate = c.Now() + backoff`, emit `IntentJobFailed` (so `PutJob` keeps it off the
  index), and arm a retry timer `TimerValue{ProcessInstanceKey, JobKey: jobKey, DueDate:
  RetryDueDate}`. Otherwise unchanged: emit the fail, and on `retries <= 0` raise the incident.
- **Index gate.** `PutJob` places a job on the activatable index iff `Retries > 0 && RetryDueDate
  == 0`.
- **Fire.** `handleTimerTriggered` gains a first branch: a timer with `JobKey != 0` re-activates
  that job — clear `RetryDueDate`, re-emit `IntentJobCreated` (back on the index), notify. A job
  gone (its instance was cancelled) is a no-op (self-retire).
- **API.** `failJobReq` gains `retryBackoff` (milliseconds), converted to nanos and threaded
  through. The in-process poller passes 0 (immediate retry, unchanged); external workers supply a
  backoff. Instance cancellation deletes the job, so its retry timer self-retires.

### Recurring-timer re-arm FEEL failure

`handleTimerTriggered` recognizes a recurring boundary / event-subprocess timer by its **compiled
type** (not by a successful resolve), then resolves its schedule with the error-returning
`resolveScheduleErr`. On error it raises the ADR-0064 job-less incident (JobKey 0, keyed by the
element instance, message "timer schedule: …") and parks — it does **not** re-arm and does **not**
complete. `handleIncidentResolved` → `rearmTimerElement` re-arms the recurring boundary /
event-subprocess (re-resolving; a still-broken schedule re-raises). On success it re-arms the next
occurrence as today. (Fidelity note: a re-arm after an incident restarts the compiled cycle rather
than resuming a partially-consumed finite repetition count — acceptable and documented.)

### Start-timer FEEL failure

A new `checkTimerStartSchedules` validation step evaluates each timer-start event's constant FEEL
schedule; one that errors or does not yield a valid temporal is a `SeverityError`
(`RuleTimerStartSchedule`), refusing the deploy. Constant, input-free schedules are safe to
evaluate at deploy (no instance, no hot path).

### Phased implementation plan (test-first)

- **Phase 1 — Retry backoff.** `RetryDueDate`/`JobKey` fields + codec; `FailJob` backoff param;
  the retry-timer arm + fire; the `PutJob` gate; the API field. *Tests:* a failed job with a
  backoff is off the activatable index until its retry timer fires, then pullable again; a
  zero-backoff fail is immediately pullable (unchanged); exhausted retries still raise an
  incident; a **recovery test** — crash mid-backoff, replay, tick, job re-activates.
- **Phase 2 — Timer-FEEL gaps.** The recurring re-arm incident + resolve/re-arm; the
  `checkTimerStartSchedules` deploy validation. *Tests:* a recurring boundary whose FEEL fails on
  a later occurrence raises an incident and stops firing; resolving re-arms; a recovery test; a
  start-timer with an unresolvable constant schedule is a deploy error.
- **Phase 3 (follow-up PR) — Operations incidents view.** List/inspect/resolve incidents in the
  Operations app over the existing `GET /incidents` / `POST /incidents/{key}/resolve` endpoints.

### Consequences

- **Positive:** completes the incident model and closes Milestone 2 — transient faults back off
  instead of hammering, recurring-timer and start-timer FEEL failures are no longer silent. All
  on the timer scheduler, the ADR-0064 incident, and the compiler's validation; two append-only
  fields, no new scheduler, no new incident shape, no new recovery path.
- **Negative / trade-offs accepted:** two new `JobValue`/`TimerValue` fields; a retry timer per
  backing-off job (self-retiring); a re-arm after an incident restarts a finite FEEL cycle's
  count rather than resuming it.
- **Follow-ups / risks to watch:** a per-task-definition default/exponential backoff (the delay
  is worker-supplied for now); the Operations incidents view (Phase 3); extending
  `rearmTimerElement` coverage if more timer-bearing element types gain FEEL schedules.

## Links

- completes ADR-0061 (incident model) and ADR-0064 (timer FEEL-failure incidents); reuses the
  timer due-date scheduler (ADR-0051/0054/0055) and the compiler validation pass (ADR-0026)
- honors I1, I2, I4, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 2 "Incident model" — the final open item; retry backoff and an operator UI
  were its named leftovers
