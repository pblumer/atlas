# ADR-0516: Scoped authenticated process starts for task users

- **Status:** Accepted
- **Implementation:** Partial
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

### Approved API shape (implemented; see the checkpoints below)

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

The URL shape is approved by this decision and implemented as the self-service
list and start checkpoints below record. The `user` role is a necessary coarse gate, never sufficient
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

### Durable grant persistence implementation checkpoint (2026-11-01)

The approved design-time sidecar approach is implemented as a separate
`api/startgrant.Store` (this follow-up PR) and wired into the server's
single-writer run loop. It **does not yet authorize an HTTP route**.

- **Record:** one versioned JSON `document` per exact
  `applicationId/processId`, with a monotonic **per-scope** revision and a
  complete logically immutable `Change` history. Each change contains the
  server-supplied timestamp and actor, `grant`/`revoke` action and full
  user/group audience. One atomic sidecar replacement/fsync commits the latest
  policy **and** its complete audit history together. This avoids both a
  visible grant without audit and a lost final revoke file resurrecting a
  previously granted audience.
- **Identity and scope:** an empty audience explicitly revokes the process
  grant; there are no default grants or viewer/editor implications. Expected
  per-scope revisions reject stale administration changes.
- **Recovery:** startup validates each complete audit chain and rebuilds the
  effective policy. Corrupt/truncated documents fail startup; a missing
  document grants nobody. A write/fsync error makes the in-memory store
  uncertain, refusing positive authorization and further writes until restart.
  Restoring an intentionally older backup is still an administrative rollback
  and needs normal restore governance; no store can infer missing future writes.
- **Ownership:** only the API run-loop may mutate or query the store. No
  processor, WAL, `applyToState` or engine hot path changes. The store is
  classified as `credential` in the backup inventory; it is not part of
  portable design-time application exports.
- **Revocation ordering:** a request checked **after** a persisted revoke is
  refused. A start that passed its policy check **before** that revoke may
  finish. This is the accepted ADR-0516 prospective ordering, not a claim of
  total-order cancellation of already accepted work.

**Still required before exposing self-service Start:** an admin-only audited
management API, server-side application/process and principal validation,
published-version resolution, the two separate user-role self-service routes,
form enforcement, end-to-end 401/403/404 and machine-token tests, and a
post-fsync instance-start/replay test. MFE0 physical-device acceptance #1451
remains open.

### Admin management API checkpoint (2026-11-02)

The administration half is implemented as the per-area service
`api/startgrant.Service` (ADR-0147) over the store above. It **authorizes no
start by itself**: a grant issued here lets nobody start anything until the
self-service start route exists.

- **Routes**, all declared `admin` in the route table and on the admin allowlist:
  `GET /api/v1/applications/{id}/start-grants` (every scope of one application),
  `GET|PUT|DELETE /api/v1/applications/{id}/start-grants/{processId}`,
  `GET …/{processId}/history` (newest first) and
  `GET …/{processId}/preview?userId=|groupId=`. The preview is a `GET`, like the
  existing deletion preview, because it writes nothing.
- **Who:** an administrator. A change (PUT/DELETE) additionally needs the
  administrator's own signed-in session (`Principal.Session`), the rule
  ADR-0475 §10 applies to a person's own acts: an API token never carries admin,
  and an application a person granted access to acts for them but is not them.
  An application owner or editor reaches none of these routes. A server without
  authentication refuses all of them with 403 — nobody could be the audited
  actor, and the self-service routes are not offered there. None of the routes is
  an MCP tool.
- **What is checked, in the loop turn that writes:** the application exists and
  is not a protected system application (409); the **newest deployed version** of
  the process id is filed under that application (404 otherwise, so a process id
  another application deployed last cannot be granted through this one); every
  user and group id names an existing account or group — by id, never by a name
  (400 naming every unknown id). The audience is non-empty (DELETE revokes),
  bounded, without padded or repeated ids, and stored sorted. `expectedRevision`
  is required; a stale one answers 409 with `currentRevision`. The body is
  decoded strictly: an unknown field — `actorId`, `at`, `revision` — is refused,
  so the audit fields are the server's.
- **Writes** go through `Store.Set` and are answered only after the atomic
  replacement and its fsync. Granting exactly the current audience, and revoking
  what is already revoked, write nothing (`changed: false`). Revoking never needs
  the application or the process to still exist. A failed write answers 500 and
  leaves the store uncertain; every route of the area then answers 503 until a
  restart reads back what reached the disk.
- **Preview** reads the account's group membership from the directory (the same
  source live sessions are updated from, ADR-0185) and answers `granted` with
  `matchedBy`, `accountIssues` (`disabled`, `missing-user-role`) and the
  `blockers` of the process's newest version. The blocker rule is one server
  function (`selfServiceBlockers`) that the self-service routes are to share:
  application missing or protected, process not deployed, newest version filed
  under another application, not executable, deactivated (ADR-0119), only
  startable by its triggers (ADR-0426), or a bound constant missing (ADR-0453).
- **Interpretation recorded here, to be confirmed with the owner:** atlas has no
  runtime "published" state apart from the deployed definition, and the operator
  and public-link starts both resolve the newest deployed version. This slice
  therefore reads "the current published process within its application" as the
  newest deployed version of the process id, filed under the grant's application;
  a superseded version is never offered. It does **not** additionally require the
  version to be a member of the application's latest release record (ADR-0128).
  Requiring that would make a plain deploy without a release withdraw the process
  from field users, and would need a release index so the listing does not read
  every release document on the run loop; it is a one-function change if wanted.

