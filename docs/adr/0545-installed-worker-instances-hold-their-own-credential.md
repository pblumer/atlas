# ADR-0545: An installed Worker Type's instance holds its own credential, confined to the job types its manifest declares

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-09
- **Deciders:** pblumer (maintainer; asked for this decision on 2026-11-09)
- **Open question:** whether an instance of an installed Worker Type — launched by atlas,
  but code atlas did not build — may ever receive a brokered, delegated credential from
  the authorization endpoint ([ADR-0475](0475-identity-aware-worker-execution.md) §11).
  ADR-0475 draws its line between *supervised* and *external* instances; installation makes
  a third kind, supervised but untrusted. This record keeps it on the external side of that
  line (no broker key, no internal token). Moving it across needs ADR-0475's question
  answered for publisher-signed code, which waits for ADR-0536's curation tiers.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0535](0535-worker-types-as-distributed-artifacts.md) step 3 lets an administrator
install a Worker Type artifact that atlas launches as a supervised child. The supervisor
hands every child the same credential: this server's internal token
([ADR-0049](0049-internal-service-auth-for-mcp.md)), which resolves to the service principal with
the `worker` scope ([ADR-0297](0297-confine-internal-worker-token.md)) — or, when the
operator set `ATLAS_TOKEN`, that API token instead.

The `worker` scope confines a credential to the worker protocol, but not to a job type.
So an installed artifact could lease, complete, fail or throw on jobs of every other
job type — mail, Entra, SQL — lease their form lookups, and post to the mock-system
reports of built-in Worker Types. It also holds the very secret that marks a request as
an MCP tool call (`X-Atlas-Via-MCP`, ADR-0196), and it passes the `isSupervisedWorker`
check the identity broker relies on (ADR-0475 §11). For atlas's own Worker Types that was
an accepted risk: they are built in this repository. For code from a third party or an
organisation it is not, and ADR-0535 §8 says a signature grants no access by itself.

ADR-0297 named the follow-up — per-worker ephemeral credentials "when their lifecycle can
remain automatic" — and ADR-0475 §11 lists per-Worker-Instance credentials as not built.
The installed release is the first case where the lifecycle is automatic: atlas installs
it, launches it, and removes it, and it knows the job types from the manifest.

## Decision drivers

- An installed instance reaches exactly the work its manifest declares, and nothing a
  built-in Worker Type or another installed type serves.
- No persisted secret: nothing to rotate, back up or leak from the data directory.
- Built-in supervised workers and externally operated workers with API tokens behave
  exactly as before.
- One enforcement mechanism: the scope allowlist of ADR-0194, applied before the role
  check, plus the smallest per-request check that a scope cannot express.
- A refusal says why, so a Worker Type author can tell a confinement from a fault.

## Considered options

1. **An ephemeral credential per supervised release, in memory**, resolved to a principal
   bound to its Worker Instance id and its manifest's job types, with a confined scope.
2. **A persisted API token with a job-type list** (a `worker` token carrying a reach of
   job types), minted at install and deleted at removal.
3. **A signed, stateless token** (an HMAC over instance id and job types under a per-start
   key), verified without a lookup.
4. **Keep the shared token** and rely on process confinement for installed artifacts.

## Decision outcome

Chosen: **option 1.**

### 1. The credential

