# ADR-0041: Connector management and the secret store

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0036](0036-clio-connector.md) decided *how a connector executes*: a
connector task creates a job, an in-process worker performs the outbound call
after fsync and completes the job, and a **server-side registry** resolves a
connector **name** to a concrete endpoint and credential so models never carry a
URL or secret. That framework is implemented for two kinds today — clio
`write-events` (`io.atlas.clio.write`) and HTTP REST (`io.atlas.http.rest`) —
each shipping a `Registry`/`Client`/`Handler` trio ([ADR-0007](0007-job-worker-protocol.md),
[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) pattern).

ADR-0036 deliberately stopped at execution. It named, as consequences and
follow-ups, the things it did **not** decide: *"The registry is new server
surface (config parsing, **secret handling**, health)."* Today those are stubs:

- A connector's credential is a bare in-memory field — `clio.Connector{Endpoint,
  Token}` — with **nothing populating the registries yet** (the workers aren't
  wired into the server run loop; ADR-0036 says so explicitly).
- The only credential wiring that exists anywhere is the DMN resolver reading
  `ATLAS_DMN_RESOLVER_URL` / `ATLAS_DMN_RESOLVER_TOKEN` from the **environment**
  (`api/server.go`). There is no connector config format, no notion of multiple
  configured instances, no lifecycle, and no operator surface.

Meanwhile the product question that motivated this — *"how do we add and manage
more connectors: X, Gmail, Stripe, whatever?"* — needs an answer that reaches
past the two in-process kinds Atlas ships. Those are third-party vendor
integrations with their own SDKs, dependencies, and OAuth credentials.

This ADR decides the three things ADR-0036 left open, all downstream of one
load-bearing choice — **where connector secrets live**:

1. the **secret store**: how `credentialsRef` is sourced and resolved, and what
   (if anything) the engine persists;
2. **connector management**: turning the ad-hoc registry into durable,
   operator-managed config (named instances, enable/disable, health), surfaced on
   the Console's Organization → Connectors page;
3. **vendor connectors**: how X/Gmail-class integrations attach without turning
   Atlas into a plugin host.

## Decision drivers

- **Secrets are never events, model data, or log records (I6).** The event log is
  a replayable record of facts; a rotating credential is neither replay-safe nor
  safe to keep in an append-only, plaintext-readable log. A BPMN file is shared,
  versioned, and rendered; it must not carry a token. This is the same rule
  ADR-0036 stated for endpoints — extended here to say precisely where the secret
  *does* live.
- **Least engine custody.** The engine should hold as little credential material,
  for as short a time, as possible — ideally none it has to encrypt and manage a
  key for.
- **Reuse the existing seam, don't invent config.** There is already a working
  pattern — a named integration whose endpoint and token come from the process
  environment (the DMN resolver). Generalize it rather than introduce a bespoke
  secrets file and crypto.
- **Single-binary, not a plugin host** ([ADR-0027](0027-element-templates.md),
  ADR-0012). Adding a vendor connector must not mean loading vendor code into the
  engine's trust and failure domain.
- **Operability.** One place shows what connectors exist, which are configured,
  and which are failing; ADR-0036 already routes connector failures through the
  standard job/incident machinery.
- **Execution semantics are already settled.** At-least-once and the job-key
  idempotency rule are ADR-0036's decision and are not reopened here.

## Considered options

**A — where the secret lives / how `credentialsRef` resolves:**

1. **Inline in a server config file.** Endpoints and tokens in an `atlas.yaml`
   read at startup into the registry.
2. **Environment / mounted-secret references (chosen base).** A connector's
   config names a credential; the value is resolved at server start from the
   process environment or a mounted secrets file, exactly generalizing the
   existing `ATLAS_DMN_RESOLVER_TOKEN` precedent. The value lives only in the
   worker's in-memory `Connector` at runtime; the engine persists only the
   *reference*.
3. **Encrypted-at-rest store inside Atlas.** The Console accepts a token, the
   engine encrypts it on disk and decrypts at call time. Turnkey, but the engine
   owns a key and a crypto/rotation story.
4. **External secret manager references.** `credentialsRef` names a Vault / cloud
   secret-manager path the worker dereferences at call time.

**B — how vendor connectors (X/Gmail/Stripe) attach:**

1. **In-process, shipped in the binary** — like clio/rest.
2. **External workers on the job protocol** ([ADR-0007](0007-job-worker-protocol.md)) —
   a separate process (any language/SDK) subscribes to the connector's job type,
   holds its own credentials, and completes jobs over the stream.

**C — connector management surface:**

1. **Startup config only** — connectors exist only as parsed config; no runtime
   CRUD, no UI.
2. **Durable managed instances + Console CRUD** — connector *instances* persist
   in a sidecar store; the Organization page lists, configures, enables/disables,
   and shows health.

## Decision outcome

**A2 (environment/mounted-secret references) as the base secret model; B1+B2
(in-process for the curated set, external workers for vendor connectors); C2
(durable managed instances with a Console surface).** A3 is accepted as an
explicit *optional* follow-up, not part of the base contract; A1 and A4 are
supported as additional resolver backends behind the same reference indirection.

Concretely:

### The secret model — the engine stores a reference, resolves a value, keeps nothing

A connector instance persists `{name, kind, endpointRef, credentialsRef, config,
enabled}` — **no secret material**. `credentialsRef` is a name (e.g.
`gmail_ops`). At server start (and on reconfiguration) a **resolver** turns that
reference into the value the worker's `Connector` needs, from a pluggable
backend:

- **env / mounted file (default):** `credentialsRef: gmail_ops` →
  `ATLAS_CONNECTOR_GMAIL_OPS_TOKEN` (or a key in a mounted secrets file). This is
  the existing DMN-resolver pattern, generalized to arbitrary connectors.
- **external secret manager (A4):** the reference is a Vault/cloud path resolved
  at call time.
- **encrypted in-engine store (A3, optional follow-up):** for single-node
  convenience, the Console writes a token, the engine encrypts it at rest and
  resolves it here. This is the *only* variant where Atlas custodies a secret,
  which is why it is opt-in and deferred to its own ADR (key management,
  rotation).

In every variant the invariant holds: **the model and the event log carry only
the reference; the secret value exists solely in the worker's memory at call
time and is never written to the WAL, a variable, or the diagram** (I6, and I2 —
resolution is a post-fsync side-effect concern, never `applyToState` / I4). The
`clio.Connector{Endpoint, Token}` field becomes the *output* of the resolver, not
a config value typed by hand.

### Vendor connectors — external workers, not in-engine plugins

The two shipped kinds (clio, REST) are generic enough to live in-process (**B1**);
between them, an HTTP REST connector already covers a large share of "call some
API" needs. Vendor-specific connectors (X, Gmail, Stripe) attach as **external
workers (B2)**: a connector task carries a reserved job type, and a separate
process — using the vendor SDK, holding the vendor credential in *its* own
environment — subscribes over the job protocol ([ADR-0007](0007-job-worker-protocol.md))
and completes the jobs. Atlas **manages** such a connector (its registry entry,
instances, health) but never runs its code or holds its secret, preserving the
single-binary, no-plugin-host stance ([ADR-0027](0027-element-templates.md)).
The design-time half is an element template ([ADR-0027](0027-element-templates.md))
that fixes the job type and exposes typed inputs plus the `credentialsRef`.

### Management — durable instances and a Console surface

The registry graduates from "populated once at startup" to **durable managed
config**: connector instances live in an on-disk sidecar store
([ADR-0019](0019-durable-deployments.md)), not the event log. The Console's
Organization → Connectors page (already listing platform connectors) gains an
**Integrations** section: a catalog of connector *kinds* and, per kind, the
configured *instances* — CRUD over `{name, endpointRef, credentialsRef, config,
enabled}`, secret **references** only. Health reuses ADR-0036's routing of
connector failures through job/incident signals (worker subscribed?, last
success, failing-job count, reference resolvable?), optionally plus a cheap
reachability probe.

### Consequences

- **Positive:** builds directly on ADR-0036 with no change to how a connector
  executes. The engine holds no credential it must encrypt in the base build, so
  the log stays replay-safe and secret-free (I6) and there is no new crypto/key
  surface. Vendor connectors attach as external workers, keeping their code,
  dependencies, and secrets out of the binary. One reference indirection spans
  env, external secret managers, and (later) an encrypted store, so deployments
  choose their posture without touching models. Operators get durable, editable
  connector instances and health in the Console.
- **Negative / trade-offs accepted:** in the base build an operator provisions
  secrets out-of-band (env / mounted file / secret manager) rather than typing a
  token into the Console — less turnkey until the optional A3 store lands. Vendor
  integrations require running an external worker process (an added moving part,
  and the token parks if that worker or the vendor API is down — the same failure
  mode as any service task). The managed registry is genuinely new server surface
  (sidecar persistence, CRUD handlers, resolver plumbing, health).
- **Follow-ups / risks to watch:** the optional encrypted in-engine secret store
  (A3) as its own ADR — key source, rotation, single-node scope; wiring the clio
  and REST workers into the server run loop (still open from ADR-0036, shared with
  the DMN worker); the external-worker registration/auth path for vendor
  connectors; OAuth **refresh** for token connectors (the resolver returns a live
  token, but who refreshes it?); connector-kind versioning alongside template
  versioning; per-connector retry/incident policy for a persistently unreachable
  endpoint (extends ADR-0036).

## Pros and cons of the options

### A2 — env / mounted-secret references (chosen base)
- Good: generalizes a pattern already in the tree; engine custodies no secret at
  rest; standard in container/orchestrator deployments; trivial to reason about
  against I6.
- Bad: not configurable from the UI (provisioned out-of-band); rotation is the
  platform's job, not Atlas's.

### A1 — inline config file
- Good: simplest to populate the registry.
- Bad: plaintext secrets in a config file checked/backed up alongside app config;
  poor rotation story; still not UI-manageable.

### A3 — encrypted in-engine store
- Good: fully turnkey — configure everything, secrets included, from the Console.
- Bad: engine owns a key and a crypto/rotation story; larger custody and attack
  surface; heavier than the first slice needs. Hence deferred/optional.

### A4 — external secret manager references
- Good: strongest secret hygiene for larger deployments; centralized rotation.
- Bad: an operational dependency (Vault/cloud) not every single-binary user wants;
  fits cleanly as an *additional* backend behind the same reference, not the base.

### B1 in-process vs B2 external workers
- B1 good for a small curated, generic set (HTTP/SMTP-class); bad if it means
  pulling every vendor SDK into the engine.
- B2 good for vendor isolation (code, deps, secrets out of the binary) and the
  no-plugin-host stance; bad in that it adds a process to run and its own
  auth/registration path.

### C2 durable managed instances vs C1 startup-only
- C2 good: real operability — multiple instances, enable/disable, health, UI; bad:
  new server surface.
- C1 good: nothing to build; bad: no instances, no lifecycle, no operator view —
  unmanageable past a couple of connectors.

## Links

- builds on [ADR-0036](0036-clio-connector.md) (connector-via-job + server-side
  registry) — this ADR decides the secret handling, management, and vendor
  extension that ADR-0036 named as follow-ups
- reuses [ADR-0007](0007-job-worker-protocol.md) (job worker protocol) for
  external vendor workers and [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
  (the in-process integration pattern)
- authoring half is [ADR-0027](0027-element-templates.md) (element templates)
- managed instances persist via [ADR-0019](0019-durable-deployments.md) (on-disk
  sidecar store)
- honors invariants I2 (durable before visible), I4 (one `applyToState`), and
  I6 (events are facts) — [`docs/architecture/invariants.md`](../architecture/invariants.md)
- surfaced by the Console Organization → Connectors page
