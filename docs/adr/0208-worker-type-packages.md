# ADR-0208: Worker Type package contract, trust, and distribution

- **Status:** Proposed
- **Date:** 2026-08-28
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0203 establishes the execution vocabulary around **Task -> Job / Work Item -> Worker** and separates a **Worker Type** (an available execution capability), a configured **Worker**, and a live **Worker Instance**. The first product slice has already moved the Console to this vocabulary while deliberately retaining the existing connector API and persistence contract as a compatibility layer.

The runtime implementation is further ahead than the product model in some places. Atlas already has:

- compiled-in connector/service-task implementations;
- worker-only kinds whose credentials never enter the engine;
- `atlas worker` processes and Atlas-supervised worker processes;
- a runtime Workers view with queue depth, in-flight work, health and worker identity;
- operator-managed connector records that are conceptually configured Workers;
- a Repository/package direction for reusable design-time artifacts and element templates (ADR-0027, ADR-0081).

What Atlas does **not** yet have is a technical definition of what it means for a Worker Type to be *available*, *installed*, *versioned* or *uninstalled*. Without that contract, a Worker Catalog can only rename the current connector-kind list. It cannot safely answer questions such as:

- Which Worker Types are built into this Atlas release?
- Which Worker Types were installed from another source?
- Which configuration and credential fields does a Worker Type require?
- Which operations/job types does it implement?
- Which Atlas/job-protocol versions is it compatible with?
- Is its runtime executed by Atlas, by an Atlas-supervised child process, or externally?
- Can two versions coexist?
- What prevents a downloaded package from becoming arbitrary code loaded into the Atlas process?
- When is uninstall safe?

This is architecture, not a UI naming exercise. In particular, allowing downloadable code to be dynamically loaded into the engine would change Atlas from a single-binary workflow engine into a plugin host and would expand the trust boundary around the processor. That must not happen accidentally as a local implementation shortcut.

## Decision drivers

- Preserve all six engine invariants, especially durable-before-visible, single writer, one `applyToState`, and compile-don't-interpret.
- Preserve the single-binary operational model for Atlas itself.
- Do not turn Atlas into an arbitrary in-process plugin host.
- Make **Worker Type**, **Worker**, and **Worker Instance** concrete product/domain concepts rather than UI aliases.
- Reuse the Repository/package direction from ADR-0081 instead of inventing a second marketplace/distribution system.
- Keep third-party runtime code outside the Atlas process by default.
- Make package provenance, compatibility and integrity explicit before community distribution exists.
- Never place credential material in a Worker Type package, BPMN model, deployment snapshot, event or WAL record.
- Keep persisted compiler job-type identifiers stable.
- Allow built-in Worker Types and externally supplied Worker Types to appear in one Worker Catalog without pretending they have the same trust or lifecycle.
- Support cloud-native deployment without requiring Kubernetes, Docker or another container runtime for a normal single-binary Atlas installation.

## Considered options

### Option 1 — Dynamic Go plugins loaded into Atlas

A Worker Type package contains a Go shared object or dynamically loaded library. Installing a Worker Type loads its implementation into the Atlas process.

**Rejected.** This creates an in-process plugin host, complicates Go portability/versioning, expands the engine trust boundary, undermines the single-binary distribution model, and makes a third-party integration capable of destabilizing the server process. It is also unnecessary because Atlas already has a durable job protocol as the execution boundary.

### Option 2 — Every Worker Type is an OCI image and Atlas launches it

A package is an OCI image. Installing it causes Atlas to pull and start that image as a managed Worker Instance.

**Rejected as the universal contract.** OCI is excellent for cloud-native deployment, but requiring a local container runtime would break the zero-container single-binary experience. Having Atlas launch arbitrary downloaded images also creates a large lifecycle/security surface (registry credentials, image policy, networking, volumes, resource limits, upgrades, rollback) that is not required to define Worker Types.

OCI remains a supported **distribution/runtime reference** for externally operated workers, especially on Kubernetes, but Atlas does not need to become a container orchestrator.

### Option 3 — Manifest package + explicit runtime mode, with third-party code external by default

