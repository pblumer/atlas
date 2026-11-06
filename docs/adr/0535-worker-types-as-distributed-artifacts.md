# ADR-0535: Worker Types are signed artifacts that atlas launches and atlas hub distributes

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-11-05
- **Deciders:** pblumer (maintainer; approval to implement on 2026-11-05)
- **Open question:** under which licence the Worker SDK and the protocol specification are
  published, and whether a Worker Type that links an AGPL-3.0-only SDK and is distributed
  through atlas hub must itself be AGPL. The licence of the SDK decides whether commercial
  and third-party Worker Types are possible at all; it needs legal review and, for a
  relicensing, the consent of every holder of rights in the code moved into the SDK
  (contributions are DCO-signed, not covered by a CLA). A second gap: how far a native
  Worker process launched by atlas on Windows can be confined without a container.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0203](0203-worker-execution-model.md) separated Worker Type, Worker and Worker
Instance. [ADR-0207](0207-worker-type-packaging.md) and
[ADR-0208](0208-worker-type-packages.md) described a Worker Type package and its trust,
but only their metadata half exists, and the two records disagree on the primary runtime
artifact (0207 prefers an OCI image, 0208 rejects OCI as the universal contract). The
manifest parser in `workertype/manifest.go` is imported by nothing outside its package.
[ADR-0499](0499-blueprint-registry-and-distribution.md) built atlas hub as a signed
registry of application Blueprints; it lists required Worker Types as needs but cannot
distribute one.

What exists in the tree today (measured 2026-11-05):

- **Every Worker Type is compiled into the `atlas` binary.** 33 packages under
  `connector/`, wired by `worker/*connector.go`. `atlas worker` is the same binary in
  another mode; outside the tree only `--handle type=command` exists.
- **The supervisor runs only itself.** `api/supervisor.go` launches `os.Executable()` and
  nothing else ([ADR-0157](0157-worker-processes-supervision-and-console.md),
  [ADR-0439](0439-supervised-workers-end-with-the-server.md)).
- **The protocol is already process-neutral.** A Worker Instance long-polls
  `/api/v1/jobs/activate` and reports `/complete`, `/fail`, `/error`
  ([ADR-0007](0007-job-worker-protocol.md)); queries travel over the worker query broker
  (`api/workerquery`, [ADR-0473](0473-form-remote-data-sources.md)).
- **The Worker client is coupled to the engine.** `worker` imports `job`, which imports
  `state` and through it Pebble. There is no SDK an outside author could depend on.
- **A Worker Type has a server half.** `managedConnectorKind` in `api/connectorkinds.go`
  imports Worker Type packages directly; `api/superviseenv.go` (about 2100 lines) holds
  one environment function per type that turns vault entries into a worker's startup
  environment; several types have their own server routes (mock directories, SQL mock
  journals, mailbox use). The reserved job-type indices live in
  `compiler.ReservedJobTypes` and `api/releasedkinds.go`.

The consequence: moving the runtime code out of the binary is the easy part. As long as
the server half is code, every new Worker Type — and every change to one — needs an atlas
release, and nobody outside this repository can ship one.

The owner set the requirements on 2026-11-05:

1. Worker Types come from four origins: free Worker Types by atlas, commercial Worker
   Types by atlas, third-party and community Worker Types, and Worker Types private to
   an organisation.
2. atlas launches installed Worker Types itself; installing only metadata is not enough.
3. Windows without containers, Linux hosts and Kubernetes are all target environments.
4. The 33 existing Worker Types are migrated, not grandfathered.

Requirement 2 together with requirement 1 changes a rule of ADR-0208, which allowed
atlas to execute only implementations shipped in its own trusted binary and made every
third-party Worker Type `external`. atlas will now start code it did not build. That
does not touch an engine invariant — the code still runs in its own process and reaches
the engine only through the public protocol — but it moves the supervisor across a trust
boundary, and it makes atlas hub a distribution channel for executable code with access
to production credentials. That is a supply-chain target, and the decision below treats
it as one.

