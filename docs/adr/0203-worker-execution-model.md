# ADR-0203: Worker execution model and integration terminology

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas currently uses several overlapping terms for the same execution seam. The Console manages "connectors" and "connector kinds"; packages under `connector/` implement in-process service-task handlers; some integrations are `workerOnly`; and the `job` package exposes the worker subscription protocol. A configured Jira account, a Jira implementation, and the runtime code consuming Jira jobs can therefore all be described as a connector even though they are different things.

This becomes misleading as Atlas grows. A BPMN task represents work to be performed. For job-backed activities, the engine's responsibility is to durably create that work and expose it through the job protocol. The engine does not need to know whether the consumer is an Atlas-shipped implementation, an external process, one of many horizontally scaled replicas, or software acting on behalf of a human or machine identity.

At the same time, not every task should become a job. FEEL script tasks are intentionally evaluated in-engine: their expression is compiled at deployment, their deterministic result is captured in durable events, and replay never re-evaluates it. Other strictly internal Atlas operations may use the same pattern when they are deterministic engine semantics rather than integration side effects.

The current terminology also conflates an installable capability with an operator's configured target. For example:

- "Jira" is an implementation/capability Atlas can make available;
- "Jira Production as Patrick" is a configured endpoint plus identity/credentials;
- several runtime consumers may execute work using that configuration;
- the BPMN model should reference the logical configured worker, not carry credentials or runtime topology.

This ADR defines a vocabulary and execution model that separates those concerns without changing Atlas's durable job semantics.

## Decision drivers

- Keep the workflow engine agnostic about who or what performs external work.
- Make the product vocabulary understandable to operators and BPMN authors.
- Separate installable capability, configured integration identity, queued work, and runtime scaling.
- Preserve `durable before visible`: external work is available only after WAL append/fsync and state commit.
- Preserve the single-writer boundary and keep network calls and other side effects outside `applyToState`.
- Preserve compile-don't-interpret: task routing and worker type identifiers are resolved at deployment where possible.
- Support both Atlas-shipped and separately deployed worker implementations without loading arbitrary plugin code into the engine process.
- Support multiple configurations of one capability, e.g. several Jira tenants/accounts or PostgreSQL databases.
- Support horizontal scaling of consumers independently from process instances.
- Keep secrets out of BPMN models, events, and the WAL.

## Considered options

1. Keep the existing Connector / Connector Kind / Worker terminology.
2. Rename configured connectors but keep connector implementations as the primary abstraction.
3. Make **Task -> Job -> Worker** the execution model and distinguish **Worker Type**, **Worker**, and **Worker Instance**.
4. Treat every BPMN task, including deterministic FEEL script tasks, as an external worker job.

## Decision outcome

Chosen option: **3 — Task -> Job -> Worker, with Worker Type / Worker / Worker Instance as distinct concepts.**

### Canonical vocabulary

**Task**

A BPMN activity describing work in the process model. Service tasks, user tasks, business rule tasks, script tasks and Atlas extensions remain BPMN concepts. A task is not itself a worker and does not imply a deployment topology.

**Job (work item)**

A durable executable work item created by Atlas for a job-backed task. The job contains the stable routing/type information and task inputs required by the existing job protocol. Jobs are the engine/worker boundary. Existing lease, retry, incident, completion and at-least-once semantics continue to apply.

The UI may use the friendlier term **Work item** where appropriate, but public protocol/API names may continue to use `job` because that is the established technical contract.

**Worker Type**

An available implementation/capability that knows how to execute one or more job types. Examples are Jira, Mail, PostgreSQL, Microsoft Entra ID, REST, or a custom company worker.

A Worker Type defines:

- stable type identifier and version;
- supported job types/operations;
- configuration schema;
- credential requirements;
- optional Modeler element templates/properties;
- runtime mode (`embedded` or `external`);
- compatibility information and health capabilities.

Worker Types form the installable **Worker Catalog**. Atlas may ship a curated built-in set. Additional types may be installed/enabled as separately versioned worker packages. Installing a Worker Type makes a capability available; it does not create credentials or a connection to a target system.

Atlas does **not** become an arbitrary in-process plugin host. Third-party/vendor worker code defaults to an external process/container using the public job/API protocol. An Atlas distribution may compile trusted embedded Worker Types into the binary, but enablement is still represented as catalog capability rather than conflated with a configured target.

**Worker**

An operator-managed configuration of a Worker Type. A Worker identifies *where and under which identity/configuration work is performed*.

Examples:

- `jira-production-patrick` -> Worker Type `jira`, production Jira endpoint, Patrick's credential reference;
- `jira-service-account` -> Worker Type `jira`, same endpoint, machine credential reference;
- `postgres-orders` -> Worker Type `postgresql`, Orders database connection reference;
- `mail-customer-service` -> Worker Type `mail`, configured mail provider/sender.

