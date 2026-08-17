# ADR-0130: Deprecating a process version — a drain state distinct from pausing

- **Status:** Proposed (sketch for discussion — see "Open questions")
- **Date:** 2026-08-17
- **Deciders:** Atlas maintainers

## Context and problem statement

Redeploying a process id mints a new version (ADR-0019) and leaves every earlier
version deployed. The Deployments view of an application (ADR-0128) therefore lists
v1…v4 of the same process side by side, which raised the question this ADR answers:
**should an old version still be startable at all, and if not, what marks it?**

Establishing the facts first, because the answer depends on them and two of them are
not what the names suggest:

1. **Superseding already works for automatic starts.** Deploying a new version
   retires the previous one's message, signal, and timer start subscriptions
   (`engine/processor.go:257`, `:338`). Only the newest version instantiates on an
   incoming message, a broadcast signal, or a timer schedule. This was once a real
   defect — one message fanned out into one instance per deployed version — and is
   fixed. So the "several versions all firing" risk does **not** exist today.
2. **An explicit start can still target any version.** `POST /processes/{key}/instances`
   resolves a definition key and starts it; it checks only that the deployment exists
   and is executable. Older versions are reachable by design, and at least one feature
   depends on that: a per-server call-activity override can deliberately **pin** a
   called process to a specific version (ADR-0105, `PinnedDefKey`).
3. **"Paused" (ADR-0119) is weaker than its name.** The `inactive` flag is consulted
   only on the auto-start path (`engine/behavior.go:895`). An explicit API start of a
   *paused* definition succeeds today. ADR-0119 was written for "stop the timer-driven
   runs during a maintenance window", and for that it is correct — but it does not mean
   "no new instances".

So an operator today has no way to say: **this version is finished; let its running
instances drain, but let nothing new start on it by any route, until I delete it.**
Delete does not fill that gap either — it is refused while instances are running, which
is exactly when the operator wants to stop new work.

## Decision drivers

- **Don't break deliberate version targeting.** Pinning a call activity to an old
  version (ADR-0105) is a supported operator act. A rule that silently makes old
  versions unusable would break it, and would do so at runtime rather than at
  configuration time.
- **Automatic supersession is not the problem.** The engine already prevents the
  fan-out. Any new mechanism must justify itself by what it adds *beyond* that.
