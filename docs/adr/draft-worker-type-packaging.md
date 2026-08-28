# ADR-DRAFT: Package Worker Types as signed external runtime artifacts

- **Status:** Proposed
- **Date:** 2026-08-28
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0203 separates a **Worker Type** (the implementation/capability), a configured
**Worker** (target, identity and configuration), and a running **Worker Instance**
(the ephemeral consumer). The first migration slices have introduced Worker-oriented
Console and HTTP terminology while intentionally retaining the existing connector
stores and handlers as compatibility infrastructure.

One question is still deliberately open: **what is the distributable unit of a Worker
Type, how does Atlas decide that it can trust and run it, and how can third parties ship
one without loading arbitrary code into the engine process?**

This is not the same problem ADR-0167 solves. ADR-0167 makes released connector/template
*metadata* discoverable in the Repository and keeps the authoring catalog complete. A
Worker Type package may additionally contain an executable runtime artifact. Treating
those two things as the same artifact would either make Repository templates executable
by accident or force every Worker Type back into the Atlas binary.

The package contract therefore has to preserve four existing boundaries:

1. **Engine safety.** No untrusted Worker Type code runs in the Atlas engine process.
2. **Durability.** A Worker Instance can only observe a job after the job is durable;
   packaging must not create another queue or completion path.
3. **Credential ownership.** Jobs carry resolved work values, not credential material
   (ADR-0168). A configured Worker references secrets; its Worker Instance receives the
   credential through its deployment environment or another explicit secret provider.
4. **Compile, don't interpret.** The Modeler and deployment compiler consume versioned
   Worker Type metadata. Runtime job execution does not parse package manifests or
   resolve authoring strings on the processor hot path.

## Decision drivers

- Third-party integrations must be installable without rebuilding Atlas.
- A package must be inspectable before installation: identity, version, compatibility,
  operations, configuration schema, execution protocol and artifact digest are facts,
  not values discovered by executing the package.
- Installation must never imply trust. Provenance and integrity have to be visible and
  policy-enforceable.
- Atlas must not become a general-purpose arbitrary-command supervisor.
- Worker Type upgrades must be deliberate because models and configured Workers can
  depend on a particular capability contract.
- Worker Instances may scale horizontally and be replaced freely; package identity must
  therefore be independent from process/container identity.
- The Repository should remain the discovery/install surface described by ADR-0081 and
  ADR-0167, but metadata-only templates and executable Worker Type artifacts must keep
  distinct trust semantics.
- The package format must work for the single-binary product first and remain usable in
  later multi-node/cloud-native deployments.

## Considered options

1. **Compile every Worker Type into Atlas.** New capabilities require a new Atlas build.
2. **Load Go plugins/shared objects into the Atlas process.** Packages contain native Go
   code dynamically loaded by the engine.
3. **Package an external runtime plus a declarative manifest.** Atlas installs metadata
   and launches or connects to a separate process/container implementing the public
   Worker protocol.
4. **Package arbitrary commands/scripts.** The manifest contains a command line Atlas
   executes directly.

## Decision outcome

Chosen: **option 3 — a Worker Type is a declarative, versioned manifest plus zero or one
external runtime artifacts. Atlas never dynamically loads Worker Type code into the
engine process.**

A package is an installation artifact. A configured Worker remains a separate operator
resource, and a Worker Instance remains an ephemeral runtime process or container.
Installing `jira@2.1.0` does not create a Jira Worker and does not start a Worker Instance;
it only makes that Worker Type available for configuration and authoring.

### Package identity

Every Worker Type package has a stable reverse-DNS-style identifier and a semantic
version, for example:

```text
io.atlas.mail@1.4.0
com.example.sap-s4@2.1.3
```

The identifier is not the job type. One Worker Type may implement multiple operations or
job types, and one public job protocol can outlive several package versions. Display
names are mutable presentation metadata and are never used as durable identity.

The package manifest contains at least:

```yaml
apiVersion: atlas.io/v1alpha1
kind: WorkerType
metadata:
  id: com.example.sap-s4
  version: 2.1.3
  name: SAP S/4HANA
spec:
  atlasCompatibility: ">=1.0.0 <2.0.0"
  workerProtocol: v1
  runtime:
    mode: external
  jobTypes:
    - io.example.sap-s4
  operations:
    - id: create-business-partner
      jobType: io.example.sap-s4
  configurationSchema: config.schema.json
  modelerTemplate: template.json
  artifact:
    platform: linux/amd64
    mediaType: application/vnd.oci.image.manifest.v1+json
    digest: sha256:...
```

