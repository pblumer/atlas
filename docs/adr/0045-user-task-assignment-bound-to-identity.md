# ADR-0045: Binding user-task assignment to real identities

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0042 made a user task's runtime assignee a field on its job and gave the
Tasks app claim/unclaim. It deliberately left one thing open: **authorization.**
With no identity in the server, "me" was a display-only string the Tasks app
carried, and claim set the assignee to *whatever the caller passed* — you could
claim as anyone, or assign to a name that belonged to no one.

ADR-0044 then introduced real identity: accounts, login, and an authenticated
`Principal` on every request (when enforcement is on). That closes the gap
ADR-0042 flagged. The question this ADR answers: **now that the server knows who
is calling, how does claim/assign use that identity — without changing the engine
or the job's assignee model (ADR-0042), and without breaking the open,
single-user build?**

## Decision drivers

- **Realize the ADR-0042 follow-up.** "Authorization once the server has identity"
  is exactly what we can now do; a claim should mean *this authenticated user*.
- **Don't touch the engine.** The assignee is a string on the job (ADR-0042). This
  is a server-surface decision only — no new value type, no `applyToState` change,
  no invariant risk.
- **Don't break the open build.** With auth off there is no session identity, so
  the old free-text behavior must remain.
- **Assign to real people.** A picker needs a list of assignable users that a
  regular task-worker (not an admin) can read.

## Considered options

1. **Server-authoritative claim, validated assign.** With auth on, an empty claim
   body claims for the signed-in user; a named assignee must resolve to a real,
   enabled account. Add a non-admin `GET /api/v1/users/assignable` for the picker.
   The stored assignee stays the username string.
2. **Keep claim as free-text, only add a UI picker.** The picker suggests real
   usernames but the server still trusts any string.
3. **Store the user id (not username) as the assignee.** Make the assignee a
   stable id reference rather than a name.

## Decision outcome

Chosen: **Option 1.**

- **Claim is authoritative under auth.** `POST /tasks/{key}/claim` with an empty
  body claims the task for the signed-in `Principal`. A named
  `{"assignee":"…"}` must resolve to a real, **enabled** user (400 otherwise) and
  is normalized to that account's stored username. With auth **off**, the caller
  must still name the assignee, unvalidated — unchanged from ADR-0042.
- **The stored assignee stays the username string** on the job. Usernames are
  immutable in the account model (ADR-0044: create-only), so a username is a
  stable handle; keeping the string means **zero engine/storage change** and full
  continuity with ADR-0042's design and its recovery guarantees.
- **A dedicated `GET /api/v1/users/assignable`** returns a minimal projection
  (username + display name) of enabled users. Unlike the admin-gated management
  list, it is available to any authenticated caller (and open when auth is off),
  because assigning work is an everyday Tasks action, not user administration.
- **Tasks UI:** with auth on, identity is the signed-in user (no free-text box)
  and Claim self-claims; an "Assign to…" picker sourced from `/users/assignable`
  assigns to a chosen user. With auth off, the typed-identity field remains.

Option 2 is rejected: a picker over an untrusted string still lets a typo or a
crafted request assign work to a non-existent user — the validation is the point.
Option 3 is rejected for now: it would change the on-disk job layout (an engine
concern, ADR-0042) for a stability the immutable username already provides;
migrating the assignee to an id reference is a later step if usernames ever become
mutable.

### Consequences

- **Positive:** Claim now means a real, authenticated identity; assignments are
  guaranteed to point at accounts that exist and are active; the picker works for
  non-admins; the engine, the job's assignee field, and their recovery behavior
  are untouched; the open single-user build is unchanged.
- **Negative / trade-offs accepted:** *Any* authenticated user may assign a task
  to *any* enabled user (assignment is collaboration, not a privileged action) —
  consistent with ADR-0042's non-restrictive stance; a role-scoped "who may
  assign to whom" is deferred. The assignee remains a username string, so making
  usernames mutable later would require migrating stored assignees.
- **Follow-ups / risks to watch:** candidate-group-based authorization (who may
  claim from a group); claim-conflict semantics (still last-writer-wins from
  ADR-0042); a "claim only if unassigned" option; and, if usernames ever become
  mutable, moving the assignee to a stable id reference.

## Links

- refines the authorization follow-up left open by
  [ADR-0042](0042-user-task-assignment-and-claim.md); builds on the identity
  introduced in [ADR-0044](0044-user-management-and-authentication-boundary.md)
- assignee-on-the-job model and its invariants are unchanged (ADR-0042, I1/I4)
