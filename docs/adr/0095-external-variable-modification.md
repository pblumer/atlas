# ADR-0095: External variable modification on a running instance

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

Until now a process variable could only be written by the model itself: seeded as a
start variable, produced as a job/task output, mapped by a zeebe:ioMapping, carried
in by a correlated message, or written by a data association. Every variable had a
legitimate in-model writer, and the HTTP API exposed variables read-only
(`GET /instances/{key}/variables`).

Long-running instances break that tidiness in production. A service task fails
because a variable holds a bad value (malformed upstream data, a wrong id); an
instance is parked mid-flight and cancel-and-restart is not an option because it has
already produced irreversible side effects (a payment sent, a mail delivered). The
operator needs to correct a single variable on a live instance and let it continue —
the same escape hatch Camunda (`PUT /process-instance/{id}/variables`) and Zeebe
(`SetVariables`) ship. The question: how does Atlas offer that without weakening the
event-sourcing guarantees, and with what scope and authorization boundary?

## Decision drivers

- **Invariant integrity.** The correction must not become a raw store write behind
  the log. State only ever changes through `applyToState` from a persisted event
  (invariants I4/I6), and recovery must rebuild the corrected value.
- **Auditability.** An operator override of live process state must leave a durable,
  inspectable trace, not silently mutate state.
- **Operational reality.** Corrections target *running* instances; a finished
  instance's state is history, not something to edit.
- **Least privilege.** Editing live process state is more dangerous than starting an
  instance and should be a narrower right.

## Considered options

1. **Raw store upsert** from the HTTP handler straight into the variable column
   family, bypassing the log.
2. **Model the correction explicitly** — require every process to include an admin
   task / message path for each variable that might need fixing.
3. **A command → event path** (chosen): a new command-only intent the processor
   folds into ordinary `VariableCreated`/`VariableUpdated` events.

## Decision outcome

Chosen option: **"a command → event path"**. A new command-only intent
`IntentVariableModify` (never persisted, like `IntentTimerStartArm`) carries the
target process instance, an optional target scope, and the variables to set. Its
handler, `handleVariablesModify`, validates that the instance is live and the scope
belongs to it, then emits one `VariableCreated` event per name new to the scope and
one `VariableUpdated` per name already present — the same events, the same
`applyToState`, the same variable-snapshot timeline the model's own writes use.

The engine surface is `Processor.SetVariables(piKey, scopeKey, vars...)`; the HTTP
surface is `POST /api/v1/instances/{key}/variables` with body
`{"variables": {…}, "scopeKey": <optional>}`.

**Scope.** Variables default to the instance root scope. An optional `scopeKey` — a
live element instance key belonging to the instance — targets an embedded
subprocess / multi-instance-body local scope instead (the activity-local scopes of
ADR-0068). A `scopeKey` that is not a live element instance of the instance is
rejected, so a stray key can never orphan a variable under a scope no FEEL lookup
reaches.

**Authorization.** The endpoint is gated by `requireAdmin` when auth is enabled — a
deliberately narrower right than starting an instance — and open in single-user mode
like the rest of the runtime surface (ADR-0044).

### Consequences

- **Positive:** The correction is a durable, replayable fact recorded in the
  instance's variable timeline (invariants I4/I6 intact); event sourcing makes this
  *more* auditable than a classic engine's side write. Reuses the existing variable
  events, handler pattern, and read endpoint — a small, in-keeping change.
- **Negative / trade-offs accepted:** It does **not** re-drive the token — a variable
  a gateway has already routed on is not re-evaluated, only its stored value changes.
  This matches Camunda/Zeebe semantics and is documented on the API, but it can
  surprise a caller who expects the instance to re-route. The admin gate is coarse
  (MVP RBAC is admin-vs-not, ADR-0044); a finer "operator" role can refine it later
  without reshaping this path.
- **Follow-ups / risks to watch:** A future audit view could surface *who* made an
  override, not just the value change (needs the acting principal threaded into the
  event, which the engine does not carry today). A typed/validated variable contract
  could reject a set that violates a declared schema once one exists.

## Pros and cons of the options

### Option 1 — raw store upsert
- Good: trivial to implement.
- Bad: bypasses the log; the corrected value would vanish on recovery and leave no
  audit trail. Breaks invariants I4/I6. Rejected outright.

### Option 2 — model the correction explicitly
- Good: keeps all writes in-model.
- Bad: requires foreseeing every fault at design time — precisely what operators
  cannot do. Bloats every model with admin plumbing. Does not solve the
  unforeseen-correction case at all.

### Option 3 — command → event path (chosen)
- Good: durable, replayable, auditable; reuses existing machinery; respects every
  invariant.
- Bad: a new intent and handler to maintain; the no-re-evaluation semantics need
  documenting.

## Links

- relates to ADR-0001 (event sourcing), ADR-0044 (authentication/authorization),
  ADR-0048 (variable snapshot timeline), ADR-0068 (activity-local variable scopes)
- builds on the variable-writing path of ADR-0035 (start variables) and the job
  output path
