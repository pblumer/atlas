# ADR-0064: Timer FEEL-failure incidents — park and raise instead of firing immediately

- **Status:** Proposed
- **Date:** 2026-07-25
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0055/0056/0057 gave timer catch and boundary events **FEEL schedules** —
`<timeDuration>=orderTimeout</timeDuration>` — evaluated at command time against
the instance's variables. When that expression can't be evaluated (a missing or
mistyped variable, a value that isn't a usable temporal), the engine currently
**fires the timer immediately**: `timerDue` and the boundary arming fall back to
`due = now`. The comment there is explicit — *"the placeholder until incidents
are modeled (ADR-0055)."*

Firing immediately is the worst possible failure. An escalation boundary with a
typo'd `=slaDedline` cancels its task the instant it starts; a catch event with a
broken `=orderTimeout` proceeds its token at once. The model looks like it ran;
it silently did the wrong thing, and nothing records why.

ADR-0061 landed the incident model: a stuck element parks a token and raises a
durable, operator-resolvable **incident**, cleared on resolve or on instance
cancel. Its follow-up list named exactly this: *"Raise incidents from the
ADR-0055/0056/0057 fail-open FEEL cases (the machinery is now here)."* This ADR
does that for the cases where an incident can be attached — a live element
instance.

## Decision drivers

- **A broken schedule must be visible, not silently wrong.** Parking + an
  incident beats firing immediately in every case.
- **Reuse the incident model (ADR-0061), don't fork it.** Same
  `IncidentCreated` / `IncidentResolved` events, same element-instance keying,
  same cancel-clears-it cleanup.
- **Hold the invariants.** The FEEL failure is detected at command time (in the
  element's `OnActivated`), where variables and the clock are already read; the
  incident — with its frozen `RaisedAt` and message — is a fact, so `applyToState`
  stays pure and recovery replays the parked incident identically (I4/I6).
- **A small, honest slice.** Cover the one-shot **catch** and **boundary** timer
  arms, where a parked element instance exists to hold the incident. Defer the
  recurring-boundary re-arm and start-event timers (below).

## Decision outcome

When a catch or boundary timer's schedule fails to resolve at `OnActivated`,
**raise an incident on the element instead of creating a timer.** The element
stays `Activated` — parked with a token, exactly as it would be while waiting for
a valid timer — but with no timer in the due-date index, so nothing fires it. The
incident carries the element, the FEEL error text as its message, and (like every
incident) a frozen `RaisedAt`.

A **timer incident has no job**, so its `IncidentValue.JobKey` is `0`. That is the
discriminator: `handleIncidentResolved` routes a `JobKey == 0` incident to
**re-arm the parked timer element** — re-running the same resolve-and-arm step
against the instance's now-corrected variables — rather than re-creating a job. If
the schedule resolves this time, the timer is created and the token waits normally;
if it still fails, a fresh incident is raised (resolve is a genuine retry, not a
blind clear).

Cleanup falls out of ADR-0061 unchanged: terminating the element (instance cancel,
interrupting boundary firing) deletes its incident in the same `applyToState`,
because incidents are keyed by element instance regardless of whether a job backs
them.

### What this deliberately leaves out

- **Recurring-boundary re-arm failures.** A non-interrupting cycle boundary that
  resolves on its first arm but whose FEEL later fails on a re-arm (variables
  changed between firings) still silently stops recurring. The element context at
  re-arm time is mid-fire; handling it well is its own slice.
- **Start-event timer FEEL.** A start timer has no instance and no element
  instance, so there is nowhere to attach an incident. Its schedule is
  compiler-constrained to a constant FEEL and an unresolvable one simply is not
  armed (ADR-0056) — unchanged here.

## Consequences

- A modeled FEEL timer that can't evaluate now surfaces as an incident in
  `GET /incidents` and parks its token, instead of firing at once. Operators fix
  the variable and resolve, which re-arms the timer.
- `IncidentValue.JobKey == 0` becomes a meaningful, load-bearing distinction
  (timer incident vs job incident). No schema change — the field already exists.
- The resolve path gains a second branch; both branches remain pure and
  replay-safe. The recurring-boundary and start-event gaps are documented, not
  hidden.