The exact serialization may evolve before acceptance, but the semantic fields above are
part of this decision: stable identity/version, Atlas compatibility, Worker protocol,
job types/operations, configuration schema, Modeler metadata, runtime mode and a
content-addressed artifact reference.

### Runtime artifacts

The preferred executable form is an **OCI image referenced by digest**. It is portable,
already content-addressed, works with local container runtimes and Kubernetes, and does
not require Atlas to invent a binary distribution protocol.

A future native executable artifact may be added for environments where containers are
not available, but it must use the same manifest and public Worker protocol and must be
verified by digest before launch. Such support is an extension of this ADR, not a reason
to make `command: ...` the package contract.

Atlas does **not** support dynamic Go plugins (`plugin.Open`) for Worker Types. They share
address space, dependency/runtime assumptions and failure domain with the engine and
would turn third-party installation into execution inside the process that owns the
partition state. That conflicts with ADR-0164/0203's worker boundary and makes isolation,
upgrade and crash containment materially worse.

Atlas also does **not** accept arbitrary shell commands as installable Worker Types. The
single-binary supervisor may launch a runtime derived from an installed, verified package,
but the package cannot be a generic remote command-execution facility.

### Trust and integrity

Every executable artifact is content-addressed by digest. Atlas verifies the digest
before a local launch and records the installed manifest plus resolved digest as
installation metadata.

Signatures are supported as provenance evidence and may be required by operator policy.
The trust model is deliberately policy-based:

- bundled Atlas Worker Types are trusted by the Atlas release;
- Repository packages can be signed by a publisher identity;
- an installation can require signatures from an allow-list or reject unsigned runtime
  artifacts entirely;
- development installations may explicitly allow unsigned local packages.

A signature does not grant runtime credentials. Trusting who published a binary and
allowing that binary to access a production system are separate operator decisions.

The first implementation may ship digest verification before signature-policy UX, but it
must not describe an unsigned artifact as verified or trusted. Provenance state remains
visible in the Repository and Worker Catalog.

### Repository relationship

The **Repository** is the discovery and installation surface. ADR-0167's data-only
connector/template package remains valid and becomes the authoring part of a Worker Type
package when executable runtime is present.

The Repository must distinguish at least:

- **template-only** packages: authoring metadata, no executable artifact;
- **bundled Worker Type** packages: runtime provided/trusted with Atlas;
- **external Worker Type** packages: executable artifact with explicit provenance/trust
  state.

Installing authoring metadata must never silently execute an artifact. Runtime
installation/enablement is an explicit step whose UI states what code will run and where.

### Configuration and secrets

The manifest describes a configuration schema and identifies which fields are secret
references. It never contains credential values.

A configured Worker binds:

```text
Worker Type + configuration + secret references + optional placement policy
```

The Worker Instance receives its effective configuration at launch/connection time.
External independently operated Workers may keep their configuration entirely outside
Atlas, as allowed by ADR-0168; Atlas then stores only the management/binding metadata it
actually owns. This ADR does not centralize all credentials into the engine.

### Modeler contract

The Modeler consumes the installed Worker Type manifest through Atlas' design-time API.
It can offer only operations the installed Worker Type declares and that are compatible
with the running Atlas version.

The BPMN model binds to the stable Worker Type/operation semantics and a configured Worker
where the operation requires one. It never binds to a Worker Instance. A Worker Instance
is runtime topology and can disappear or multiply without changing a model.

Manifest parsing, schema validation and compatibility resolution happen outside the
processor hot path. Deploy-time compilation continues to produce the integer-indexed
runtime structures required by invariant I5.

### Upgrade and removal

Multiple package versions may be installed concurrently when their compatibility and
runtime isolation permit it. An update is not an in-place mutation of an artifact digest:
new version/digest means a new installed package version.

Atlas refuses removal while a configured Worker or deployed model still depends on that
exact package contract unless the operator uses an explicit forced administrative path
whose consequences are shown. Removal does not rewrite BPMN models or silently retarget a
configured Worker to another version.

A future automatic update policy may select newer compatible versions for *new*
configuration, but existing bindings move only through an explicit migration decision.

### Worker protocol and execution semantics

Packaging does not create a new execution protocol. Installed runtimes consume the
existing/public Worker job protocol:

```text
Job becomes durable
-> Worker Instance leases job
-> external side effect
-> complete/fail command
-> completion/failure events become durable
-> process continues
```

At-least-once execution, lease/fencing rules and idempotency requirements remain exactly
those of the existing Worker path. An OCI registry, Repository server or package cache is
never an authoritative job queue.

### Supervision and cloud-native placement

