# ADR-0281: A role change takes effect on the next request, not the next login

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

A login session snapshots the account's roles and group ids so a request can be
authorized from the session alone, without a store read on the access path
([ADR-0044](0044-user-management-and-authentication-boundary.md)).

[ADR-0185](0185-live-group-membership.md) then made group ids **live**: adding or removing an
account from a group pushes into every session that account holds, so membership
takes effect on the next request. Roles were left as they were — a login-time
snapshot, refreshed only by logging out and back in.

That asymmetry is the defect. Sessions last twelve hours by default, so demoting
an administrator answered `200`, showed a narrowed account in the console, and
left the old rights in force for the rest of the day. An external review
reproduced it: after a successful `PATCH` reducing an account to `user`, that
account's existing session still reached `GET /api/v1/users` and got `200`.

Roles decide strictly more than group membership does. They cannot be the slower
of the two to take effect.

## Decision drivers

- A revocation an operator can see must be a revocation the server enforces.
- The access path stays snapshot-based: no store read per request.
- Removing rights and granting them travel one path, so the two cannot drift.
- Disabling an account is a different act from narrowing one and already has its
  own answer.

## Considered options

1. Push the new roles into the account's live sessions.
2. Destroy the account's sessions on any role change, forcing a re-login.
3. Read roles from the user store on every request instead of snapshotting.

## Decision outcome

Chosen option: **option 1**. `sessionStore.setUserRoles` replaces the role
snapshot in every live session of a user, and `handlePatchUser` calls it beside
the OAuth-grant rewrite it already performed. This is the mechanism ADR-0185
built for groups, applied to the other half of the snapshot.

A demotion narrows the session; it does not end it. Dropping the session would be
a heavier answer than the question asks — the account is still the same person,
still signed in, now with fewer rights — and it is what `destroyUser` already
does when the account itself is disabled or deleted. A grant travels the same
path, so promotion is live too and the two directions cannot fall out of step.

The slice is copied on the way in, so a caller that keeps mutating what it passed
cannot rewrite a live session's rights and no two sessions share a backing array.
An account with no live session is a no-op: its next login snapshots the current
roles.

Option 3 was rejected because it puts a store read on every authorized request to
solve a problem that a push solves at the moment of change. Option 2 was rejected
as disproportionate, and because a forced logout is indistinguishable to the user
from a bug.

### Consequences

- **Positive:** an administrative role change is enforced from the account's next
  request. What the console shows and what the server does now agree.
- **Positive:** roles and groups are maintained the same way, so the next field
  added to the snapshot has an obvious precedent to follow.
- **Negative / trade-offs accepted:** a request already in flight when the PATCH
  lands keeps the authorization decision it started with. Narrowing that further
  would mean re-checking mid-request, which the snapshot design exists to avoid.
- **Negative:** API tokens are deliberately *not* changed here. A token can carry
  its own standing rights, and revoking those is a different act on a different
  object. The console should show that difference rather than imply a role change
  reaches them.
- **Follow-ups / risks to watch:** a single written revocation model covering
  local login, OIDC, groups, API tokens and OAuth grants is still missing; this
  record closes the session half of it. `O-14` (session administration — listing
  and ending live sessions) remains open and would give an operator the direct
  control this makes unnecessary for the common case.

## Pros and cons of the options

### Option 1 — push roles into live sessions
- Good: immediate, symmetric with groups, no per-request cost.
- Bad: an in-flight request keeps its decision.

### Option 2 — destroy sessions on a role change
- Good: no stale snapshot can survive at all.
- Bad: logs a person out for an edit that was meant to adjust them, and reads as
  a fault rather than a policy.

### Option 3 — read roles per request
- Good: no snapshot to keep current.
- Bad: a store read on every authorized request, undoing what the snapshot is for.

## Links

- extends [ADR-0185](0185-live-group-membership.md) from group ids to roles
- amends the session model in [ADR-0044](0044-user-management-and-authentication-boundary.md)
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) (what a role decides)
- reported as F10 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
