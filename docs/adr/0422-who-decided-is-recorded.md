# ADR-0422: Who decided is recorded, on the task, the order and the right

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-28
- **Deciders:** maintainers

## Context and problem statement

[ADR-0421](0421-a-task-is-listed-to-whoever-it-was-addressed-to.md) made a task offered
to a group visible to that group and nobody else. Once approvals go to a group rather
than a person, three questions an audit asks no longer have an answer: who decided a
given approval, who approved a right that is still held after its order has been
deleted, and who recertifies rights granted by such a group.

## Decision drivers

- The decider is the server's to name, never the client's: a form that could write it
  would let anybody decide in somebody else's name.
- The approver has to outlive the order, which retention deletes long before the right
  it granted ends.
- The entitlement families are append-only; any new field must be readable beside every
  record written before it, with no migration (invariants I4/I6).
- A standing function — the integration managers — should be able to recertify without
  a list of people somebody has to keep current.

## Decision outcome

## Who decided

A task offered to a group raises a second question the moment it is completed: who
decided. The model knows only the group, so an approval process could only record
the group as the decider of a refusal, and an audit asks for a person. Completing a
user task, through `POST /api/v1/tasks/{key}/complete` or the collective decision of
ADR-0362, now writes `completedBy` into the instance: the signed-in caller's principal
id, the identifier an order already records for `decidedBy`. A value the body sends
under that name is dropped first, because who decided is the server's to say. With
authentication off there is nobody to name and nothing is written.

An order kept the author of a refusal (`decidedBy`) and nothing about an approval,
because an approved line simply went on to be provisioned. For a group approval that
leaves the record unable to say who let a right through. A line now also carries
`approvedBy` and `approvedAt`, written by the same decision route with
`"approved": true`. An approval settles nothing, so the line's status is unchanged and
nothing is woken; it is recorded once, because a second approval would overwrite the
author, and never without one. Calling it is optional, so an approval process that
does not leaves the line as every line was before.

The order is deleted by retention long before the right it granted ends, and the
approver with it. So the approver also travels with the grant: the entitlement
(`model.EntitlementValue`) and the history row a hold becomes when it ends
(`model.EntitlementHistoryValue`) both carry `ApprovedBy`, and `GET /api/v1/inventory`
and `GET /api/v1/entitlements/history` return it as `approvedBy`. Both families are
append-only, and the field is appended after the last existing one, which is the
extension the entitlement record was written to allow: a record from before it ends
early and decodes with no approver, which is what it had. The history row copies the
field from the hold inside `EndEntitlement`, reading state the same pipeline built, so
the fold stays deterministic (I4/I6).

## Recertification by a group

The same function that approves a right is the natural one to recertify it, and a
recertification campaign could only address rows to named people or leave them with
the campaign's owner. `POST /api/v1/recertification` now takes `reviewerGroup`, a
group by name or id: every row whose holder `reviewers` does not name is offered to
that group (`reviewerGroup` on the row, as the group's id), and any member may keep or
revoke it — the rule a candidate group already means for a task. A named reviewer
still wins, operators and administrators still may answer any row, and a group Atlas
does not know is refused at opening, because rows offered to nobody would wait on a
name that reaches no inbox. Such a row is not counted as unassigned, and the pending
work of a member lists it.

### Consequences

- Good, because a refusal and an approval both name a person, and the approval survives
  the order in the right and in its history.
- Good, because a recertification campaign can be handed to the group that approves.
- Neutral: a line or right from before this change has no approver on record, which is
  what it had; nothing is back-filled into an append-only family.
- Bad, because a server that writes `approvedBy` into the entitlement families cannot be
  rolled back to a version that predates it without that field being ignored — older
  decoders stop reading at the fields they know.