### Self-service list checkpoint (2026-11-02)

`GET /api/v1/me/startable-processes` is implemented (`api/selfservicestart.go`),
the first of the two approved self-service routes. It lists; it starts nothing.

- **Gate:** role `user` from the route table (admin reaches it too), and the
  caller must be a person's own signed-in session. An API token or a connected
  application is refused with 403 even when it carries `user`. A server without
  authentication answers 404 and keeps its operator Start view. A logged-out or
  disabled account has no session and gets 401.
- **What is listed:** every scope whose latest durable change grants the user id
  or one of the group ids the session carries now — kept current by every
  membership change (ADR-0185) — resolved to the newest deployed version of the
  process and offered only when the same `selfServiceBlockers` rule the preview
  uses finds nothing. So no superseded version, no process whose newest version
  another application deployed, nothing deactivated, non-executable, trigger-only,
  missing constants, or in a deleted or protected application. Each item carries
  the definition key, process id, display name, version, application id and name,
  and whether and from which form it starts (`startForm.available`, `formId`);
  never an audience or another person.
- **Cost on the run loop:** the grants are in memory; the registry is walked once
  per request and each granted application read once. The answer is sorted by
  application name, then process name, with ids as tie-breakers, and capped at 200
  with `total` and `truncated`. While the grant store is uncertain after a failed
  write the route fails closed with 503 rather than an empty list.
- The definition key in an item is not a permission; the start route must check
  everything again.

Tests: `api/startableprocesses_http_test.go` (empty by default, group and user
grants, redeploy shows only the newest version, sorting, leaving the group,
revocation, deactivation, non-executable newest version, process id redeployed
into another application, deleted application, anonymous 401, machine token 403,
account without the user role 403, logout and disabled account 401, open server
404), `api/selfservicestart_internal_test.go`, `api/startgrant/store_test.go`.

Tests: `api/startgrant/service_test.go` (deny by default, open server, delegated
credential, malformed and forged bodies, unknown identities and scopes, competing
administrators, idempotent and always-possible revocation, persistence failure and
recovery, preview), `api/startgrants_http_test.go` (anonymous 401, task user,
application owner and machine token 403, administrator end to end, group
membership change, restart over the same data directory).

### Self-service start checkpoint (2026-11-02)

`POST /api/v1/me/processes/{key}/instances` is implemented
(`handleMyProcessStart`, `api/selfservicestart.go`). With it the server side of
MFE1's scoped start is complete; the mobile and desktop Start UI that calls it is
the remaining slice.

- **Same gate as the list:** role `user`, a person's own session, 404 on a server
  without authentication. Nothing a listing said is taken for granted.
- **Authorization, twice, on the loop:** the key is resolved (404 for an unknown
  or malformed key), then `selfStartDecisionOnLoop` answers 403 when the person
  holds no current grant for the definition's application and process, 409 when
  they hold one but the key is not the newest version or the shared blocker rule
  refuses it, and 503 while the grant store is uncertain. 403 rather than 404 for
  an existing definition is no oracle: every signed-in identity already lists the
  deployed definitions (`definitionReadable`). The **same decision runs again as a
  `startGuard` inside the turn that submits the start command**, so a revocation
  that was durable before that turn refuses the start, and one that lands after it
  finds the start already accepted. That is this record's prospective ordering,
  now pinned to the command rather than to the first check.
- **One start path:** `startInstance` became `startInstanceGuarded` with an
  optional guard; the operator routes pass none and behave as before. Sealing
  (ADR-0314), `CreateInstanceStartedBy` with the session's user id (ADR-0475 §3),
  the drive, the read-back and the answer are the existing ones — answered only
  after the start passed the WAL fsync. No processor, WAL or `applyToState` change.
- **Values are held to the start form before anything is sealed or started:** the
  body is strict JSON holding only `variables` (bounded by the Settings budget); a
  process without a start form takes no values; a missing form refuses the start
  (409); a directory field refuses it (409), because its value can only be checked
  through a task's lookup route; decision-field values are checked against the
  decision (`refuseDecisionValues`); the shape check every public form uses refuses
  any path the form does not submit, a disabled field included; and the new
  `startFormProblems` holds the constraints the form declares — required (unless
  shown under a condition), the type a component submits, minimum and maximum
  length and value, and a closed list of options. Not checked on the server, and
  stated so in the code: a `pattern` (JavaScript and Go regular expressions differ),
  fields inside repeating lists beyond their shape, and options read from a
  variable or expression. Every other door keeps its existing shape-only rule.
- **Answer:** the existing `{definitionKey, instanceKey, stats, wizard}`; the
  counts are the ones `GET /api/v1/stats` gives every signed-in identity. A
  disconnected or timed-out client has an unknown outcome; nothing here is
  idempotent, which stays MFE5's.

Tests: `api/selfstart_http_test.go` (granted start with and without a form, every
constraint and injection case creating nothing, foreign, private, ungranted,
unknown, malformed, superseded and deactivated keys, revocation without a listing,
anonymous, machine token, OAuth-connected application, account without the user
role, operator routes unchanged, open server),
`api/selfstart_recovery_internal_test.go` (the safety-inspection example granted to
a group and started by a member, `startedBy`, timeline/variables/tasks identical
after a restart that replays the WAL, refusal after revocation with no new
instance, the started instance still completing; and the guard refusing a
revocation durable between the first check and the command turn),
`api/startformcheck_internal_test.go`.

## Consequences

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
