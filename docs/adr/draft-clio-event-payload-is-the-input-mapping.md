# ADR-DRAFT: A clio event's payload is the task's input mapping

- **Status:** Proposed
- **Date:** 2026-08-21
- **Deciders:** Atlas engine team

## Context and problem statement

A `clio:write-events` connector task appends an event to the clio event store. What
that event *carries* has never been modelled: the worker sends the whole
process-instance variable scope as the body. ADR-0036 said so explicitly as an
interim — its decision outcome names "a **payload mapping** (which process variables
form the event body)" as part of a write task's compiled detail, and the code that
shipped instead read every variable of the instance, commented "until output mappings
exist (Milestone 1) the whole variable scope is the payload".

The general mechanism ADR-0036 was waiting for arrived as ADR-0068: `zeebe:ioMapping`
inputs, evaluated on activation into an **activity-local** variable scope that the
activity and its worker see. Its implementation note lists the clio worker among the
job workers still to be moved onto the scope-chain read.

Until that move, a clio write task with input mappings sent an **empty body**: the
mapped values live at the activity-local scope (keyed by the element-instance key)
and the worker read the process-instance scope, which in a model whose data comes
only from mappings holds nothing. That is the reported symptom — events arriving at
clio with `{}` — and it is what forces the decision *now*: moving the worker onto the
scope chain fixes the empty body, but leaves open what a mapped task's body should be.

Two answers, materially different for an **outbound** connector:

- everything the task sees (its locals *plus* everything inherited), the ADR-0068
  reading that the script worker already implements; or
- only what the model mapped.

An event store is not an internal variable scope. Events are retained, replayed,
projected, and increasingly schema-checked (clio validates against a registered event
schema); a body that silently carries every scratch and internal variable of the
instance leaks process state into an external, append-only system and breaks the
moment a schema is registered for the event type.

## Decision drivers

- **A model should state what leaves it.** An outbound event's shape is part of the
  contract with its consumers, not an accident of which variables the instance
  happens to hold at that moment.
- **ADR-0036's intent.** A payload mapping was always the design; the whole-scope body
  was a stated interim, not a decision.
- **No new concept.** I/O mappings already exist, are already in the properties panel,
  and already compile at deploy time (I5) and freeze their values into variable events
  (I6). A second, clio-only payload syntax would be a parallel mechanism to maintain.
- **Backwards compatibility.** Models that send the instance's variables today must
  keep working unchanged.
- **ADR-0068 consistency.** Diverging from "locals plus inherited" needs to be a
  deliberate, recorded decision, not a quiet difference between two workers.

## Considered options

1. **Scope-chain body (locals + inherited).** Move the worker onto the ADR-0068
   scope-chain read and stop there, exactly as the script worker does. Fixes the empty
   body; input mappings *add to* the payload but cannot restrict it.
2. **Input mappings are the payload (chosen).** With input mappings, the body is
   exactly the activity-local scope they wrote. With none, the body is every variable
   the task sees, resolved up the scope chain.
3. **A dedicated payload field on the clio task.** A FEEL expression (or a named
   variable) on the connector task that evaluates to the event body.

## Decision outcome

Chosen: **option 2 — a write task's input mappings are its event body.**

- **Input mappings present:** the body is the task's activity-local scope, which on
  activation holds exactly the mapped values and nothing else (a job's result is
  written there only on completion). Nothing is inherited: the model said what to send.
- **No input mappings:** the body is every variable visible at the task, resolved up
  its scope chain with the nearest scope winning — so a task inside a subprocess also
  carries that subprocess's variables. For a top-level task in a flat process the
  chain is just the process instance, so the previous behaviour is preserved exactly.

This is scoped to the clio **write** payload — the one place where a variable scope
crosses into an external event store. It does not change what any other connector
sends, nor how FEEL fields elsewhere on a clio task resolve; those follow ADR-0068's
scope-chain rule unchanged.

### Consequences

- **Positive:** a modelled event has a modelled shape — rename, reshape, and restrict
  what a process publishes, using the mapping editor that is already in the panel and
  the mechanism that is already compiled and replay-safe. Internal and scratch
  variables stop leaking into an append-only external store, and a registered clio
  event schema becomes satisfiable from a model. The reported empty-body bug is fixed
  in the same change: a mapped task now sends its mappings instead of `{}`.
- **Negative / trade-offs accepted:** the clio write body deliberately departs from
  ADR-0068's "locals plus inherited" for one field of one connector kind — a task's
  FEEL fields still resolve up the chain while its body does not. Adding a first input
  mapping to an existing write task becomes a breaking change to that task's payload,
  which is the point but must be visible in the panel (the Event-type hint says so).
  A model that wants both a mapped value *and* an inherited one must map both.
- **Follow-ups / risks to watch:** the other job workers (REST, DMN, mail, and the
  rest of `connector/`) still read the process-instance scope flat and so still ignore
  input-mapped locals — the same empty-body bug in a different connector; they should
  move onto the scope chain per ADR-0068, and the REST request body raises the same
  "does the model state what leaves it" question this record answers for clio. If the
  answer generalises, this record should be superseded by one covering every outbound
  body rather than copied per connector.

## Pros and cons of the options

### Option 1 — scope-chain body (locals + inherited)
- Good: literally ADR-0068; one rule for every worker; smallest change.
- Bad: the payload still cannot be restricted, so internal state keeps reaching clio;
  a registered event schema stays unsatisfiable; mappings can only add.

### Option 2 — input mappings are the payload (chosen)
- Good: delivers ADR-0036's payload mapping with no new syntax; the model states what
  leaves it; backwards compatible for mapping-free tasks; fixes the empty body.
- Bad: a connector-local departure from ADR-0068's inheritance rule; adding a mapping
  changes an existing task's payload.

### Option 3 — a dedicated payload field
- Good: the payload is visibly one field; can express a non-object body.
- Bad: a second payload mechanism beside I/O mappings, with its own compile, freeze,
  and replay story; duplicates the mapping editor; ADR-0036's own wording points at
  mappings, and Camunda-shaped models already reach for `zeebe:ioMapping`.

## Links

- delivers the payload mapping ADR-0036 named but left to the variable subsystem
- builds on ADR-0068 (task I/O variable mappings and activity-local scopes) and
  deliberately narrows its inheritance rule for this one body
- honors I5 (mappings compile at deploy) and I6 (mapped values are frozen into
  variable events, so replay re-applies rather than re-evaluates them)
- relates to ADR-0035 (a message throw publishes its instance's variables), which
  keeps the whole-scope rule
