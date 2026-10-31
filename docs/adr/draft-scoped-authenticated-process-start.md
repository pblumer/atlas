# ADR-DRAFT: Scoped authenticated process starts for task users

- **Status:** Accepted
- **Implementation:** Not started
- **Decision accepted:** 2026-10-31 by MFE programme owner pblumer (explicit MFE1 implementation and associated ADR approval)
- **Date:** 2026-10-31
- **Deciders:** atlas maintainers; MFE programme owner pblumer
- **Programme:** [Mobile & Field Experience, MFE1](../planning/mobile-field-experience-program.md)
- **Open question:** Exact policy persistence and revocation ordering against in-flight
  instance starts must be validated against the existing application-sidecar and
  run-loop boundaries before the authorization API is implemented.
- **Question checked:** 2026-10

## Context and problem statement

The MFE1 mobile Tasks entry is implemented in PR #1468, the confirmed mobile
completion receipt in #1469, and a deployable safety inspection BPMN/form
example in #1470. None grants a normal `user` the ability to start a
permitted business process. The mobile Start destination is currently offered
to `operator` only.

[ADR-0209](0209-roles-per-endpoint-group.md) intentionally gates both
`POST /api/v1/processes/{key}/instances` and
`POST /api/v1/instances` behind `operator`. The same role also permits
cancellation, termination and runtime inspection. Giving it to field staff
for one Start button would confer unrelated operations privileges.

A process start is not a general right to administer a definition. Atlas
already has two complementary authorization axes: a role on an API route
(ADR-0209) and resource-specific access for an application/project
(ADR-0071, ADR-0278). Application *viewer* rights are not a start grant, and
an application *editor* need not authorize another person to launch work.
The public form-start token of [ADR-0029](0029-public-process-start-links.md)
is an anonymous capability, not a substitute for a logged-in user or a task
assignment.

Current code confirms the relevant seams:

- `api/openapi.go` declares route roles; `api/auth.go` and
  `api/routeroles.go` enforce them for sessions and machine principals.
- `api/httpapi/httpapi.go` carries principal ID, user roles and group IDs.
  Live group membership can update sessions (ADR-0185).
- `api/startinstance.go` already funnels operator starts by key and by
  process ID into `startInstance`; `startedByOf` captures the initiating
  principal for the event. The engine, WAL and replay path must be reused.
- `api/web/app.js` offers `#/tasks/start` for operators only; the
  navigation check is a convenience, never a server authorization decision.

**Question:** How can a signed-in task user launch *only the specific
published processes an administrator permits*, without widening the
operator's runtime-control endpoints or creating a second mobile engine?

## Decision drivers

1. Deny by default; no global `user` start permission and no inferred right
   from application visibility, editability, task assignment or QR possession.
2. A single policy for desktop, mobile and callers of the HTTP API; the UI
   can list only currently startable definitions, but server checks every POST.
3. Preserve ADR-0209's fail-closed route declarations and all existing operator
   routes, API-token scopes and machine integrations.
4. Bind a grant to a concrete application plus BPMN process identity; resolve
   the current active published definition on each request, rather than let a
   user start a hidden historical version.
5. Reuse existing form-js schemas and server validation, preventing clients
   from submitting hidden/unapproved start variables.
6. Persist permission configuration atomically, record the granting actor,
   and support immediate prospective revocation without hot-path allocations
   or direct cross-partition state writes.
7. Never report successful initiation before the existing instance start has
   passed WAL fsync; keep the `startedBy` fact replay-stable.
8. Keep privacy-safe denial/list semantics (no private application existence
   oracle) and avoid granting implicit incident, repair, deploy or terminate
   rights.

## Considered options

1. **Separate signed-in self-service start routes and explicit scoped grants.**
   Keep the operator API untouched; introduce user-role routes backed by a
   server-side grant for the selected process.
2. **Relax the existing operator start routes to `user`, then add optional
   handler filters.** Reuses URLs but widens the authority of a trusted
   runtime command in every client and token scope.
3. **Add a global `starter` role.** Smaller role model, but it cannot say
   *which* processes the caller may launch; extra resource policy remains
   necessary.
4. **Use public form links or application viewer grants.** Would confuse
   possession or visibility with authenticated, auditable permission.

## Accepted decision outcome

**Accepted option 1:** retain the existing operator start contract; add a
distinct authenticated, least-privilege path for MFE1 self-service starts.

### Approved API shape (not yet implemented)

- `GET /api/v1/me/startable-processes` (role `user`): return a bounded
  list of currently published, executable, startable definitions the
  principal may initiate, with a display name, application context and
  supported start-form metadata. Never disclose ungranted definitions.
- `POST /api/v1/me/processes/{key}/instances` (role `user`):
  accept a definition key chosen from that list and form field values.
  Server-side resolve the exact deployment and its owning application,
  verify it is the **latest eligible published** version and matches an
  effective grant, then revalidate the allowed start form and values.
  Only then delegate to the existing `startInstance` path. A client-supplied
  key, URL, favourite or QR deep link is not a permission.

The URL shape is approved in principle by this decision, **not yet an implemented
endpoint**. The `user` role is a necessary coarse gate, never sufficient
authorization; no self-service POST may mutate state before the object
policy check. Keep both existing operator POST routes and their roles
unchanged, including the by-process-ID and CSV/trigger paths. The initial
self-service path must not offer those privileged variants.

### Accepted grant and administration contract

