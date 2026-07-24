# ADR-0051: Timer start events (duration, date, cycle)

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

> **Draft for review.** This ADR proposes the design; no code has been written
> against it yet. It supersedes nothing — it fills a gap left open by ADR-0035
> (message start events) and the Milestone 2 timer note in `ROADMAP.md`.

## Context and problem statement

A **timer start event** is a `<startEvent>` carrying a `<timerEventDefinition>`.
It starts a fresh process instance on a schedule with no external trigger:

- **after a delay** — `<timeDuration>PT1H</timeDuration>` (start one hour after
  deploy),
- **at a fixed instant** — `<timeDate>2026-08-01T09:00:00Z</timeDate>`,
- **on a recurring schedule** — `<timeCycle>R/PT1H</timeCycle>` (the modeler's
  "Jede volle Stunde" / *every full hour* in the Atlas Modeler).

Today Atlas compiles **every** `<startEvent>` to a none start or, if it has a
`messageEventDefinition`, a message start (ADR-0035). A `timerEventDefinition` on
a start event is silently ignored: the element becomes a plain none start that
only ever runs on an explicit API create, so the modeled schedule never fires.
Separately, timers anywhere in Atlas are **duration-only** (`PT30S`, parsed by
`parseISO8601Duration`); `<timeDate>` and `<timeCycle>` semantics do not exist
for catch, boundary, *or* start events (`ROADMAP.md`: "Date/cycle timers … still
to come").

So there are two gaps, and this ADR closes both **for the start-event case**:

1. **Start-event → timer wiring.** A `<startEvent>` with a timer definition must
   compile to an entry point that the engine schedules.
2. **Date and cycle timer kinds.** The engine must understand an absolute instant
   and a recurring interval, not only a relative duration.

### Why this is not just "message start with a clock"

ADR-0035 got message start events working with **no new durable state and no new
recovery path**: the external message is the trigger and carries its own timing,
so a message start is a pure in-memory index (`messageStarts`) derived from the
compiled definitions and rebuilt by `Deploy` on every restart. A timer start has
neither property:

- **No external trigger.** Something in the engine must *remember* the next
  scheduled start and fire it autonomously. The server timer scheduler already
  does exactly this for instance timers (`Server.timerScheduler` → `TickTimers` →
  `TriggerDueTimers`, on the run-loop goroutine, honoring I3), driven by the
  `cfTimer` due-date range-scan index — but that index only holds timers owned by
  a running element instance.
- **No instance yet.** `handleTimerTriggered` today completes the *waiting element
  instance* named on the timer. A start timer has no instance and no token; firing
  it must **create** an instance, not complete an element.
- **Must not double-fire across a restart.** A message start re-derives from the
  log for free. A fixed `<timeDate>` that already fired must not fire again after
  a restart, and a cycle must resume at the right occurrence — so the schedule's
  progress has to be **durable**, materialized from the WAL like all other state.

And a sharp constraint from ADR-0019: **`Processor.Deploy` is not event-sourced.**
Deployments live in a JSON sidecar; on startup the server re-calls
`proc.Deploy(cp)` for every persisted definition. So `Deploy` runs on **every
restart**, and any arming it performs must be **idempotent** — arming naïvely
inside `Deploy` would re-arm (and eventually storm) on every boot.

## Decision drivers

- **Hold the invariants.** Arming and firing must be deterministic,
  side-effect-free `applyToState` mutations replayable identically on recovery
  (I4); every generated due date and key is frozen into an event, never
  recomputed on replay (I6); no per-command allocation on the hot path (I1 — the
  scheduler tick and instance creation are off the hot command path, but the fire
  handler stays allocation-lean). Schedule parsing happens at deploy time, never
  at runtime (I5).
- **Reuse, don't reinvent.** Ride the existing `cfTimer` due-date index, the
  existing scheduler, and the existing create-instance command (ADR-0035's
  pattern) rather than a parallel mechanism.
- **One start-instantiation path.** A timer-started instance must go through the
  same instance-activating command an API create and a message start use, so its
  events — and its recovery — are identical.
- **A small, honest slice.** Land duration + date + cycle (ISO interval and cron)
  with recovery tests; state FEEL-expression schedules as an explicit follow-up.

## Considered options

**For where the schedule lives:**

1. **Pure in-memory index, derived from compiled defs (the ADR-0035 shape).**
   Rejected: it cannot remember that a `<timeDate>` already fired, so it re-fires
   every restart; a cycle has no durable anchor.
2. **Durable start timer in the existing `cfTimer` index, armed by an idempotent
   deploy-triggered command.** `Deploy` enqueues an "ensure start timers armed"
   command; the handler creates the timer (a `TimerCreated` event, due date
   frozen) only if one is not already present, so the restart re-deploy no-ops.
   Firing reuses the scheduler and the create-instance command.
3. **A brand-new durable "start subscription" column family** with its own encode/
   decode and recovery. Rejected: duplicates the `cfTimer` index and the scheduler
   for no new capability — the same objection ADR-0035 raised against a durable
   message-start column family.

**For how firing turns into an instance:** reuse the create-instance command
(chosen, per ADR-0035) vs. a distinct start-timer instantiation path (rejected —
a second create path to keep in lockstep, more replay risk, no benefit).

## Decision outcome

Chosen: **option 2** for the schedule, reusing the create-instance command for
instantiation.

### Compiler

- A `<startEvent>` with a `<timerEventDefinition>` compiles to a new
  `TypeTimerStartEvent`, a normal process entry point (it is in `StartEvents()`
  and, at runtime, flows straight on exactly like a none/message start — the timer
  is what *creates* the instance, not what the first element waits on).
- The timer definition is parsed at deploy time (I5) into a compiled
  `TimerSchedule{Kind, BaseNanos, Repetitions}` where `Kind ∈ {Duration, Date,
  Cycle}`:
  - **Duration** — `BaseNanos` is the ISO-8601 duration; the first (only) due date
    is `armTime + BaseNanos`.
  - **Date** — `BaseNanos` is the absolute instant; the only due date is
    `BaseNanos`.
  - **Cycle** — a recurring schedule, in either of two forms the compiled
    `TimerSchedule` distinguishes:
    - an **ISO-8601 repeating interval** `Rn/<duration>` (or `R/…` = infinite):
      `BaseNanos` is the interval, `Repetitions` the count (`-1` = infinite),
      anchored at arm time (`armTime + interval`, then each fire + interval).
    - a **cron expression** (e.g. `0 * * * *` — every full hour), for
      **wall-clock-aligned** recurrence: the next due date is the next instant
      matching the cron fields *after* the reference time, so "every full hour"
      fires at the top of the hour, not deploy-time + n hours. Cron cycles are
      infinite (`Repetitions = -1`).
    The compiled schedule is the single source of truth for computing each next
    occurrence (I5).
- New builder method `AddTimerStartEvent(schedule)` and accessor
  `TimerStartEvents()` on `CompiledProcess`, mirroring `MessageStartEvents()`.
- `parseISO8601Duration` is reused; new parsers for `<timeDate>` (RFC3339), for
  the `<timeCycle>` ISO repeating-interval form (`Rn/PT…`), and for the
  `<timeCycle>` cron form (5-field `min hour dom mon dow`, sufficient for
  top-of-hour and the common recurrences). FEEL-expression schedules remain a
  follow-up.

### Model

- `TimerValue` gains **`ProcessDefKey uint64`** (0 for an instance-owned timer).
  It already has `Repetitions int32` (pre-existing groundwork, `-1 = infinite`).
  A **start timer** is one with `ProcessInstanceKey == 0` and a non-zero
  `ProcessDefKey`; its `TargetElementId` is the timer-start element. The encoded
  timer size grows by 8 bytes; codec round-trip and coverage tests updated.
- The cycle **interval** is *not* stored on the timer — it is re-derived from the
  compiled definition (`processes[ProcessDefKey]` → the timer-start element's
  `TimerSchedule`) at fire time, keeping `TimerValue` small and the compiled def
  the single schedule authority (I5). The event still freezes the *computed* next
  due date (I6).

### Engine — arming (idempotent, durable)

- `Deploy(cp)` enqueues an **"ensure start timers armed"** command carrying the
  def key (it does not touch state directly — Deploy is not event-sourced, so the
  arming must flow through the normal command→event path to be durable and
  replayable). The handler, for each `TimerStartEvents()` element of `cp`:
  1. **Retires** any start timer armed for a *prior version of the same process
     id* (only the latest version's schedule is active — the versioning rule).
  2. If **no** start timer for this `(ProcessDefKey, elementId)` already exists,
     creates one with a `TimerCreated` event whose `DueDate` is frozen at arm time
     (I6): duration → `Now()+BaseNanos`; date → `BaseNanos`; cycle → first
     occurrence. If one already exists (the restart re-deploy case), it **no-ops**.
- Because the `TimerCreated` was persisted on first deploy and replayed on
  recovery, the start timer is **restored from the log** — the restart re-deploy
  finds it already in state and does nothing. No double-arm, no new recovery path
  beyond the ordinary `applyToState` for `TimerCreated`/`TimerTriggered`.
- `Undeploy(defKey)` enqueues retirement of that definition's start timers (the
  same self-retiring pattern instance cancellation uses; a timer that fires after
  its def is gone finds nothing and no-ops).

### Engine — firing (creates an instance; cycles reschedule)

- `TriggerDueTimers` finds a due start timer through the **same** `cfTimer` range
  scan — no change to the scheduler.
- `handleTimerTriggered` learns to branch on `ProcessInstanceKey == 0`:
  - retire the timer (`TimerTriggered`), then
  - schedule the **same** instance-activating command an API create uses
    (`CreateInstance`'s `VTProcessInstance/IntentActivating`) for `ProcessDefKey`,
    so the timer-started instance's events and recovery are identical to any other
    instance, and
  - if the timer is a **cycle** with remaining repetitions, arm the next
    occurrence: emit a fresh `TimerCreated` with the next due date computed from
    the compiled schedule and **frozen into the event** (I6), `Repetitions`
    decremented (`-1` stays `-1`). A finite cycle that reaches 0 does not
    re-arm.
- An instance-owned timer (`ProcessInstanceKey != 0`) keeps today's behavior
  exactly.

### Consequences

- **Positive:** timer start events work for duration, date, ISO cycle, **and cron**
  (wall-clock-aligned, so "every full hour" fires at the top of the hour). The
  schedule is durable and recovery-correct with **no new column family and no new
  recovery path** — just a wider `TimerValue`, an idempotent arm command, and a
  branch in the existing fire handler. Instantiation reuses the create-instance
  command, so a timer-started instance is indistinguishable downstream from an API
  or message-started one. The production scheduler already fires it.
- **Negative / trade-offs accepted:** cron support is a **5-field** expression
  (`min hour dom mon dow`), not the 6/7-field second-precision Quartz form some
  engines accept, and **FEEL-expression schedules remain a follow-up.**
  `TimerValue` grows 8 bytes for every timer, including instance timers that leave
  `ProcessDefKey` zero. A process with a timer start can still be started by a
  plain API create (it then just flows on), permissive but consistent with how
  ADR-0035 treats message starts.
- **Follow-ups / risks to watch:** FEEL-expression durations/dates; date/cycle for
  **catch and boundary** timers (this ADR does start events only — the compiler
  parsing and `TimerSchedule` are shared, so those become small extensions); a
  catch-up policy for cycles whose due dates elapsed while the server was down
  (this ADR fires each due occurrence the scan surfaces, then re-anchors the next
  from the current clock so a long outage does not replay a backlog indefinitely).

## Pros and cons of the options

### Option 1 (pure in-memory, derived) — rejected
- Good: zero durable state, matches ADR-0035.
- Bad: cannot remember a fired date or a cycle's progress; re-fires on restart.

### Option 2 (durable timer in `cfTimer`, idempotent deploy arm) — chosen
- Good: reuses the index, scheduler, and create-instance command; one new
  `applyToState` path (a `TimerValue` field); recovery falls out of the existing
  `TimerCreated`/`TimerTriggered` replay.
- Bad: arming must be carefully idempotent against the non-event-sourced `Deploy`
  re-run; versioning requires retiring a prior version's start timer.

### Option 3 (new start-subscription column family) — rejected
- Good: explicit, symmetric with a hypothetical durable message start.
- Bad: duplicates `cfTimer` and the scheduler with its own encode/decode and
  recovery, for no new capability.

## Links

- builds on ADR-0035 (message start events — the reuse-create-instance pattern)
- builds on ADR-0040 (boundary events — the timer-as-waiting-element machinery)
- builds on ADR-0019 (durable deployments — why `Deploy` re-runs and arming must
  be idempotent)
- relates to ADR-0020 (message events and correlation)
- honors the invariants in docs/architecture/invariants.md (I1, I3, I4, I5, I6)