## Decision drivers

- No third-party code in the engine process; the six invariants stay untouched.
- One build product per Worker Type that runs on every supported platform, including
  Windows without a container runtime.
- No type-specific code in the server: a new Worker Type, or a new version of one, needs
  no atlas release.
- Installation, upgrade and removal are operator decisions; nothing changes on its own.
- A compromised key, registry or publisher must not be able to place code on a server or
  start it there.
- A published release that turns out to be malicious or broken can be stopped on every
  installation that trusts the registry, quickly and without a human on each server.
- Organisation-internal Worker Types work without a public registry and offline.
- Persisted job-type indices and existing deployments never change meaning (I6).

## Considered options

1. **Keep every Worker Type compiled in** (status quo).
2. **Two binaries:** the engine, and one first-party `atlas-workers` binary holding all
   built-in Worker Types.
3. **One signed artifact per Worker Type:** a native executable per platform as the
   baseline, an OCI image as an optional additional form, launched by atlas through
   pluggable launchers, with a generic server half; distributed through atlas hub with
   publisher and registry signatures and a signed revocation list.
4. **OCI image as the only artifact.**
5. **WebAssembly modules executed inside the atlas process.**
6. **Go plugins** — already rejected by ADR-0207 and ADR-0208; not reconsidered.

## Decision outcome

Chosen: **option 3.** Option 2 is the first migration step toward it, not an end state.

### 1. The artifact

A Worker Type release is:

- a manifest in the `atlas.worker/v1` form of ADR-0208 (identity, semantic version,
  atlas and protocol compatibility, operations and job types, configuration and
  credential schemas, element templates, capability claims);
- **one native executable per supported platform.** `linux/amd64`, `linux/arm64` and
  `windows/amd64` are required for a release to be listed in atlas hub; `darwin/*` is
  optional. A release lacking the platform of an installation is refused there with that
  reason, before anything is downloaded;
- optionally an **OCI image** referenced by digest, for launchers that run containers;
- declared **prerequisites** the artifact cannot carry itself (for example PowerShell 7
  and the `MicrosoftTeams` module for `teamsphone`). They are checked at install and at
  every launch; a missing prerequisite is an installation error, not a failing job.

The native executable is the baseline because it is the only form that runs everywhere a
supported atlas runs. The OCI image exists for isolation and for prerequisites a native
binary cannot bundle.

### 2. Launchers

Where a Worker runs is the Worker's **placement**, chosen by the operator per configured
Worker. atlas realizes it through one of these launchers:

| Launcher | Environments | Starts |
|---|---|---|
| `process` (default) | Windows, Linux, macOS | the verified native executable as a child process |
| `container` | hosts with Docker or Podman | the verified OCI image, or the native Linux executable inside the atlas worker-host image |
| `kubernetes` | clusters, opt-in | a Deployment per Worker, replicas as Worker Instances |
| `external` | anywhere | nothing; an operator runs the Worker Instance and atlas observes it |

The supervisor launches **only** artifacts from its content-addressed store
`<data>/workertypes/<id>/<version>/<sha256>/`, verifies the digest again before every
launch, and builds argv and environment from a contract atlas defines. No manifest value
becomes a command line. The `kubernetes` launcher needs RBAC for atlas's service account
and is therefore never enabled by default.

Confinement differs by launcher and is stated, not implied:

- `process` on Linux applies a Landlock/seccomp profile derived from the script sandbox
  ([ADR-0303](0303-script-sandbox-isolation.md)) and may run under a separate user;
- `process` on Windows limits resources with a job object but offers no file-system or
  network confinement comparable to Linux. The trust policy (section 8) therefore
  requires an explicit operator allowance before a non-atlas Worker Type is launched by
  `process` on Windows;
- `container` and `kubernetes` confine through the container runtime.

### 3. Protocol and SDK

- The Worker protocol (jobs, queries, handshake) is specified as a versioned,
  language-neutral contract under `docs/contracts/`. A Worker Type may be written in any
  language.
