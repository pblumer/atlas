# ADR-0071: Sharing scopes — private and shared access boundaries for design-time work

- **Status:** Accepted
- **Date:** 2026-07-28
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas now has identity (ADR-0044) and a way to group design-time work into
named **projects** (ADR-0034). What it does **not** have is any notion of *who
a piece of work belongs to* or *who may see and change it*. Every project,
draft, DMN reference, and form is visible to, and editable by, everyone who can
reach the server. ADR-0044 turned the front door into a lock (opt-in `--auth`,
a `*Principal` per request) but behind that door the workspace is still a single
shared heap: authentication without authorization of design-time content.

The next step users are asking for is an **access boundary on a body of work**:
some work is **private** (only mine), and some I want to **share with, or work
on together with, specific other people**. This is exactly the axis ADR-0044
deliberately left open and listed as a follow-up ("groups", "per-endpoint RBAC
beyond admin", "multi-tenancy"). It is now the concrete next slice.

The question: **how do we introduce a private-vs-shared access boundary over
the existing design-time artifacts, reusing the identity model (ADR-0044) and
the sidecar/grouping model (ADR-0034), without touching the engine invariants
and without breaking today's open single-user deployments?**

### A naming note

The word *scope* is already used inside the engine for **FEEL / activity-local
variable scopes** (ADR-0068). To avoid collision, this ADR calls the new concept
a **sharing scope** (or "access scope") throughout, and the runtime concept
stays "variable scope". If the shorthand "scope" proves confusing in code and
UI, "workspace" is the fallback name — but the user-facing concept the request
names is *scopes*, so we keep it, disambiguated.

## Decision drivers

- **Reuse identity, don't reinvent it.** Ownership and membership must reference
  the opaque, stable `User.ID` from ADR-0044 — never a username or email, which
  can change. Sharing is meaningless without identity, so its enforcement lights
  up only under `--auth`; with auth off, the fields exist but everything stays
  open (backward compatibility, exactly as ADR-0044 gated enforcement).
- **Design-time only; do not touch the invariants.** Like projects (ADR-0034)
  and users (ADR-0044), a sharing scope is operator/config data. It must live in
  a durable **sidecar store below the HTTP API** and never reach the WAL, the
  processor, or `applyToState`. The six invariants (`docs/architecture/invariants.md`)
  stay untouched — no hot path, no event, no recovery impact.
- **Backward compatibility with no migration.** Pre-existing projects and
  artifacts predate ownership. They must keep working: unowned/legacy content
  degrades to an implicit bucket (open when auth is off, admin-only when on),
  the same "no migration, degrade gracefully" stance as ADR-0034's *Ungrouped*.
- **Smallest honest slice, but don't box in the trajectory.** Ship one coherent
  vertical (private vs shared, with a member list and viewer/editor roles), while
  leaving room for the expensive-to-change shapes: groups as members, more roles,
  org/tenant-level visibility, and eventually runtime isolation.
- **One unit of sharing, not N.** Sharing "these related diagrams" should be one
  action, not a grant per file. The grouping concept that already means "a body
  of work" is the **project** (ADR-0034); it is the natural unit to share.
- **Clear boundary of what sharing governs.** This ADR governs **design-time
  content** (projects and the artifacts tagged into them). Runtime visibility
  (Operations, running instances, per-scope data isolation in the engine) is a
  much larger, invariant-adjacent question and is explicitly **out of scope** —
  see follow-ups.

## Considered options

For **what a sharing scope attaches to**:

1. **Scope = an access facet on the existing Project (ADR-0034).** A project
   gains an owner, a visibility, and a member list. The project stays the unit of
   grouping *and* becomes the unit of sharing.
2. **Scope = a new first-class container above projects** (a "workspace"/tenant
   that owns projects and artifacts). Two nested grouping concepts.
3. **Per-artifact ACLs.** Every draft/deployment/DMN reference/form carries its
   own owner and share list; no container-level sharing.
4. **No explicit sharing — rely on ADR-0044 roles only.** `admin` sees all,
   others see their own; visibility is global, not per-body-of-work.

For **the sharing model on the chosen unit**:

- **a.** A `visibility` enum (`private` | `shared`) plus a `members` list of
  `{ principalRef, role }`, `role ∈ {viewer, editor}`, owner implicit.
- **b.** A bare boolean `shared` with no per-member roles (all-or-nothing, every
  collaborator is an editor).

For **enforcement point**:

- **i.** In the HTTP API handlers, off the resolved `*Principal` (ADR-0044) —
  filter list endpoints, guard mutations by membership+role.
- **ii.** A new middleware/policy layer separate from the handlers.

## Decision outcome

Chosen option: **the project is the unit of sharing (option 1), with a
`visibility` enum + role-bearing member list (a), enforced in the HTTP API off
the `*Principal` (i).**

A **sharing scope** is not a new storage entity. It is the existing **project**
(ADR-0034) extended with three access fields:

```
project {
  id, name, createdAt, updatedAt,          // unchanged (ADR-0034)
  ownerId,                                  // User.ID (ADR-0044); required for a scoped project
  visibility,                               // "private" | "shared"
  members [ { ref, role } ]                 // ref = { type: "user", id }; role = "viewer" | "editor"
}
```

- **`ownerId`** references the opaque `User.ID`. The owner can read, write,
  share (edit membership), transfer ownership, and delete. Ownership is the one
  role that is implicit rather than listed.
- **`visibility`**: `private` (owner only) or `shared` (owner + everyone in
  `members`). A later `org`/`public` value can be added without reshaping the
  field — the enum is open, mirroring how ADR-0044 kept `Roles`/`Source` open.
- **`members`**: a list of `{ ref, role }`. `ref` is a **principal reference**
  carrying a `type` (today only `"user"`), deliberately shaped so **groups**
  (ADR-0044's named follow-up) slot in later as `type: "group"` with no
  migration. `role` starts as `viewer` (read) or `editor` (read/write); owner is
  the implicit third, highest role. This is the same "role list, only some
  values enforced now" discipline as ADR-0044 — richer roles cost no reshaping.

**Membership is inherited by the project's artifacts.** A draft, DMN reference,
or form tagged into a project (ADR-0034's optional `projectId`) is governed by
that project's scope. There is **no per-artifact ACL** — the project is the one
place you manage access. An artifact with no `projectId` (the *Ungrouped*
bucket) is treated as the owner's implicit **private personal space** when its
creator is known, and as legacy/open content otherwise (see backward compat).

**Persistence and placement.** The three fields ride on the existing project
sidecar (`<data-dir>/projects/`, atomic-write + reload-on-startup, owned by the
run-loop goroutine — ADR-0034/0019). No new store, no new persistence mechanism,
no event, no engine contact. This keeps sharing entirely within the design-time
layer and out of the six invariants.

**Enforcement** lives in the HTTP API handlers, reading the request's resolved
`*Principal` (ADR-0044):

- **Under `--auth`:** list endpoints return only projects the principal owns or
  is a member of (plus everything for `admin`); mutations check the required role
  (viewer for reads, editor for writes, owner for share/delete/transfer);
  membership/role is snapshotted from the store on the run loop, never read off
  it concurrently.
- **With auth off (default):** there is no principal to key on, so the server
  behaves exactly as today — fully open, single-user. The access fields are
  written and preserved but not enforced. **Zero blast radius on existing
  deployments**, the same opt-in stance ADR-0044 chose.

**Backward compatibility.** Existing projects carry no `ownerId`. On load, an
ownerless project is a **legacy/open** project: visible to everyone when auth is
off, and to `admin` (who can then assign an owner) when auth is on. No migration
step; unowned content degrades gracefully, precisely mirroring ADR-0034's
*Ungrouped* fallback and ADR-0044's "existing deployments untouched until an
operator opts in".

### What this ADR does *not* decide (deliberately out of scope)

- **Runtime / Operations isolation.** Deployed processes run in the single shared
  engine; a shared project's *running instances* are **not** partitioned by scope
  by this ADR. Who sees which instances in Operations, and any per-scope data
  isolation in the engine, is a separate, larger, invariant-adjacent decision.
  Sharing here governs **authoring**, not execution.
- **Groups as members** — the `ref.type` hook is reserved, not implemented.
- **Org/tenant-wide visibility** — the enum has room, this slice ships
  `private`/`shared` only.

### Consequences

- **Positive:** Private-vs-shared arrives by extending one concept users already
  understand (the project), with one place to manage access; reuses the identity
  model and the sidecar mechanism verbatim; enforcement is opt-in with zero
  impact on existing open deployments; the principal-refs and role-list shapes
  leave groups, more roles, and org visibility as additive changes with no
  migration; the engine and its invariants are untouched.
- **Negative / trade-offs accepted:** the project sidecar now carries
  authorization data, so its read/write path must be careful to project out
  nothing secret (there is nothing secret here, but membership is now
  security-relevant metadata); sharing is **coarse-grained** — you share a whole
  project, not a single diagram (deemed a feature, not a bug); sharing does
  **nothing** with auth off, so the feature is only meaningful once an operator
  turns on `--auth`; **design-time only** — a shared project's running instances
  are not yet access-controlled, which must be stated clearly in the UI to avoid
  a false sense of runtime isolation.
- **Follow-ups / risks to watch:** groups as members (`ref.type: "group"`);
  richer roles and per-endpoint RBAC beyond viewer/editor/owner; org/public
  visibility; **runtime/Operations access control and per-scope engine data
  isolation** (the big one); making user-task assignment (ADR-0042/0045) aware of
  a task's owning scope; audit logging of share/transfer actions; the "scope"
  naming overlap with FEEL variable scopes (ADR-0068) — revisit if it confuses.

## Pros and cons of the options

### What a scope attaches to

**Option 1 — access facet on the Project (chosen).**
- Good: one concept to learn and manage; the project already means "a body of
  work", which is exactly what people share; no new store; naturally inherits to
  artifacts via the existing `projectId` tag.
- Bad: forces work to live in a project to be shared (Ungrouped content needs the
  implicit-personal-space rule); sharing granularity is the whole project.

**Option 2 — new container above projects (workspace/tenant).**
- Good: cleanest path to true multi-tenancy later.
- Bad: two nested grouping concepts to explain and keep consistent; far heavier
  than "private vs shared" needs today; duplicates much of ADR-0034 for little
  near-term gain.

**Option 3 — per-artifact ACLs.**
- Good: maximal granularity.
- Bad: management surface explodes (a grant per draft/DMN ref/form); sharing a
  related bundle becomes N actions; contradicts ADR-0034's "the project is the
  body of work".

**Option 4 — roles-only, no explicit sharing.**
- Good: nothing new to build; pure ADR-0044.
- Bad: cannot express "share with *these* people"; only `admin`-sees-all vs
  owner-sees-own, which is not collaboration. Fails the actual request.

### Sharing model

**(a) visibility enum + role-bearing members (chosen)** — Good: expresses
private, read-only sharing, and collaborative editing; RBAC-ready. Bad: slightly
more than an MVP boolean.
**(b) bare `shared` boolean** — Good: minimal. Bad: no read-only sharing, every
collaborator can edit; boxes in the exact axis (roles) ADR-0044 told us to keep
open.

### Enforcement point

**(i) in the handlers off `*Principal` (chosen)** — Good: reuses the established
auth boundary; nothing new to invent. Bad: every list/mutate handler must
remember the check (mitigated by a small shared helper).
**(ii) separate policy layer** — Good: centralizes checks. Bad: premature for one
resource type; adds indirection ADR-0044's boundary already provides.

## Links

- extends [ADR-0034](0034-projects-and-artifacts.md) (projects — the unit that
  gains the access facet)
- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md)
  (identity, `*Principal`, opt-in `--auth`, the role-list discipline, and the
  named follow-ups this ADR takes up)
- reuses the durable-sidecar mechanism of [ADR-0019](0019-durable-deployments.md)
  / [ADR-0021](0021-diagram-drafts.md)
- relates to [ADR-0042](0042-user-task-assignment-and-claim.md) /
  [ADR-0045](0045-user-task-assignment-bound-to-identity.md) (runtime identity —
  a future link once scopes reach runtime)
- naming overlap noted with [ADR-0068](0068-task-io-variable-mappings.md)
  (activity-local *variable* scopes — a different concept)
