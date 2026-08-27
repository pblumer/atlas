# Worker execution model migration

This document turns [ADR-0203](../adr/0203-worker-execution-model.md) into an
implementation sequence. It is deliberately a migration plan, not a second source
of architectural truth: if this document and the ADR disagree, the ADR wins.

## Roadmap placement

The migration belongs primarily to **Milestone S — Single-binary server & web UI**
because it changes the Console, Modeler, worker runtime, HTTP API and operator
experience. Packaging and third-party Worker Types extend into **Milestone 6 —
Ecosystem**.

The work builds on the already delivered worker protocol and execution decisions:

- [ADR-0157](../adr/0157-worker-processes-supervision-and-console.md) — worker
  processes, worker registry, HTTP pull, supervision and Workers observability;
- [ADR-0164](../adr/0164-no-in-process-service-tasks.md) — side-effecting service
  tasks belong on workers;
- [ADR-0168](../adr/0168-connector-work-on-a-worker.md) — resolved task detail
  travels with the job and integration credentials live where they are used;
- [ADR-0203](../adr/0203-worker-execution-model.md) — canonical Task / Job /
  Worker Type / Worker / Worker Instance vocabulary.

## Runtime model

A BPMN task does **not** create a physical queue when it is modeled or deployed.
Deployment compiles the task's stable routing information. Only when a process
instance reaches a job-backed task does Atlas create a durable `JobCreated` fact.
After WAL append, fsync and state commit, that job becomes activatable.

Conceptually:

```text
BPMN task definition
    -> deployment compiles routing
    -> process instance reaches task
    -> durable Job / Work Item is created
    -> WAL + fsync
    -> state commit
    -> job becomes activatable
    -> Worker Instance leases it
    -> external work
    -> complete/fail command
    -> durable completion/failure events
```

The "queue" visible to an operator is therefore a **logical work stream**: the
ordered/set-like view of activatable jobs matching a Worker / job type. Atlas must
not create one broker queue per BPMN element and must not introduce a second source
of truth beside the durable job store merely to implement this vocabulary.

Fifty process instances parked at the same service task therefore mean fifty
durable jobs in the corresponding logical work stream, not fifty queues.

Deterministic in-engine tasks are the explicit exception. A FEEL script task does
not create a worker job. Local deterministic engine semantics remain in-engine and
are replayed from their resulting durable facts rather than re-executed during
recovery.

## Target vocabulary

| Current / legacy term | Target term | Meaning |
|---|---|---|
| Connector kind | Worker Type | Available implementation/capability and configuration schema |
| Connector instance | Worker | Configured target/identity for one Worker Type |
| Job | Job / Work Item | Durable executable unit created by a process instance |
| Worker process / registration | Worker Instance | Live runtime consumer; one Worker may have 0..N instances |
| Queue | Logical work stream | Derived view of activatable jobs; not an independent durable broker queue |

`connector` remains a compatibility and package-name term during migration. New
operator-facing concepts and APIs should use the Worker vocabulary unless they are
maintaining an explicitly deprecated contract.

## Configuration boundary

ADR-0203's `Worker` object is the logical configured integration identity. The
physical placement of its secrets follows ADR-0168:

- BPMN models and durable events contain no credential values;
- external Worker Instances own/use the credential where the integration runs;
- supervised Worker Instances may receive configuration through the controlled
  spawn path described by ADR-0168;
- the engine may persist metadata and secret references needed for management, but
  must not turn job payloads into a credential-distribution channel.

This distinction is important: renaming a connector instance to Worker must not
silently reverse ADR-0168 and centralize every integration secret in the engine.

## Implementation slices

### Slice 1 — Vocabulary and compatibility facade

- Add Worker-oriented DTO/domain names around the existing connector management
  store without changing persisted identifiers.
- Keep existing connector API routes as compatibility aliases.
- Mark legacy names as deprecated in OpenAPI and operator documentation.
- Do not rename persisted job type indices or replayed records.

**Done when:** the API can describe Worker Types and Workers without changing
runtime behavior or recovery output.

### Slice 2 — Console information architecture

Replace the operator-facing `Connectors` concept with `Workers`:

```text
Organization -> Workers
    Worker Catalog
        Jira
        Mail
        PostgreSQL
        ...
    Configured Workers
        jira-production
        mail-customer-service
        postgres-orders
```

The catalog describes capability, version, supported operations and runtime mode.
A configured Worker describes endpoint/configuration metadata, credential
references and aggregate health. Worker Instances belong in Operations/monitoring,
not in design-time configuration.

**Done when:** an operator can clearly distinguish "Jira is available" from "this
Jira account/tenant is configured" and from "three replicas are currently serving
it".

### Slice 3 — Modeler binding

- Service tasks select a Worker Type / operation and, where a concrete target is
  required, a configured Worker.
- The model never selects a Worker Instance.
- Element templates expose only operations the installed Worker Type actually
  supports.
- Existing connector-authored BPMN continues to load and deploy through a
  compatibility mapping.

**Done when:** design time expresses *what/which configured target*, while runtime
placement and replica count remain operational concerns.

### Slice 4 — Logical work-stream observability

Expose backlog without inventing a new queue subsystem. Useful views/metrics:

- ready/activatable jobs by Worker and job type;
- oldest ready job age;
- leased jobs and lease holder;
- completed/failed/timed-out counters;
- Worker Instance last-seen, concurrency and capacity.

The UI may label this view `Queue` if that is clearest for operators, but docs and
APIs should make clear that it is a projection over the durable job store.

**Done when:** fifty parked jobs appear as one work stream with backlog 50, and
adding Worker Instances drains the same stream without cloning Worker
configuration.

### Slice 5 — Worker Type packaging

Define a follow-up ADR before downloadable third-party Worker Types are supported.
It must cover at least:

- manifest/schema and stable type identifier;
- version/Atlas protocol compatibility;
- configuration schema and Modeler template metadata;
- artifact integrity/signing/trust;
- install/upgrade/remove lifecycle;
- external process/container launch contract;
- failure isolation and supervision boundaries.

No dynamic Go shared-object loading or arbitrary untrusted code in the Atlas engine
process is introduced by this migration.

**Done when:** Atlas can install/enable a capability without conflating package
installation with configuration or runtime replicas.

## Invariants and acceptance criteria

Every slice must preserve:

- **I2 durable before visible:** a Worker Instance cannot observe/lease newly
  created work before WAL append, fsync and state commit;
- **I3 single writer:** Worker registry, UI and external execution do not gain
  direct mutation access to partition state;
- **I4 one `applyToState`:** Worker telemetry and runtime registration stay derived
  operational state and are not replay side effects;
- **I5 compile, don't interpret:** stable Worker/job routing is resolved at deploy
  where possible; runtime does not parse BPMN or compile FEEL;
- **I6 events are facts:** job key, routing and decisions needed for replay are
  durable facts and are never regenerated differently on recovery.

Persistence, processor, job or routing changes require a recovery test comparing
live state with replayed state. Runtime changes must also preserve lease/fencing and
at-least-once semantics.

## Non-goals

- One physical message-broker queue per BPMN task.
- Kafka/RabbitMQ/NATS as a second authoritative job store.
- Treating humans as `Worker` objects in the Tasks UI; User Tasks retain their BPMN
  and human-centric product vocabulary.
- Moving pure FEEL computation out of the engine for the sake of superficial
  uniformity.
- A big-bang repository-wide rename that mixes terminology churn with behavioral
  changes.
