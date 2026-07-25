# ADR-0061: Incident model — job-failure incidents, raise, resolve, resume

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

> **Implementation note.** The in-process job runner now routes a worker handler's
> failure into this model: instead of surfacing a hard error that aborts the whole
> drive (and every deploy or completion that drives jobs — one un-runnable job, e.g.
> a business rule task whose decision model isn't deployed, used to fail every
> future deploy), it calls `FailJob(retries-1, message)`, which retries while
> retries remain and then raises an incident that parks the token. So a single
> failing job can no longer poison the run loop; it becomes a visible, resolvable
> incident. Job leases with timeout/backoff remain a later milestone.

## Context and problem statement

Atlas has no way to represent a *stuck* instance. When work can't proceed — a job a
worker keeps failing, an expression that won't evaluate — the engine either
swallows the failure (a FEEL error becomes `null`, a FEEL timer that can't resolve
fires immediately, per ADR-0055) or simply has no path at all: nothing fails a job
today, so a job that a worker cannot complete would just be retried forever with no
record of why.

BPMN/Zeebe model this as an **incident**: a durable fault attached to the element
where progress stalled, holding the token there until an operator inspects and
resolves it. The `VTIncident` value type and the `IncidentCreated`/`IncidentResolved`
intents were reserved for this (model/record.go) but never given a value, state, or
behavior. This ADR gives the incident its first real form, for the clearest and
most common trigger: **a job whose retries are exhausted.**

## Decision drivers

- **Hold the invariants.** Raising and resolving an incident are deterministic
  `applyToState` mutations replayed identically on recovery (I4); generated keys and
  timestamps are frozen into events (I6). No new hot-path allocation (I1) — job
  failure is a worker-driven, off-hot-path event.
- **One mechanism for "blocked".** Prefer a single rule that makes a job
  un-activatable rather than a parallel "blocked" flag to keep in sync.
- **Reuse the job lifecycle.** A resolved incident should return the job to the
  normal activatable path a fresh job takes, so a retry after resolution is
  indistinguishable from a first attempt.
- **A small, honest slice.** Job-failure incidents, raise/resolve/resume, recovery-
  tested. Expression/timer-driven incidents and the operator UI are stated follow-
  ups.

## Decision outcome

**Failing a job carries the remaining retry count** (Zeebe's model): a worker (or
the API) reports `FailJob(jobKey, retries, message)`. `handleJobFailed` sets the
job's retries to that value and re-emits the job:

- **retries > 0** — the job is *retried*: it goes back on the activatable index for a
  worker to pick up again.
- **retries ≤ 0** — an **incident is raised**: an `IncidentValue` is created and the
  job parks, no longer activatable.

The single mechanism behind "blocked" is a rule change in `Tx.PutJob`: **a job is on
the activatable index iff `Retries > 0`.** Every existing job is created with
positive retries, so nothing changes for them; a 0-retry job is stored but never
handed to a worker. No separate blocked flag.

**An incident is keyed by its element instance.** One activity holds at most one
job, so at most one incident; keying the `IncidentValue` by `ElementInstanceKey`
makes lookup O(1) and cleanup trivial. It carries the process instance key, the job
key, the stuck element (its compiled-graph id, which maps to a BPMN element for the
operator), the raised-at time (read at command time and frozen into the event, I6),
and the failure message. A new `cfIncident` column family stores it; a store scan
lists incidents for the operator view.

**Resolving an incident resumes the work.** `ResolveIncident(elementInstanceKey,
retries)` (operator action) emits `IncidentResolved` (deleting the incident) and
re-creates the job with `retries` (default 1) positive, so it returns to the
activatable index and a worker retries it — the same path a fresh job takes. If the
underlying cause is unfixed the job fails again and a new incident is raised.

**Cleanup falls out of keying.** When an element instance is terminated (instance
cancel, interrupting boundary), its `Terminated` apply also deletes the incident
keyed by that element — an idempotent delete, harmless when there is none — so a
cancelled instance leaves no orphan incident.

Everything is expressed with `JobFailed`, `IncidentCreated`, `IncidentResolved`,
and the existing `JobCreated` events, so `applyToState` stays pure and recovery
replays a raised — or resolved — incident identically.

### Consequences

- **Positive:** a stuck instance is now a first-class, durable, inspectable fact
  instead of a silent hang or an infinite retry. Resolution reuses the job
  activatable path, so retry-after-resume needs no special case. Recovery and
  cleanup are free (ordinary events; incident keyed by element). The
  activatable-iff-retries-positive rule is a simpler model than a blocked flag.
- **Negative / trade-offs accepted:** this slice raises incidents only from job
  failure. The ADR-0055 fail-open cases (a FEEL timer that can't resolve, a FEEL
  eval error) are *not* yet rerouted to incidents — they remain follow-ups, though
  the machinery is now here to do so. No retry backoff/deadline is modeled (a failed
  job is immediately re-activatable). The operator surface is minimal (a query +
  resolve command); a full incidents UI is a follow-up.
- **Follow-ups:** raise incidents from expression/timer failures (closing the
  ADR-0055/0056/0057 fail-open note); retry backoff via the job deadline; an HTTP
  API + operator "incidents" view; incident types beyond a free-text message.

## Alternatives considered

- **A "blocked" flag on the job.** A second source of truth to keep in lockstep with
  the retry count; the activatable-iff-retries rule needs no flag.
- **Delete the job and store its fields in the incident, recreate on resolve.**
  Loses the stable job key across the incident and duplicates the job's fields into
  the incident record. Keeping the job (un-activatable) and pointing the incident at
  it is less state.
- **Auto-decrement retries on the engine side.** Zeebe lets the worker report the
  remaining count, which lets a worker distinguish a retryable from a terminal
  failure. Carrying it on the command matches that and keeps the engine from
  guessing.

## Links

- gives a value and behavior to the `VTIncident` / `IncidentCreated` /
  `IncidentResolved` reservations (model/record.go)
- relates to ADR-0055/0056/0057 (the fail-open FEEL-timer cases a later slice can
  reroute here)
- honors the invariants in docs/architecture/invariants.md (I1, I4, I6)
