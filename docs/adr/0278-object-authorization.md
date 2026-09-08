# ADR-0278: Filing a deployment into a project is a write on that project

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas authorizes on two axes. [ADR-0209](0209-roles-per-endpoint-group.md) gives
every `/api/v1` route the role it requires — `admin`, `modeler`, `operator`,
`user` — enforced in one place for sessions, API tokens, deploy tokens and OAuth
grants alike. [ADR-0071](0071-sharing-scopes.md) gives each project an owner, a
visibility and a member list, and says membership is inherited by the project's
artifacts: reading one needs viewer, writing one needs editor.

The second axis was applied unevenly. `POST /api/v1/applications/{id}/deployments`
checks it — "deploying a project's artifacts is a write on the project, so it
needs the editor role". `POST /api/v1/deployments` did not: it checked that a
named `?projectId=` **existed**, never that the caller could write it. The
inherited path was looser still — with no `?projectId=` the deploy adopts the
project of whatever draft shares its process id, with no check at all.

So the global `modeler` role was enough to publish a runnable definition into any
private project whose id the caller knew, or whose draft's process id they could
guess. An external review reproduced it: a user holding only `modeler`, who gets
404 reading the project, gets 200 deploying into it.

Random ids make that harder to stumble into. They are not an access control.

## Decision drivers

- One rule for one action: filing an artifact into a project is a write on it,
  whichever endpoint does the filing.
- A refused request must leave nothing behind — no sidecar file, no registry
  entry, no claimed message name.
- The single-writer boundary: `runloop.Loop.Do` is a rendezvous, so authorization
  that reads the project store cannot run inside another `Do`.
- Deleting a project must not turn every deploy of an orphaned draft into an
  error.

## Considered options

1. Raise the route's required role (e.g. to `admin`).
2. Apply the existing object check — `authorizeTargetProject(..., editor)` — to
   the effective project, on both the explicit and the inherited path.
3. Check only the explicit `?projectId=` path and leave inheritance alone.

## Decision outcome

Chosen option: **option 2**.

The effective project is resolved first, off the deploy's own run-loop dispatch:
an explicit `?projectId=` wins, otherwise the matching draft's project is
inherited. A project that has since been deleted degrades to Ungrouped, which is
[ADR-0034](0034-projects-and-artifacts.md)'s existing behaviour for a
dangling reference, so a stale record belonging to someone else does not fail the
caller's deploy. Then, when a project is named at all, the request must hold
editor on it.

The check runs before `claimBlockingModel` and before `deployModel`, so a refusal
happens before anything is persisted or registered. Access denied answers 404
when the caller cannot see the project and 403 when they can see it but may not
write it — the same two answers every other project-scoped route gives, so a
deploy does not become an existence oracle.

Option 1 was rejected because it answers a different question. A role cannot
express "this project"; raising it to `admin` would lock out the modelers the
endpoint exists for and still say nothing about which project may be written.
Option 3 was rejected because inheritance is the same write through a quieter
door: adopting a project's identity for a new definition is filing into it.

### Consequences

- **Positive:** the raw deploy, the project deploy and the draft save now enforce
  one rule, in one helper, with one pair of status codes.
- **Positive:** a protected system project ([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md))
  grants at most viewer, so it is now refused through this path too, matching the
  project deploy. The startup bootstrap deploys through `deployModel` directly and
  is unaffected.
- **Negative / trade-offs accepted:** two extra store reads on the inherited path
  (the draft, then its project) before the deploy's own dispatch. They are point
  reads on the design-time side, off the token path.
- **Negative:** an inherited project that has been deleted now degrades to
  Ungrouped rather than filing under a dead id. That is a behaviour change, and
  the more honest of the two.
- **Negative:** a failure to *read* the draft or its project now answers 500
  instead of quietly filing the deploy as Ungrouped, which is what the previous
  code's discarded error did. On a path that decides who may write where, a guess
  is worse than a refusal.
- **Follow-ups / risks to watch:** the same axis is still missing on the runtime
  side — instance variables are readable by any signed-in account
  (`O-02` in the ISDS list). That is the next slice, not this one.

## Pros and cons of the options

### Option 1 — raise the route's role
- Good: one line.
- Bad: locks out legitimate modelers, and still authorizes nothing about the
  project actually being written.

### Option 2 — object check on the effective project
- Good: closes both doors; reuses the helper and the semantics already in place.
- Bad: the deploy handler grows a resolution step that has to stay outside the
  run-loop dispatch.

### Option 3 — explicit path only
- Good: smallest change.
- Bad: leaves the inherited path open, which is the harder one to notice.

## Links

- applies [ADR-0071](0071-sharing-scopes.md) to the raw deploy endpoint
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) (the first axis)
- relates to [ADR-0034](0034-projects-and-artifacts.md) (project
  inheritance and the degrade-to-Ungrouped rule)
- reported as F09 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