- A Go reference SDK becomes its own module with no dependency on `job`, `state`,
  `compiler` or any engine package. The built-in Worker Types are rewritten against it;
  extracting it is the first migration step.
- **Handshake.** A Worker Instance registers with its Worker Type id, version, artifact
  digest, protocol version and operations. For an instance atlas launched, a digest
  other than the installed one is refused. An `external` instance that reports a digest
  atlas does not know is shown as unverified and admitted only if the trust policy
  allows it.

### 4. The server half becomes generic

At the end of the migration the server imports no Worker Type implementation package; a
test enforces it.

- Configuration and credential forms are rendered from the manifest's JSON Schemas;
  validation runs against the schema.
- Credentials leave the vault through one generic contract instead of one function per
  type in `api/superviseenv.go`: a configuration document handed to the launched process
  at start, and the per-job credential broker that already exists for identity-aware
  execution ([ADR-0475](0475-identity-aware-worker-execution.md)).
- Behaviour that is type-specific today becomes a **query the Worker answers** over the
  existing worker query broker: `validate-config`, `check-connection` (as in
  [ADR-0220](0220-checking-a-database-connector.md)), `lookup`
  ([ADR-0473](0473-form-remote-data-sources.md)) and `diagnostics` (replacing routes such
  as the AD mock directory and the SQL mock journal). The server offers the plumbing;
  the Worker Type supplies the meaning.

### 5. Job-type identity and deployments

- A Worker Type declares namespaced job types (`com.acme.sap-s4.create-bp`). They get
  dynamic indices from the existing `jobtype` registry.
- A migrated built-in keeps its reserved index permanently: the registry maps its new
  namespaced name to the old index. No index is renumbered or reused.
- A deployment pins the Worker Type id and major version of every job type it uses; the
  compiler resolves it at deploy (I5). Installing, upgrading or removing a Worker Type
  never changes an existing deployment. Removal is refused while a configured Worker or a
  deployed model depends on the version (ADR-0208 § 8).

### 6. Distribution through atlas hub

atlas hub gains a release kind `worker-type`. Because a v1 index refuses unknown
properties, this is a new index schema version, reviewed for compatibility as ADR-0499
requires.

**Two signatures, both required.**

1. The **publisher** signs a release statement: Worker Type id, version, manifest digest
   and the digest of every artifact.
2. The **registry** signs the index that lists the release, as today.

atlas installs a release only if both verify. The registry key is pinned by the operator
out of band (unchanged from ADR-0499). A publisher key is accepted either because the
registry's signed publisher directory binds it to a namespace, or because the operator
pinned it. A namespace belongs to one publisher; `io.atlas.*` is accepted only with the
atlas release key.

The four origins map onto this:

| Origin | Registry | Publisher key | Notes |
|---|---|---|---|
| atlas, free | public atlas hub | atlas release key | built in this repository's CI |
| atlas, commercial | authenticated registry (ADR-0499 extension) | atlas release key | entitlement decides the download, never the execution |
| third party, community | public or the publisher's own registry | the publisher's key, bound by the registry | |
| organisation-internal | the organisation's registry, or a file imported from the command line ([ADR-0437](0437-a-package-is-imported-from-the-command-line.md)) | the organisation's key, pinned by its operator | works offline |

A commercial Worker Type may check its own licence; the engine never does, and no
process instance depends on a licence server (ADR-0499).

### 7. Supply chain: compromise and revocation

This section must be implemented and tested **before the first Worker Type binary is
published** in any registry atlas installations trust.

**What each compromise can do.** The design aims for: no single stolen key places code on
a server.

| Compromised | Can | Cannot |
|---|---|---|
| publisher key alone | sign a release | get it listed in a registry it does not control, or into another publisher's namespace |
| registry key alone | list or revoke releases | produce a valid publisher signature, so it cannot list new code |
| both | list a malicious new version | install it: upgrades are explicit operator actions |
| the registry host | serve old or altered bytes | pass the digest and signature checks |

