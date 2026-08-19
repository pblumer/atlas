# ADR-0129: Remote deployment targets — publish an application to another Atlas server

- **Status:** Proposed
- **Date:** 2026-08-17
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0128](0128-process-applications.md) made the process application the unit of
bundling and versioning: Phase 2 ships `POST /api/v1/applications/{id}/publish`,
which validates the application's artifacts, deploys them together, and records an
`applicationRelease` — a versioned manifest of what shipped. Phase 2 also moved the
"what is deployed" view *into* the application.

All of that is **local to the one engine**. Deploy has always meant "register in
this server's processor" (ADR-0019). The only ways work crosses machines today are
operator-level whole-directory transfers: the design-time backup tar (ADR-0107) and
the whole-instance snapshot (ADR-0109). Neither is an application-scoped release,
neither is repeatable as a routine action, and both require someone to drive a
restore on the far side.

Users think in environments: *Test* and *Produktion* are places the same
application runs at possibly different versions. ADR-0128 named this Phase 3 and
deferred the transport and trust questions to their own ADR — this one.

Two findings from the codebase shape the problem, and neither was obvious when
ADR-0128 was written:

1. **Atlas has no machine-to-machine credential.** Authentication is opt-in
   (ADR-0044) and, when on, is local password + an **in-memory** opaque session
   cookie (`api/auth.go`); sessions do not survive a restart. There is no API
   token, no bearer credential an operator can mint, and no scoped identity a
   peer server could present. ADR-0049 did establish the *mechanism* — the auth
   middleware already resolves `Authorization: Bearer <token>` to a principal in
   constant time — but the only token that exists is the process-internal MCP one,
   minted at startup and never persisted or served.
2. **The single writer must not block on a network call.** The run loop owns the
   deployment registry and every store (ADR-0002). An outbound HTTP call to a peer
   inside `s.do()` would stall the partition for the duration of a remote request
   or timeout.

The question: **how does an application publish to another Atlas server — what is
sent, how is the far side authenticated and authorized, and what does "published to
Test at v5" mean when both servers number their own releases?**

## Decision drivers

- **Stay a design-time concern.** Like ADR-0034/0127, this must live below the HTTP
  API and must not enter the event log, `applyToState`, or the hot path. The remote
  call is an outbound side effect, in the same category as a connector invocation.
- **Never block the single writer.** Resolve state on the loop, do network I/O off
  it, record the result back on the loop (ADR-0002).
- **Credentials by reference, never by value.** The connector precedent (ADR-0041,
  `connector.CredentialsRef`) and the vault (ADR-0069/0070) already fix this: a
  store holds a vault handle, never secret material, and secrets never appear in a
  response or a log.
- **Least privilege on the far side.** A credential that lets a peer publish should
  not also let it administer users or read secrets — the same reasoning ADR-0049
  applied to the MCP service principal.
- **Reuse the remote's existing capability.** The far side already knows how to
  validate and deploy a bundle all-or-nothing. Publishing to it should drive that,
  not invent an engine-to-engine protocol or a second deploy path.
- **Honest failure reporting.** Multi-target publishing can partially fail. The
  result must say precisely which targets carry which version, never a single
  optimistic "published".

## Considered options

### A. How the local server authenticates to the remote

1. **A durable, scoped deploy token minted on the remote**, presented by the local
   server as `Authorization: Bearer`, extending the bearer→principal path ADR-0049
   already built.
2. **Store a remote user's username + password**, log in over `POST /api/v1/auth/login`,
   hold the session cookie, re-login on 401.
3. **mTLS / a shared static secret** configured symmetrically on both servers.

### B. What is sent, and which endpoint receives it

1. **A bundle-import endpoint on the remote** — one request carrying the release
   manifest and its artifacts; the remote ensures the application exists, validates
   and deploys all-or-nothing, and records the release.
2. **Drive the remote's existing granular API** — create/lookup the application,
   then `POST /api/v1/deployments` per member, then publish.
3. **Ship a backup archive** (ADR-0107 tar) and have the remote restore it.

### C. How an application is addressed across servers

1. **A per-target binding record** mapping local application id + target → the
   remote's application id, learned on first publish.
2. **A portable application key** (a stable slug) that every server uses as the
   application's identity.

## Decision outcome

**A → option 1** (scoped deploy token), **B → option 1** (bundle-import endpoint),
**C → option 1** (per-target binding record).

### The deployment target