A Worker persists configuration and secret **references**, never secret values in BPMN or events. This concept supersedes the operator-facing term **connector instance** from ADR-0041.

A BPMN job-backed task references a Worker (or, for deliberately generic external jobs, a stable worker/job type according to the public protocol). The model does not select a concrete runtime replica.

**Worker Instance**

A live runtime consumer executing jobs for a Worker. Worker Instances are ephemeral runtime topology, not design-time configuration. One Worker may have zero, one, or many Worker Instances.

Worker Instances may be:

- an embedded Atlas consumer;
- a supervised local worker process;
- an independently deployed container/process;
- multiple replicas consuming the same logical work stream.

Scaling creates additional Worker Instances, not additional Workers. Credentials/configuration belong to the Worker and are supplied to its runtime instances through the chosen secret/configuration mechanism.

### Execution model

For job-backed work the conceptual flow is:

```text
BPMN Task
  -> durable Job / Work Item
  -> WAL + fsync
  -> state commit
  -> job becomes available
  -> Worker Instance claims/executes it
  -> completion/failure command
  -> durable completion/failure events
  -> process continues
```

The engine owns task lifecycle and durable jobs. The Worker owns the external side effect. A Worker Instance must never perform the side effect before the corresponding job is durably visible under the existing processor ordering.

A queue is therefore a **logical work stream**, not a new source of truth. Atlas must not introduce a second independently durable queue beside the job store merely to implement this vocabulary. Partition/job indexes and the worker protocol remain authoritative. Future brokers may transport work across deployment boundaries only if their delivery/acknowledgement semantics preserve the same durability contract.

### In-engine execution is an explicit exception

Not every task creates a worker job.

A task may execute **in-engine** only when execution is part of deterministic workflow semantics and does not require an external side effect. FEEL script tasks are the canonical example: FEEL is compiled at deployment, evaluated as part of live processing, and the result is frozen into durable events so recovery re-applies the fact instead of re-running the expression.

Atlas-internal deterministic activities may use the same category when justified. They are called **in-engine task handlers**, not Workers, because no independently scalable consumer or external work boundary exists.

Network access, filesystem integration, vendor SDK calls, user interaction, database access, mail delivery, current-time-dependent external behavior, and similar side effects must not use this exception. They are job-backed and execute after durability through a Worker.

### User tasks fit the same work model but keep their BPMN identity

A User Task also creates work that waits for an actor, but Atlas should not rename the human to a Worker in the product UI. Tasks/Operations expose the human work item, assignment and completion lifecycle. Conceptually it uses the same durable-work principle: the engine records work and later receives a completion command; the human-facing task application is the execution surface.

This keeps BPMN language natural while preserving one architectural rule: process execution parks on durable work and resumes only from an explicit durable completion.

### Product and UI structure

The current **Organization -> Connectors** surface is replaced over time by **Organization -> Workers** with two levels:

```text
Workers
├── Worker Catalog
│   ├── Jira
│   ├── Mail
│   ├── PostgreSQL
│   └── ...
└── Configured Workers
    ├── jira-production-patrick
    ├── jira-service-account
    ├── postgres-orders
    └── mail-customer-service
```

A Worker Type page shows installation/availability, version, execution mode, supported operations and configuration schema. A Worker page shows its type, target/configuration, credential references, enabled state and aggregate runtime health. Runtime/Operations may additionally show connected **Worker Instances** and their status/capacity.

The Modeler selects a Worker Type/operation and, where a concrete target is required, a configured Worker. It never exposes Worker Instance IDs because runtime replicas are operational topology.

### Relationship to existing connector ADRs and code

This ADR changes the conceptual model and terminology; it does not require an unsafe big-bang rename.

- ADR-0036's job-backed execution seam remains valid.
- ADR-0041's durable managed configuration and secret-reference rules remain valid, but its **connector kind** maps to **Worker Type** and its **connector instance** maps to **Worker**.
- Existing `connector/` packages are implementation packages for Worker Types and may be renamed incrementally.
- Existing `managedConnectorKind` becomes conceptually `WorkerTypeDefinition` (exact Go naming is an implementation decision).
- Existing `workerOnly` is evidence that the code already distinguishes capability/configuration from execution location; it should evolve into an explicit runtime mode rather than remain connector-specific terminology.
- Existing job type indices remain stable. A terminology migration must not renumber or reinterpret persisted identifiers.
- Public APIs may provide compatibility aliases/deprecation windows where renaming `/connectors` to `/workers` would otherwise break clients.

### Worker Type packaging and installation

The Worker Catalog needs a manifest contract before arbitrary downloadable workers are supported. A future Worker Type package should be able to declare metadata, configuration schema, job types/operations, element templates, minimum Atlas/protocol version, artifact integrity information and runtime launch information.

The first implementation slice should **not** dynamically load Go shared objects or untrusted code into the Atlas process. External worker packages should be processes/containers speaking the public protocol. Built-in embedded workers remain compiled with Atlas. This preserves Atlas's single-binary core and isolates vendor dependencies and failures.