The residual risk is the operator approving a malicious upgrade signed by both keys. The
upgrade dialog therefore shows the publisher, the signature state, the digest and the
change in requested prerequisites and placement before approval.

**Revocation.** The signed index carries revocation entries: Worker Type id, version range
or digest, severity, reason and date. Installations read them off the run loop on a fixed
interval and at every start.

- A revocation can only **prevent and stop**: refuse new installs, refuse launches, and —
  at severity `critical`, unless the operator's policy says otherwise — stop the
  instances atlas launched. It can never install, upgrade, start or reconfigure anything.
  The hub therefore never becomes a remote-control channel: the worst a stolen registry
  key achieves through revocation is a denial of service, which is visible and
  recoverable.
- A stopped Worker leaves its jobs durable and waiting; processes park on them as they
  would for any absent Worker. Nothing is failed or rerouted.
- An installation that cannot reach its registry keeps its last verified revocation list
  and says how old it is. Whether a stale list blocks launches is operator policy.
  Offline organisations distribute revocations the same way they distribute releases.
- A publisher key is revoked by the same mechanism; releases signed only by it are then
  treated as revoked.

**Provenance.** Every release built by atlas CI records a build provenance statement
(source commit, builder, digests) beside the artifact. A third-party release may carry
one and the Console shows whether it does. This is evidence, not a gate, until a later
record makes it one.

**What the registry signature attests.** Under ADR-0499 a known publisher is *attested
by the registry operator*. For a Blueprint that is harmless; for executable code with
access to production credentials it means the operator of the public atlas hub vouches
for code somebody else wrote. Whether that attestation stands for a review, an automated
scan, a verified publisher identity or nothing beyond listing is a product and liability
decision this record does not take. **No third-party Worker Type is listed in the public
atlas hub before a separate record decides it.** First-party and organisation-internal
registries are not affected: their operator vouches for their own code.

### 8. Trust policy per installation

The operator decides, per installation:

- which registries are trusted, with their pinned keys;
- which publishers and namespaces are accepted;
- which launchers a trust tier may use (for example: third party only in `container`
  or `kubernetes`; `process` on Windows only for atlas and organisation-internal
  Worker Types);
- whether unsigned local artifacts are allowed — off by default, for development only.

A signature establishes who published an artifact. It grants no credential and no
network access; binding a Worker to a credential remains a separate operator action.

### 9. Migration of the existing Worker Types

All 33 are migrated. What stays in the engine is what ADR-0164 and ADR-0233 already keep
there: DMN, user tasks, FEEL script tasks, user provisioning. These are in-engine task
handlers, not Worker Types.

1. **SDK and contract.** Extract the SDK module; write the protocol contract and the
   handshake. No change to the job semantics.
2. **Two binaries (option 2 as a step).** The first-party Worker Types build into a
   separate `atlas-workers` executable; the supervisor launches it in place of itself.
   The engine binary drops the worker dependencies.
3. **Verified local artifacts.** Content-addressed store, digest check, `process`
   launcher, installation from a file. Pilot with one simple Worker Type
   (`wikiwikibangbang` or `paperless`) as its own artifact.
4. **Generic server half.** Schema-driven configuration, the generic credential
   contract, the `validate-config`/`check-connection`/`diagnostics` queries. Migrate the
   types with the smallest server half first.
5. **Supply chain** (section 7) and the `worker-type` release kind in atlas hub. No
   Worker Type binary is published before this step is complete.
6. **`container` and `kubernetes` launchers.**
7. **The hard cases:** `ad`, `entra`, `exchange`, `teamsphone`, the SQL kinds, `mail`,
   `script`. Each may need its own record where its server half does not fit section 4.
8. **Removal.** The engine binary no longer contains Worker Type implementations; the
   test from section 4 guards it. The `atlas-embedded` runtime mode ends. A fresh
   installation still works offline because the atlas distribution archive ships the
   first-party artifacts preinstalled beside the binary, not compiled into it.

