# ADR-0106: A BMC Remedy connector — server-registered ITSM entry creation

- **Status:** Accepted (amended 2026-08-26 — the connector's work moved onto a worker; see the amendment note below)
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

> **Amendment (2026-08-26): the entry is created on a worker.** The original decision
> shipped the Remedy worker **in the engine's own process** — an in-process job handler
> holding a registry the server built from the connector store and the vault. That was
> the right first slice, and it is the shape [ADR-0164](0164-no-in-process-service-tasks.md)
> and [ADR-0168](0168-connector-work-on-a-worker.md) then moved every side-effecting
> service task away from; the 2026-08-25 implementation audit named `remedy` among the
> offloadable kinds with no worker implementation behind them.
>
> It now has one, in mail's exact shape ([ADR-0079](0079-outbound-mail-connector.md)/ADR-0168),
> because Remedy is mail's situation: one operator-managed instance behind a name, with
> its address and service account in the connector store rather than in the model.
>
> - **The split.** `remedy.Resolve` (engine) turns the compiled task into a `remedy.Job`
>   of plain values — the connector *name*, the form, the evaluated field values, the job
>   key as `X-Request-ID`, the result variable. `remedy.Run` (either process) creates the
>   entry through whichever registry its caller holds. Both paths call the same pair, so
>   in-engine and on-worker cannot drift. A `remedy.Job` has nowhere to put a base URL or
>   a password, which is the property that makes the offload safe rather than the code
>   that fills it in.
> - **The worker.** `atlas worker --connector remedy` reads `ATLAS_REMEDY_CONNECTORS` and,
>   per name, `ATLAS_REMEDY_<NAME>_ENDPOINT`, `_USERNAME` and `_PASSWORD` — from the
>   environment, never argv. A worker with no instance configured parks the kind instead
>   of leasing its jobs and failing them, as mail and Entra do.
> - **The handover.** A *supervised* worker is provisioned from the connector store and
>   the vault at spawn (`remedyWorkerEnv`, [ADR-0157](0157-worker-processes-supervision-and-console.md)),
>   so an operator who adds a Helix instance in the Console gets a worker that can file
>   against it without setting anything by hand. A connector with no endpoint, or whose
>   bundle is missing or half-filled, is left out rather than handed over incomplete.
> - **What did not change.** The model is byte-identical, the in-process handler remains
>   and is still what a default server runs, and the default offload set is untouched:
>   an operator moves the kind with `--offload-connectors remedy` (a worker they run) or
>   `--supervise-connector remedy` (one Atlas starts). Whether Remedy should be offloaded
>   *by default* is a separate decision, as it was for AD ([ADR-0182](0182-ad-default-offload.md)).
>
> The payoff is the one the ADR's own trade-off list could not offer: an AR System
> reachable only from inside a customer's network can now be served by a worker sitting
> there, and the service account can live only in that worker.

## Context and problem statement

Atlas processes routinely need to raise a ticket in an ITSM system — most often
**BMC Remedy** (now BMC Helix ITSM, built on the AR System). A user asked for a
first-class **BMC Remedy connector**: a modeled step that creates an entry (e.g. an
incident on `HPD:IncidentInterface_Create`) in a Remedy form, so a process can open a
ticket at a specific point without a hand-rolled REST task and without leaking Remedy
credentials into the model.

Atlas already runs several connector *kinds* through the job path, discriminated by a
reserved job type a `TypeConnectorTask` carries (ADR-0036/0067): clio, HTTP REST, and
mail. The service-task connector **catalog** (ADR-0067) was built precisely so that
"the next connector kind" is a data entry plus a worker, not a bespoke subsystem. A
Remedy connector is the next kind.

Two shaping questions:

1. **Where do the endpoint and credentials live?** The REST connector authors its full
   URL in the model (ADR-0067), because its value is ad-hoc per-URL calls. Remedy is
   the opposite: one operator-managed instance, an AR System base URL, and a service
   account whose password must never appear in a shared, versioned, rendered BPMN file.
   This is the clio/mail situation, not the REST one.
2. **What is authored in the model?** The *form* the entry lands in and the entry's
   *field values* — the modeled, per-task data — exactly as a mail task authors its
   recipients/subject/body while the provider and secret stay server-side (ADR-0079).

## Decision drivers

- **Reuse the proven seam.** The connector-via-job pattern (ADR-0036/0007) gives crash
  recovery, non-blocking execution, and dependency isolation for free: the outbound
  call runs only in the worker, post-fsync, off the single writer, never in
  `applyToState` (I1/I2/I4, ADR-0005/0007). No engine change.
- **Extensibility is the point (ADR-0067).** Adding Remedy should be one catalog entry
  + one moddle type + one compiler branch + one worker + one server registry — additive
  at every layer, colliding with nothing.
- **Secrets belong to ops, not to models (ADR-0036/0041).** A Remedy base URL is fine in
  a record; the service-account credentials are a vault-held reference, resolved at call
  time, never authored in a model.

## Considered options

**A. A dedicated Remedy connector kind, server-registered (chosen).** A new
`remedy` kind: a `<serviceTask>` bearing an `<atlas:remedyConnector connector form
resultVariable>` extension with `<atlas:remedyField>` children carries the reserved
`io.atlas.remedy.entry` job type; an in-process worker resolves the named connector's
AR System client and creates the entry. The base URL and the `{username,password}`
credential bundle live in the managed connector store + vault, like mail (ADR-0093).

**B. Just use the generic REST connector.** Rejected as the primary answer: it works,
but forces every author to hand-model the AR System's two-step auth (JWT login) and the
`/api/arsys/v1/entry/{form}` shape, and — fatally — has nowhere to put the Remedy
password except a model-authored secret *reference* on an HTTP header, which is clumsy
and easy to get wrong. A purpose-built kind hides the AR System protocol and keeps the
credential fully server-side. (REST remains available for the long tail of AR System
operations the connector doesn't yet cover.)

**C. Inline call from a behavior.** Rejected on sight (as in ADR-0036): network I/O on
the single writer, tempts a call inside `applyToState`; violates I1/I4/ADR-0007.

## Decision outcome

Chosen: **option A**, the dedicated server-registered `remedy` connector kind, first
operation **create entry**.

- **Model-authored:** the Remedy **form** and the entry's **field values** (each literal
  or a FEEL expression over the instance's variables — the fx toggle, ADR-0067), plus an
  optional **result variable** the created entry's id is written back into (the
  output-mapping path, ADR-0014/0066). Compiled into the shared `ConnectorTaskDetail`
  (new `RemedyForm`/`RemedyFields`, reusing `Connector`/`ResultVar`) as deploy-time data
  (I5).
- **Server-managed:** the AR System **base URL** and the **credential bundle**
  (`{username,password}`) — a managed connector record of kind `remedy` whose
  `credentialsRef` resolves to a vault secret (ADR-0041/0069), never stored inline and
  never in a model (I6). Rebuilt into the live `remedy.Registry` on every connector
  change, like clio/mail.
- **Worker:** the `remedy` package (`Client`/`Registry`/`HTTPClient`/`Handler`) speaks
  the AR System REST API — `POST /api/jwt/login` (form-encoded) for a JWT, `POST
  /api/arsys/v1/entry/{form}` with an `AR-JWT` bearer and a `{"values": …}` body, the
  entry id read from the response `Location` header, and a best-effort `POST
  /api/jwt/logout`. Registered under the reserved `RemedyJobTypeIndex` via
  `HandleWithOutput`, so one worker serves every deployed process.

Delivery keeps the ADR-0036 job-path guarantees: at-least-once, recovery inherited from
the job protocol, no engine change. The AR System has no idempotency-key header, so the
job key rides along as an `X-Request-ID` for a downstream de-duplicator; a Remedy-side
dedup field is a follow-up.

### Consequences

- **Positive:** a process opens a Remedy incident/entry at a modeled point, authored
  end-to-end in the Modeler (a searchable "BMC Remedy Connector" catalog entry) and
  executed off the processor loop with recovery; the base URL and credentials are
  centrally managed and models stay portable and secret-free; adding the *next* Remedy
  operation (update, query) is additive on the same framework, exactly as clio grew
  write → query → read.
- **Negative / trade-offs accepted:** at-least-once over an API with no idempotency key
  can create a duplicate entry on a replay (mitigated by `X-Request-ID`; a Remedy dedup
  field is future work); a partial operation set to start (create-entry now); per-call
  JWT login (no token caching yet); field values are sent as strings (typed AR System
  values are a follow-up); a process reaching a Remedy task parks until the worker and
  Remedy are reachable — the same failure mode as any connector task.
- **Follow-ups / risks to watch:** update/query/delete operations; JWT caching and a
  configurable timeout; typed field values and attachments; mapping AR System error
  bodies to incidents; a Remedy-side idempotency/dedup field.

## Links

- realizes the intent of ADR-0067 (service-task connector catalog) for a new kind, and
  mirrors ADR-0079 (mail connector) for the server-registered, credential-managed shape
- reuses ADR-0036 (connector-via-job) / ADR-0007 (job worker protocol) wholesale — no
  engine change; honors I1/I2/I4/I5/I6 and ADR-0005
- resolves credentials through ADR-0041 (connector management + secret store) / ADR-0069
  (encrypted vault); output mapping mirrors ADR-0014/0066
