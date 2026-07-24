# ADR-0057: First-class FEEL temporals for timer schedules

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

## Context and problem statement

FEEL timer schedules (ADR-0055/0056) evaluate an expression and then consume its
result as **text**: `expr.Classify` renders any non-scalar — including FEEL's
temporal values (`duration(...)`, `date and time(...)`) — to a canonical string,
and the timer resolver parses that string with the ISO-8601 duration / RFC3339
date parsers. This is reliable only when the expression yields a **string** the
modeler put in a variable (`orderTimeout = "PT1H"`). When the expression yields a
genuine FEEL temporal value — `=duration("PT1H")`, `=deadline + duration("PT2H")`,
`=date and time(slaDate)` — correctness hinges on the FEEL library's canonical
string happening to fall inside Atlas's parser's accepted subset. It is a lossy
round-trip through text for values that already carry an exact numeric length or
instant.

The `feel` library models temporals as first-class typed values —
`DaysTimeDuration` (with `Duration() time.Duration`), `DateTime` / `Date` (with
`Time() time.Time`). Atlas's `expr` boundary (ADR-0015) does not expose them, so
the engine cannot read a duration's nanoseconds or a date-time's instant directly.

## Decision drivers

- **Exactness.** A FEEL duration already *is* a nanosecond count and a date-time
  already *is* an instant; read them directly instead of formatting to text and
  re-parsing.
- **Keep the `expr` boundary narrow (ADR-0015).** Expose just enough — two typed
  accessors — rather than leaking the FEEL value package across Atlas.
- **No behavior regression.** The string path (variable holds an ISO string) must
  keep working unchanged; first-class handling is an *additional*, preferred path.

## Decision outcome

`expr` gains two accessors that keep the FEEL value package inside the boundary:

- `DurationNanos(Value) (int64, bool)` — the length of a FEEL days-and-time
  duration in nanoseconds. A years-and-months duration is calendar-dependent and
  not convertible, so it reports `false` (matching the ISO subset Atlas already
  excludes).
- `InstantNanos(Value) (int64, bool)` — the unix-nanosecond instant of a FEEL
  date-time, or a date (resolved to midnight in its zone).

Timer resolution consumes the evaluated **value**, not its text. A new
`TimerSchedule.ResolveFeelValue(v)` first tries the first-class accessor for the
field — `DurationNanos` for a FEEL duration schedule, `InstantNanos` for a FEEL
date schedule — and only if that fails falls back to the existing
`Classify`→text→`ResolveFeel` path. The engine's `resolveSchedule` evaluates the
expression once and calls `ResolveFeelValue`; the due date it produces is frozen
into the `TimerCreated` event exactly as before (I4/I6). A FEEL **cycle** has no
first-class temporal form (a cycle is an interval/cron string), so it goes straight
to the string path.

So `=duration("PT1H")` on a catch resolves to an exact one-hour delay, and
`=date and time(deadline)` to the exact instant, while `=orderTimeout` (a string
variable) keeps resolving through text. An expression whose result is neither a
usable temporal nor a parseable string stays *unresolvable* — the ADR-0055 fire-
immediately / stop-recurring / don't-arm stance is unchanged.

### Consequences

- **Positive:** data-driven timers computed with FEEL date arithmetic
  (`=deadline + duration("P2D")`) resolve exactly, no text round-trip. The `expr`
  boundary grows by two small, typed functions; the rest of Atlas still never
  imports the FEEL value package. Recovery and the timer firing path are untouched.
- **Negative / trade-offs accepted:** years-and-months durations remain
  unsupported (calendar-dependent), consistent with the literal ISO subset. FEEL
  cycles still travel as strings — there is no FEEL "cycle" value to make first-
  class. `InstantNanos` treats a bare date as midnight in its own zone, which is a
  choice the modeler should be aware of.
- **Follow-ups:** raising an incident instead of fire-immediately when a timer
  can't resolve (still open from ADR-0055/0056).

## Alternatives considered

- **Widen `Classify` to emit a temporal kind.** `Classify` exists to reduce a value
  to Atlas's *stored* scalar/JSON form; temporals have no storage representation
  yet, and timers don't need one — they need a number now. Two focused accessors
  are narrower than a new persisted kind.
- **Import `feel/value` in the engine directly.** Breaks the ADR-0015 boundary; the
  engine would depend on the FEEL library's value API throughout.

## Links

- builds on ADR-0055 / ADR-0056 (FEEL timer schedules — the resolution this makes
  exact)
- builds on ADR-0015 / ADR-0008 (the `expr` FEEL boundary this extends minimally)
- honors the invariants in docs/architecture/invariants.md (I4, I5, I6)
