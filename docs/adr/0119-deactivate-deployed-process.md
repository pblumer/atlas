# ADR-0119: Deactivating a deployed process

- **Status:** Accepted
- **Date:** 2026-08-12
- **Deciders:** Atlas maintainers

## Context and problem statement

A deployed definition can start instances of its own accord: a timer start event
fires on schedule (ADR-0051), a message start event instantiates on a correlating
message (ADR-0035), a signal start event on a broadcast (ADR-0088). Operators need to
**pause** such a process without deleting it — a timer-driven process that should stop
kicking off runs during a maintenance window, an integration whose downstream is down,
a scheduled job to hold while a bug is investigated — and resume it later, unchanged.

Today the only lever is delete, which is refused while instances run and throws the
definition (and its diagram, versions, and armed timers) away. There is no reversible
"stop starting new instances, but keep everything and keep the running ones going."

The question: how does an operator mark a deployed definition inactive so it does not
auto-start new instances, in a way that is durable, survives restart, and does not
compromise the engine's determinism invariants (I4/I6)?

## Decision drivers

- **Reversibility.** Pause and resume must be cheap and lossless — no redeploy, no lost
  timers, no effect on running instances.
- **Determinism (I4/I6).** Whatever gates instantiation must not make replay diverge
  from the live run. `applyToState` stays pure; events remain the sole source of truth.
- **Single-writer (I3).** The flag is touched only by the run-loop goroutine, like the
  rest of process state and the existing operator-config maps.
- **Operational simplicity.** Reuse the deployment sidecar and the established
  "operator config pushed into the processor" pattern rather than inventing a new
  durable mechanism.

## Considered options

1. **A live gate in the create path, flag stored on the deploy sidecar and pushed into
   the processor** (mirrors ADR-0105 call-activity overrides).
2. **Undeploy/redeploy as the pause mechanism** — cancel the armed timers and drop the
   start-event indexes, re-arm on resume.
3. **An event-sourced suspend/resume** — persist `ProcessDeactivated` / `ProcessActivated`
   in the WAL and fold them in `applyToState`.

## Decision outcome

Chosen option: **"a live gate in the create path"**. The processor holds an `inactive`
set of definition keys, set on the run loop via `SetProcessActive(defKey, active)` and
read as `ProcessActive(defKey)`. The three automatic start triggers —
`fireStartTimer`, the message-start loop, and the signal-start loop — skip scheduling
their create-instance followup when the definition is inactive. Nothing else changes:
an explicit operator/API `CreateInstance` is **not** gated (an explicit start is a
deliberate act), and running instances are untouched.

The flag is operator configuration, exactly the category of ADR-0105's call-activity
overrides: it is **not** event-sourced. It lives as `Inactive bool` on the deployment
record (`persistedDeployment`, the ADR-0019 sidecar), written with `omitempty` so every
pre-existing record loads active by default. On a fresh toggle the server rewrites the
record (durable before visible, I2) and then calls `SetProcessActive`; on restart,
`loadDeployments` re-applies the flag before the loop serves traffic and before timers
tick.

Surfaced over HTTP as `PUT /api/v1/processes/{key}/active` with body `{"active": bool}`,
reported as `active` on the process listing, and toggled from the Modeler's Deployed
list with an "Inactive" badge and an Activate/Deactivate button.

### Consequences

- **Positive:** Reversible and lossless — pausing keeps the definition, its versions,
  its diagram, its armed timers, and its running instances. Resume is one call. No new
  durable mechanism: the flag rides the existing sidecar and the existing operator-config
  pattern. The gate is a single boolean read at the three trigger sites.
- **Positive (determinism):** The flag gates only the *live* decision to schedule a
  create followup. It is never consulted in `applyToState`, so no `Activated` event is
  ever suppressed on replay — a run that created an instance persisted the events, and a
  run that did not created nothing to replay. I4/I6 hold with no new event type.
- **Negative / trade-offs accepted:** A **one-shot** timer start (a duration/date, not a
  cycle) that comes due while the process is inactive is *missed*, not deferred — the
  timer fires, is consumed, and no instance is created. Recurring timers (the motivating
  case) keep ticking and re-arming, so they resume cleanly. Deactivation is **per
  definition key (per version)**: deploying a new version starts active regardless of a
  prior version's flag, since only the latest version's start timers are armed
  (ADR-0051). An explicit `CreateInstance` still runs on an inactive definition by
  design; "inactive" means "does not start *itself*", not "cannot be started".
- **Follow-ups / risks to watch:** If a future need arises to also suspend *running*
  instances, or to block explicit starts, that is a separate, larger decision (a new
  ADR) — this one deliberately scopes to the automatic start triggers. Public start
  links (ADR-0029) are an explicit external start and are intentionally out of scope.

## Pros and cons of the options

### Option 1 — live gate + sidecar flag (chosen)
- Good: reversible, lossless, no new event type, reuses ADR-0019/ADR-0105 patterns,
  trivially I4/I6-safe (the flag never touches `applyToState`).
- Good: the three trigger sites are the natural, single choke points for "auto-start".
- Bad: the flag is not in the WAL, so a WAL-only replay into a fresh processor is active
  until the server layer re-applies it — acceptable, since deployments are re-registered
  from the sidecar on the same path (the call-override precedent).

### Option 2 — undeploy/redeploy to pause
- Good: no new state at all.
- Bad: not lossless — cancelling and re-arming timers loses the original schedule anchor,
  and undeploy is refused while instances run, which is exactly when a pause is wanted.
  Fails the reversibility driver.

### Option 3 — event-sourced suspend/resume
- Good: the flag would survive a pure WAL replay with no server-layer re-apply.
- Bad: introduces a new value type/intent and folds a non-instantiation decision into
  `applyToState` for no determinism gain — instantiation is already frozen in the events
  the create path emits. Heavier than the problem warrants; diverges from the established
  operator-config category (ADR-0105) for no benefit.

## Links

- relates to ADR-0019 (durable deployment sidecar) — the flag's storage
- relates to ADR-0105 (per-server call-activity target overrides) — the operator-config,
  not-event-sourced pattern this mirrors
- relates to ADR-0051 (timer start events), ADR-0035 (message start events),
  ADR-0088 (signal start events) — the three triggers gated
- upholds invariants I3 (single writer), I4 (one `applyToState`), I6 (events are facts)