Configured Workers move to the Worker record of ADR-0208 with `workerTypeId` and
`workerTypeVersion`; the `/connectors` compatibility routes follow ADR-0208's deprecation.

## Invariants

- **I1 No allocation on the hot path:** download, verification, schema validation and
  version resolution happen at install, configuration or deploy time.
- **I2 Durable before visible:** Worker Instances see jobs only through the existing
  post-fsync boundary, whichever launcher started them.
- **I3 Single writer:** Worker Type and Worker records are design-time state owned by the
  run loop; no Worker touches partition state.
- **I4 One `applyToState`:** nothing here adds a state path; revocation and installation
  are design-time state, not engine events.
- **I5 Compile, don't interpret:** job types and Worker Type versions are resolved at
  deploy.
- **I6 Events are facts:** reserved indices keep their meaning; a deployment's pinned
  Worker Type version is part of the deployment, never looked up again on replay.

## Consequences

- **Positive:** a Worker Type ships, updates and is patched for a CVE without an atlas
  release; the engine binary loses the integration dependencies and their attack
  surface; third parties, commercial offers and organisation-internal Worker Types use
  one mechanism; the server half becomes data instead of code.
- **Positive:** the supply-chain risk introduced by executable distribution is bounded
  by design — no single key places code, revocation can only stop — and is in place
  before the first binary is published.
- **Negative / trade-offs accepted:** version skew between atlas, a Worker Type and the
  deployed models becomes a real support matrix; three artifacts per release and three
  launchers to test; Windows confinement is weaker than on Linux and the policy must say
  so; the migration of the hard cases is large and some may need new protocol features.
- **Negative / trade-offs accepted:** ADR-0499's single-registry configuration and its
  index-only signature are no longer sufficient for this release kind.
- **Follow-ups / risks to watch:** SDK licence (open question); the exact revocation
  polling interval and stale-list policy; key ceremony for the atlas release key
  separate from the hub registry key; whether ADR-0299's admission criteria apply to
  Worker Types published outside this repository; conformance evidence
  ([ADR-0483](0483-technical-capability-conformance-governance.md)) for artifacts built
  outside this tree.

## Pros and cons of the options

### Option 1 — keep every Worker Type compiled in

- Good: one binary, one version, one test matrix; no version skew between the server
  half and the Worker; no supply chain beyond the Go modules; conformance evidence is
  tied to the build; the simplest operation on Windows.
- Bad: every change or CVE in a Worker Type is an atlas release; the engine process
  carries every integration's dependencies; no third party can ship without a fork;
  ADR-0299 shows the tree as sole channel accumulating ownership debt. Contradicts the
  owner's requirements 1 and 2.

### Option 2 — engine plus one `atlas-workers` binary

- Good: separates the dependencies quickly; few new lifecycle concepts.
- Bad: still one release for all Worker Types; no third-party path. Kept as migration
  step 2.

### Option 3 — per-type signed artifacts, native baseline (chosen)

- Good: meets all four requirements; native baseline keeps Windows without containers.
- Bad: largest effort; Windows confinement is limited.

### Option 4 — OCI only

- Good: strong isolation, bundles prerequisites, established signing tooling.
- Bad: requires a container runtime on every installation, which excludes the Windows
  single-binary installation ADR-0208 protects.

### Option 5 — WebAssembly in the atlas process

- Good: isolation without a process; pure Go runtimes exist, so no cgo.
- Bad: the real Worker Types need sockets, database drivers, certificate stores and
  PowerShell, which WASI covers poorly or not at all; third-party code in the engine
  process contradicts ADR-0203 and ADR-0208.

## As built

