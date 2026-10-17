# ADR-DRAFT: Atlas installations are managed by a process application, and kept running by their platform

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-17
- **Deciders:** Atlas maintainers
- **Open question:** three facts this record could not establish. (1) The interface of
  the ROS platform, one of the four substrates named by the requester; its adapter is
  therefore named but not specified. (2) Whether an installation may be started on an
  older binary after a newer one has written its data directory; the upgrade below
  treats rollback as a restore for that reason. (3) The fleet size: the requester
  estimates about twenty domains of about ten installations each and calls the figure
  uncertain. The choice of one management installation per domain rests on it.
- **Question checked:** 2026-10

## Context and problem statement

Atlas installations are multiplying. A business domain needs at least DEV, INT and
PROD, and the expected estate is on the order of twenty domains with about ten
installations each — some two hundred `atlas serve` processes, each owning one data
directory (`docs/install.md:47-50`). A hoster that runs Atlas for others has to look
after several domains at once.

Nothing in Atlas creates, starts, stops, upgrades or deletes an installation. What
exists works *between* installations that are already running:

- [ADR-0129](0129-remote-deployment-targets.md) promotes an application release to a
  registered peer and reads its deployments back (`/api/v1/targets`,
  `/api/v1/applications/{id}/releases/{version}/promote`).
- The node descriptor (`GET`/`PUT /api/v1/node`, `api/node.go`) gives each
  installation a runtime id, an operator-owned name, environment and up to twenty
  labels, its version and the features it serves.
- [ADR-0453](0453-application-values-per-installation.md) holds the values DEV, INT
  and PROD each answer for themselves.
- [ADR-0437](0437-a-package-is-imported-from-the-command-line.md) equips an
  installation from a package on the command line, designed for CI.
- [ADR-0402](0402-one-estate-several-nodes.md) stitches several installations into one
  picture, and rejects a central master *for that picture*.

Every installation is created and run by hand on one of four substrates: Kubernetes
through the shipped Helm chart (a one-replica StatefulSet,
`deploy/helm/atlas/templates/statefulset.yaml`), Docker, a Windows service wrapped by
WinSW or NSSM because `atlas.exe` does not speak the Service Control Manager protocol
(`docs/install.md:729-757`), and a platform the requester calls ROS.

The binary deliberately does not fill the gap.
[ADR-0157](0157-worker-processes-supervision-and-console.md) lets Atlas spawn only its
own executable and "declines to fight a platform";
[ADR-0207](0207-worker-type-packaging.md) and [ADR-0208](0208-worker-type-packages.md)
refuse to make Atlas a general command supervisor or a container orchestrator. The
supervisor in `api/supervisor.go` starts `atlas worker` and nothing else.

The request that prompted this record asks whether the management could be an Atlas
process application — eating our own dogfood, as the system processes already do
([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md),
[ADR-0411](0411-system-processes-call-atlas-directly.md)) — present only where it is
installed, possibly as its own process and port.

The question: **what manages the lifecycle of many Atlas installations across several
substrates and domains, where does it run, and which parts of that lifecycle belong
to Atlas at all?**

## Decision drivers

- **Nothing in the engine.** No invariant moves. Whatever is built is content,
  workers and calls to routes that exist; a product change it needs is its own record.
- **Losing the manager loses management, never the fleet.** A managed installation
  keeps running, restarting and serving while its manager is down. Recovery of Atlas
  must not depend on Atlas.
- **The log records decisions and operations, not the passage of time.** An
  event-sourced engine persists every transition; a loop that polls two hundred
  installations writes a history proportional to time multiplied by the fleet, and
  buys nothing a kubelet or a service manager does not already provide.
- **The domain is the unit people are responsible for.** Domain operators act on their
  domain's installations and on nothing else; a hoster acts on several domains.
- **Only where it is wanted.** An installation that is not a manager carries no trace
  of management: no routes, no menu entries, no processes in its system project.
  ADR-0434 records what shipping an area unconditionally costs.
- **Four substrates, and more to come.** The lifecycle must not be written once per
  substrate.
- **Credentials stay out of the log.** No token, password or archive passes through a
  process variable.
- **Destructive operations are governed.** Stopping or deleting a production
  installation is a decision somebody is accountable for, and it is recorded.

## Considered options

For **who manages the lifecycle**:

1. **Platform tooling only** — GitOps (Helm with Argo CD or Flux) on Kubernetes,
   scripts elsewhere; Atlas is not involved.
2. **A Kubernetes operator** with an `AtlasInstallation` resource.
3. **Lifecycle code in the binary** — an `atlas fleet` command, or a supervisor that
   spawns and watches `atlas serve`.
