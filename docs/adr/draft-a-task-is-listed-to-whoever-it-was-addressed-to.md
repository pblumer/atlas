# ADR-DRAFT: A task is listed to whoever it was addressed to

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-28
- **Deciders:** maintainers

## Context and problem statement

[ADR-0317](0317-task-commands-are-an-object-question.md) closed the write half of the
task axis: a task the model addressed, to an assignee or to candidate groups, may be
completed, claimed or released only by whoever it was addressed to, by an operator or
by an administrator. It left the read half open on purpose. Its argument was that the
task list "already tells any signed-in caller that the task exists".

That argument holds as long as the list is harmless, and a catalogue approval makes it
harmful. An approval offered to a group of integration managers carries the order, the
product and the recipient in its row. Every account that can open the Tasks app has the
`user` role, so every one of them read every approval in the installation, including
the ones for other people's orders. An installation asked for the obvious thing: the
integration managers' approvals are shown to the integration managers and to the
administrators, and to nobody else.

## Decision drivers

- The read rule and the write rule must be one rule. Two rules drift, and a list that
  shows a task its reader may not act on invites exactly the 403 ADR-0317 exists for.
- Unaddressed work stays shared. BPMN's convention, and the shared inbox of ADR-0042,
  is that a task naming nobody is anybody's to pick up.
- Operators and administrators keep everything, as they keep every instance.
- No new cost on the run loop.

## Considered options

1. Leave the list open and rely on the write gate.
2. Filter every task view by the rule ADR-0317 already applies to commands.
3. A per-process or per-group visibility setting.

## Decision outcome

Chosen option: **2**, because it adds no concept. `taskVisibleTo` applies
`holdsTask` to the viewer. A task is shown when the viewer is an operator or an
administrator, when the task names nobody, when it is assigned to the viewer, or when
it is unclaimed and offered to a group the viewer belongs to. A claimed task belongs
to its assignee, as it does for the commands.

It applies to `GET /api/v1/tasks` (unfiltered, per folder and per instance), to
`GET /api/v1/tasks/{key}`, which answers 404 for a task the viewer may not see, and
to the folder counts. The unfiltered list for a viewer who does not see everything
runs off the loop through the same walk a folder uses, because skipping tasks makes it
grow with the inbox. The operator and administrator page is unchanged.

Option 3 was rejected because it would be a second authorization model beside the
assignment the model already declares.

### Consequences

- Good, because an approval is seen by the people who can decide it and nobody else.
- Good, because the Tasks app's list and its commands now agree.
- Neutral: the shop's own task view (ADR-0416) is unchanged. An orderer still sees
  that the approval for their own order waits on a group, which is their business.
- Bad, because a group member no longer sees a group task once a colleague has
  claimed it. That is what the commands already said: a claimed task is its
  assignee's.
