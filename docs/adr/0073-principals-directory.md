# ADR-0073: A principals directory for member and assignee pickers

- **Status:** Accepted
- **Date:** 2026-07-28
- **Deciders:** Atlas maintainers

## Context and problem statement

Sharing scopes (ADR-0071) let a project owner grant other users a role on a
project. A grant references the **stable, opaque `User.ID`** (ADR-0044), never a
username — a username can change or be reassigned by an external IdP, so binding
a share to it would silently break. The Modeler's share dialog therefore needs a
way to turn ids into names (to render the member list) and to let the owner pick
a person to add.

The only endpoint that lists users by id is `GET /api/v1/users`, and it is
**admin-only** (ADR-0044): listing accounts is user administration, and the
management projection carries roles, email, disabled state, and source. So a
**non-admin project owner cannot resolve member names or pick people to share
with** — the Phase-1 share dialog had to degrade to typing raw user ids, which
is the gap this ADR closes.

There is already a precedent for a non-admin identity list: `GET
/api/v1/users/assignable` (ADR-0045) returns `{username, displayName}` for
enabled users so the Tasks app can assign work. But it is keyed on **username**
(task assignment is username-bound in ADR-0045), not the id a scope grant needs,
and it carries no type tag for the groups ADR-0071 anticipates.

The question: **how does a non-admin owner get the minimal identity directory —
ids and names — that a scope-member (and, later, assignee) picker needs, without
exposing the admin-only management surface?**

## Decision drivers

- **Reference the stable id, never the username.** A scope member is
  `principalRef{ type, id }` (ADR-0071); the directory must expose that id so the
  picker writes the same reference the store holds.
- **Least privilege / no over-exposure.** Any authenticated user may need to pick
  a colleague, but no non-admin should see roles, email, disabled state, source,
  or the password hash. The directory must be a *minimal* projection.
- **Group-ready.** ADR-0071 reserves `ref.type` for groups. The directory should
  carry the same `type` tag so groups slot in later with no shape change.
- **Reuse the established boundary.** The endpoint is authenticated-but-not-admin,
  exactly like `/users/assignable`; it must sit off the six invariants (it only
  reads the user sidecar) and behave open when auth is off, like every other
  read.

## Considered options

1. **Relax `GET /users`** to non-admins (perhaps trimming the projection when the
   caller isn't admin).
2. **Reuse `/users/assignable`** — add the `id` to its existing
   `{username, displayName}` shape and let the share dialog consume it.
3. **A dedicated `GET /api/v1/principals`** — a new minimal directory of
   `{ type, id, name }` for enabled users, readable by any authenticated caller.
4. **Client-side only** — keep typing raw ids in the dialog (the Phase-1
   fallback).

## Decision outcome

Chosen option: **a dedicated `GET /api/v1/principals` (option 3).**

It returns one entry per **enabled** user:

```
{ "type": "user", "id": "usr_…", "name": "<displayName or username>" }
```

- **`id`** is the opaque `User.ID` a scope grant (and a future assignee-by-id
  flow) references — the whole point.
- **`type`** is `"user"` today; groups will appear as `"group"` with no shape
  change, matching `principalRef` (ADR-0071).
- **`name`** is the display name, falling back to the username — enough to render
  a picker, and nothing more.
- Nothing sensitive is included: no email, roles, disabled flag, source, or hash.

Authorization mirrors `/users/assignable` (ADR-0045): the endpoint is **not**
admin-gated, so any authenticated caller may read it; under `--auth` the standard
middleware still requires a session (it is not on the pre-login exemption list),
and with auth off it is open like every other read. It only reads the user
sidecar on the run loop, so it touches none of the six invariants.

We keep it **separate from `/users/assignable`** rather than merging them: the two
have different reference contracts — assignment is username-bound (ADR-0045),
scope membership is id-bound (ADR-0071/0044) — and collapsing them would couple
two features that are free to diverge. They may converge later behind this
id-based directory once assignment-by-id is on the table; that is a follow-up,
not this ADR.

### Consequences

- **Positive:** a non-admin owner can resolve member names and pick people to
  share with, so the share dialog drops its raw-id fallback for the normal case;
  the projection leaks nothing an authenticated user shouldn't see; the
  `{type,id,name}` shape is group-ready and matches the scope member model; the
  endpoint reuses the proven authenticated-not-admin boundary and stays off the
  invariants.
- **Negative / trade-offs accepted:** any authenticated user can now enumerate the
  display names and ids of all enabled users — an accepted disclosure for a
  collaboration tool, and strictly less than `/users/assignable` already reveals
  (it exposes usernames too). Two similar non-admin identity lists now exist
  (`/principals` and `/users/assignable`) until a later convergence.
- **Follow-ups / risks to watch:** groups as `type:"group"` entries; folding
  assignment onto the id-based directory once assignment-by-id lands; optional
  filtering/pagination if instances ever hold enough users to matter.

## Pros and cons of the options

### Option 1 — relax `/users`
- Good: no new endpoint.
- Bad: the management list is deliberately admin-only and carries roles, email,
  and status; conditionally trimming its projection by caller role invites an
  accidental leak, and overloads one endpoint with two authorization postures.

### Option 2 — extend `/users/assignable`
- Good: reuses an existing non-admin list.
- Bad: it is username-keyed for a different feature (ADR-0045); adding an id
  conflates task assignment with scope membership and ties their shapes together.

### Option 3 — dedicated `/principals` (chosen)
- Good: id-referenced and type-tagged exactly like `principalRef`; minimal, safe
  projection; clean, single-purpose authorization.
- Bad: a second non-admin identity list until a later convergence.

### Option 4 — client-side raw ids
- Good: no backend change.
- Bad: unusable for real people — nobody knows a colleague's `usr_…` id; leaves
  the Phase-1 gap open.

## Links

- builds on [ADR-0071](0071-sharing-scopes.md) (scope members are `principalRef`
  by id — what this directory feeds)
- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md)
  (opaque `User.ID`, the admin-only management list, the auth boundary)
- mirrors [ADR-0045](0045-user-task-assignment-bound-to-identity.md) /
  `/users/assignable` (the non-admin identity-list precedent)
