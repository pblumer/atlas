# ADR-0180: Groups as scope members

- **Status:** Proposed
- **Date:** 2026-08-24
- **Deciders:** Atlas maintainers

## Context and problem statement

Sharing scopes (ADR-0071) let a project owner grant a role — viewer or editor —
to individual users. A scope member is a `principalRef{ type, id }`, and the
`type` field was reserved from the start for a value beyond `"user"`: **groups**,
named as a follow-up in both ADR-0071 and ADR-0044.

Individual grants do not scale to a team. Onboarding a new colleague means an
owner re-sharing every relevant project with them one by one; off-boarding means
hunting those grants down. A **group** — a named set of users, shared with once —
is the standard answer, and the member model already has the shape for it.

The question: **how do we add a group as a first-class principal that a scope can
grant a role to, without pulling group lookups onto the hot path of the access
check or reversing the "pure `effectiveRole`" design that every handler leans
on?**

## Decision drivers

- **Keep `effectiveRole` pure.** The access rule (`scopes.go`) is a pure function
  of a project and a `*Principal` — no store reads, no run-loop access, unit-
  testable in isolation, callable from any handler goroutine. Resolving "is this
  user in that group?" must not break that: it cannot load the group store inside
  `effectiveRole`.
- **Reuse the identity model and its patterns.** A group is operator/config data,
  exactly like a user (ADR-0044): a durable sidecar store, off the six engine
  invariants. Its management is administration, so it is admin-gated like the user
  list; the roster a non-admin owner needs for a picker comes from the principals
  directory (ADR-0073), not the management API.
- **Least surprise on the reference model.** A group member is just a
  `principalRef{ type: "group", id }` in the same `members` list — no parallel
  structure, so private/shared, revoke, and the 404-hides-existence behavior all
  keep working unchanged.
- **Group-ready hooks already exist.** `principalRef.type`, the directory's
  `type` field, and the member list were all shaped in ADR-0071/0073 to admit
  groups with no migration; this ADR spends those hooks.

## Considered options

For **how membership is resolved in the access check**:

1. **Snapshot the user's group ids into the session at login**, into
   `Principal.Groups`, exactly as roles are snapshotted (ADR-0044). `effectiveRole`
   then matches a `type:"group"` member ref against `pr.Groups` — a pure slice
   check, no store access.
2. **Resolve live inside `effectiveRole`** by loading the group store on each
   check.
3. **Denormalize onto the user record** — store each user's group ids on their
   `User` and read them from the already-loaded principal's user record.

For **the group entity**: a sidecar store mirroring users (chosen without a real
alternative — it is how every other config entity is stored, ADR-0019/0044).

## Decision outcome

Chosen: **a group is a sidecar entity, and a user's group ids are snapshotted into
the session at login (option 1).**

- **`Group` model** (`api/groupstore.go`), a sidecar store like the user store
  (ADR-0044): a stable opaque `ID` (`grp_…`), a `Name`, and `Members []string` —
  the ids of the users in it. Managing groups (create, rename, delete, add/remove
  a user) is **admin-gated**, the same boundary as managing users.
- **`Principal.Groups []string`** carries the ids of the groups the signed-in
  user belongs to, computed once **at login** by scanning the group store and
  snapshotted into the session — the same discipline, and the same acceptable
  tradeoff, as the role snapshot: a group membership change takes effect on the
  user's **next login**. `effectiveRole` gains one branch: a shared project's
  `type:"group"` member ref grants its role when `pr` is in that group
  (`pr.Groups` contains the ref id). The function stays pure.
- **Sharing accepts a group.** The member-add endpoint takes an optional member
  `type` (`"user"` default, or `"group"`); it validates the target exists in the
  matching store and records `principalRef{ type, id }`. Revoke, visibility, and
  role changes are unchanged — a group member is just another entry.
- **The principals directory** (ADR-0073, `/api/v1/principals`) lists groups
  alongside users as `{ type:"group", id, name }`, so a non-admin owner can pick a
  group to share with without the admin-only group-management API.
- **Console management UI**: a Groups card beside the Users card — create, rename,
  delete a group and add/remove its users — and the share dialog's picker offers
  groups as well as people.

When two grants apply to one user (a direct grant and a group grant, or two
groups), the **highest role wins** — the natural, least-surprising resolution, and
what a rank-max over the matching refs gives for free.

### Consequences

- **Positive:** an owner shares a project with a team once; the member model,
  revoke path, and existence-hiding all work unchanged; `effectiveRole` stays pure
  and off the run loop; groups reuse the proven user-sidecar and login-snapshot
  patterns and touch none of the six invariants; the group-ready hooks from
  ADR-0071/0073 are spent exactly as intended.
- **Negative / trade-offs accepted:** a group membership change is visible to
  access checks only on the affected user's **next login** (identical to the role
  snapshot; a forced re-login applies it immediately). Groups are **flat** — no
  nested groups — for now. A group grant is coarser than per-user grants, which is
  the point but also means an owner must trust the group's roster, which an admin
  controls.
- **Follow-ups / risks to watch:** immediate propagation (re-resolve groups on
  each request, or invalidate sessions on group change) if next-login latency ever
  bites; nested groups; and, once assignment-by-id lands (ADR-0073 follow-up),
  letting a task be assigned to a group.

## Pros and cons of the options

### Membership resolution

**Option 1 — snapshot at login (chosen).** Good: keeps `effectiveRole` pure and
off the run loop; mirrors the role snapshot exactly, so the discipline and its
one caveat are already understood. Bad: a membership change waits for the next
login.

**Option 2 — resolve live in `effectiveRole`.** Good: always current. Bad: makes
the access rule impure — a store read on every check, from handler goroutines, off
the single-writer loop — reversing the design every handler and the unit tests
depend on.

**Option 3 — denormalize onto the user record.** Good: available wherever the
principal's user record is. Bad: two places to keep in sync (the group's member
list and each user's group list); a group rename/delete must fan out to every
member's record; the principal doesn't currently carry the full user record
anyway, so it would still need a snapshot.

## Links

- builds on [ADR-0071](0071-sharing-scopes.md) (scope members are
  `principalRef{type,id}`; `type:"group"` reserved here)
- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md) (the
  user sidecar, the admin boundary, and the login role-snapshot this mirrors)
- extends [ADR-0073](0073-principals-directory.md) (the directory that now lists
  groups for pickers)