A Worker Type is a signed/verifiable metadata package. It describes capability, configuration, operations, compatibility, element templates and how its runtime is expected to execute. Atlas may execute only implementations that are part of the trusted Atlas binary. Third-party implementations use the public worker/job protocol and run as external processes or containers.

**Chosen.**

### Option 4 — Keep Worker Types compiled into source forever

The Worker Catalog remains a renamed hard-coded connector-kind list. Adding a Worker Type always means changing Atlas source and releasing a new Atlas binary.

**Rejected as the long-term model.** It is safe but prevents the Worker Catalog and community distribution model from becoming real. It also keeps configuration schemas, templates and runtime metadata scattered through code rather than expressed as one capability contract.

## Decision outcome

Atlas adopts a **Worker Type package manifest** as the stable description of an executable integration capability. The manifest is metadata and design-time configuration, not dynamically loadable engine code.

### 1. Worker Type identity

Every Worker Type has a stable, globally namespaced identifier and a semantic version.

Examples:

```text
atlas.mail
atlas.jira
atlas.postgresql
com.acme.sap-s4hana
org.example.servicenow
```

The stable identity is the `workerTypeId`; display names are mutable presentation text and never become routing keys.

A package version identifies the Worker Type contract version. It does **not** replace existing reserved compiler job-type indices and does not renumber persisted deployments.

### 2. Manifest

The canonical manifest is serializable data with at least:

```yaml
schemaVersion: atlas.worker/v1
id: atlas.jira
version: 1.0.0
title: Atlassian Jira
vendor: Atlas
atlasCompatibility: ">=0.x <1.0"
workerProtocolCompatibility: "v1"
runtime:
  mode: atlas-supervised
operations:
  - id: createIssue
    jobType: atlas.jira.create-issue
    title: Create issue
configuration:
  schema: worker-config.schema.json
credentials:
  schema: worker-credentials.schema.json
templates:
  - jira-create-issue.element-template.json
provenance:
  digest: sha256:...
```

Exact wire field names may evolve while this ADR is Proposed, but the contract must contain these concepts:

- stable Worker Type id;
- semantic version;
- human-facing metadata/vendor;
- Atlas compatibility;
- worker protocol compatibility;
- runtime mode;
- supported operations/job types;
- configuration schema;
- credential-reference schema (shape only, never values);
- optional BPMN element templates / Modeler metadata;
- integrity/provenance metadata.

The compiler continues to own immutable execution identifiers. A package may describe the job types it supports, but installing a package cannot mutate or reassign a reserved job-type index in an existing deployment.

### 3. Runtime modes

The boolean `workerOnly` distinction evolves into an explicit runtime-mode concept.

The initial domain values are:

- **`atlas-embedded`** — trusted implementation compiled into the Atlas binary and executed through Atlas's existing post-fsync in-process job-worker path. This is a compatibility/runtime mode for curated built-ins, not a mechanism for third-party code loading.
- **`atlas-supervised`** — trusted implementation compiled into the Atlas binary but executed as an `atlas worker` child process supervised by Atlas. Atlas starts only its own executable with typed arguments; no package may supply an arbitrary command line.
- **`external`** — Worker Instances run outside Atlas and consume the public worker/job API. They may be a native process, systemd service, Docker/Podman container, Kubernetes Deployment, Nomad job, or another platform-specific deployment. Atlas does not assume or require one orchestrator.

A deterministic **in-engine task handler** such as a FEEL script task remains outside the Worker Type model. It is workflow semantics, not an integration runtime.

No manifest value may cause Atlas to `dlopen`, load a Go plugin, execute an arbitrary package-provided command, or evaluate downloaded code inside the server process.

### 4. Built-in versus installed Worker Types

The Worker Catalog contains two origins:

- **Built-in:** manifests compiled/shipped with the Atlas release. Their implementation is part of the trusted binary. They cannot be physically uninstalled from that binary, but may be unavailable/disabled according to runtime/configuration support.
- **Installed:** manifests added through the Repository/Worker Catalog. Installation adds metadata, schemas and templates. Unless the runtime is a trusted built-in already known to Atlas, the runtime mode is `external`.

