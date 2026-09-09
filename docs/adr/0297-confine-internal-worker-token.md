# ADR-0297: Confine the internal worker token to the worker protocol

- **Status:** Proposed
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas mints one ephemeral internal token when authentication is enabled. The MCP
adapter no longer uses it as a bearer (ADR-0196); supervised workers are its only
bearer holders. Nevertheless `principalFor` resolves it to the full set of
non-admin roles with no scope, so possession reaches the product API: process and
instance data, deployments, tasks, and other non-admin operations.

That reach is larger than the holder needs. A supervised worker performs the same
small HTTP protocol as an external worker: lease jobs, complete or fail them, and
post the bounded reports produced by preview and mock workers. ADR-0194 already
defines the fail-closed `worker` scope for exactly those calls.

The mismatch becomes a data-access issue for general-purpose script tasks. Atlas
now removes the worker environment before starting an interpreter, but arbitrary
code sharing an operating-system trust domain must still be assumed capable of
attacking its worker process. An accidentally disclosed worker credential must not
therefore become a credential for Atlas's process data.

The question is whether the internal token remains an unscoped compatibility
credential or is confined to the protocol its sole bearer actually uses.

## Decision drivers

- A machine credential must have no more reach than its holder needs.
- Supervised and external workers use the same HTTP protocol and should have the
  same authorization boundary.
- Confinement must reuse the one fail-closed scope mechanism of ADR-0194.
- Existing supervised workers, including preview, AD mock and SQL mock reporting,
  must continue to operate.

## Considered options

1. Keep the internal token unscoped and rely on process isolation alone.
2. Give the internal service principal the existing `worker` scope.
3. Mint and persist a separate API-token record for every supervised worker.

## Decision outcome

Chosen option: **give the internal service principal the existing `worker`
scope.** Its legacy non-admin roles remain because worker routes require the
operator role; the scope is the additional fail-closed boundary applied before
the role check.

The worker allowlist contains only the calls made by `atlas worker`: batch
activation, completion, failure, preview outbox delivery, AD mock-directory
reporting, and SQL mock-journal reporting. The SQL report is added here because
the worker already makes that call and an externally issued worker token must be
able to make it too.

### Consequences

- **Positive:** stealing the internal worker token no longer permits reading
  process, instance or task data, deploying models, or driving other product API
  operations. Supervised and external workers have the same network reach.
- **Negative / trade-offs accepted:** the token can still interfere with worker
  jobs by leasing or settling them. That is inherent in a worker credential and
  remains reason to isolate general-purpose scripts at the operating-system or
  container boundary.
- **Follow-ups / risks to watch:** replace the shared internal worker token with
  per-worker ephemeral credentials when their lifecycle can remain automatic;
  provide a first-class sandbox profile for script Worker Instances, including a
  dedicated OS identity, filesystem and network policy, CPU and memory limits.

## Pros and cons of the options

### Keep the token unscoped

- Good: no compatibility change in authorization.
- Bad: a child-process credential reaches unrelated business and operational data;
  process isolation is the only protection around an unnecessarily broad secret.

### Reuse the worker scope

- Good: smallest change, fail-closed, already exercised by external workers, and
  its allowlist is reviewable in one place.
- Bad: every actual worker callback must be kept in that short allowlist or it is
  refused after an upgrade.

### Persist one API token per supervised worker

- Good: individual identity, revocation and scope for each Worker Instance.
- Bad: turns an ephemeral child-process credential into durable secret lifecycle
  and storage, even though Atlas owns both ends and recreates the children.

## Links

- narrows the internal credential introduced by
  [ADR-0049](0049-internal-service-auth-for-mcp.md)
- reuses the worker scope from [ADR-0194](0194-api-tokens.md)
- follows the MCP removal of the internal bearer in
  [ADR-0196](0196-authenticated-mcp-transport.md)
- complements script isolation in
  [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)