For the single-binary product, Atlas may supervise a locally installed Worker Type by
starting its verified runtime with a controlled argument/environment contract. The
supervisor launches only installed Worker Type artifacts; it is not a general command
runner.

For cloud-native deployments, the same package can be realized as a Deployment/Job or by
an external operator. Replica count/autoscaling creates **Worker Instances**, not new
Workers or Worker Types. Atlas does not require direct access to another partition's
state; instances communicate through the public Worker API.

## Compatibility and migration

Existing compiled/bundled connector implementations are not forced through the package
loader immediately. They map to bundled Worker Types and can acquire manifests
incrementally.

During migration:

- existing connector kind identifiers and persisted job type indices remain stable;
- existing configured connector records remain the compatibility store for configured
  Workers until their store migration is deliberately implemented;
- built-in Worker Types may have generated manifests from the current catalog metadata;
- externally shipped Worker Types use the new manifest contract first;
- no package installation changes WAL/event formats.

This preserves ADR-0203's staged migration instead of turning packaging into a big-bang
rename or persistence migration.

## Consequences

### Positive

- Third parties can ship capabilities without rebuilding Atlas or executing code inside
  the engine process.
- OCI/digest identity gives reproducible installation and deployment.
- Worker Type, configured Worker and Worker Instance remain cleanly separated.
- Modeler metadata and runtime compatibility have one versioned package contract.
- The design scales from the single binary to containers/Kubernetes without changing job
  semantics.
- Repository discovery remains useful while executable artifacts receive a stronger trust
  treatment than template-only packages.

### Negative / trade-offs accepted

- Packaging introduces lifecycle concepts Atlas does not yet need for compiled-in
  connectors: install, version coexistence, provenance, upgrade and removal checks.
- OCI support adds a registry/container-runtime integration surface.
- Signature verification and trust policy require product/operations UX, not only a
  cryptographic library.
- Supporting independently operated Workers means Atlas cannot always know or enforce the
  exact runtime artifact behind a Worker Instance; such instances must report enough
  identity for observability, and operators decide whether to allow them.

## Implementation sequence

This ADR defines the target contract, not a claim that packaging is already implemented.
A vertical implementation should proceed in this order:

1. **Manifest package and validation** — parser, schema, stable identity/version,
   compatibility and digest validation; no runtime launch.
2. **Worker Catalog integration** — expose installed/bundled Worker Type manifests through
   the Worker-oriented design-time API; generate manifests for selected built-ins.
3. **Repository installation** — install/remove metadata and content-addressed artifact
   references with dependency/refusal checks.
4. **Verified local runtime** — OCI-by-digest launch through the existing supervisor and
   Worker protocol; credentials remain outside job payloads.
5. **Provenance policy** — signature verification, publisher identity and operator trust
   rules/UX.
6. **Cloud-native deployment integration** — deployment/operator hooks and Worker Instance
   identity reporting; no engine-state coupling.

Each implementation slice gets tests for manifest validation, compatibility refusal and
installation lifecycle. Any later change touching jobs, durable state or completion
semantics additionally requires the repository's recovery test pattern.

## Roadmap placement

- **Milestone S — Single-binary server & web UI:** Worker Catalog presentation, installed
  Worker Type management and controlled local supervision.
- **Milestone 6 — Ecosystem:** third-party Worker Type SDK/package tooling, Repository
  distribution, signing/provenance and interoperability.
- **Milestone 5 — Scale-out:** only the deployment/placement realization for multiple
  nodes; package identity and Worker protocol are intentionally independent from the
  partitioning design.

## Rejected shortcuts

- No `plugin.Open` or other same-process third-party code loading.
- No package-defined arbitrary shell command.
- No credential values in manifests, BPMN or job payloads.
- No physical queue per Worker Type or BPMN task.
- No runtime manifest interpretation in the processor batch cycle.
- No implicit upgrade of deployed models/configured Workers when a package version is
  installed.
- No Worker Instance identifier in BPMN.

## Links

- [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker Instance and
  staged Connector terminology migration
- [ADR-0168](0168-connector-work-on-a-worker.md) — Worker-side configuration and credential
  ownership
- [ADR-0164](0164-no-in-process-service-tasks.md) — side-effecting service tasks move out
  of the engine process
- [ADR-0157](0157-worker-processes-supervision-and-console.md) — Worker process supervision
  and runtime observability
- [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md) — released connector
  authoring metadata belongs in the Repository
- [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md) — Repository/marketplace
  discovery and trust boundary
- [ADR-0027](0027-element-templates.md) — element-template package metadata
- [Worker execution migration plan](../architecture/worker-execution-migration.md)
- [Engine invariants](../architecture/invariants.md)
