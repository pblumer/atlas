# ADR-0291: One place names every resource budget, and one way sets them

- **Status:** Draft
- **Date:** 2026-09-08
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas bounded external input in about ninety places. Thirty-odd named constants
(`maxXMLBytes`, `maxUserBytes`, `maxRestoreBytes`, …), each declared next to the
handler that used it, plus a scattering of bare literals written straight into the
call. Every one of them was a defensible number where it stood.

Together they were not a policy, because nothing said what the set was. And a set
nobody can enumerate is a set nobody notices a hole in.

The audit of 2026-09-07 found the hole. F16 reported the resource budgets as
incomplete, and the sharp half of it was a multi-instance activity allocating from a
count that came out of an instance variable — `nullList(n)` with no ceiling between
the number and the allocation, sitting among twenty neighbours that were bounded.
That half is fixed (ADR-0276). This decision is the other half, which the audit
stated as *einheitliche, konfigurierbare Budgets*: one place that names them, and one
way to change them.

Two further things were true and worth stating plainly:

- **A script's output had no ceiling at all.** `cmd.Output()` collects stdout into a
  `bytes.Buffer` that grows to whatever arrives, and a script's output is written by
  code the model author controls. `while true: print(x)` was an unbounded allocation
  on the host, held back only by the 30-second timeout — which, at a gigabyte a
  second, is not a bound.
- **The two engine budgets were settings nobody could set.** `SetExecutionBudget` and
  `SetMaxIterations` existed and had no caller. Both numbers are estimates, and an
  estimate an operator cannot move is a number that will be wrong for somebody.

## Decision drivers

- **Enumerable beats correct.** Any one ceiling can be argued about. What cannot be
  argued about is whether the list is complete, and that is the property the audit
  found missing.
- **No number changes.** Gathering the budgets must not quietly loosen or tighten
  one; a moved ceiling is a decision about how much a caller may make this server
  hold, and it should read as one in a diff.
- **A knob must not be able to remove a budget.** The failure this prevents is memory
  exhaustion; a configuration surface that can set a ceiling to "off" reintroduces it
  as a typo.
- **A list is only as good as what stops it going stale.** The store registry
  (ADR-0282) is the shape that worked: not a maintained list, but a test that fails
  when something unclassified exists.

## Considered options

1. A `limits` package naming every budget, defaults identical to today's constants,
   set from the environment, with a completeness test over the source. (chosen)
2. Leave the constants where they are and add environment overrides one by one.
3. One global maximum-request-size and let everything share it.
4. Leave it; each ceiling is defensible where it stands.

## Decision outcome

Chosen option: **option 1.**

- `limits.Limits` is one struct whose fields are the budgets, grouped by *what they
  hold* rather than by which handler reads them — `ModelUpload` for a BPMN document,
  `Request` for an ordinary JSON body, `Archive` for a restore, `TokenSteps` and
  `Iterations` for the engine's two. Where several constants already held the same
  number for the same kind of thing, they become one field; where they differed, they
  stay different. `Default()` is exactly what the code carried before.
- `Names`, the environment variables, and `FromEnv` are all **derived from the struct
  by reflection**. A budget added to `Limits` is configurable and enumerable the
  moment it exists, because there is no second list to keep in step. That is the same
  failure this package ends, and writing it out again would have reproduced it.
- Configuration is `ATLAS_LIMIT_` plus the field name in upper snake case. A value
  that is unset, empty, unparseable, not positive, or too large for its field leaves
  the default standing and is logged at startup. It is deliberately **not** an error:
  a malformed knob must not stop a server coming up, and it must never be the reason
  a ceiling is missing.
- The server holds its budgets and hands them to every sub-service it builds; each
  handler reads `s.limits.X` rather than a constant beside it. Components that run in
  a worker's process, where the server's environment does not reach, take the *named
  default* — they have a name in one place, which is the half of this that applies to
  them.
- A script's stdout is now collected through a bounded buffer at `Payload`. The
  process is left to finish or hit its deadline; bytes past the ceiling are dropped as
  they arrive rather than held.
- `TestNoCeilingWithoutAName` walks the repository's own sources for `io.LimitReader`,
  `io.CopyN` and `http.MaxBytesReader`, and fails on any ceiling that is neither read
  from the registry nor listed as something else with the reason it is not a budget.

### Consequences

- **Positive:** the set of budgets is a thing that can be read in one sitting, and an
  operator can move any of them without a rebuild.
- **Positive:** a script can no longer take the host down by printing.
- **Positive:** the completeness test is what lasts. The next unbounded read fails a
  test rather than waiting for an audit.
- **Negative / trade-offs accepted:** sixteen knobs is a large configuration surface
  for something most installations should never touch. The mitigation is that every
  one has a default that is the historical value, and none of them can be turned off.
- **Negative:** a handler now reaches through its server for a number that used to be
  a compile-time constant. That is a real loss of locality, and the reason the field
  names say what they hold rather than which endpoint reads them.
- **Negative:** the budgets in worker-process components are named but not
  configurable from the server's environment. They read their own defaults. Wiring a
  worker's configuration into them is a separate change, and it is not pretended here.
- **Negative / deliberately not done:** F16's list also names *variable size*, and
  there is no `Variable` budget here. Every path by which a value can now *enter* a
  variable is bounded — an HTTP body, a script's output, a connector's answer, and
  the iteration budget behind a computed one — so what is missing is defence in
  depth rather than an open door. It is left out because it needs a decision this
  one does not: `AppendVariableEvent` is the single funnel every write passes
  through, and it cannot fail. Refusing there means either dropping a write in
  silence (worse than a large variable), or raising an incident while the caller
  carries on as though the value exists, or giving twenty-four call sites an error
  to handle. That is a decision about the failure mode, and it deserves its own
  record rather than a clause in this one.
- **Follow-ups / risks to watch:** the completeness test knows three call shapes. A
  fourth way to bound a read — a custom reader, a framework's own limit — would pass
  unseen until somebody adds it to `ceilingCalls`. The count assertion (it must find
  at least forty) is there so the test cannot go quiet, but it does not know what it
  has not been taught to look for.

## Pros and cons of the options

### Option 1 — a limits package with a completeness test (chosen)
- Good: enumerable, configurable, and self-policing; no number moves.
- Bad: a large diff across many files, and a knob surface most people will not use.

### Option 2 — environment overrides on the existing constants
- Good: minimal diff.
- Bad: solves configurability and not enumerability, which is the half that found the
  bug. The next missing ceiling would be just as invisible.

### Option 3 — one global maximum
- Good: one number, impossible to forget.
- Bad: a 4 MiB model upload and a 64 KiB user record are not the same risk, and one
  number for both is either far too loose or unusable.

### Option 4 — leave it
- Good: no churn.
- Bad: the state the audit reported, with the sharp half fixed and the reason it
  happened intact.

## Links

- completes F16 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
- follows [ADR-0282](0282-store-registry.md) in shape: a registry whose guarantee is a
  completeness test rather than a maintained list
- makes settings of the budgets from [ADR-0272](0272-execution-budget.md) and
  [ADR-0276](0276-iteration-budget.md), which introduced them as defaults with a
  setter and no caller
- relates to [ADR-0018](0018-test-driven-development.md), whose coverage floor is the other
  repository-wide rule that only works while something enforces it
