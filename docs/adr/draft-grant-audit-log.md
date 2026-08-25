# ADR-DRAFT: Grant audit log

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Pat, Atlas maintainers

## Context and problem statement

ADR-0071 gave a project an owner, a visibility, and a role-bearing member list, and
later slices added group members (ADR-0180) and an ownership-transfer control. Those
mutations are the access-control surface of the product: who can see and edit an
application's diagrams. Today they leave no trace — a member appears or disappears,
visibility flips, ownership moves, and afterwards nobody can answer *who* changed it,
*when*, or *what it was before*.

For an access boundary that is worth enforcing, "who granted this, and when" is a
question an owner (and an admin) will eventually need to answer — a departing
colleague's access, an unexpected editor, a visibility change nobody remembers
making. The question is how to record grant changes durably without touching the
engine or the access path.

## Decision drivers

- **Trustworthy history.** An audit record that can be silently rewritten or dropped
  is worse than none. Entries are append-only and snapshot what happened by value.
- **Off the six invariants.** A grant change is operator/config data, exactly like a
  project, a user, or a release. It must not enter the WAL, the event log, or
  `applyToState` — the same stance ADR-0071 took for the scope itself.
- **The access path stays pure.** `effectiveRole` must not grow a store read.
  Recording happens on the mutation path (the handler), never on the check path.
- **Reuse the shapes we already have.** A per-application, append-only history that
  lists newest-first is exactly what `releaseStore` (ADR-0128) already is.
- **Least privilege on read.** The history names every member and every actor, so it
  is more sensitive than the member list itself; only the owner and an admin may read
  it.

## Considered options

1. **A dedicated append-only sidecar store**, one JSON record per grant event,
   written on the mutation path and read back per application — modelled on
   `releaseStore`.
2. **Reuse structured logging only** — emit a log line per grant change and rely on
   the operator's log pipeline (ADR-0114 OpenSearch export) to retain and query it.
3. **Embed the history inside the project record** — grow `project` with an
   `[]grantEvent` that each mutation appends to.

## Decision outcome

Chosen option: **1 — a dedicated append-only sidecar store**, because it is the only
option that gives a durable, first-class, in-product history without coupling the
audit to a mutation of the thing being audited or to an optional external pipeline.

A grant event is one immutable record under `data/grant-audit/`, keyed by a random
id, ordered newest-first by timestamp. Four actions are recorded, each on the handler
that performs it, after the project save succeeds:

- **share** — a member was added or their role changed (subject ref + granted role);
- **unshare** — a member was revoked (subject ref);
- **visibility** — visibility changed (from → to);
- **transfer** — ownership moved (from owner id → to owner id).

The actor (id + a username snapshot for display) comes from the request principal;
with auth off there is no principal and no grant to audit, so nothing is recorded.
The read endpoint `GET /api/v1/applications/{id}/audit` returns an application's
history newest-first and requires the **owner** role (admins resolve as owner). When
an application is deleted, its audit records are deleted with it, exactly as its
releases are.

### Consequences

- **Positive:** Owners and admins get an answer to "who changed access, and when",
  in the same UI where they manage sharing. The history survives edits to the project
  because each entry snapshots its facts by value.
- **Negative / trade-offs accepted:** The audit write is part of the mutation's
  transaction — a failed audit save fails the request, mirroring how a release is
  recorded. A share flips a private project to shared as a side effect; that implicit
  flip is folded into the single *share* entry rather than double-logged as a
  separate *visibility* event. The log grows unbounded; retention/rotation is a
  follow-up, not part of this slice.
- **Follow-ups / risks to watch:** a global (cross-application) admin audit view; a
  retention policy; recording of failed/denied attempts (this slice records only
  successful mutations).

## Pros and cons of the options

### Option 1 — dedicated sidecar store
- Good: durable and in-product; append-only and value-snapshotted, so it is
  trustworthy; reuses the `releaseStore` shape and its per-application lookup and
  delete-on-project-delete cleanup; keeps `effectiveRole` pure.
- Bad: a second store to write on each grant mutation; unbounded growth until a
  retention policy is added.

### Option 2 — structured logging only
- Good: no new store; naturally append-only; flows into the existing OpenSearch
  export for querying.
- Bad: not first-class in the product — an owner cannot see their own application's
  history without operator tooling; retention depends on a pipeline that is optional
  and admin-owned; the format is a log line, not a queryable record tied to the app.

### Option 3 — embed history in the project record
- Good: one store; the history travels with the project.
- Bad: every read of the project pays for the growing history; a mutation of the
  project rewrites the same record that holds the audit, so a bug in the write path
  can corrupt or truncate the trail — exactly the property an audit log must not
  have; it bloats the record the access path loads.

## Links

- relates to ADR-0071 (sharing scopes), ADR-0180 (groups as members), ADR-0128
  (`releaseStore`, the append-only per-application history this mirrors)
- relates to ADR-0044 (users/principals — the actor identity), ADR-0018 (tolerated
  defensive save-error branches)
