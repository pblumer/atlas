# ADR-0097: Audit trail for external variable modifications

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0095 added the operator escape hatch to set or overwrite variables on a running
instance (`POST /api/v1/instances/{key}/variables`). It records each override as an
ordinary `VariableCreated`/`VariableUpdated` event, so the *value* change lands in the
existing per-step variable timeline (ADR-0048). ADR-0095 explicitly deferred one
thing to a follow-up: the timeline shows *what* changed but not *who* changed it, and
an override is indistinguishable there from a write the model made for itself.

Editing live process state is a sensitive, admin-gated action. Without attribution
there is no answer to "who set this variable, and when" — the first question asked
when a corrected instance later behaves unexpectedly. This ADR adds that attribution.

## Decision drivers

- **Accountability.** A sensitive manual override must leave a durable, inspectable
  "who/when/what" record, not just a value change.
- **Invariant integrity.** Attribution must be a persisted fact rebuilt by replay
  (invariants I4/I6), never a side write, and must not touch the hot path (I1).
- **Don't pollute the operational model.** The core `VariableValue` and its codec are
  read and written on every model variable write; attribution is relevant only to the
  rare external override.
- **Distinguish overrides from model writes.** The audit must surface exactly the
  external corrections, separate from the model's own variable writes.

## Considered options

1. **Add an `Actor` field to `VariableValue`** and surface it in the existing
   variable timeline.
2. **A dedicated, append-only audit event** (`VTVariableAudit`) emitted alongside the
   variable event, keyed by process instance, read via its own endpoint.
3. **Log the override to an external/operational log** outside the event store.

## Decision outcome

Chosen option: **"a dedicated append-only audit event"**, mirroring the
decision-evaluation history record (ADR-0066). A new persisted value type
`VTVariableAudit` with intent `IntentVariableAudited` carries `{ProcessInstanceKey,
ScopeKey, Actor, Name, Kind, Bool, Text}`. `handleVariablesModify` emits one such
event per variable it sets, right after the `VariableCreated`/`VariableUpdated`, and
`applyToState` records it in a per-instance history column family
(`cfVariableAudit`), keyed `(piKey, ts, pos)` so it folds into the same instance
timeline by log position and survives the instance finishing.

The acting principal's username is threaded from the HTTP layer onto the modify
command (`Command.Actor`, `Processor.SetVariables(piKey, scopeKey, actor, vars...)`)
and frozen into the event, so replay rebuilds the identical attribution without
re-running the command (invariant I6). It is read via
`GET /api/v1/instances/{key}/variable-audit`, returning each override's time, actor,
scope, variable name, and typed new value.

**Actor semantics.** Under auth, the actor is the authenticated username. With auth
off (single-user) or an unidentified caller, it is `""` — there is no identity to
attribute, and the record still captures when/what.

### Consequences

- **Positive:** Full "who changed it" trail, durable and replay-safe (I4/I6), off the
  hot path (I1). The override is now distinguishable from a model write by the mere
  existence of an audit record. `VariableValue` and its codec are untouched, so every
  model variable write is unaffected. Reuses the ADR-0066 history-event pattern
  end to end (value type, column family, per-instance scan, read endpoint).
- **Negative / trade-offs accepted:** A second event per overridden variable (one
  audit record alongside each variable event). This is negligible: overrides are a
  rare, manual, low-volume operation, never on the token-movement path. The audit
  read is not yet exposed as an MCP tool (the MCP service principal is non-admin and
  cannot perform overrides anyway) — a recorded follow-up.
- **Follow-ups / risks to watch:** Surface the actor inline in the unified
  instance timeline/replay view, not only via the dedicated endpoint. Expose the
  audit read as an MCP tool if agent-facing introspection wants it. When a finer
  "operator" role lands (ADR-0044 trajectory), attribute to it as well.

## Pros and cons of the options

### Option 1 — Actor field on `VariableValue`
- Good: smallest surface; the actor rides the existing snapshot timeline for free.
- Bad: bloats the core variable model and changes its codec for a field empty on ~all
  writes; stores an actor on the live variable record too; does not by itself
  distinguish an override from a model write. Rejected.

### Option 2 — dedicated audit event (chosen)
- Good: clean separation; append-only history; respects every invariant; reuses the
  decision-evaluation precedent; overrides are self-evidently distinct.
- Bad: more moving parts (a value type, column family, endpoint) and a second event
  per override.

### Option 3 — external operational log
- Good: zero engine change.
- Bad: not replay-derived, not part of the instance's durable history, and drifts from
  the event store the rest of the system trusts. Rejected as inconsistent with
  event sourcing (ADR-0001).

## Links

- follows up ADR-0095 (external variable modification)
- mirrors ADR-0066 (durable decision-evaluation records) as the history-event pattern
- relates to ADR-0044 (authentication boundary — source of the actor), ADR-0048
  (per-step variable snapshots — the value timeline this attributes)
