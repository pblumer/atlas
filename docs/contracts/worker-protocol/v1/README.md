# Worker protocol v1

This is the contract between an atlas server and a **Worker Instance**: the process that
leases durable jobs of named types, does the work, and reports how each job ended. It also
covers form lookups (queries) a Worker answers with its own credential, and the question a
governed Worker asks before acting.

Any language may implement it. The Go reference client is `workersdk`, which depends on
nothing else in this repository ([ADR-0535](../../../adr/0535-worker-types-as-distributed-artifacts.md)).
The protocol was established by [ADR-0007](../../../adr/0007-job-worker-protocol.md) and
extended by ADR-0168, ADR-0440, ADR-0471, ADR-0473 and ADR-0475; this document states it as
one contract and adds the handshake of ADR-0535 §3.

The OpenAPI description at `GET /api/v1/openapi.json` is authoritative for field types. This
document is authoritative for meaning and for the compatibility rule.

## Transport

- HTTP/1.1 or HTTP/2, JSON request and response bodies, `Content-Type: application/json`.
- Every request is a `POST` to a path below `/api/v1/`.
- Authentication: `Authorization: Bearer <token>` when the server requires it. The token
  needs the operator role or an API token scoped to these routes.
- A non-200 answer carries `{"error": "<reason>"}`. A 404 without a JSON body means the
  server does not have the route at all, which is different from "no such job".
- TLS verification is never optional. A client may add certificate authorities; it may not
  skip verification.

## Versioning and the handshake

The protocol has a **major version**, currently `1`.

Every poll (`jobs/activate` and `worker-queries/activate`) carries:

| Field | Meaning |
|---|---|
| `protocol` | The major version the client speaks. Absent means `1`: every client from before the handshake. |
| `workerType` | Optional. The Worker Type the instance runs: `{"id": "com.example.sap-s4", "version": "2.1.3", "digest": "sha256:…"}`. `id` is a lower-case reverse-DNS identifier, `version` a semantic version, `digest` the sha256 of the artifact when the instance knows it. |

Every answer to a poll, refusals included, carries the header
`X-Atlas-Worker-Protocol: <versions>` with the major versions the server accepts,
comma-separated.

**Compatibility rule.**

1. **Within a major version every change is additive.** A new request field is optional and
   a server that does not know it ignores it. A new response field is ignored by a client
   that does not know it. Clients must ignore unknown response fields.
2. **A change either side could not ignore is a new major version.**
3. **A server accepts the current major version and the one before it**, so Worker
   Instances can be upgraded after the server rather than with it, for at least one
   release.
4. **A poll naming a version the server does not accept is refused with `400`**, naming the
   versions it does accept. A client should log that and keep retrying: an upgrade of either
   side then shows up as a visible error, not as silence.
5. **A malformed `workerType` is refused with `400`** and the reason.

The server shows the reported `protocol` and `workerType` per Worker Instance in
`GET /api/v1/workers`. They are runtime topology: replaced on every poll, never durable.

## Jobs

A job becomes visible to a Worker only after it is durable on the server: written to the
log, fsynced, and committed. Execution is **at least once**. A job whose lease runs out
before it is reported is offered again, so a handler with side effects must be idempotent.

**Idempotency key.** Every leased job carries `idempotencyKey`, `atlas:<node id>:<job key>`:

- It is the same for every delivery of one task execution: a retry after a failure, a
  redelivery after a lease ran out, and the retry an operator starts by resolving the
  job's incident.
- It is different for every execution (a loop iteration or multi-instance element gets a
  new job) and for every installation (the node id).
- Hand it to a target that filters duplicates (an idempotency header, a request id), or
  use it to check before acting.
- An installation started from a copy of another's data directory shares its node id and
  so its keys.

**Declaring idempotency.** A Worker Type's manifest declares, for every operation, its
`idempotency` and its `effect`; both are required. atlas uses them to decide where to warn
about a repeated effect
([ADR-0538](../../../adr/0538-operations-declare-idempotency-and-effect.md)).

| `idempotency` | Meaning |
|---|---|
| `natural` | A second delivery is harmless by nature |
| `key` | The target filters duplicates by `idempotencyKey` |
| `check` | The worker checks before acting whether the action already happened |
| `none` | A second delivery can repeat the effect |

`effect` is `reversible` or `irreversible`.

### Lease: `POST /api/v1/jobs/activate`