A grant identifies `applicationId`, stable `processId` and an audience:
explicit user IDs and/or group IDs from the existing identity directory.
The grant is an independent `mayStart` permission, not the project's
viewer/editor role. The only default is **no grant**; legacy operator
rights and existing public links do not change. Ungrouped or protected
system deployments are not automatically startable by ordinary users.

Only an administrator can issue/revoke a start grant in the first slice.
Expose an auditable management action and a read-only grant preview before
a process is offered. Grants follow the current published process within
their application, not every historical deployment or another application
reusing a similar name. Revocation affects future authorization checks;
a start already durably accepted cannot be retroactively erased.

Store grant configuration in the existing **design-time sidecar** family
using its atomic-write/fsync discipline, not in the BPMN engine's WAL or
`applyToState`. Implementation must first specify the exact sidecar
record shape, versioning, grant/revoke audit and how an in-flight start
races a revocation. No second cross-partition writer, engine lock or
authorization-network call is allowed in the processor batch loop. A
request that passed authorization before a revocation may finish; a
request checked after the persisted revocation must be refused. A
stronger total-order guarantee would require a separate design review.

### Identity, errors and compatibility

- Require a **human authenticated session** for the initial self-service
  route. Machine tokens do not automatically acquire this new right
  because they carry `user`; dedicated service delegation needs a future
  scoped design. Auth-disabled deployments continue to use the established
  operator/start surfaces, not an implicitly exposed self-service grant.
- On missing login, return 401. Use 404 for absent or non-visible process
  keys and 403 only where the caller may already see the definition but
  lacks Start permission. Invalid submitted form values return a
  validation refusal without an instance; stale/superseded versions
  cannot be used as a bypass.
- Success returns the existing durable instance reference and keeps the
  initiating user identity as `startedBy`. A timed-out or disconnected
  client has an **unknown outcome**, not proof of failure or success;
  idempotent retry/reconciliation belongs to MFE5, not this ADR.
- For MFE1, show the Start destination to a normal user only when the
  authenticated list returns an eligible process. Show a clear empty
  state and preserve operator/desktop behaviour. UI availability is not
  an access-control boundary.

### Required test-first implementation slices (owner approval granted; validation still mandatory)

1. Tests for grant deny/default, admin-only mutation, effective group
   membership, revocation, cross-application mismatches and unpublished,
   retired or historical versions; test the public routes remain separate.
2. New role-`user` API listing and POST tests for 401/403/404,
   forged definition keys, direct POST without listing, manipulated form
   values, protected/ungrouped definitions, auth-disabled mode and scoped
   bearer credentials. Operator routes must remain unchanged.
3. Integration test starting the MFE1 safety-inspection model with a
   permitted user, checking both BPMN outcomes and the event's actor;
   compare live state with WAL replay after restart. Include post-fsync
   visibility and refusal-before-command assertions where applicable.
4. Browser E2E for mobile Start (320/390/768px), list empty/error/loading
   states, session expiry, double tap and unchanged desktop navigation;
   physical-device acceptance stays in MFE0 #1451.
5. Run `go build ./...`, `go test ./...`,
   `go test -race -timeout=45m ./...`, `go vet ./...`, `gofmt -l .`,
   and Playwright E2E. Update public OpenAPI documentation, security
   runbooks and programme evidence together with the implementation PR.

### Consequences

- **Positive:** real process starts for field users without handing out
  `operator` or exposing runtime repair controls; one server authority
  for mobile, desktop and API clients.
- **Positive:** current operator integrations and public links keep
  their existing roles and semantics.
- **Cost:** a persisted and audited grant model, process-scoped list,
  version checks and sidecar/revocation discipline; more work than a
  one-line role switch, but that switch would create a security gap.
- **Limit:** no offline process start, shared public task access, QR
  acknowledgement capability or general operator privilege redesign
  is implied by this record.

## Pros and cons of the options

### 1 — Explicit self-service routes and grants (accepted)
- Good: limited blast radius, precise resource scope, testable fail closed.
- Bad: extra HTTP endpoints and grant management surface.

### 2 — Relax existing operator routes
- Good: the current Tasks Start call sites need fewer changes.
- Bad: risks privilege broadening for programmatic clients and a
  role-policy change hidden under a mobile feature; conflicts with
  ADR-0209's intent.

### 3 — Global starter role
- Good: clear coarse role in the existing route catalogue.
- Bad: every process becomes startable without the object-specific grant;
  an additional policy is still necessary.

### 4 — Public token / application viewer rights
- Good: reuses a visible existing affordance.
- Bad: wrong identity and authority semantics; not acceptable as a
  substitute for an authenticated, attributable start.

## Links

- [MFE programme](../planning/mobile-field-experience-program.md), MFE1
  [issue #1456](https://github.com/pblumer/atlas/issues/1456), decision
  [issue #1467](https://github.com/pblumer/atlas/issues/1467)
- [ADR-0514](0514-mobile-first-tasks-start-and-incident-triage.md):
  accepted mobile product scope; this draft is a separate access decision
- [ADR-0209](0209-roles-per-endpoint-group.md): route roles
- [ADR-0071](0071-sharing-scopes.md),
  [ADR-0278](0278-object-authorization.md): object authority
- [ADR-0028](0028-forms-and-the-tasks-app.md),
  [ADR-0029](0029-public-process-start-links.md): form and public-start
  boundaries
- [ADR-0475](0475-identity-aware-worker-execution.md): recording who
  initiated an instance; no separate replay path