**2026-11-05 — step 1, first slice.** The protocol client (`worker.go`, `query.go`,
`authorize.go`) moved from package `worker` to a new package `workersdk`. It imports no
engine package; `TestTheSDKImportsNothingOfTheEngine` walks its imports transitively and
admits only `logging` and `connector/lookup`, both leaves and both still to be removed
before `workersdk` becomes a module of its own. `BPMNError` is now defined in `workersdk`
and `job.BPMNError` is an alias of it; the retry policy is restated in
`workersdk.DefaultRetryBackoff` and held equal to `job.DefaultRetryBackoff` by a test.
Package `worker` keeps every old name as an alias. Not yet done in step 1: its own Go
module, the language-neutral protocol contract, the handshake, and moving the built-in
Worker Types off the engine packages they still import (`job`, `compiler`, `expr`, `dmn`).

**2026-11-05 — step 1, second slice.** `workersdk` imports nothing of atlas outside
itself. The form-lookup wire shape moved from `connector/lookup` to `workersdk/lookup`
(every importer follows; the package is unchanged). The worker's own warnings no longer
go through atlas's `logging` package: `Options.Logger` takes any `*slog.Logger`, nil
meaning `slog.Default()`, and each line carries `event=worker.poll_failed`
(`workersdk.EventPollFailed`), held equal to the catalogue's `logging.WorkerPollFailed` by
a test in package `worker`. The guard is now `TestTheSDKImportsNothingOfAtlas` and admits
only `workersdk` and its subpackages. The SDK's own Go module is deliberately deferred to
the licence decision (ADR-0536): a separate module
exists to carry a separate licence and version tag, and until that is decided it would
only cost CI and coverage plumbing; the package boundary and the guard already give the
technical separation.

**2026-11-05 — step 1, third slice: the protocol contract and the handshake.** The Worker
protocol is written down, language-neutral, in
[`docs/contracts/worker-protocol/v1`](../contracts/worker-protocol/v1/README.md), with the
compatibility rule: changes within a major version are additive; a server accepts the
current major version and the one before it; a poll naming no version is version 1. Every
job and query poll now carries `protocol` and, when the instance has one, `workerType`
(`id`, `version`, `digest`); the server refuses a version it does not accept and a
malformed identity with 400 and the reason, answers every poll with
`X-Atlas-Worker-Protocol`, and shows both per instance in `GET /api/v1/workers`
(`api/workerproto`, `workertype.CheckIdentity`). `atlas worker` reads its identity from
`ATLAS_WORKER_TYPE_ID`, `_VERSION` and `_DIGEST`, which the supervisor will set when it
launches an installed artifact (step 3). Comparing a reported digest with the installed
one is step 3's; the server checks shape only. Still open in step 1: the built-in Worker
Types' own dependencies on `job`, `compiler`, `expr` and `dmn`, which step 2 does not need
removed and step 4 removes with the server half.

**2026-11-05 — step 2: the worker-only executable.** `cmd/atlas-workers` builds
`atlas-workers`, which runs the worker command and nothing else; `atlas worker` runs the
same command, now in `internal/workercmd` (with the CA pool and outbound-proxy start-up
moved to `internal/trustroots` and `internal/sysproxy` so both executables share them).
The supervisor runs `atlas-workers` when it is a regular, executable file beside the
server binary, without the `worker` subcommand, and falls back to its own `worker` mode
otherwise; `ATLAS_WORKERS_BINARY` (`self` or a path) is the operator's override. The
strict script sandbox re-executes whichever binary hosts the script worker, so
`atlas-workers` answers `script-sandbox` too. Release archives, the container image and
`make server` ship both binaries. Measured on linux/amd64: `atlas` 88 MB, `atlas-workers`
35 MB. **What step 2 does not yet deliver:** the server binary still contains every
Worker Type, because some kinds still run in-process and the server half
(`api/connectorkinds.go`, `api/superviseenv.go`) imports their packages; that goes with
step 4. Nor is `atlas-workers` verified by digest before launch — it is trusted as the
server binary is, by sitting beside it; verified artifacts are step 3.