This makes “Install Worker Type” honest: Atlas installs the capability contract and authoring/configuration metadata. It does not silently execute third-party code.

### 5. Reuse the Repository package model

ADR-0081 already chooses one Repository package concept for reusable artifacts. Worker Types extend that direction rather than creating a second registry.

A Repository package may carry a Worker Type manifest plus its schemas and element templates. The package index/search/discovery, provenance and future remote/federated registry concerns are shared with ADR-0081.

This ADR **refines** ADR-0081 where executable integrations are concerned:

- an element template alone is not a Worker Type;
- the Worker Type manifest is the executable-capability contract;
- script/template packages and Worker Type packages may share the Repository transport/envelope while retaining distinct trust rules;
- runtime implementation is never embedded in the BPMN template itself.

### 6. Configuration and credentials

A **Worker** is a configured instance of one Worker Type.

Conceptually its durable operator-managed record becomes:

```text
Worker {
    id
    name
    workerTypeId
    workerTypeVersion
    config
    credentialsRef
    enabled
}
```

Migration from today's connector sidecar records is staged and compatibility-preserving. Existing records, IDs, endpoints and public `/connectors` API remain valid until a separately versioned migration/API ADR changes them.

The Worker Type manifest describes the *shape* of credentials, never the secret values. A configured Worker stores only a credential reference. Resolution stays in the server vault/environment boundary defined by ADR-0041/0069/0070 and, for external Worker Instances, is provisioned only into that worker's runtime boundary.

### 7. Versioning and compatibility

Worker Type versions are semantic versions. Atlas evaluates compatibility at installation and before enabling/configuring a Worker.

Two concerns are separate:

- **Package compatibility:** can this Atlas release understand/configure this Worker Type manifest?
- **Execution compatibility:** can a Worker Instance speak the worker/job protocol required by this Atlas release and execute the operations referenced by deployed models?

A Worker may stay pinned to a Worker Type version while a newer version is installed. Upgrading a configured Worker is an explicit operator action when the new version changes configuration or operation contracts.

The first implementation may restrict one installed version per Worker Type id if coexistence would add storage/UI complexity, but persisted records must retain enough version information to make future coexistence possible without guessing.

### 8. Install and uninstall safety

Installation is a design-time/operator action, never part of the processor hot path.

Uninstall is rejected while the Worker Type is still referenced by configured Workers. The first slice also warns when deployed process versions reference operations/job types supplied by that Worker Type.

A force-uninstall, if added later, must be an explicit destructive operator action and must not rewrite existing immutable deployments. Existing process instances may consequently incident/park if no compatible Worker Instance remains; Atlas must never silently reroute them to another Worker Type.

Built-in Worker Types are not physically uninstallable without installing another Atlas binary.

### 9. Provenance, signing and trust

Every installed package has a content digest. Remote distribution must add signature verification before third-party packages are treated as trusted catalog content.

The preferred cloud-native direction is OCI-compatible provenance/signing (for example digest-pinned artifacts and Sigstore/cosign-style signatures), but the Worker Type contract is not coupled to OCI as its only transport.

Trust affects **catalog installation**, not engine-code loading: even a correctly signed third-party Worker Type remains `external` unless its implementation ships as trusted Atlas code.

Atlas must surface at least package origin, version, digest/signature state and compatibility in the Worker Catalog.

### 10. Worker Instances and scaling

Installing a Worker Type does not create runtime replicas.

A configured Worker has zero or more **Worker Instances**. Existing lease/claim semantics remain authoritative; multiple instances may consume compatible work concurrently for horizontal scaling.

For `external` Worker Types, instance lifecycle is normally owned by the surrounding platform. Atlas observes registration/traffic/health but does not need to own deployment.

A future Kubernetes/OCI integration may offer deployment convenience, but it must remain an orchestration adapter over the same public worker protocol rather than a new execution path.

## Invariants

This decision does not change the processor execution order:

```text
Command
-> Events
-> WAL
-> fsync
-> State commit
-> Followup Commands
-> Side Effects / Worker visibility
```

Specifically:

- **I1 No allocation on the hot path:** package parsing, schema validation, signature checks and version resolution happen at install/deploy/configuration time, never per command.
- **I2 Durable before visible:** Worker Instances only receive work through the existing post-fsync job boundary.
- **I3 Single writer:** Worker catalog/configuration writes use design-time/run-loop ownership; external workers never access partition state directly.
- **I4 One `applyToState`:** manifests and runtime discovery add no alternate state-apply path.
- **I5 Compile, don't interpret:** element-template resolution and operation/job-type binding happen before runtime.
- **I6 Events are facts:** package versions, routing decisions and generated identifiers used by execution must be frozen into deployment/events where replay requires them; package lookup must never reinterpret old events.

## Migration plan

1. **Domain vocabulary:** introduce `WorkerTypeDefinition` / `WorkerRuntimeMode` internally while retaining compatibility aliases around `managedConnectorKind` and current connector records.
2. **Built-in manifests:** express existing managed connector/worker kinds as built-in Worker Type metadata and make the Worker Catalog read that metadata instead of hard-coded presentation strings.
3. **Configured Worker DTO:** add a Worker-oriented API representation over the current connector store without changing the stored record initially.
4. **Repository package support:** add `worker-type` packages carrying manifest + schemas + templates; installed third-party types are `external`.
5. **Trust:** persist origin/digest and verify signatures for remote packages before enabling remote sources.
6. **Public API migration:** introduce Worker Type/Worker endpoints and deprecate connector aliases with an explicit compatibility window.
7. **Runtime observability:** associate observed Worker Instances with Worker Type id/version when workers report that metadata.
8. **External deployment adapters:** only after the protocol/manifest is stable, consider optional Kubernetes/OCI deployment helpers as separate ADRs.

Each step is independently shippable. No step requires renumbering persisted job types or changing existing process semantics.

## Consequences

### Positive

- Worker Catalog becomes a real capability catalog rather than renamed connector UI.
- Third-party integrations can be distributed without loading third-party code into Atlas.
- Built-in, supervised and external execution are explicit instead of encoded in `workerOnly bool` plus scattered supervision rules.
- Repository/marketplace work is reused instead of duplicated.
- Configuration, credential shapes, operations and compatibility become inspectable metadata.
- The model supports Kubernetes/container deployments while preserving a container-free single-binary installation.
- Package provenance and versioning are designed in before community distribution becomes a supply-chain problem.

### Negative / trade-offs accepted

- “Install” does not mean “Atlas automatically runs arbitrary third-party code”; an external Worker Instance still has to be deployed somewhere.
- The manifest becomes a versioned public contract that Atlas must maintain.
- During migration, both Worker terminology and connector compatibility APIs/storage coexist.
- Built-in Worker Types have a different physical lifecycle from installed external Worker Types even though they share one catalog.
- True one-click deployment of third-party runtime containers is deferred.

## Follow-ups

- Exact `atlas.worker/v1` schema and validation implementation.
- Worker-oriented HTTP API and deprecation policy for `/connectors`.
- Stable operation/job-type naming for third-party Worker Types without exposing mutable compiler internals.
- Repository remote source and signing implementation, coordinated with ADR-0081.
- Vulnerability/de-listing/revocation metadata for compromised packages.
- Optional Kubernetes/OCI runtime deployment adapter.
- Worker Instance registration handshake carrying Worker Type id/version and supported operations.
- Upgrade/rollback UX for configured Workers.

## Links

- ADR-0007 — Job worker protocol
- ADR-0011 — Single-binary distribution
- ADR-0027 — Element templates
- ADR-0041 — Connector management and secret store
- ADR-0067 — Service-task connector catalog
- ADR-0069 / ADR-0070 — encrypted secret vault
- ADR-0081 — Repository/community package distribution
- ADR-0156 — worker processes, supervision, and Workers console
- ADR-0168 — worker execution split for side-effecting work
- ADR-0203 — Worker execution model and integration terminology
