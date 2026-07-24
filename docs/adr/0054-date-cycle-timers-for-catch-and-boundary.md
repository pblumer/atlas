# ADR-0054: Date and cycle timers for catch and boundary events

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0051 gave timer *start* events the full range of schedules — duration, date,
and cycle (ISO interval and cron) — behind a shared compiled `TimerSchedule`.
Intermediate **timer catch** events and **boundary timer** events, however, still
accept only an ISO-8601 **duration** (`PT30S`): their compiled details carry a bare
`DurationNanos`, and `<timeDate>` / `<timeCycle>` on them are a parse error.

Two gaps remain, and they are not symmetric:

1. **A fixed date** (`<timeDate>`) on a catch or boundary is a straight extension —
   the token (or the boundary) waits until an absolute instant instead of a
   relative delay. Same "fire once" lifecycle, just a different due date.
2. **A cycle** (`<timeCycle>`) means something only for a **non-interrupting
   boundary event**: a recurring reminder that fires again and again while the
   host activity runs ("nudge the approver every hour until they act"). A cycle on
   a plain intermediate catch is meaningless — a catch fires once and the token
   moves on — and a cycle on an *interrupting* boundary is pointless — it cancels
   the host on the first fire, so there is never a second.

## Decision drivers

- **Reuse the ADR-0051 schedule.** The compiled `TimerSchedule` (with `FirstDue`
  and `NextDue`) already computes duration/date/cycle/cron due dates
  deterministically. Catch and boundary details should carry it, not a bare
  duration, so all timer kinds share one parser and one due-date computation (I5).
- **Hold the invariants.** Due dates read the clock at command time and are frozen
  into events (I4/I6); recovery replays the same events (I4); no per-command
  allocation on the hot path (I1).
- **Reject the meaningless, don't silently mangle it.** A cycle where it cannot
  recur is a modelling mistake; fail the compile with a message that says what to
  use instead, rather than quietly firing once.
- **A recurring boundary is the point.** Don't ship "cycle support" that refuses
  the one place a cycle belongs.

## Decision outcome

`TimerCatchDetail` and `BoundaryEventDetail` carry a `TimerSchedule` instead of a
`DurationNanos`. Their `OnActivated` arms the timer at `schedule.FirstDue(now)`.
The duration-based `AddTimerCatchEvent` / `AddBoundaryTimerEvent` builder methods
stay as thin wrappers that build a `TimerDuration` schedule, so existing callers
and the fast common case are unchanged.

**Parsing.** The intermediate-catch and boundary timer parse paths now call the
shared `parseTimerSchedule`. Validation rejects the meaningless combinations at
compile time:

- a **catch** event with `<timeCycle>` → error (a catch fires once; use
  `<timeDuration>` or `<timeDate>`);
- an **interrupting boundary** with `<timeCycle>` → error (it fires once; use a
  duration/date, or make the boundary non-interrupting).

Duration and date are accepted everywhere; a cycle is accepted only on a
non-interrupting boundary.

**Recurring non-interrupting boundary.** This is the one lifecycle change. Today a
boundary timer fires by driving its boundary element instance to `Completing`,
which emits `Completed` (removing the instance) and takes the outgoing flow. For a
recurring boundary that is wrong — the listener must persist and fire again. So
`handleTimerTriggered`, when the fired instance is a **non-interrupting boundary
whose schedule recurs**, instead:

- takes the boundary's outgoing flow (spawns the parallel reminder token), **without**
  emitting `Completed` — the boundary element instance stays `Activated`; and
- arms the next occurrence: a fresh `TimerCreated` keyed to the same boundary
  element instance, its due date `schedule.NextDue(now)` frozen in (I6), its
  `Repetitions` counted down for a finite `Rn` cycle (an infinite `R`/cron stays
  `-1`). A finite cycle that runs out simply stops arming; the idle boundary is
  removed when its host completes, by the existing disarm.

Everything else about boundaries is unchanged: an interrupting boundary and a
one-shot (duration/date) non-interrupting boundary still fire once through
`Completing`; the host's normal completion still disarms armed boundaries, and the
recurring boundary's last re-armed timer self-retires when it finds its instance
gone (the "fires later, finds nothing, does nothing" pattern).

### Consequences

- **Positive:** every timer element — start, catch, boundary — now shares one
  schedule vocabulary. Deadlines can be absolute dates; recurring non-interrupting
  boundaries express "remind every hour" natively. No new event types: the recur
  is expressed with the existing `TimerCreated`/`TimerTriggered` events, so
  recovery replays a recurring boundary identically (I4).
- **Negative / trade-offs accepted:** a recurring boundary's reminder tokens are
  spawned but not tracked as a group — there is no fan-in or max-count beyond the
  cycle's own `Rn`. A cycle is refused on catch and interrupting boundaries rather
  than accepted-and-ignored; this is a compile error a modeler must fix. FEEL
  expression schedules remain a follow-up (as in ADR-0051).
- **Follow-ups:** FEEL-expression durations/dates; a catch-up policy for a
  recurring boundary whose occurrences elapsed during downtime (this ADR fires
  each occurrence the scan surfaces, then re-anchors from the clock).

## Alternatives considered

- **Keep `DurationNanos`, add parallel `DueDate`/cycle fields.** Rejected: two
  representations of "when" on the same detail, and every reader must branch on
  which is set. The compiled `TimerSchedule` already unifies them.
- **Support `<timeCycle>` on catch / interrupting boundary as fire-once.** Rejected:
  silently reinterpreting a modeler's cycle as a single fire hides a modelling
  error; a compile-time rejection is honest.
- **A distinct "recurring boundary" element type.** Rejected: it is the same
  boundary event with a recurring schedule; the recur belongs in the timer firing
  path, not a new type.

## Links

- builds on ADR-0051 (timer start events — the shared `TimerSchedule`)
- builds on ADR-0040 (boundary events — the boundary lifecycle this extends)
- honors the invariants in docs/architecture/invariants.md (I1, I4, I5, I6)