A **deployment target** is admin-owned server configuration — the same category as
managed connectors (ADR-0041) and per-server call-activity overrides (ADR-0105),
not per-application user data. Applications *reference* targets; targets are not
owned by any one application. Persisted as its own design-time sidecar under
`<data-dir>/targets/`, holding a name, a base URL, a kind, and a **credential
reference** — a vault handle, never the token itself (I6 / ADR-0041 precedent).
Writes are admin-gated; an application editor may publish to a target but not
create, retarget, or read the credential of one.

TLS verification is always on. There is deliberately no "skip verify" switch: a
target is a trust relationship, and an option to disable verification would be the
first thing reached for when a certificate is wrong.

### Authentication: a deploy token, not a borrowed password

The remote mints a **durable, scoped deploy token** which an operator copies into
the publishing server's vault. The local server presents it as a bearer; the
remote's existing middleware resolves it to a **non-admin, publish-scoped
principal** — deploy and read design-time state, nothing else. This extends the
ADR-0049 mechanism (bearer → principal, constant-time compare) from one
process-internal token to a small durable set, and is the honest cost of this
decision: **it adds a persistent credential surface to Atlas that does not exist
today**, with the revocation, listing, and rotation that implies.

Option A2 — storing a human's username and password — was rejected despite being
the only option that works against a *stock, unmodified* remote today. A password
cannot be scoped below the user's own rights (an admin's password grants
administration), cannot be revoked without locking the human out, and turns every
publish into an impersonation. Borrowing a person's identity for a machine action
is precisely the pattern ADR-0049 declined for the MCP adapter.

Option A3 (mTLS/shared secret) is a reasonable posture for a locked-down fleet but
requires certificate plumbing on both ends and gives no per-peer identity inside
the application, so it is deferred rather than dismissed — a target's credential
reference is deliberately opaque enough to carry a client certificate later.

