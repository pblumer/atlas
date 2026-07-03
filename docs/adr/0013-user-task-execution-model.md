# ADR-0013: User-task execution model

- **Status:** Accepted
- **Date:** 2026-07-03
- **Deciders:** Core team

## Context and problem statement

BPMN user tasks are work performed by a human: a task appears in someone's list,
they open a form, fill it in, and complete it. ADR-0011 makes the tasklist a
first-class surface, but the engine has no notion of a user task today — the
roadmap only mentions receive tasks. Before a tasklist can show anything, the
processor needs a runtime model for user tasks: how one is created, who it is
offered to, how it is claimed and completed, and how all of that stays durable
and replayable.

The shape is familiar. A service task hands work to an external worker and gets a
result back as a command (ADR-0007). A user task is the same *hand-out-and-get-a-
command-back* pattern with a different consumer — a person via the tasklist API
instead of a job worker via gRPC — and a different lifecycle (no short lease;
tasks persist until a human acts, and can be claimed/reassigned in between).

The question: do user tasks reuse the job machinery, or are they a distinct
record type that borrows its philosophy?

## Decision drivers

- **Durable before visible (invariant 2, ADR-0005).** A task must not appear in
  anyone's list until its creating event is on disk.
- **Single writer / one `applyToState` (invariants 3–4).** Claim, assign,
  complete, and unclaim must all be commands folded into events on the one
  processor path, identically live and on recovery.
- **Human lifecycle, not worker lifecycle.** Tasks are queried by assignee and
  candidate group, live for minutes-to-days, and are claimed/reassigned — not
  leased for seconds and auto-retried.
- **Determinism.** Assignment (candidate users/groups, assignee expressions) must
  resolve deterministically enough to replay; anything non-deterministic is
  written into the event, not recomputed (invariant 6).
- **Reuse the proven pattern** from ADR-0007 without forcing a human lifecycle
  into a worker-shaped hole.

## Considered options

1. **Reuse jobs as-is** — model a user task as a job of a special type pulled by
   the tasklist.
2. **Distinct user-task record, same philosophy** — a dedicated record type and
   index with its own lifecycle, completed by a command, mirroring ADR-0007's
   hand-out-then-command-back shape.
3. **Inline/human-blocking** — not applicable; the processor never blocks.

## Decision outcome

Chosen option: **a distinct user-task record type that borrows the ADR-0007
philosophy.**

1. Activating a user task emits a `UserTaskCreated` event carrying its assignment
   (candidate users/groups and/or an assignee, resolved from the compiled model —
   FEEL assignee expressions evaluated once and written *into* the event so
   replay is deterministic) and its **form reference** (see ADR-0014). It is
   indexed as **claimable**, keyed for query by assignee and candidate group.
2. After the batch's fsync, a post-commit side effect notifies tasklist
   subscribers (same durable-before-visible rule as job notification).
3. Humans interact through the tasklist API with commands that flow back through
   the processor and become events via `applyToState`:
   `ClaimUserTask` / `UnclaimUserTask` / `AssignUserTask` (lifecycle) and
   `CompleteUserTask` (carrying the form output as variables), which moves the
   element instance to `COMPLETING`.
4. There is **no short lease and no auto-retry.** A task persists until completed
   or until the element's scope is terminated (e.g. an interrupting boundary
   event cancels it). Optional due dates and follow-up dates are index entries,
   not leases.

The record is distinct from `Job`, but the transport and API layer is shared
where it helps: the tasklist API is another consumer of the same post-fsync
notification mechanism.

### Consequences

- **Positive:** The processor never blocks on human latency; backpressure is
  natural (tasks queue in state). The human lifecycle (claim/reassign, long-lived,
  no retry) is modeled honestly instead of contorted into leases. Every state
  transition is a durable, replayable event on the single-writer path. Assignment
  is deterministic on replay because it is written into the creating event.
- **Negative / trade-offs accepted:** A second hand-out mechanism alongside jobs
  (distinct record, index, and commands) — more surface than reusing jobs, chosen
  because the lifecycles genuinely differ. Assignment/authorization semantics
  (who may claim/complete) introduce an identity concept the engine did not have;
  the engine stores candidate/assignee *data* but delegates authentication to the
  API/tasklist layer.
- **Follow-ups / risks to watch:** Define the claimable index keys and query
  filters. Decide interrupting-boundary cancellation of an in-flight task.
  Specify due-date/follow-up-date indexing (reuse the timer due-date scanner from
  Milestone 2). Nail down the identity/authorization boundary — the engine holds
  assignment data; auth lives in the API layer. Add user tasks to `ROADMAP.md`.

## Lifecycle

```
UserTaskCreated ──notify──► tasklist shows it
      │                          │
      │                   ClaimUserTask ──► assignee set
      │                          │
      └── scope terminated       CompleteUserTask (+ variables) ──► COMPLETING
          ──► UserTaskCanceled
```

## Pros and cons of the options

### Reuse jobs as-is
- Good: no new record type; one hand-out mechanism.
- Bad: leases/retry/type-pull semantics are wrong for humans; assignment and
  claim/reassign don't fit the job model; queries differ fundamentally.

### Distinct record, same philosophy
- Good: honest human lifecycle; reuses the proven durable-hand-out-then-command
  pattern and the post-fsync notification path; clean queries.
- Bad: a second mechanism to build and maintain.

## Links

- mirrors ADR-0007 (hand-out via record, complete via command) for a human consumer
- respects ADR-0005 (notify only after fsync), ADR-0001/0002 (single writer, one
  `applyToState`), invariant 6 (assignment written into the event)
- the task's form reference is defined by ADR-0014; the tasklist surface is ADR-0011