Package distribution, signing/trust, registry discovery, automatic download and lifecycle supervision require a follow-up ADR before Atlas advertises an Internet-installable marketplace.

### Scaling model

Worker scaling is expressed as the number of Worker Instances serving a logical Worker/job type, not by cloning Worker configuration records.

The job protocol must remain safe under concurrent consumers: one work item is leased/claimed by one Worker Instance at a time, with retry after lease expiry/failure according to the established at-least-once semantics. Side-effecting Worker Types must therefore continue to follow the job-key/idempotency guidance of the existing worker protocol.

Autoscaling can later use backlog, oldest-job age, execution latency and configured concurrency as signals. Scaling policy is operational state and is not persisted in process events.

### Consequences

- **Positive:** one coherent mental model spans Jira, mail, databases, external workers and human work: Atlas creates durable work; an executor completes it.
- **Positive:** "installed capability" is cleanly separated from "configured endpoint/identity" and from "running replica".
- **Positive:** multiple Jira accounts, databases, mailboxes and similar targets become natural rather than overloaded connector records.
- **Positive:** horizontal scaling has an explicit object — Worker Instance — without leaking replica topology into BPMN models.
- **Positive:** the model strengthens Atlas's engine boundary: side effects remain post-fsync and outside deterministic replay.
- **Positive:** Worker Types can evolve toward a catalog/package ecosystem without making arbitrary plugins part of the engine hot path.
- **Negative / trade-offs accepted:** `connector` is already widespread in packages, APIs, ADRs and UI; migration must be staged and backwards compatible where contracts are public.
- **Negative / trade-offs accepted:** `Worker`, `Worker Type`, and `Worker Instance` are close names and require precise UI copy and documentation. The product should normally show "Worker" and hide "Worker Instance" outside Operations.
- **Negative / trade-offs accepted:** User Tasks conceptually fit the work-item model but should retain human-centric BPMN/UI terminology rather than forcing users to think of people as workers.
- **Follow-ups / risks to watch:** define Worker Type manifest/package format; define trust/signing/distribution; specify external worker authentication/registration; migrate APIs/UI and package names; expose worker-instance health/capacity; define autoscaling metrics/policy; reconcile older connector ADR wording through links rather than rewriting historical decisions.

## Pros and cons of the options

### Option 1 — keep current terminology

- Good: no migration work.
- Bad: "connector" continues to mean capability, configured endpoint and execution implementation depending on context; scaling and installation remain hard to explain.

### Option 2 — rename configured connectors only

- Good: smaller UI change.
- Bad: leaves the architectural abstraction connector-centric and does not define installable capability or runtime replicas.

### Option 3 — Task -> Job -> Worker with three worker levels (chosen)

- Good: aligns terminology with Atlas's existing job protocol and durable execution model; clean separation of design time, configuration and runtime topology.
- Bad: requires staged migration and careful compatibility handling.

### Option 4 — make every task a worker job

- Good: superficially uniform execution path.
- Bad: moves deterministic FEEL/script semantics out of the engine, adds unnecessary scheduling/persistence overhead, weakens compile-don't-interpret, and confuses workflow semantics with integration execution.

## Migration plan

1. **Vocabulary/documentation:** adopt Task, Job/Work Item, Worker Type, Worker and Worker Instance in new documentation; mark Connector terminology as legacy where it refers to the same concepts.
2. **Console information architecture:** introduce Organization -> Workers with Worker Catalog and Configured Workers, initially backed by the existing connector store and APIs.
3. **Internal type cleanup:** rename connector-management DTOs/types behind compatibility boundaries; retain stable job type indices and persisted record compatibility.
4. **Public API transition:** add Worker-oriented endpoints/resources and deprecate Connector-oriented aliases with a documented compatibility window rather than silently breaking clients.
5. **Runtime observability:** expose Worker Instance registration/health/backlog where the job protocol can support it truthfully.
6. **Packaging:** write a follow-up ADR for Worker Type manifests, distribution, integrity/signing, installation and supervision before implementing downloadable third-party packages.
7. **Package migration:** rename `connector/` implementation packages only when the public/API migration is established; avoid a repository-wide mechanical rename that obscures behavioral changes.

## Links

- builds on ADR-0007 (job worker protocol)
- reframes ADR-0036 (connector-via-job execution) without changing its durable execution semantics
- partially supersedes the terminology of ADR-0041 (connector management and secret store): connector kind -> Worker Type; connector instance -> Worker
- relates to ADR-0015 / the in-engine FEEL execution model
- relates to ADR-0027 (element templates and the no-arbitrary-plugin-host direction)
- honors invariants I2 (durable before visible), I3 (single writer), I4 (one `applyToState`), I5 (compile, don't interpret), and I6 (events are facts)
