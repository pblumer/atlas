# ADR-0056: FEEL cycles, and FEEL on timer start events

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0055 introduced FEEL-expression timer schedules — `<timeDuration>=orderTimeout</timeDuration>`
and `<timeDate>=slaDeadline</timeDate>` on catch and boundary events — but
deferred two cases with explicit compile-time rejections:

1. **A FEEL `<timeCycle>`** (e.g. `=reminderInterval` on a non-interrupting
   boundary), because a recurring schedule from an expression needs a stated
   *re-evaluation policy* — is the interval read once, or re-read each occurrence?
2. **FEEL on a timer *start* event**, because a start timer is armed at deploy
   with no instance, so a variable reference has nothing to evaluate against.

Both are natural asks — a data-driven reminder cadence, and a start schedule
computed from a constant FEEL expression (`=duration("PT1H")`). This ADR settles
the two questions and lifts the rejections where they can be lifted.

## Decision drivers

- **Reuse the compile-once `expr` boundary (I5)** and the ADR-0055 evaluate-at-
  command-time/freeze-into-event pattern (I4/I6) — no new machinery.
- **One resolution path.** A FEEL schedule of any field should reduce to the *same*
  concrete `TimerSchedule` the literal path produces, so `FirstDue`/`NextDue`/
  `Repetitions` stay identical downstream.
- **Say what can't be done.** A start FEEL that references variables cannot be
  evaluated at arm — reject it at compile time with a precise message, rather than
  arming a timer that silently never resolves.

## Decision outcome

**A FEEL schedule resolves to a concrete schedule.** `TimerSchedule` gains a
`TimerFeelCycle` kind (alongside the existing `TimerFeelDuration`/`TimerFeelDate`),
and a single `ResolveFeel(text) → (TimerSchedule, ok)` turns an evaluated
expression's text into the concrete `TimerDuration`/`TimerDate`/`TimerCycleInterval`/
`TimerCycleCron` schedule the literal parser would have produced. The engine's
`resolveSchedule(scope)` evaluates the expression against a scope and calls
`ResolveFeel`; every timer site then computes `FirstDue`/`NextDue`/`Repetitions`
on the *resolved* schedule, so FEEL and literal timers share one code path.

**FEEL cycles — re-evaluated each occurrence.** A non-interrupting boundary with a
FEEL `<timeCycle>` resolves the expression when it arms (for the first due date and
the repetition count) and **again on each re-arm** (for the next due date). Re-
evaluation reads the instance's *current* variables, so a reminder cadence can
change as the process runs; the repetition countdown is tracked on the timer value
(as for a literal cycle), not re-read. This is the policy ADR-0055 left open; it
falls out of the resolution path with no stored interval and no new event.

**FEEL on start events — constant expressions only.** A timer start event may
carry a FEEL schedule *only if it references no variables* (`Expr.Inputs()` is
empty) — a constant like `=duration("PT1H")` or `="0 0 9 * *"`. Such an expression
is evaluated against an empty scope when the start timer is armed (and re-armed for
a cycle). A start FEEL that references a variable is a **compile error** naming the
element: a start event has no instance, so there is nothing to read. The literal-
first rule still holds, so `=PT1H` remains a literal.

**Cycles stay boundary-only.** A FEEL `<timeCycle>` on a plain catch or an
interrupting boundary is rejected by the same `Repeats()` check literals already
use — a cycle only recurs on a non-interrupting boundary.

**Unresolvable at runtime** keeps the ADR-0055 stance: a catch/boundary FEEL timer
that can't resolve fires immediately; a boundary re-arm that can't resolve simply
stops recurring; a start timer that can't resolve is not armed.

### Consequences

- **Positive:** data-driven reminder cadences and constant-FEEL start schedules
  work, through the same resolve→FirstDue/NextDue path as literals. No new event
  types; recovery is unchanged (a FEEL cycle's re-armed occurrences are ordinary
  `TimerCreated` events with frozen due dates). The last two ADR-0055 follow-ups
  are closed.
- **Negative / trade-offs accepted:** a FEEL cycle is re-evaluated each occurrence
  rather than frozen once — more flexible, but a modeler who mutates the interval
  variable changes the cadence mid-flight. Start FEEL is constant-only; a schedule
  that should depend on the *triggering* data still can't be expressed on a start
  event (it belongs on a downstream catch). Runtime temporal values are still
  consumed via their canonical string form (ADR-0055), so a variable holding an
  ISO-8601 string remains the reliable pattern.
- **Follow-ups:** first-class FEEL temporal handling in `expr`; raising an incident
  instead of fire-immediately / skip-arming when a FEEL timer can't resolve.

## Alternatives considered

- **Freeze a FEEL cycle's interval at first arm.** Would need the resolved interval
  stored on the timer value (a new field) so re-arm can recompute without the
  expression; re-evaluation needs neither and gives a useful dynamic cadence for
  free. Rejected as more state for less flexibility.
- **Allow variable-referencing FEEL on start events, evaluated as null.** It would
  arm a timer that never resolves (fires immediately or never) — a silent trap. A
  compile error is honest.

## Links

- builds on ADR-0055 (FEEL-expression timer schedules — the resolution this
  generalizes)
- builds on ADR-0051 / ADR-0054 (the `TimerSchedule` and the recurring-boundary
  machinery)
- honors the invariants in docs/architecture/invariants.md (I4, I5, I6)