4. **A process application that does everything**, including keeping installations
   alive by polling and restarting them.
5. **A process application that orchestrates the lifecycle, while each platform keeps
   its installations running.**
6. **Fewer installations** — serve several domains from one installation per stage,
   so that there is little left to manage.

For **where the management runs and how far it reaches**:

- **A.** One management installation for every domain, with domain operators confined
  to their domain inside it.
- **B.** One management installation per domain; a hoster's management installation
  manages those managers and any domain that does not manage itself.
- **C.** A second listener, on its own port, inside an existing installation.

## Decision outcome

Chosen: **option 5, with scope B**. A process application on a dedicated management
installation orchestrates the lifecycle; each platform keeps its installations
running; the domain is the management scope.

### What belongs to whom

| Part of the lifecycle | Character | Owner |
|---|---|---|
| Keeping a process alive, restarting it, mounting its volume | continuous, mechanical | the substrate |
| Creating, starting, stopping, upgrading, deleting | episodic, ordered, may fail half-way | the process application, acting through an adapter |
| Labels, constants, secrets, tokens of a new installation | episodic, ordered | the process application, through the installation's own API |
| Content: applications and releases | episodic | unchanged — ADR-0129 promotion and ADR-0437 import |
| Request, approval, four-eyes for production, audit | human, long-running | the process application |
| Observing version, health and drift | continuous | deployment-target observation that exists (Panorama, ADR-0189) |

The line runs between **orchestrating a lifecycle operation** and **holding a desired
state**. Atlas is built for the first: an ordered sequence with retries, incidents,
human tasks, timers and a durable record of who decided what. It is a poor controller
for the second, which Kubernetes and the service managers already are.

### The management installation

A management installation is an ordinary Atlas installation that carries the fleet
application. It is never also a domain's working installation. It holds the most
dangerous rights in the estate, and it must be upgradable, and able to fail, on its
own schedule.

The "own process and port" of the request is answered at this level and not by a
second listener (scope C). A second port in the same binary shares the run loop, the
data directory, the vault and the user store, so it isolates nothing.
[ADR-0204](0204-hosted-apps-on-an-isolated-origin.md) (proposed) considers a second
origin only to separate browser origins, which is not this problem.

A management installation is itself run by plain platform tooling — the Helm chart, a
service wrapper, or a manager one level up. It never performs a lifecycle operation
on itself: the fleet model compares a target's runtime id with `server.id`
([ADR-0456](0456-constant-namespaces.md)) and refuses.

### The domain is the scope, and the installation is its boundary

Scope A would need domain operators to see only their domain's instances of a shared
definition. That is not expressible today. Reading an instance follows the
application its definition was deployed from
([ADR-0275](0275-instance-visibility.md), [ADR-0071](0071-sharing-scopes.md)). A
process id names one definition per server
([ADR-0019](0019-durable-deployments.md)), so the same models cannot be filed into
twenty domain applications on one installation. Making scope A work means a new
instance-level authorization axis, which is the most sensitive code in the server.

Scope B uses the one boundary Atlas already enforces without exception: the
installation, with its own data directory, vault and users.

- **One manager per domain is the default.** With about ten installations per domain,
  that is ten lifecycles per manager.
- **A hoster runs a manager whose managed installations are other managers.** It also
  manages any domain that does not operate itself. This is recursion, not a new
  concept. A manager may manage several domains whenever the same people answer for
  them.
- **The overhead is one installation per self-managing domain.** That is about twenty
  on two hundred. It is the price of not touching the authorization model, and it is
  revisited if the estimate in the open question proves wrong.

Every managed installation names its manager in its node labels: `atlas.fleet/domain`,
`atlas.fleet/stage` and `atlas.fleet/managed-by`, the last holding the manager's
runtime id.

- **One manager per installation.** A manager refuses to adopt an installation whose
  `managed-by` names another runtime. It refuses to adopt the installation its own
  `managed-by` names, which rules out the two-manager cycle.
- **A claim, not a lock.** The label is a claim the managers honour. An administrator
  of the managed installation can remove it, and the record does not pretend
  otherwise.
- **Labels are merged, never overwritten.** `PUT /api/v1/node` replaces the whole label
  map (`api/node.go:276-277`), so the fleet reads, merges and writes, and keeps the
  labels an operator set.
- **A lost manager can be rebuilt by discovery.** Because the installations state who
  manages them, a lost manager's register is found by asking the substrates and the
  installations. What is lost with it is history, not the fleet.

### One lifecycle instance per managed installation