**2026-11-05 — step 3: verified local artifacts.** `workertype/install` keeps installed
releases content-addressed under `<data>/workertypes/<id>/<version>/<sha256>/`, written
atomically with the metadata last; a release is immutable (the same digest again changes
nothing, another is refused). The manifest gains `spec.artifacts`, one per platform, and
the executable media type `application/vnd.atlas.worker.executable`. An admin installs a
release with `POST /api/v1/worker-types/installed` (manifest and artifact for the
server's platform, verified against the manifest's digest before anything is kept),
lists them with `GET` and stops and removes one with `DELETE …/{id}/{version}`. The
supervisor launches each with no arguments, the launch contract of
[`docs/contracts/worker-protocol/v1`](../contracts/worker-protocol/v1/README.md#launched-by-atlas)
and an allowlisted environment — never the server's own — and verifies the digest again
before every start, refusing (state `refused`) an executable altered on disk. Because
signatures are not verified yet, the server refuses every install unless started with
`--allow-unsigned-worker-types`, and every release shows `signed: false`. The pilot is
`examples/workertypes/echo`, built against `workersdk` alone; a test builds it, installs
it, and has it complete a deployed process's job. **Not yet:** the `container` and
`kubernetes` launchers (step 6), placement per configured Worker, configuration and
credentials beyond the server token (step 4), process confinement for installed
artifacts (Landlock/seccomp on Linux, a job object on Windows), and the Console view
of installed releases; the API carries them.

**2026-11-05 — step 4, first slice: configuration as data.** A manifest carries its
configuration contract inline (`spec.configuration`, a JSON Schema object whose
`writeOnly` top-level properties are secrets; `workertype.ConfigurationSchema`). An
administrator configures Workers of an installed type with
`PUT|GET|DELETE /api/v1/worker-types/installed/{id}/workers/{name}`: settings are checked
against every installed release's schema, a secret is refused inline and given only as the
name of a vault entry that must exist, and the API never shows a secret's value. The
configured Workers are a run-loop-owned sidecar store. At every spawn the supervisor hands
the instance `ATLAS_WORKER_CONFIG` — each Worker's configuration with secrets resolved from
the vault and validated against that release's schema; one that fails is left out and
logged — and a change restarts the type's instances (`refresh` on a changed environment).
`workersdk.LaunchConfig` reads it, and `LaunchOptions` reports the Workers' names so the
Workers view shows them. No line of the server code involved knows a particular Worker
Type. **Not yet in step 4:** `validate-config`, `check-connection` and `diagnostics` as
worker queries; binding a BPMN task to one configured Worker of a model-authored job type
(a job naming no Worker is served by any instance, as before); the per-job credential
broker for installed types; and migrating the built-in Worker Types onto this path, which
starts with the types whose server half is smallest.

## Links

- refines [ADR-0203](0203-worker-execution-model.md): ends compiled-in embedded Worker
  Types
- partially supersedes [ADR-0207](0207-worker-type-packaging.md) (native executable is the
  baseline; OCI is optional) and [ADR-0208](0208-worker-type-packages.md) (atlas may
  launch verified third-party artifacts)
- extends [ADR-0499](0499-blueprint-registry-and-distribution.md) with a `worker-type`
  release kind, publisher signatures and revocation; relates to
  [ADR-0506](0506-marketplace-catalogue-bundles-and-updates.md)
- builds on [ADR-0007](0007-job-worker-protocol.md),
  [ADR-0157](0157-worker-processes-supervision-and-console.md),
  [ADR-0164](0164-no-in-process-service-tasks.md),
  [ADR-0168](0168-connector-work-on-a-worker.md),
  [ADR-0233](0233-in-process-connectors-refused.md),
  [ADR-0473](0473-form-remote-data-sources.md),
  [ADR-0475](0475-identity-aware-worker-execution.md)
- relates to [ADR-0299](0299-worker-type-admission-criteria.md),
  [ADR-0303](0303-script-sandbox-isolation.md),
  [ADR-0437](0437-a-package-is-imported-from-the-command-line.md),
  [ADR-0483](0483-technical-capability-conformance-governance.md)
- [Engine invariants](../architecture/invariants.md)