**When the remote runs with auth off** (ADR-0044's default), no credential is
needed and none is required; the target simply carries no credential reference.
That is the operator's existing choice about that server's exposure, not a new
weakening introduced here.

### Transport: one bundle-import request

Publishing to a target sends **one request** carrying the release manifest and its
artifact payloads (the BPMN XML and the already-resolved DMN models the local
publish snapshotted). The remote handles it exactly as a local publish: ensure the
application exists, validate the whole bundle, deploy all-or-nothing, record the
release. One request means one atomic outcome to report and one round trip to
reason about — where option B2's per-member calls would leave a half-deployed
application on any mid-sequence failure, the very failure mode ADR-0034's
"validate all, then deploy all" exists to prevent. Option B3 (ship a backup
archive) was rejected because a restore is a whole-directory overwrite of every
application on the far side, not an application-scoped release, and needs a restart
to take effect.

The models travel **already resolved**, from the local release snapshot: the remote
must not be required to reach the same temis instance, and a release is supposed to
be the frozen thing that shipped (ADR-0128).

### Release versions travel with the bundle

The application's release version is minted **once**, by the publishing server, and
**travels with the bundle**. A target records "application X, release v5, deployed
here at T" rather than numbering independently. A release identifies *what shipped*;
a target is a *place it runs*. Independent per-server numbering would make "v5" mean
different artifacts on different servers — exactly the confusion the version is
meant to remove — and would make the mockup's honest reading ("Prod v4, Test v5")
impossible to state.

The consequence, accepted: a target that also receives *local* publishes has two
sources minting versions into one sequence and can collide. The remote therefore
treats an incoming release version as authoritative for the application it
imports, and refuses an import whose version it already holds with different
content, rather than silently overwriting. Making a target read-only for direct
authoring is the operator's discipline, not something this ADR enforces.

### Addressing across servers, and reading target state

The remote's application id is **not** the local one. The local server records the
mapping (local application + target → remote application id) in the target binding
when a publish first succeeds, and uses it to read that target's state afterwards
by calling the remote's `GET /api/v1/applications/{id}/deployments` — the endpoint
Phase 2 already ships. The per-target rows in the application's Deployments view
are those reads, one per bound target.

A portable application key (option C2) is the tidier long-term identity and is
probably required by ADR-0128 Phase 4 (git), where one repository is one
application and the id must survive a clone into an empty server. It is deliberately
**not** decided here: introducing a global identity for applications is Phase 4's
question, and the binding record works without it and remains valid underneath it.

Reading target state is best-effort and must degrade: an unreachable target renders
as "unreachable", never as an error that empties the whole view or blocks the page.

### Concurrency and failure

The publish resolves the application and its release on the run loop, performs
every remote call **off** the loop with a bounded timeout, and records outcomes
back on the loop — never an outbound request inside `s.do()` (I3 / ADR-0002).

Publishing to several targets is **not** atomic across them and must not pretend to
be. Each target succeeds or fails on its own and the response reports per-target
status; the local release records which targets carry it. There is no distributed
transaction across independent engines, and inventing a two-phase protocol to fake
one is far beyond what the problem warrants.

### Consequences

- **Positive:** an application ships to real environments as a routine, repeatable,
  versioned action; the far side reuses the validation and all-or-nothing deploy it
  already has; credentials follow the established reference-not-value rule; the
  single writer never blocks on a peer; per-target status is reported honestly.
- **Negative / trade-offs accepted:** a **new durable credential surface** (deploy
  tokens) with the revocation and rotation duties that follow; a new outbound
  network dependency in a product that had none for deploy; a **new endpoint on the
  remote**, so publishing works only against servers new enough to expose it —
  there is no path to a stock older Atlas; multi-target publishing is
  non-atomic by construction; version collisions are possible on a target that is
  also authored against directly.
- **Follow-ups / risks to watch:** token rotation and revocation UX; whether a
  portable application key should replace the binding record (Phase 4 decides);
  how an import that partially matches an existing remote application is reconciled
  (rename vs. adopt); rate/size limits on the import endpoint, which accepts a
  multi-megabyte bundle from an authenticated peer; and whether target *state*
  reads deserve caching once a fleet grows past a handful of targets.

## Pros and cons of the options

### A1 — scoped deploy token (chosen)
- Good: least privilege and independently revocable; reuses the ADR-0049
  bearer→principal path; no human identity is impersonated; the credential is a
  vault reference like every other secret.
- Bad: Atlas gains a durable token concept it does not have today, including
  listing, revocation, and rotation; requires the remote to be new enough.

### A2 — store a remote user's password
- Good: works against a stock, unmodified remote right now; no new concept.
- Bad: cannot be scoped below that user's rights; revocation locks out the human;
  every publish is an impersonation; in-memory sessions mean re-login churn; stores
  a reusable human credential to enable a machine action.

### A3 — mTLS / shared secret
- Good: strong transport-level mutual trust; no application-level token to leak.
- Bad: certificate plumbing on both ends; no per-peer identity inside the
  application, so authorization cannot be expressed in the existing principal
  model. Deferred, not dismissed.

### B1 — bundle-import endpoint (chosen)
- Good: one request, one atomic outcome; the remote reuses its own validate-then-
  deploy-all path; nothing half-lands.
- Bad: a new endpoint to design, version, and bound in size.

### B2 — drive the granular API
- Good: no new remote endpoint; works with what exists.
- Bad: a mid-sequence failure leaves a half-deployed application — the exact
  failure ADR-0034's bundle semantics prevent; many round trips; the local server
  would have to re-implement the remote's ordering and validation rules.

### B3 — ship a backup archive
- Good: reuses ADR-0107 wholesale.
- Bad: whole-directory overwrite of every application on the far side, not an
  application-scoped release; needs a restart to take effect; no per-application
  version semantics.

### C1 — per-target binding record (chosen)
- Good: works today with no new global identifier; local to the publishing server;
  stays correct if a portable key is introduced later.
- Bad: the mapping is state that can go stale if the application is deleted and
  recreated on the remote.

### C2 — portable application key
- Good: one identity everywhere; almost certainly what git (Phase 4) needs.
- Bad: introduces a global identity and a uniqueness/collision rule that belongs
  in the ADR that actually needs it, not here.

## Links

- implements Phase 3 of [ADR-0128](0128-process-applications.md) (process
  applications — the release this publishes, and the deployments view it fills)
- relates to [ADR-0049](0049-internal-service-auth-for-mcp.md) (the bearer →
  principal mechanism the deploy token extends, and the "don't impersonate a human
  for a machine action" precedent)
- relates to [ADR-0044](0044-user-management-and-authentication-boundary.md) (the
  opt-in auth boundary and the session model this works with and around)
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) and
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) /
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md) (credential by
  reference, never by value — the target credential follows the connector shape)
- relates to [ADR-0019](0019-durable-deployments.md) (the per-processId versioning
  the remote applies when it registers an imported bundle)
- relates to [ADR-0107](0107-backup-and-restore.md) and
  [ADR-0109](0109-full-instance-snapshot.md) (the whole-directory transfers this
  replaces for the application-scoped case)
- relates to [ADR-0105](0105-per-server-call-activity-target-overrides.md) (the
  adjacent "this server's operator config" category a target belongs to)
- relates to [ADR-0002](0002-single-writer-partition-model.md) (why the outbound
  call runs off the run loop)