Each managed installation has one long-running process instance on its manager, in
the shape [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)
and [ADR-0428](0428-a-product-lifecycle-may-run-as-one-instance-per-position.md)
established for products. It is created by *provision* or *adopt*, receives the later
operations as messages correlated by the installation's key, and ends with
*decommission* or *release*. Its history is the installation's audit trail and grows
with operations, not with time. A changed model reaches running lifecycles through
instance migration ([ADR-0162](0162-process-instance-migration.md)).

The operations, and the guards each one carries:

- **Provision.**
  1. A request form; a policy decision (DMN) on version, size and substrate for the
     stage; an approval, from two people for a production stage.
  2. The substrate adapter creates the installation with its start-time settings.
  3. The fleet waits, with a bounded timer, until `GET /api/v1/node` answers.
  4. The peer adapter writes the API state: labels, constants, secrets, the targets
     this installation promotes to.
  5. Content arrives over the existing paths, and the fleet verifies the result.
- **Adopt.** The same configuration steps, applied to an installation that exists, so
  that the estate already running can be brought under management.
- **Start / stop.**
  - Stop is the substrate's graceful stop, which reaches `atlas serve` as SIGTERM
    (`cmd/atlas/main.go:393`). Its supervised workers end outright, and their jobs
    return when their leases expire ([ADR-0439](0439-supervised-workers-end-with-the-server.md)).
  - An adapter never starts a process while one may still own the same data directory,
    because two will corrupt it (`docs/install.md:47-50`). An upgrade is therefore
    stop-then-start, never start-then-stop.
- **Upgrade.** A full snapshot first ([ADR-0109](0109-full-instance-snapshot.md)), then
  stop, swap the version, start and verify the version the node descriptor reports.
  Rollback is a restore of that snapshot, not a downgrade (open question 2).
- **Snapshot.** `GET /api/v1/backup/full`, written by the peer adapter straight to the
  archive store. The archive never passes through a variable.
- **Decommission.** Two-person approval, a retention decision by the domain owner, an
  archived full snapshot that has been read back, and only then the substrate's delete.
  The portable backup is not enough: it carries design-time data only
  ([ADR-0107](0107-backup-and-restore.md), [ADR-0357](0357-a-portable-backup-does-not-overwrite-another-installations-identity.md)),
  and a deleted volume takes the log and the state with it.
- **Release.** The manager removes its `managed-by` claim and ends the lifecycle; the
  installation keeps running unmanaged.

### Substrate adapters: one contract, a worker per platform

The lifecycle model is substrate-agnostic. It calls a fixed contract — `provision`,
`start`, `stop`, `upgrade`, `delete`, `describe` — as service tasks whose job types
name the operation and the substrate. Which substrate an installation runs on is part
of its record and selects the adapter. The routing mechanism — a gateway, or one call
activity per substrate — is settled in the slice that builds it.

Each adapter is an external worker
([ADR-0203](0203-worker-execution-model.md), [ADR-0208](0208-worker-type-packages.md)
`external` mode), placed near its substrate and holding that substrate's credentials
([ADR-0168](0168-connector-work-on-a-worker.md)). The engine never holds a kubeconfig,
a Docker socket or a Windows administrator password, and side effects stay off the
engine ([ADR-0164](0164-no-in-process-service-tasks.md)).

- **Kubernetes** installs, upgrades and removes the shipped Helm chart, or writes its
  values to a GitOps repository and waits for the controller. An operator-backed
  implementation (option 2) fits behind the same contract and is not precluded.
- **Docker** drives the Docker Engine API on its host. Access to that socket is
  root-equivalent on the host, so the adapter runs there and nowhere else.
- **Windows service** installs and controls the WinSW or NSSM wrapper with PowerShell.
  It runs as a dedicated worker on the host with the right to manage services, not as
  a script task on a shared script worker: the strict script sandbox is Linux-only
  ([ADR-0303](0303-script-sandbox-isolation.md)).
- **ROS** gets an adapter once its interface is known (open question 1).

Settings split by where they live:

- **Start-time settings** — authentication, federated login
  ([ADR-0210](0210-federated-authentication.md)), TLS, trusted proxies, the
  administrator seed — are flags and environment, so the substrate adapter renders
  them at provision and upgrade.
- **API state** — labels, constants, secrets, targets, tokens — is written by the peer
  adapter once the installation answers.

### The peer adapter holds the credentials of the managed installations

A REST connector task names its credential as a fixed secret reference compiled at
deploy time (`compiler/parse.go:134-140`). One model therefore cannot choose, per
call, between the tokens of ten or two hundred installations. Passing the token as a
variable would write it into the log.