```json
{
  "type": "io.atlas.mail.send",
  "worker": "mailer-1",
  "leaseMs": 300000,
  "maxJobs": 4,
  "waitMs": 30000,
  "connectors": ["office365"],
  "endpoints": {"office365": "https://graph.microsoft.com:443"},
  "protocol": 1,
  "workerType": {"id": "io.atlas.mail", "version": "1.4.0"}
}
```

- `type` (required) is the job type; `worker` is this instance's id, shown in the Workers
  view. Give each deployment its own id.
- `leaseMs` is how long the job is held for this worker. It must comfortably exceed the
  work. A report after the lease has run out is fenced out.
- `maxJobs` is the number of free places. A client asks only for as many jobs as it can
  start at once, because a leased job nobody is running is a job nobody else can take.
- `waitMs` long-polls: the server holds the request until a job is durable or the wait ends.
- `connectors` are the configured Workers this instance holds credentials for. The server
  hands out only jobs naming one of them. Reporting them is also what makes the instance
  present for those Workers.
- `endpoints` are where those Workers connect, reduced to `scheme://host:port`.

Answer:

```json
{"jobs": [{"jobKey": 42, "type": "io.atlas.mail.send", "processInstanceKey": 7,
           "elementInstanceKey": 9, "processDefKey": 1, "elementId": "notify",
           "retries": 3, "leaseToken": 11,
           "idempotencyKey": "atlas:4f1c…:42", "variables": {"to": "a@example.com"},
           "connector": {"kind": "mail", "fields": {"…": "…"}}}]}
```

`connector` is present only for a built-in Worker Type whose task detail the server
resolved into plain values. It contains no credential.

A poll that fails (server restarting, job type not deployed yet: `404` naming the type) is
not a reason for the client to exit. It retries after a delay.

### Complete: `POST /api/v1/jobs/{jobKey}/complete`

```json
{"worker": "mailer-1", "leaseToken": 11, "variables": {"messageId": "…"}}
```

Optional, and ignored for job types that do not use them:

- `decision`: for a business rule task, `{"decisionId", "inputs", "outputs", "trace"}`.
- `toolCalls`: for an agent round, `[{"tool", "callId", "arguments"}]`. An empty or absent
  list means the agent is done.

### Fail: `POST /api/v1/jobs/{jobKey}/fail`

```json
{"worker": "mailer-1", "leaseToken": 11, "retries": 2, "message": "SMTP 421", "retryBackoff": 15000}
```

- `retries` is the number of attempts left after this failure. At `0` the server raises an
  incident.
- `retryBackoff` (milliseconds, optional) holds the job back before it is offered again.
  The server's own policy is 60 s with one retry left and 15 s with more. Clients should use
  the same, so a job waits equally long wherever it runs.

### Throw a BPMN error: `POST /api/v1/jobs/{jobKey}/error`

```json
{"worker": "mailer-1", "leaseToken": 11, "errorCode": "NO_RECIPIENT", "errorMessage": "mailbox unknown"}
```

This is for an outcome the process has a path for. It spends no retry. A matching error
boundary catches it; with none in reach the job waits on an incident. An empty `errorCode`
is refused, and a client should then fail the job instead.

### Ask before acting: `POST /api/v1/jobs/{jobKey}/authorize`

```json
{"worker": "sharepoint-1", "leaseToken": 11}
```

A governed Worker Type asks this before it touches the target system (ADR-0475). The answer:

```json
{"enforcement": "enforce", "permitted": true, "enforced": true, "reasonCode": "…",
 "decisionKey": 99, "job": {"…": "…"},
 "credential": {"type": "bearer", "accessToken": "…", "expiresAt": 1730000000}}
```

- `enforcement` is `off` for a Worker without an identity policy. The client then proceeds
  as before.
- `job` holds the parameters the decision covers. The handler acts on these, not on what it
  was handed at lease time.
- `credential` is present only for a delegated action, and only for an instance the server
  supervises. That instance sends `X-Atlas-Broker-Key` with the request.
- If the answer is refused, the server has already settled the job. The client reports
  nothing further.
- A `404` without a JSON body means the server predates this step. Treat it as
  `enforcement: off`.

## Queries (form lookups)

A query is not a job. Nothing about it is durable, it has no retries, and it lives only as
long as the person's request waiting on it (ADR-0473).

### Lease: `POST /api/v1/worker-queries/activate`

```json
{"type": "io.atlas.entra", "worker": "entra-1", "connectors": ["ucc_entra"],
 "maxQueries": 4, "waitMs": 30000, "protocol": 1}
```

The poll also carries `workerType` when the instance has one. The answer:

```json
{"queries": [{"id": "q1", "leaseToken": "…", "type": "io.atlas.entra", "connector": "ucc_entra",
              "deadline": 1730000000000,
              "query": {"resource": "users", "search": "anna", "searchFields": ["displayName"],
                        "properties": ["id", "displayName"], "pageSize": 25}}]}
```

The query shape is `workersdk/lookup.Query`: `resource`, `value`, `valueProperty`,
`search`, `searchFields`, `filters`, `activeOnly`, `properties`, `pageSize` and `next`. The
server never hands over a URL. The Worker Type builds its own request from its own
allowlist. `deadline` is in Unix milliseconds.

### Answer: `POST /api/v1/worker-queries/{id}/answer`

Exactly one of:

```json
{"worker": "entra-1", "leaseToken": "…", "page": {"items": [{"id": "…", "displayName": "…"}], "next": "…"}}
{"worker": "entra-1", "leaseToken": "…", "error": {"code": "provider_throttled", "retryAfter": 10}}
```

- `items` is a list, possibly empty, never `null`. Each record carries only the requested
  `properties`.
- The error codes are `invalid_query`, `worker_unconfigured`, `provider_throttled`,
  `provider_unavailable` and `provider_refused`.
- **No message travels with an error.** A provider's own error text can contain the
  person's search term, so it must not leave the Worker.
- A refused answer (the asker has gone) needs no action.

## Launched by atlas

A Worker Type installed on a server as an artifact
(`POST /api/v1/worker-types/installed`) is launched by the server's supervisor
([ADR-0535](../../../adr/0535-worker-types-as-distributed-artifacts.md) §2). The supervisor
verifies the executable's sha256 against the digest it was installed with before every
start, and refuses one that no longer matches.

It starts the executable **with no arguments**. The environment holds a few runtime basics
(`PATH`, `HOME`, temporary directories, locale, the outbound proxy variables) and nothing
else of the server's. On top of that it sets:

| Variable | Meaning |
|---|---|
| `ATLAS_SERVER_URL` | The server to work for |
| `ATLAS_WORKER_ID` | The instance's id, `workertype:<id>@<version>` |
| `ATLAS_TOKEN` | The instance's own bearer credential, when the server requires one (below) |
| `ATLAS_WORKER_TYPE_ID`, `ATLAS_WORKER_TYPE_VERSION`, `ATLAS_WORKER_TYPE_DIGEST` | The release the supervisor verified, to report in the handshake |
| `ATLAS_WORKER_CONFIG` | The configured Workers this instance serves, as `{"workers":[{"name":"…","config":{…}}]}`. Each configuration has its secrets resolved from the vault and has been checked against the manifest's `spec.configuration` schema. Absent when nothing is configured. |

**Configuration.** A manifest declares its configuration contract inline as `spec.configuration`, a JSON Schema of `"type": "object"`. A top-level property marked `"writeOnly": true` is a secret. An administrator configures a Worker with `PUT /api/v1/worker-types/installed/{id}/workers/{name}`, giving `config` for the settings and `secrets` that map each secret property to the vault entry holding it. A secret value is never stored in the Worker and never shown by the API; only the launched instance receives it. When a Worker changes, the type's instances restart with the new configuration.

**Credential.** The token is minted for this release when the supervisor registers it,
kept in the server's memory only, and confined to this protocol: leasing, completing,
failing and throwing on jobs, and polling and answering queries. It serves only the job
types the manifest declares in `spec.jobTypes`, and only as `ATLAS_WORKER_ID`, on a lease
the instance holds. Anything else is refused with `403` and the reason: a poll for another
job type, a report on a job of another type, a report or poll naming another worker id or
none, and every other route. It stays the same across restarts of the instance, is
refused (`401`) once the release is removed, and is replaced by a new one when the server
restarts. It is never the server's internal token nor an operator's `ATLAS_TOKEN`, and an
installed instance is never brokered a delegated credential
([ADR-draft-installed-worker-instances-hold-their-own-credential](../../../adr/draft-installed-worker-instances-hold-their-own-credential.md)).
Without authentication on the server there is no token and nothing to confine.

`workersdk.LaunchOptions(os.Getenv)` reads these, and `workersdk.LaunchConfig(os.Getenv)` reads the configured Workers. The instance stops when the server stops
or when the release is removed. The smallest example is
[`examples/workertypes/echo`](../../../../examples/workertypes/echo/main.go).

## What a Worker Instance must not do

- Perform a side effect before it holds the job's lease.
- Report a job it no longer holds the lease for, or report one job twice.
- Put credential values into variables, messages or query answers.
- Exit because a poll failed.