- **Reversibility and determinism (ADR-0119's constraints still hold).** Whatever gates
  instantiation must not make replay diverge from the live run: `applyToState` stays
  pure, the flag is operator config pushed into the processor, and running instances
  are untouched.
- **Name things by what they do.** The current pair (paused/active) already misleads;
  a third state must not add a fourth ambiguity.
- **Deletion needs a runway.** "Stop new work → wait for drain → delete" is the
  lifecycle operators actually want, and today the middle step is missing.

## Considered options

1. **Do nothing beyond the display fix.** The Deployments view now distinguishes
   *current* from *superseded*, which is honest about what the engine does. No new
   runtime state.
2. **Automatic: a superseded version becomes non-startable.** Deploying v4 makes v1–v3
   refuse every start, explicit ones included.
3. **An operator-set `deprecated` state**, a third value alongside active/paused: no new
   instances by *any* path (auto-start, explicit API, call-activity target), running
   instances continue, reversible, and the intended step before delete.
4. **Auto-deprecate on supersede, with an explicit opt-out** for a version something
   pins.

## Decision outcome (proposed)

Chosen: **option 3 — an operator-set `deprecated` state.**

`deprecated` is a *lifecycle* state an operator sets, not a consequence of deploying
something newer. It means: **this version accepts no new instances, from any route.**
Its running instances keep running to completion; its history stays; the definition and
its diagram stay readable. It is reversible (back to active) until the definition is
deleted.

The distinction from the existing flag, stated so the two never blur:

| | stops auto-starts (timer/message/signal) | stops explicit starts | intent |
|---|---|---|---|
| **paused** (ADR-0119) | yes | **no** (today) | temporary hold, resume expected |
| **deprecated** (this ADR) | yes | **yes** | terminal, drain toward deletion |

Consequences of that definition, each deliberate:

- **Pinning interacts at configuration time, not at runtime.** Setting a call-activity
  pin to a deprecated version is refused when the override is set, with a clear reason,
  rather than failing later when an instance tries to call it. An existing pin to a
  version that is *then* deprecated is surfaced as a warning in the call-activity view;
  the deprecation still applies, because the operator's later explicit act wins over an
  earlier one.
- **Superseding stays automatic and separate.** Deploying a new version continues to
  retire the old one's start subscriptions and nothing more. *Superseded* stays a
  derived, displayed fact (ADR-0128's Deployments view); *deprecated* is stored operator
  config. Conflating them is exactly what option 2 does and what this ADR rejects.
- **Delete gets its runway.** The recommended sequence becomes deprecate → watch the
  running count fall to zero → delete, with the existing "refused while instances run"
  guard unchanged as the backstop.

Option 2 is rejected because it makes an old version unusable as a side effect of an
unrelated act (deploying something new), breaking ADR-0105 pinning at runtime and
removing a capability operators may be relying on, all to prevent a fan-out the engine
already prevents. Option 4 is the same rule with an escape hatch, and inherits the same
objection: the default is still a silent runtime behaviour change driven by a deploy.
Option 1 remains the honest fallback if the added state does not earn its keep — see
the open questions.

### Sketched shape

Storage and plumbing follow ADR-0119 exactly, which is the point of choosing this
option — it is a widening of an existing mechanism, not a new one:

- The deploy sidecar's operator-config field widens from a boolean to a lifecycle value
  (`""`/active, `paused`, `deprecated`), with the absent value reading as active so
  every existing record loads unchanged and no migration is needed.
- The processor's `inactive` set becomes a per-key lifecycle map, consulted on the
  auto-start path as today and **additionally** on the explicit create path, which is
  the one new gate.
- `PUT /api/v1/processes/{key}/active` widens (or gains a sibling) to set the state;
  the deployments view reports it; the UI shows it as a third pill next to
  current/superseded.

### Consequences

- **Positive:** the missing "stop new work, drain, then delete" step exists; the
  explicit-start hole in "paused" is closed for the case that needs it; deliberate
  version targeting keeps working; no engine invariant is touched (operator config
  pushed into the processor, `applyToState` untouched, running instances unaffected).
- **Negative / trade-offs accepted:** a third state to explain and to render; the
  explicit-start gate is a new runtime check on a hot-ish path (a map lookup, but a
  check that did not exist); an interaction with ADR-0105 pinning that has to be
  handled in two places (set time and display time); and it is operator-driven, so a
  team that never deprecates anything keeps accumulating startable old versions —
  which is the status quo, not a regression.
- **Follow-ups / risks to watch:** whether "paused" should *also* block explicit starts
  (it arguably should — see open questions); whether deprecation should be offered
  automatically as a *suggestion* in the UI when a version is superseded and has zero
  running instances; and whether an application-level "deprecate everything below the
  current release" action is worth having once ADR-0128 releases are in wider use.

## Open questions (this is a sketch)

1. **Should `paused` also block explicit starts?** Today it does not, which is
   surprising given the name. Fixing that would make paused and deprecated differ only
   in *intent*, which may be too thin a distinction to justify two states — in which
   case option 1 plus a stricter `paused` is the smaller, better answer, and this ADR
   collapses into a fix of ADR-0119.
2. **Auto-suggest vs. auto-apply.** Is a superseded version with zero running instances
   something the UI should offer to deprecate in one click, or should that stay fully
   manual?
3. **Scope.** Is deprecation per definition version (as sketched) or should an
   application-level release be deprecable as a unit (ADR-0128)?

## Links

- extends [ADR-0119](0119-deactivate-deployed-process.md) (the operator flag this
  widens into a lifecycle, and whose explicit-start gap this documents)
- relates to [ADR-0019](0019-durable-deployments.md) (per-processId versioning, and the
  sidecar this state is stored on)
- relates to [ADR-0105](0105-per-server-call-activity-target-overrides.md) (version
  pinning — the deliberate old-version targeting that rules out automatic
  non-startability)
- relates to [ADR-0128](0128-process-applications.md) (the Deployments view that
  surfaces current vs superseded, and where a deprecated state would render)
- relates to [ADR-0035](0035-message-start-events.md),
  [ADR-0051](0051-timer-start-events.md), [ADR-0088](0088-signal-events.md) (the
  automatic start paths that supersession already retires)
