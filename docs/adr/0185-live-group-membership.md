# ADR-0185: Live group membership

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Pat, Atlas maintainers

## Context and problem statement

ADR-0180 made a user's group ids a snapshot taken at login and carried in the
session, so `effectiveRole` (ADR-0071) can resolve a group grant from the request
principal alone — no store read on the access path, which is the invariant that keeps
the access check pure and cheap. The documented cost was latency of effect: adding a
user to a group, or removing them, did nothing until that user logged out and back
in. For an access-control feature that is a surprising, easily-missed gap — an admin
grants access and the person still cannot see the project, with no signal why.

We want a membership change to take effect **without a re-login**, while keeping the
access path free of a per-request store read and `effectiveRole` pure.

## Decision drivers

- **The access path stays pure and disk-free.** `principalFor` resolves a request
  from the in-memory session and token index only, never the user or group store
  (ADR-0044); `effectiveRole` reads only the principal. Neither may grow a store read.
- **Live effect for a logged-in user.** A membership change must apply on that user's
  very next request, on every device they are signed in on.
- **Reuse the mechanism already here.** Deleting or disabling a user already reaches
  into the in-memory sessions immediately (`destroyUser`); the session store is a
  mutex-guarded map touched from handler goroutines, built for exactly this.
- **Correctness under concurrency.** Updating a session's snapshot must not alias one
  session's slice into another's.

## Considered options

1. **Push the change into live sessions (chosen).** When a group's membership
   changes, update the `groupIDs` of every affected live session in place; a session
   that does not exist (a user not signed in) needs nothing — their next login
   snapshots the current membership.
2. **Resolve group ids per request.** Have `principalFor` (or the auth middleware)
   read the group store on every authenticated request and attach fresh ids. Always
   live, but it puts a store read on the access path — the very thing ADR-0180
   avoided — on every request, not just the rare membership change.
3. **Force a re-login.** On a membership change, drop the affected user's sessions
   (like `destroyUser`), so their next request re-authenticates and re-snapshots.
   Live, but it logs the user out of everything for a background admin action.

## Decision outcome

Chosen option: **1 — push the change into live sessions.** It is the only option that
makes membership live while leaving the access path exactly as ADR-0180 left it: no
store read per request, `effectiveRole` unchanged and still pure. It mirrors the
`destroyUser`-on-user-change mechanism already trusted for immediacy, and it is
cheapest — work happens on the rare membership mutation, not on every request.

The session store gains two operations, called from the group handlers after the
store mutation succeeds:

- **add / remove a user from a group** → `setUserGroupMembership(userID, groupID,
  member)` adds or drops that group id across all of the user's live sessions;
- **delete a group** → `dropGroupFromSessions(groupID)` drops that id from every live
  session, so the group's grants stop applying for everyone at once.

Each update rebuilds the affected session's id slice fresh, so no two sessions ever
share a backing array. A user with no live session is a no-op: their next login
snapshots the now-current membership, exactly as before.

Scope: this makes **group membership** live. A user's **roles** remain a login-time
snapshot (a role change still takes effect on next login, or immediately via the
existing `destroyUser` on delete/disable) — roles are not part of this change, and
folding them in is a separate decision.

### Consequences

- **Positive:** An admin adding or removing a group member sees it take effect on the
  member's next request, on every device — no re-login, no confusing lag. The access
  path is untouched: still no store read, `effectiveRole` still pure and unit-tested
  as before.
- **Negative / trade-offs accepted:** The group handlers now also touch the session
  store, a second structure beyond their own store. Sessions remain in-memory, so a
  server restart still re-snapshots from the store on the next login (unchanged). The
  push is best-effort in the sense that it converges the sessions that exist; there is
  no cross-process fan-out, because sessions do not cross processes (ADR-0044).
- **Follow-ups / risks to watch:** making role changes live by the same mechanism; a
  future durable/shared session store would need the same push to stay live.

## Pros and cons of the options

### Option 1 — push into live sessions
- Good: access path unchanged (no per-request store read); `effectiveRole` stays
  pure; reuses the session-mutation mechanism `destroyUser` established; work is on
  the rare mutation, not every request.
- Bad: the group handlers gain a dependency on the session store; only converges
  in-process sessions (which is all there are).

### Option 2 — resolve per request
- Good: always live by construction; nothing to push.
- Bad: a group-store read on every authenticated request — reintroduces exactly the
  access-path cost ADR-0180 removed; scales with request rate, not change rate.

### Option 3 — force re-login
- Good: trivially correct; reuses `destroyUser` verbatim.
- Bad: a background admin action logs the user out of every device; hostile UX for
  what should be an invisible grant change.

## Links

- relates to ADR-0180 (groups as members — the snapshot-at-login this makes live),
  ADR-0071 (sharing scopes / `effectiveRole`), ADR-0044 (auth, sessions, `destroyUser`)
