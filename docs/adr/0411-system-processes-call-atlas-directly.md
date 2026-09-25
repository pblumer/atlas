# ADR-0411: The shipped system processes call Atlas as REST connector tasks, told their own address

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-22
- **Deciders:** Atlas maintainers

## Context and problem statement

The fulfilment orchestration and the three approval models do all their work by
calling Atlas's own HTTP API: ask which positions may start, start them, report a
decision, hand an approval on. For four releases those calls were authored as
plain service tasks:

```xml
<zeebe:taskDefinition type="rest" />
<zeebe:taskHeaders>
  <zeebe:header key="connector" value="atlas" />
  <zeebe:header key="method" value="GET" />
</zeebe:taskHeaders>
<zeebe:ioMapping>
  <zeebe:input source="=&quot;/api/v1/orders/&quot; + orderId + &quot;/next&quot;" target="path" />
</zeebe:ioMapping>
```

Three facts make that unrunnable, and none of them is visible from the model:

1. `"rest"` is not a reserved job type. `reservedJobTypes` in `compiler/builder.go`
   names the REST one `io.atlas.http.rest`; `"rest"` therefore interns as an
   ordinary custom type, and no shipped worker leases it. The `rest` kind's worker
   registers a handler for `compiler.RestJobType`, which is the reserved name.
2. **A leased job carries no task headers.** `worker.Job` carries `Variables` and,
   for a typed connector task, a resolved `Connector` payload. The `connector` and
   `method` headers reach nobody. The `ioMapping` inputs do arrive, as variables,
   so a hand-written worker would receive `path` and `body` and neither the target
   nor the verb.
3. The documented remedy does not exist. The models and
   `examples/produkt-erfassung/README.md` instruct the operator to configure "a
   worker of type `rest` named `atlas`" under Console → Workers;
   `POST /api/v1/configured-workers` refuses `rest`, which is not among the
   managed Worker Types.

What that produces is the worst available failure: the tokens **park**. A parked
job is work waiting, not work failed — no retry is spent, no incident is raised,
nothing turns red. On the installation that reported it, fourteen orders sat at
"Wartet" for weeks with twelve jobs parked, zero incidents and zero open tasks,
and the catalogue's own approver report was clean.

The question is not whether to fix it but which side is wrong: the four models, or
a product that owes a `rest` Worker Type it does not offer.

## Decision drivers

- A shipped process must be runnable on a stock installation, or it is a defect
  shipped as a feature.
- A model cannot carry an installation's address.
- Whatever the models need must fail loudly when it is missing, never silently —
  that is the property whose absence caused this.

## Considered options

1. **Add a `rest` Worker Type.** Makes the documentation true, and keeps a task
   shape whose instructions still never reach the worker. It repairs the sentence,
   not the mechanism.
2. **Leave the job type and document a worker the operator writes.** The headers
   still do not travel, so the operator's worker would have to infer the verb from
   the path. A contract nobody can implement correctly is not a contract.
3. **Author the calls as REST connector tasks and tell the models where Atlas is.**

## Decision outcome

Chosen option: **3**.

```xml
<atlas:restConnector method="GET"
  url="=atlasApiBase + &quot;/api/v1/orders/&quot; + orderId + &quot;/next&quot;"
  resultVariable="bereit"
  authType="bearer" authSecret="atlas" />
```

- **The reserved job type.** A connector task compiles to `io.atlas.http.rest`,
  which the engine serves itself, and which the shipped `rest` worker serves where
  an operator has offloaded the kind. Nothing to configure either way.
- **`atlasApiBase` is a start variable**, set by the server from the same address
  it hands its supervised workers (`cmd/atlas`: `internalURL`). The fulfilment
  orchestration passes it on to every process it starts, so an approval model and
  a provisioning model have it too.
- **Deliberately not `portalBaseUrl`.** That is the operator's configured external
  origin *or empty* (ADR-0200), and a request built on an empty base is this
  same silent failure in a new place. The two values answer different questions:
  where a person clicks a link, and where this process reaches this server.
- **The body is the input mappings.** A REST connector task sends its
  activity-local scope as the JSON body ([ADR-0174](0174-connector-payloads-are-the-input-mapping.md)), so
  `target="body"` carrying `{processId, variables}` becomes two mappings,
  `target="processId"` and `target="variables"`. One level less, not one more.
- **`authSecret="atlas"`** names a secret, never a value ([ADR-0041](0041-connector-management-and-secret-store.md)). The
  operator mints an API token with the `operator` role and sets
  `ATLAS_CONNECTOR_ATLAS_TOKEN`. That obligation is not new — the models always
  documented it — but the mechanism behind it exists, and the one they named did
  not.

### The diagnosis that has to come with it

A worker's registry entry counted the jobs it had **leased**. A worker that is
connected and polling a queue with no work in it therefore looked exactly like a
worker that is not there, which made "is anybody serving this job type?"
unanswerable for every quiet queue. Each poll now records the type it asked for
(`workerStat.Serves`), whether or not a job came back. Without it the fulfilment
report added alongside this record would call a healthy idle installation broken.

## Consequences

- The fulfilment path runs on a stock installation with one operator step that
  exists, and that step fails loudly: a missing or wrong token is an HTTP 401 on
  the call, which fails the job and raises an incident.
- `examples/produkt-erfassung` still carries the old shape. It is started by hand
  rather than by the portal, so it has no `atlasApiBase` and needs its own answer
  for where Atlas is. Its README now says so instead of instructing a setup step
  that cannot be carried out.
- The Workers view gains `serves` beside `types`: what a worker asks for, beside
  what it has been given.
