# ADR-0067: A service-task connector catalog, and REST with a model-authored endpoint

- **Status:** Accepted
- **Date:** 2026-07-27
- **Deciders:** Atlas engine team

> **Update (2026-08-21).** The REST request body is no longer always the whole
> variable scope: a task's `zeebe:ioMapping` inputs, when it has any, are the body,
> and the url/header/query FEEL fields resolve up the task's scope chain so they see
> those mapped locals (ADR-0068). See
> [ADR-0174](0174-connector-payloads-are-the-input-mapping.md).

## Context and problem statement

Atlas already runs several "kinds" of service task through the job path: a plain
job-worker task (`zeebe:taskDefinition`), a clio event-store connector
(`atlas:clioConnector`, ADR-0036) and an HTTP-REST connector
(`atlas:restConnector`, ADR-0036). Each rides the same seam — a
`TypeConnectorTask` that creates a job carrying a reserved job type, picked up by
an in-process worker off the hot path (ADR-0007) — so the *engine* side already
generalises well.

Two gaps remain, both surfaced by users trying to actually use the REST connector
after it merged:

1. **No authoring surface.** The hand-written modeler panel (ADR-0012/0025) only
   knows the plain job-worker service task; there is no way to pick "this service
   task is a REST connector" and fill in its fields, and a hand-edited
   `<atlas:restConnector>` extension is dropped on save because no moddle type
   preserves it. The reference tooling solves this with **element templates**
   (ADR-0027, still *Proposed*): a searchable picker ("REST Outbound Connector —
   Invoke REST API") swaps a bare task for a pre-configured one and renders only
   that kind's fields. We want that UX, and we want adding the *next* connector
   kind to be cheap — a data entry, not a bespoke panel.

2. **REST endpoint shape.** ADR-0036 mandated that a connector reference an
   endpoint **by registry name only**, never a URL, so secrets and endpoints stay
   with ops. In practice the REST connector's whole value is per-task URLs,
   methods, headers and query parameters authored in the model (as every
   BPMN tool does). Forcing every distinct URL to be pre-registered at the server
   makes the connector unusable for ad-hoc integration. ADR-0036's rule was
   written for clio (one instance, one endpoint, credentials that must not leak);
   it does not fit REST.

## Decision drivers

- **Extensibility is the point.** The user asked explicitly for "a concept for
  further service-task types", not a REST one-off. Adding a kind should be one
  catalog entry (UI) + one moddle type + one compiler branch + one worker.
- **Buildless (ADR-0012).** The vendored bpmn-js is a single bundle without the
  bpmn.io `element-templates` module, so the native template-chooser popup is not
  available without re-bundling. We approximate it in the hand-written panel.
- **Honor the invariants.** Whatever the model carries, the outbound call still
  runs only in the worker, post-fsync, off the single writer, never in
  `applyToState` (I1/I2/I4, ADR-0005/0007). Model-authored config is deploy-time
  data interned into the compiled process (I5).
- **Secrets never live in a model.** A BPMN file is shared, versioned and
  rendered; a URL is fine, a credential is not.

## Considered options

**For the authoring concept:**

1. **A data-driven service-task-kind catalog** in the modeler: an array of
   entries `{id, name, description, icon, extension, fields[]}`. The "Implement"
   panel renders a searchable picker over it and renders the selected kind's
   fields generically; applying a kind upserts its extension and removes the
   others. The compiler keeps discriminating by extension element / job type.
2. **Re-bundle bpmn-js with the bpmn.io element-templates module** to get the
   exact reference popup. Rejected for now: breaks buildless (ADR-0012), large
   and risky, and buys UX polish over the substance (the catalog) we need first.
3. **Keep hand-authoring** every extension in raw XML. Rejected: the reason the
   feature looked "missing".

**For the REST endpoint:**

A. **Registry name only (ADR-0036 as written).** B. **Model-authored URL, method,
headers, query — secrets excluded.** C. **Hybrid: URL in the model, plus an
optional named credential reference resolved at the server for auth.**

## Decision outcome

Chosen: **the catalog (option 1) and, for REST, a model-authored endpoint
(option B now, extensible to C).**

- The **service-task connector catalog** is the concept for "further service-task
  types". It lives as data in the modeler and is mirrored by the compiler's
  extension/job-type discrimination and one worker per kind. The plain job-worker
  task, clio and REST are its first three entries; the next kind (an email sender,
  a queue publisher, a second event store) is additive at every layer.
- For **REST**, the model carries the **method** and the **full URL** (and, as they
  land, headers and query parameters), and names a **result variable** the JSON
  response is written into on completion — the same output-mapping path a business
  rule task uses for its decision result (ADR-0014/0066). This **revises ADR-0036
  for the REST connector only**: clio stays registry-only (its endpoint and
  credentials genuinely belong to ops). **Credentials are still never authored in
  the model:** authentication is deferred to a follow-up as an auth *type* plus a
  reference to a server-registered credential, so a token never appears in BPMN.

Delivery keeps the ADR-0036 job-path guarantees: at-least-once with the job key as
a deterministic idempotency key (I6), recovery inherited from the job protocol,
no engine change beyond the connector detail it already carries.

### Consequences

- **Positive:** a REST task is authored end-to-end in the modeler and actually
  executes (the worker is wired into the run loop); adding another connector kind
  is a small, well-shaped change; models stay portable (URLs are fine, secrets are
  not).
- **Negative / trade-offs accepted:** the catalog approximates, rather than is,
  the bpmn.io element-templates popup (buildless trade-off); REST endpoints now
  live in models, so a URL change is a redeploy (acceptable and expected for a
  modeled integration); a partial Camunda property set to start (method/URL/result
  now; headers, query, auth, timeout, follow-redirects, store-response and error
  handling are follow-ups on the same framework).
- **Follow-ups / risks to watch:** headers/query maps; FEEL expressions in fields;
  the auth type + server-side credential reference; connection timeout, redirect
  policy, response storage and error/incident mapping; revisit re-bundling bpmn-js
  for the native chooser once a build step is justified; fold clio's authoring into
  the same catalog.

## Links

- revises ADR-0036 (clio connector / connector-via-job) for the REST endpoint shape
- realizes the intent of ADR-0027 (element templates) within the buildless panel (ADR-0012)
- honors I1/I2/I4/I5/I6 and ADR-0005/0007; output mapping mirrors ADR-0014/0066
