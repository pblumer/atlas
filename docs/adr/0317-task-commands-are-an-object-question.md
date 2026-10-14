# ADR-0317: Acting on a user task is an object question

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether "addressed" should also cover a task a *lane* names, once
  lanes carry identity. A lane is metadata today (ADR-0121) and matching on it would be
  a second, weaker reading of "who is this for"; the argument for it is that a modeller
  who drew a lane believes they said something about authority.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0275](0275-instance-visibility.md) closed the read half of the object axis on a
running instance: an external review had signed in as an unrelated account and read the
variables of somebody else's instance, and the answer was that being signed in is not a
relationship to a thing.

The finding was about reading. Reading is what it closed.

The three commands that *act* on a user task — complete, claim, release — kept the gate
they had always had, which is the role gate, and the role they require is `user`: the
role every signed-in account holds. Any account could therefore complete any open user
task by key. A task key is a job key and the task list hands them out to anybody.

That was survivable while every account belonged to the organisation running the
server. The portal ends that. A customer ordering from a catalogue has an account with
`user`, and an approval is a user task assigned to a line manager or offered to a named
group. Without an object question, the person whose order is waiting can complete the
task that decides it.

Approving one's own order is not a subtle weakness in an authorization model. It is the
absence of one, in the one place the whole approval feature exists to provide.

It was reproduced before it was fixed: a second account with nothing but `user`
completed a task assigned to another user and received `200`.

## Decision drivers

- The read half is already decided. The write half should not invent a second rule.
- A fix that changes what existing installations do needs a line somebody can defend,
  not the strictest rule available.
- Releasing is not a lesser act than completing. A caller who cannot decide a task but
  can take it away from the person who can leaves an approval nobody holds.
- Operators and administrators must not be narrowed. They can cancel the instance the
  task sits on.

## Considered options

1. **Only the assignee, always.** Simplest to state.
2. **The task the model addressed belongs to who it addressed; a task it did not
   address is open work.**
3. **A new, separate endpoint for portal approvals, leaving the task commands as they
   are.**

## Decision outcome

Chosen option: **"the task the model addressed"** — `api/taskauthority.go`.

`mayWorkTask` answers one question for all three commands: authentication off is a
single user and passes; an operator or administrator passes; otherwise the task is read
and, **if the model addressed it** — it names an assignee, or candidate groups — the
caller must hold it under [ADR-0042](0042-user-task-assignment-and-claim.md)'s rule, which
`holdsTask` already implements for the read side. A refusal is `403` and not `404`: the
task list already tells any signed-in caller that the task exists, so hiding it here
would withhold nothing and explain nothing, where "this one is not yours" is something
a person can act on.

Option 1 was rejected on what it would break rather than on principle. A user task with
neither an assignee nor candidate groups is unassigned work — BPMN says nothing about
who it is for, the shared inbox has always let anybody pick it up, and narrowing it
closes nothing, because a task nobody was addressed about has no holder to impersonate.

Option 3 was rejected as a façade. A better door does not help while the old one is
open: a caller who wanted to approve their own order would post to
`/api/v1/tasks/{key}/complete` and never see the new endpoint.

### Consequences

- **Positive:** The write half of the axis now matches the read half, under one
  predicate rather than two readings of it. Every approval the portal creates is
  addressed — to a fixed person, to a group, or to the superior the directory
  resolved — so every one of them is covered.
- **Negative / trade-offs accepted:** An installation where an ordinary user completes
  tasks addressed to other people now needs an operator, an administrator, or the right
  candidate group. That is a behaviour change on an endpoint that has existed since
  ADR-0028, and it is deliberate. Claiming a task *on behalf of* somebody else likewise
  now requires holding it or being an operator — a supervisory act, gated as one.
- **Follow-ups / risks to watch:** The candidate-group rule matches by group name as
  well as id, which ADR-0042 chose and this record inherits rather than revisits. An
  installation wanting claimed-only is still the one line that record described.

## Implementation

`Server.mayWorkTask` and `Server.refuseTaskWork` in `api/taskauthority.go`, called by
`handleCompleteTask` and by `assignTask` — which claim and release share, so one call
covers both. `holdsTask` is reused unchanged from the read side: one predicate, so the
two halves cannot drift into disagreeing about who holds a task.

Three tests hold it, each on its own model rather than a shared one, because the cases
turn on exactly what the model addressed: a stranger is refused all three commands and
the assignee is unchanged afterwards, an unaddressed task still lets its taker complete
it, and an administrator reaches a task assigned to somebody else.

## Links

- extends [ADR-0275](0275-instance-visibility.md) — the same axis, the write half
- reuses [ADR-0042](0042-user-task-assignment-and-claim.md) — who holds a task
- required by [ADR-0312](0312-portal-catalogue-order-inventory.md) — an approval a customer can grant themselves is not an approval