When the supervisor registers an installed release and authentication is on, the server
mints a random 256-bit bearer secret for it and keeps it in memory only, indexed by its
SHA-256. The secret reaches the instance through `ATLAS_TOKEN` in its allowlisted launch
environment ([launch contract](../contracts/worker-protocol/v1/README.md#launched-by-atlas)),
never argv. The instance never receives the internal token, the broker key, or an
operator's `ATLAS_TOKEN`.

It is stable for as long as the release stays registered, so a crash restart or the
Restart button gives the new process the same secret, and the supervisor's
"restart when the environment changed" rule does not see a change on every spawn.

### 2. What it reaches

The secret resolves to the principal `system:instance:<worker id>` with the role
`operator` (the worker routes ask for it) and the scope `workertype`, which is not
mintable. Its allowlist is the job and query protocol and nothing else:

- `POST /api/v1/jobs/activate`, `POST /api/v1/jobs/{key}/complete`, `…/fail`, `…/error`
- `POST /api/v1/worker-queries/activate`, `POST /api/v1/worker-queries/{id}/answer`

Not the authorization route (no installed type is governed yet, and none may be brokered —
open question), not the mail outbox and not the mock-system reports, which belong to
built-in Worker Types.

The principal also carries a **binding**: its Worker Instance id
(`workertype:<id>@<version>`) and the manifest's `spec.jobTypes`. Each of the six handlers
checks it, because a route pattern cannot say which job a key names:

- a poll for jobs or queries must name one of the bound job types and the bound worker id;
- a completion, failure or thrown error must name the bound worker id (the protocol door
  with a lease, never the operator door), and the job behind the key must be of a bound
  job type;
- an answer to a query must name the bound worker id; the query's lease token, handed only
  by a poll of a bound type, fences the rest.

### 3. How a refusal answers

`403 Forbidden` with the reason, and an audit line (`auth.denied`) for the job routes:

- outside the scope: the existing scope refusal, naming the scope and the request;
- a foreign job type: *this credential belongs to Worker Instance `workertype:…` and serves
  only the job types its manifest declares (…); it may not serve `<type>`*;
- another worker id, or no worker id: *… it reports only as that instance, on a lease it
  holds*.

A job key that does not exist is still `404`. A key of a foreign job type is `403`, which
tells the instance that the key exists; it learns nothing else about it, and guessing keys
is not cheaper than polling.

### 4. Removal

`DELETE /api/v1/worker-types/installed/{id}/{version}` stops the instance, waits for it to
exit, and then revokes its secret, before the files are removed. Revoking after the exit
lets a report the instance sends while shutting down still land; from then on the secret
is refused with `401`, like any unknown bearer. Installing the same release again mints a
new secret.

### 5. Restart of the server

The index lives in memory, so a restart forgets every secret, and registering the
installed releases at start mints new ones. Supervised children end with the server
([ADR-0439](0439-supervised-workers-end-with-the-server.md)); a process that somehow
survived holds a secret the new server does not know, and is refused with `401`. Nothing
about the credential is persisted, and nothing in the log or the state changes.

### 6. Authentication off

With `--auth=false` nothing is minted and nothing is enforced, as for every other
credential: a server that accepts requests without a credential cannot confine one.

### Invariants

None is touched. The credential is server-local, in-memory, outside the run loop's state;
the handlers read the job's type inside the closure they already run on the loop (I3); no
event, record or `applyToState` path changes (I4, I6); nothing runs on the processor's hot
path (I1).

### Consequences

- **Positive:** an installed artifact can no longer take, settle or answer work of another
  job type, use the built-in types' report routes, mark a request as an MCP tool call, or
  pass for a built-in supervised worker at the broker. The Workers view's worker id for an
  installed instance is no longer self-asserted.
- **Positive:** it closes ADR-0297's follow-up for the one case whose lifecycle is
  automatic, without a new store.
- **Negative / trade-offs accepted:** the secret is per registration, not per spawn, so a
  secret leaked from one process of a release stays valid for its successors until the
  release is removed or the server restarts. The installed instance's report of its digest
  in the handshake is still only checked for shape (ADR-0535 §3 asks for a comparison with
  the installed digest; the binding now makes it possible).
- **Follow-ups / risks to watch:** compare the reported digest with the launched one; a
  secret per spawn, which needs the supervisor's environment comparison to leave the
  credential out; the same confinement for built-in supervised workers once each knows its
  job types (ADR-0297's general case); the open question above.

### Scale-out

The index is node-local, like the internal token and the broker key it sits beside: the
node that launches a process mints its secret and the process calls that node. In a
cluster with stateless gateways (ADR-0175, proposed) a gateway that is not the launching
node would refuse it. The seam is `principalFor`: the lookup can become a signed token
verified with a cluster key (option 3) or a replicated credential directory without
changing the scope, the binding or the refusals. Until then a launched instance must be
pointed at its own node.

## Pros and cons of the options

### Option 1 — ephemeral, in memory (chosen)

- Good: nothing persisted; the lifecycle is the supervisor's; restart and removal revoke by
  construction; reuses the scope mechanism.
- Bad: node-local; one secret per registration rather than per process.

### Option 2 — persisted API token with job types

- Good: survives a restart; visible in the token list; would work for an external worker
  too.
- Bad: a secret on disk for a credential nobody needs after a restart; records left behind
  by a failed removal; an administrator could see and revoke a token the supervisor then
  silently lacks. For external workers it is the right shape, and remains possible as an
  extension of ADR-0194's `reach`.

### Option 3 — signed stateless token

- Good: verifiable by any gateway holding the key.
- Bad: revocation at removal needs a deny list or a key rotation, which is the lookup it
  set out to avoid; a key per start is just as node-local today.

### Option 4 — keep the shared token

- Good: no change.
- Bad: leaves the gap described above; process confinement is not built for installed
  artifacts and is weak on Windows (ADR-0535 §2).

## As built

**2026-11-09.** `api/instancecredential.go` holds the index and the binding; the scope is
`apiScopeInstalledWorker` in `api/apitokenscope.go`; `httpapi.Principal.Instance` carries
the binding and `httpapi.InstanceRefusal` states the refusal; the six handlers check it.
`superviseRelease` hands the minted secret, and removal revokes it after the process has
exited. Tests: an installed instance with its own credential completes its job end to end
with authentication on; it cannot lease, complete, fail, throw on, poll queries of, or
answer as another job type or worker; it cannot reach the authorization or report routes;
its secret is refused after removal and unknown to a new server; built-in supervised
workers and API tokens are unchanged. Everything §1–§6 decides is built; the record is
`Proposed` / `Partial` only because acceptance is the owner's and the guard admits
`Landed` only under `Accepted`. On acceptance it becomes `Accepted` / `Landed`.

## Links

- refines [ADR-0297](0297-confine-internal-worker-token.md) (its per-worker follow-up, for installed releases)
- builds on [ADR-0194](0194-api-tokens.md), [ADR-0535](0535-worker-types-as-distributed-artifacts.md) §§2, 8
- relates to [ADR-0475](0475-identity-aware-worker-execution.md) §11 and its open question
- relates to [ADR-0439](0439-supervised-workers-end-with-the-server.md), [ADR-0049](0049-internal-service-auth-for-mcp.md)
