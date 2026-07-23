# ADR-0038: Boundary events — timer and message, interrupting and non-interrupting

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas engine team

> **Implementation status.** Timer and message boundary events are implemented,
> both interrupting (`cancelActivity="true"`, the BPMN default) and
> non-interrupting (`"false"`), attached to any waiting activity (service, user,
> business-rule, connector task — and, mechanically, any node that stays
> `Activated`). Error/signal boundary events, cycle timers, and boundary events on
> embedded subprocesses are future work (subprocesses do not exist yet).

## Context and problem statement

A boundary event is attached to an activity and arms while that activity runs. If
it fires before the activity finishes, it either **interrupts** the activity —
cancels it and routes the token out the boundary's own outgoing flow — or runs
**non-interrupting**, spawning a parallel token while the activity keeps going. It
is how a model expresses a timeout ("escalate if the tweet isn't reviewed in 30
minutes"), a cancellation message, or a deadline reminder.

This is the last structural gap for human-in-the-loop approval processes: with
user tasks (ADR-0028) a token can park on a person, but nothing could time it out
or cancel it from outside. The lifecycle already anticipated this — the
`ACTIVATING → … → TERMINATING → TERMINATED` path in `docs/ARCHITECTURE.md` is
described as "interrupted by a boundary event."

## Decision

**A boundary event is armed as its own waiting element instance**, a sibling of
the host in the same flow scope, reusing the existing timer-catch and
message-catch machinery rather than inventing a new wait mechanism.

- **Arming.** When a host activity activates, `handleElementActivating` arms each
  attached boundary event (compiled as a `BoundaryEvents` span on the host node)
  by activating a boundary element instance. That instance's `OnActivated` creates
  a timer keyed to itself, or opens a message subscription — exactly like an
  intermediate catch event. The boundary instance carries a new
  `AttachedToKey` field pointing at the host instance.
- **Firing.** The existing timer/message paths drive the boundary instance to
  `Completing`. Its behavior then, if interrupting, terminates the host, and
  always takes the boundary's outgoing flow.
- **Interrupting.** `interruptHost` cancels the host's job (see below), emits a
  `Terminated` event for the host, and terminates the host's *other* boundary
  siblings (their timers/subscriptions self-retire — the same "fires later, finds
  no element, does nothing" pattern instance cancellation already relies on).
- **Normal completion.** When a host completes normally, `handleElementCompleting`
  disarms any still-armed boundary siblings the same way.
- **Idempotency.** A boundary whose host a sibling already interrupted is skipped
  by a liveness guard, so two boundaries firing in one batch route the token out
  exactly once.

Everything is expressed with existing event types (`Activated`, `Terminated`,
`TimerCreated`, `SubscriptionCreated`, `JobCanceled`), so `applyToState` stays
pure and recovery replays a parked boundary and its interrupt identically
(invariants I4/I6).

### Two supporting changes

- **`AttachedToKey` on `ElementInstanceValue`** (0 for every non-boundary element)
  links a boundary token to its host unambiguously — robust even if future
  loops/multi-instance put two tokens on one host node, which a topology-only link
  could not distinguish.
- **An element→job reverse index** (`cfJobByElement`, with `Tx.JobOfElement`) lets
  an interrupting boundary find and cancel the host's job via a new
  `IntentJobCanceled`. Without it an interrupted user task would linger in the
  Tasks inbox as a phantom job (the job self-retires but stays activatable). One
  activity holds at most one job, so the element key alone identifies it.

## Alternatives considered

- **Topology-only host link (no `AttachedToKey`).** Find the host by scanning the
  attached node's live instances. Rejected: ambiguous the moment two tokens sit on
  one host node, and it needs a scan where a stored key is O(1).
- **Leave the host's job to self-retire (no reverse index).** Matches how whole-
  instance cancellation behaves today, but leaves a phantom task in the inbox after
  a timeout — the wrong experience for the motivating approval use case.

## Consequences

- Approval processes with timeouts/cancellation are now expressible and durable.
- A boundary event shows as a live token on its node in the Operations overlay
  while armed — an accurate depiction of a pending trigger.
- The reverse index adds one more key per job write; job writes are already off
  the hot path relative to token movement.
- Authoring boundary events in the bpmn-js editor's properties panel is a
  follow-up; the engine and compiler accept them today.
