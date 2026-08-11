# ADR-0104: Token simulation — entering embedded subprocesses

- **Status:** Accepted
- **Date:** 2026-08-07
- **Deciders:** Atlas maintainers

## Context and problem statement

The Design-view token simulation (ADR-0078, extended by ADR-0096/0097/0101) walks a token
along sequence flows so a newcomer can *see* the control flow. Every prior increment kept one
deliberate simplification: the simulation is **flat**. Every element — including an embedded
subprocess — was treated as a single node the token rests on and then leaves by its outgoing
flow.

That made a modelled subprocess a lie on screen. Given `Start → Sub[ InnerStart → Task →
InnerEnd ] → End`, the token slid from `Start` straight across `Sub` to `End` and the
subprocess's own content — the whole point of drawing a subprocess — never ran. The inner
start event *was* offered as a separate manual spawn point, so a user could start the inside
by hand, but nothing connected entering `Sub` to running it. The one thing the picture should
teach — "this subprocess runs, and the flow waits for it" — didn't happen.

## Decision drivers

- **Faithful to control flow.** The simulation's whole job is to show how the token moves; an
  expanded subprocess that never runs is actively misleading.
- **Stay a teaching aid, not the engine (ADR-0078/0096).** No FEEL, no correlation keys, no
  data. Just the shape of the flow.
- **Buildless and self-contained (ADR-0012/0013).** Plain JS in the `atlasTokenSimulation`
  bpmn-js module, `go:embed`'d. No npm, no CDN.
- **Reuse the machinery already there.** The OR-join already fires on *quiescence* (no live
  token can still reach it). Subprocess completion is the same idea one scope down.

## Considered options

1. **Leave it flat** and only document that subprocesses are not entered.
2. **Enter and run** an expanded embedded subprocess as a nested scope, completing it on
   quiescence before the outer token continues.
3. **Full nested-instance semantics** — per-entry scope instances, multi-instance
   subprocesses running the body N times, temporarily expanding collapsed subprocesses.

## Decision outcome

Chosen option: **"2 — enter and run expanded embedded subprocesses as a scope"**.

When a token arrives at an **expanded** embedded subprocess (or transaction) that has a
rendered plain start event, the simulation now:

- **Enters** it: a **held token** rests on the subprocess shape (the badge shows it, and an
  `atlas-sim-scope` tint marks the running container), and a fresh token spawns on each inner
  plain start event. The held token is excluded from Play/Step/pump — it must not be
  hand-advanced out.
- **Runs** the inner flow with the ordinary machinery — gateways, joins, events, nested
  subprocesses all just work, because they are the same elements one level down.
- **Completes** on **quiescence**: once no token rests, animates, or waits in a join anywhere
  *inside* the subprocess, the held token(s) leave by the subprocess's outgoing flow(s). An
  **inner** end event therefore does **not** count as a process completion — only the token
  that finally runs off the top-level graph does. An **interrupting boundary event** on the
  subprocess cancels the scope: every token inside it goes away and the token leaves via the
  boundary.

What stays flat, on purpose (fall through to the old pass-over behaviour):

- **Collapsed subprocesses** — their inner shapes are not in the diagram, so there is nothing
  to animate. They pass the token over as before.
- **Multi-instance subprocesses** — they keep the ADR-0097/0100 instance-count badge rather
  than entering; running a nested body N times is deferred.
- **Call activities** — a called process is a separate diagram the Design view does not have.

### Consequences

- **Positive:** an expanded subprocess finally *does something a newcomer can watch* — the
  flow descends, runs the inside, and waits for it before moving on. It reuses the quiescence
  idea from the OR-join, so the new code is small and consistent, and nested subprocesses fall
  out for free.
- **Negative / trade-offs accepted:** the simulation is no longer strictly flat — it tracks a
  set of active scopes and tests scope membership by walking the parent chain. Collapsed and
  multi-instance subprocesses still don't run their bodies; an interrupting boundary on a
  subprocess aborts in-flight dots via the global epoch bump (as an interrupting event
  subprocess already does), which is coarse but matches existing behaviour. Multiple tokens
  entering the same subprocess share one scope instance and all leave together, rather than
  modelling independent instances.
- **Follow-ups / risks to watch:** multi-instance subprocesses that visibly run the body N
  times; per-entry scope instances if a diagram ever needs them.

## Relationships

- extends ADR-0078 (Design-view token simulation) and ADR-0096/0097/0101 (its later increments)
- builds on ADR-0012 / ADR-0013 (embedded, buildless, self-contained modeler)
- teaches the shape of ADR-0074 (embedded-subprocess scope lifecycle) without being the engine
