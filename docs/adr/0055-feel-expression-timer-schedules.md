# ADR-0055: FEEL-expression timer schedules for catch and boundary events

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0051 and ADR-0054 gave timer events the full range of *literal* schedules —
`<timeDuration>PT1H</timeDuration>`, `<timeDate>2026-08-01T09:00:00Z</timeDate>`,
`<timeCycle>R3/PT1H</timeCycle>`. All are compiled to a fixed `TimerSchedule` at
deploy time, so the due date depends only on the clock, never on the running
instance.

Real processes need timers that depend on **instance data**: "escalate this order
`orderTimeout` after it arrives", "remind until the `slaDeadline` the contract
set". BPMN expresses this with a FEEL expression in the timer definition —
`<timeDuration>=orderTimeout</timeDuration>` — evaluated at runtime against the
instance's variables. Atlas compiled every timer definition as a literal (it even
*stripped* a leading `=` to tolerate the modeler's FEEL marker), so a modeled FEEL
timer silently became a broken literal.

## Decision drivers

- **Compile once, evaluate many (I5, ADR-0008).** The FEEL expression is parsed
  and lowered at deploy time via the existing `expr` boundary; only the cheap
  evaluation happens at runtime.
- **Hold the invariants.** The expression is evaluated at *command* time (in the
  element's `OnActivated`, where variables and the clock are already read), and
  the resulting due date is frozen into the `TimerCreated` event, so `applyToState`
  stays pure and recovery replays the same due date (I4/I6).
- **Backward compatible.** A literal that a modeler wrote with a leading `=`
  (tolerated today) must keep working.
- **A small, honest slice.** Land FEEL for duration and date on catch and boundary
  timers — where an instance context exists — and defer FEEL cycles and FEEL on
  start events, which need more (a re-evaluation story, and an evaluation context
  that a start event does not have).

## Decision outcome

A timer field is a **FEEL expression** when it begins with `=` *and* its body is
not itself a literal schedule. `TimerSchedule` gains two kinds —
`TimerFeelDuration` and `TimerFeelDate` — carrying the compiled `expr.Compiled`
instead of a precomputed value.

**Compilation.** For each timer field, the compiler first tries to parse the
(`=`-stripped) text as the field's literal (duration/date/cycle); a literal wins,
so `=PT1H` and `PT1H` both compile to the same fixed schedule (backward
compatible). Only when the body is *not* a literal and the field carried a leading
`=` is it compiled as FEEL, via `expr.CompileAuto` (which discovers the variable
names it reads). A non-literal, non-`=` body stays a compile error, as before.

**Runtime.** When a catch or boundary timer activates, its `OnActivated` evaluates
the expression against the instance's variables (the same `bindInputs` path a
script task or a correlation key uses), reduces the result to text, and parses
that text as the field's literal to get the due date — `now + duration` for
`TimerFeelDuration`, the absolute instant for `TimerFeelDate`. The due date is
frozen into the `TimerCreated` event exactly as a literal timer's is; nothing
about firing, recovery, or the recurring-boundary machinery changes.

**Unresolvable expression.** Incidents are not modeled yet (Milestone 2), and FEEL
is null-propagating, so an expression that errors, evaluates to null, or yields
text that isn't a valid duration/date resolves to **now** — the timer fires
immediately rather than wedging the token. This is the same fail-open stance
script tasks take on a failed evaluation, and it is called out as the placeholder
until the incident model lands.

**Deferred, by explicit rejection:**

- **FEEL `<timeCycle>`** — a compile error. A recurring schedule from an
  expression needs a re-evaluation policy (re-read variables each occurrence?
  freeze the first?) that this slice does not settle.
- **FEEL on a timer *start* event** — a compile error. A start event is armed at
  deploy with no instance, so there are no variables to evaluate against.

Both rejections name the element and say a literal is required for now.

### Consequences

- **Positive:** dynamic, data-driven timeouts and deadlines — the common real-world
  case — work on catch and boundary events, reusing the `expr` boundary and the
  existing evaluate-at-command-time/freeze-into-event pattern. No new event types,
  no recovery changes. Literal timers (with or without a leading `=`) are
  untouched.
- **Negative / trade-offs accepted:** an unresolvable FEEL timer fires immediately
  rather than raising an incident — visible-but-crude until incidents exist. FEEL
  temporal *values* (a `duration(...)`/`date and time(...)` result) are consumed
  via their canonical string form through the same literal parser, so a result
  whose canonical text falls outside Atlas's duration subset is treated as
  unresolvable; the reliable pattern is a variable holding an ISO-8601 string
  (`orderTimeout = "PT1H"`). Cycles and start events stay literal-only.
- **Follow-ups:** FEEL cycles (with a stated re-evaluation policy); FEEL on start
  events (constant expressions evaluated against an empty context); first-class
  FEEL temporal handling in the `expr` boundary; incident-raising instead of
  fire-immediately.

## Alternatives considered

- **Strict Zeebe rule — any leading `=` is FEEL, no literal fallback.** Cleaner in
  principle, but it reinterprets `=PT1H` (tolerated as a literal today) as an
  invalid FEEL expression, a silent regression. The literal-first rule keeps
  compatibility.
- **Model FEEL durations/date-times as first-class values in `expr`.** The correct
  long-term answer, but it widens the deliberately narrow `expr` surface
  (ADR-0015) well beyond this slice. Consuming the canonical string is enough for
  the variable-holds-an-ISO-string case that dominates.
- **Evaluate at deploy instead of at activation.** Impossible for the data-driven
  case — the variables don't exist until the instance runs.

## Links

- builds on ADR-0051 / ADR-0054 (the compiled `TimerSchedule`)
- builds on ADR-0008 / ADR-0015 (the compile-once `expr` FEEL boundary)
- honors the invariants in docs/architecture/invariants.md (I4, I5, I6)