So a **peer adapter** — a worker of the fleet application — makes every call to a
managed installation. It holds that installation's credential in the substrate's
secret store (a Kubernetes Secret, the host's credential store), keyed by
installation. The process carries only the installation's key. This is ADR-0168's
split applied once more: the engine decides *what* is sent; the worker knows *where*
and *with which credential*.

The calls are ordinary routes that already exist:

- `PUT /api/v1/node` and `PUT /api/v1/secrets/{name}`, both admin;
- the constants of ADR-0453;
- `POST /api/v1/targets` and the promotion route of ADR-0129;
- `GET /api/v1/backup/full`.

### Observation reuses what exists, and only transitions enter the log

A manager registers each managed installation as a deployment target with a read
credential (the ADR-0129 amendment). The existing landscape observation then reads
those installations off the run loop, with bounded concurrency and per-target
isolation, and keeps a drift journal of transitions rather than samples (ROADMAP,
Panorama P4c and P5a). The fleet application does not poll in the engine.

Turning an observed drift into a task or an incident on the lifecycle instance is
deferred to a later slice. When it comes, it must deliver only the transition as a
message, never one record per look.

### Content paths are unchanged

The manager adds no way of moving content. A domain keeps promoting releases from its
own installation (ADR-0129) or equipping installations from git in CI (ADR-0437).

The manager may *trigger* those paths — a promotion to production behind a two-person
approval — and it is where the per-installation constants of ADR-0453 are declared,
because those are exactly the values that must not travel with a release.

Whether a manager should also become its domain's release hub is not decided here.

### Present only where installed

The fleet application ships as a package in the ADR-0437 format and is imported onto a
management installation with `atlas import`. It is content and external workers, so
it needs no start flag: an installation it was never imported into has no trace of it.

It is deliberately **not** a system process
([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)), because those
are bootstrapped onto every installation. Should product code be added for it later —
routes or Console views — that code arrives behind a start flag in the manner of
[ADR-0434](0434-the-catalogue-can-be-switched-off.md), **off** by default.

### Product changes this decision depends on

Each of these needs its own record:

- **A machine credential at first start.** A fresh installation seeds only an
  administrator from `ATLAS_ADMIN_USERNAME`/`ATLAS_ADMIN_PASSWORD`
  (`api/auth.go:578-627`). API tokens are minted afterwards by an administrator
  (`POST /api/v1/api-tokens`, [ADR-0194](0194-api-tokens.md)).
  - Until a record lets the substrate hand an installation a pre-generated token at
    first start, the peer adapter seeds the administrator password through the
    substrate's secret (the Helm chart's `atlas.auth.existingSecret`), signs in over
    `POST /api/v1/auth/login` and mints a scoped token.
  - That interim path has not been exercised end to end, and it leaves an
    administrator password the fleet must rotate.
- **A typed Worker Type per substrate, if any.** Not before the adapter contract has
  carried real installations. A Worker Type in `connector/` would then have to meet
  the admission criteria proposed in [ADR-0299](0299-worker-type-admission-criteria.md).

### Slices

1. **One domain, one manager, Kubernetes.** The lifecycle model; the Kubernetes and
   peer adapters; provision, adopt, start, stop and decommission; the package and its
   import. The bootstrap-credential record runs in parallel.
2. **Docker and Windows-service adapters; upgrade and snapshot archiving.**
3. **The hoster level:** managers managing managers, discovery-based rebuild of a lost
   register, drift delivered as transitions.
4. **The ROS adapter**, once its interface is known.

### Consequences

- **Positive:**
  - No engine change and no invariant touched.
  - The lifecycle is written once for four substrates.
  - Every operation on a production installation carries its approval and its actor in
    a durable history.
  - The fleet survives its manager.
  - Domain delegation needs no new authorization axis.
  - Atlas runs its own operations as Atlas processes, which surfaces its gaps where we
    feel them first.
- **Negative / trade-offs accepted:**
  - About one extra installation per self-managing domain.
  - A manager holds administrator credentials to every installation it manages, and
    substrate rights to delete them. Its compromise is the estate's compromise, which
    is why it runs on its own installation with separate credentials per stage.
  - The `managed-by` claim is honoured, not enforced.
  - The interim bootstrap path leaves an administrator password to rotate.
  - Lifecycle instances live for years and need migration when the model changes.
- **Follow-ups / risks to watch:**
  - The bootstrap-credential record.
  - Upgrade rollback (open question 2).
  - Drift reaction.
  - Whether scope A becomes worth an instance-level authorization axis once the fleet
    is larger than estimated.
  - Whether a manager should become a domain's release hub.
  - Cycles longer than two managers, which the adoption rule does not rule out.

## Pros and cons of the options

### Option 1 — platform tooling only
- Good: declarative, versioned and reviewable; self-healing on Kubernetes; the Helm
  chart exists; no new code; no circular dependency between Atlas and its own
  recovery. For a handful of installations on one substrate it is enough, and this
  record does not replace it — it is the substrate layer.
- Bad: the Atlas-specific steps after install (credential, labels, secrets, constants,
  targets, content, verification) are ordered imperative work that Helm hooks carry
  badly; there is no governed request and approval beyond a pull-request review;
  Windows services and plain Docker are left to per-host scripts; four substrates mean
  four lifecycles.

### Option 2 — a Kubernetes operator
- Good: the native shape on Kubernetes; continuous reconciliation; ADR-0175 (proposed)
  already foresees an operator for partition cells.
- Bad: covers one substrate of four; no approvals; a second codebase with its own
  release cycle. It remains welcome *behind* the Kubernetes adapter.

### Option 3 — lifecycle code in the binary
- Good: typed, tested, one binary; the supervisor's restart and backoff code exists.
- Bad: contradicts ADR-0157, ADR-0207 and ADR-0208; spawning `atlas serve` locally
  works on one host only; it rebuilds what the platforms already do, inside the
  process whose failure it would have to survive.

### Option 4 — a process application that also keeps installations alive
- Good: maximal dogfooding; everything visible in Operations.
- Bad: a liveness loop over the fleet writes a history proportional to time multiplied
  by the fleet and duplicates the kubelet and the service manager; a manager outage
  would become a fleet outage.

### Option 5 — orchestration in a process application, liveness in the platform (chosen)
- Good: each layer does what it is built for; the engine stays untouched; governance
  and audit come from what Atlas already is.
- Bad: two layers to understand; adapters to write per substrate; the bootstrap
  credential needs a product change.

### Option 6 — fewer installations
- Good: operating cost, upgrades and monitoring scale with the number of installations;
  Atlas has sharing scopes, object authorization and reach-bounded tokens
  ([ADR-0410](0410-a-peer-credential-carries-the-reach-a-membership-cannot-give-it.md)).
- Bad: an installation is one writer with one partition (`api/node.go`; scale-out in
  [ADR-0175](0175-replicated-partition-cells.md) is proposed, not started), so domains
  sharing one would share its throughput, its incident floods and its upgrade date.
  Not rejected as a practice; rejected as the answer to this question, because the
  installations exist and are expected to grow.

### Scope A — one manager, domains confined inside it
- Good: one installation for the hoster; one place to look.
- Bad: needs instance visibility by domain within one definition, a new authorization
  axis beside ADR-0275; until then every operator of the manager sees every domain.

### Scope B — a manager per domain, a hoster's manager above (chosen)
- Good: the installation boundary already isolates data, vault and users; a domain can
  run its own manager without a hoster; the hoster level is the same application one
  level up.
- Bad: one extra installation per self-managing domain; a cross-domain view needs the
  estate picture (ADR-0402) rather than one list.

### Scope C — a second listener in an existing installation
- Good: no extra installation.
- Bad: shares the run loop, data directory, vault and users with the installation it
  sits in, so it isolates nothing; it puts the estate's most dangerous rights inside a
  domain's working installation.

## Links

- relates to [ADR-0128](0128-process-applications.md) — the process application this
  record ships the fleet as
- relates to [ADR-0129](0129-remote-deployment-targets.md) — promotion and the
  deployment targets observation reads
- relates to [ADR-0402](0402-one-estate-several-nodes.md) — the estate picture stays
  derived; the manager is an actor for the lifecycle, not the source of that picture
- relates to [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md) and
  [ADR-0411](0411-system-processes-call-atlas-directly.md) — the dogfooding precedent,
  and why the fleet is not a system process
- relates to [ADR-0157](0157-worker-processes-supervision-and-console.md),
  [ADR-0207](0207-worker-type-packaging.md), [ADR-0208](0208-worker-type-packages.md) —
  why the binary does not manage installations itself
- relates to [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)
  and [ADR-0428](0428-a-product-lifecycle-may-run-as-one-instance-per-position.md) — the
  lifecycle-instance shape
- relates to [ADR-0434](0434-the-catalogue-can-be-switched-off.md) — how product code
  for the fleet would be switched, the other way round
- relates to [ADR-0437](0437-a-package-is-imported-from-the-command-line.md) — how the
  fleet application reaches a manager
- relates to [ADR-0453](0453-application-values-per-installation.md) — the
  per-installation values a manager declares
