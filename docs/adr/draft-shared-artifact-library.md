# ADR-DRAFT: The shared artifact library — a library application, not a global flag

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether a library process needs an information model of its own, and how a data object typed against the *consuming* application's vocabulary is validated across a call into a library process — ADR-0230's vocabulary is per application, and `applicationProcessesOnLoop` returns nothing for an artifact outside one
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0034](0034-projects-and-artifacts.md) made membership in a project an
**optional tag** on an artifact, and an artifact carrying no tag falls into an
implicit bucket the Modeler labels *Not assigned*.
[ADR-0071](0071-sharing-scopes.md) then gave that bucket a meaning it did not
have before: an ungrouped artifact is governed by its own `OwnerID` and is, under
`--auth`, **its creator's personal space** (`api/artifactscope.go`,
`ungroupedRole`). The label says "nothing has been decided about this artifact";
the enforcement says "this belongs to one person".

Meanwhile, three facts about how Atlas actually runs have drifted away from that
picture:

1. **Reuse across applications is already the runtime behaviour, everywhere.** A
   call activity resolves purely by process id — `latestDeploymentByProcessID`
   (`api/callactivities.go`), no application filter. A form is fetched by id with
   no scope check at all, deliberately, because rendering a form for a running
   task is execution and not authoring (`api/forms.go`, `handleGetForm`; the
   reasoning is written down in `api/artifactscope.go`). The decision picker in
   the Modeler offers the whole catalogue on purpose, and says so in a comment:
   project scoping organises artifacts, it does not gate reuse
   (`api/web/editor.js`). The form picker and the call-activity suggestion list
   fetch `/api/v1/forms`, `/api/v1/processes` and `/api/v1/drafts` unfiltered.
2. **The id namespace is already global.** A draft is keyed by its process id, a
   form by its form id, a DMN reference by its id — one record per id in one
   directory each, with a save onto an occupied id refused
   ([ADR-0222](0222-artifact-id-renames.md), `api/artifactids.go`). There is no
   per-application namespace to fall back on, and there never was.
3. **Nothing makes a cross-application dependency visible.** An application's
   source export takes exactly the artifacts tagged with its own id
   (`api/appsource.go`). A process in application A that calls a process in B, or
   binds a form from B, exports with a **dangling reference and no diagnostic**:
   the manifest does not mention it, and the publish preflight
   (`api/dmnvalidate.go`, `handleValidateProject`) only validates the DMN
   references filed into A. The gap surfaces on the target server of a promotion
   ([ADR-0129](0129-remote-deployment-targets.md)) as a call activity that parks
   for a callee that is not there.

So users are asked to file every artifact into exactly one application, while the
engine has always treated the whole server as one pool, and the moment they use
that pool the portability story quietly breaks.

The request this record answers is to stop pretending: make artifacts that are
meant for every application an **explicit, first-class thing** — so that forms,
decisions and processes can be offered once per Atlas instance and called from
any application — while an application's own artifacts stay local and travel with
it. A second, smaller request rides along: **folders inside an application**, so a
large application can be structured. ADR-0034 listed nested folders as a later
slice and left open "whether folders warrant real nesting or stay a flat label";
`ROADMAP.md` still carries the item.

The question: **what is the unit of shared, reusable design-time content, how does
an application that depends on it stay portable, and does any of it belong to a
different server?**

## Decision drivers

- **Do not turn a private space into a public one by renaming it.** Relabelling
  *Not assigned* as *Global* would, under `--auth`, publish every modeller's
  personal drafts, forms and DMN references to everyone on the server in one
  release. Whatever shape is chosen, "shared" must be a **third state somebody
  opts into**, never a reinterpretation of the second, and no existing record may
  change meaning without an explicit act by its owner.
- **A dependency needs a name and a version.** The thing this record exists to fix
  is an *invisible* dependency. Replacing it with a visible one is only progress
  if the visible one can be written into a manifest, pinned, and moved to another
  server — which means the shared content needs an identity that survives a clone,
  the way [ADR-0134](0134-git-backed-applications.md)'s portable key does.
- **One instance per domain is the estate model.** It is the premise the
  cross-instance epic states outright
  ([ADR-0369](0369-cross-instance-message-addressing.md)) and the one the business
  architecture already builds on, where an application is the unit of a business
  capability ([ADR-0305](0305-business-capabilities-and-value-streams.md),
  `docs/architecture/business-architecture.md`). A library is therefore scoped to a
  domain by construction, and "shared across the estate" is a second question with a
  different answer — which this record settles as a copy per domain rather than a
  lookup across domains.
- **Design-time only; the invariants stay untouched.** Like ADR-0034, ADR-0071 and
  [ADR-0128](0128-process-applications.md), this is an organising layer below the
  HTTP API. Nothing here enters the event log, the WAL, the processor or
  `applyToState`, and nothing changes what the engine resolves at runtime — the
  engine already resolves globally.
- **Reuse the mechanisms that exist.** A protected, server-wide, visible-to-all
  application already exists and is already bootstrapped at startup
  ([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md),
  `api/systemproject.go`). Sharing, export, releases, promotion and git binding
  are all defined on the application. A fourth concept that has to re-earn all of
  them is a worse deal than a flag on the one that already has them.
- **Do not spend the word "scope" a third time.** ADR-0071 already had to
  disambiguate its *sharing scope* from ADR-0068's *variable scope*. A third
  meaning — visibility to other applications — would make the word useless. This
  record says **library**.
- **Backward compatible, no migration.** Every existing project, tag and sidecar
  file keeps working untouched; the new state is additive and opt-in.
- **Incremental slices.** The library flag, the dependency manifest and folders
  are three independently shippable changes, in that order of value.

## Considered options

For **what "available to every application" attaches to**:

1. **A `scope: "global"` field on each artifact record.** The draft, form and DMN
   reference records gain a value that means "no application owns this, everyone
   may use it".
2. **A library application** — an ordinary application (ADR-0034 / ADR-0128)
   carrying a `Library bool`, readable by every principal, writable by its members,
   published and versioned like any other.
3. **Keep it implicit.** Leave *Not assigned* as it is and accept that
   cross-application reuse works but is invisible.
4. **A new container above applications** (a workspace or shared tenant that owns
   applications and artifacts).

For **how a dependent application records what it uses**:

- **a.** The manifest names each external artifact with the key of the library it
  comes from and the library release it was published against.
- **b.** Nothing is recorded; a missing artifact is discovered on the target
  server.

For **whether shared artifacts resolve across servers**:

- **i.** No. An artifact is local to the Atlas instance that serves it; content
  reaches another server by being **pushed** to it (ADR-0129) or read from git
  (ADR-0134), and is materialised in the target's own stores before anything runs.
- **ii.** Yes: a reference becomes a URI, and the engine resolves it over the
  network when it is needed.

## Decision outcome

Chosen: **option 2 (a library application)**, with **a** for the dependency
record, and **i** — local materialisation — for the cross-server question.

### A library is an application with one flag

The `project` record gains a `Library bool` (omitempty, so every existing record
is unaffected — the additive pattern ADR-0044/0071/0122 already use). A library
application differs from an ordinary one in exactly three ways:

- **Everyone may read it.** `effectiveRole` grants every authenticated principal
  at least `viewer` on a library, whatever its `Visibility` says. Write access is
  unchanged: owner and members only. Marking an application as a library is
  therefore a deliberate act of publication, and un-marking one is a narrowing
  that must be refused while another application still depends on it.
- **It is offered everywhere.** The Modeler's form, decision and call-activity
  pickers group their options as *this application*, then *libraries*, then the
  rest. This is presentation only; all three pickers already fetch unfiltered
  lists, so nothing they can reach today becomes unreachable.
- **It is a dependency, not a member.** A library's artifacts are never exported
  as part of a consuming application. They are named in its manifest instead
  (below).

Everything else about a library is an application: it has a portable key, members,
a release history, a bundle publish, a git binding and — once the third slice
lands — folders. *Not assigned* keeps the meaning ADR-0071 gave it and is
untouched by this record. An ungrouped artifact becomes shared by being **moved
into a library application**, which is a move the existing `PATCH` handlers
already perform and already authorise.

This is deliberately the opposite trade from a per-artifact flag: it costs a
container and a publish step, and it buys the thing the flag cannot give — a
dependency with a name, a key and a version number.

### The manifest states what an application depends on

`sourceManifest` (`api/appsource.go`) gains a `dependencies` section: per external
artifact, its kind, its id, the **portable key** of the library that owns it, and
the library **release version** the application was last published against. The
format version is raised, and the round trip stays byte-stable, which is the
property ADR-0134 already asserts with a test.

Two checks follow from it, and they are the point of the whole record:

- **Publish preflight.** `handleValidateProject` walks each member process for
  call-activity targets, bound form ids and decision references, and reports every
  one that resolves outside the application: in a library (fine — recorded as a
  dependency), or nowhere and in no library (a finding). This turns today's silent
  dangling export into a diagnostic at the moment it is created.
- **Import and promotion.** Importing a source tree, or promoting a release to a
  target, refuses when a named dependency is absent on the receiving server, and
  says which library and which release is missing. A refusal that names the
  missing library is the whole improvement over a call activity that parks.

The import guard in `api/appsource.go` also needs one correction that is a
prerequisite, not a nicety: it refuses an artifact that belongs to *another*
application, but an artifact with an empty `ProjectID` is **absorbed silently**.
A library-owned artifact must be refused the same way an application-owned one is,
or the first git import of a consuming application swallows the library.

### Cross-server: content is pushed and materialised, never resolved over the wire

A reference stays a **local identifier resolved against local state**. It does not
become a URL, and the engine never fetches an artifact from another server in
order to run something. This is a decision, not an omission, and it rests on three
separate arguments:

- **A call activity cannot be remote at all.**
  [ADR-0076](0076-call-activities.md) starts the callee as a **child process
  instance in the caller's partition**, linked by a caller key, and resumes the
  caller when that child completes. A process on another server cannot be that
  child: it is not in this partition, so it is not reachable under the
  single-writer rule ([ADR-0002](0002-single-writer-partition-model.md), I3), and
  its completion is not this log's fact. A URI would not make a remote process
  callable; it would make an unimplementable call *look* configurable. Invoking
  work on another Atlas is a different modelling act with different failure
  semantics — a service task, or a message pair — not a call activity with a
  longer target string.
- **Resolution is a deploy-time step, on purpose.** Invariant I5 — compile, don't
  interpret — puts XML parsing, validation and binding at deploy time so the hot
  path does none of it. A reference resolved over the network at runtime moves a
  guaranteed local lookup onto a remote server's availability, latency and
  version, on the processor's path. A URI also does not deliver what it promises
  in the request: it makes a reference **addressable**, not **resolvable**. The
  target still has to be reachable, authorised, compatible and, for a decision,
  the *same version* the model was validated against.
- **Atlas already has the cross-server mechanism, and it is a push.**
  ADR-0129 defines the deployment target, the scoped deploy token and the bundle
  import; ADR-0134 defines the git route. Both move content to the other server so
  it can be validated and deployed **there**, against its own registry and its own
  operator's overrides ([ADR-0105](0105-per-server-call-activity-target-overrides.md)).
  A library is exactly the unit these two mechanisms want: one key, one release,
  one push. "Same library, release v7, on Test and on Production" is a statement an
  operator can verify; "the URI resolved when we tried it" is not.

There is one honest exception, and it is already built and already limited: a
**DMN model file** may come from a remote source at *deploy* time, through
`dmn.ServiceResolver` and `ATLAS_DMN_RESOLVER_URL`
([ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)). Note what it does *not*
do — the `modelRef` is a bare handle, and `safeModelRef` rejects anything with a
path separator so a reference cannot address an arbitrary URL. The base URL is
**operator configuration**, the handle is model data, and the two are kept apart
on purpose. Letting a reference inside a BPMN or DMN file carry a full URI would
collapse that separation and make every imported model a request the server will
make on its author's behalf — the outbound-request surface ADR-0069/0070 keep
behind vault-held, operator-configured targets, and the origin question
[ADR-0186](0186-embed-public-forms-cross-origin.md) and
[ADR-0204](0204-hosted-apps-on-an-isolated-origin.md) both answered restrictively.

Two narrower uses of a URI are **not** refused here, because neither puts the
engine on the network: a *client* fetching a form schema cross-origin is already
possible under ADR-0186's operator allow-list, and a decision evaluated on a
remote service is already a modelled service call
([ADR-0050](0050-temis-decision-connector.md)) rather than a resolved reference.

Nor does any of this leave "reach a process on another node" unanswered — it is
answered elsewhere, and deliberately not by a reference. The cross-instance epic
([ADR-0369](0369-cross-instance-message-addressing.md) to
[ADR-0374](0374-white-box-participant.md)) settles it as **collaboration**: the
publisher publishes a versioned *interface* rather than its model
([ADR-0373](0373-published-process-interface.md)), a participant in the consumer's
model names that interface while the server says which node it is
([ADR-0371](0371-participant-binds-a-published-interface.md)), and the message flow
compiles to a job a reserved Worker Type carries
([ADR-0372](0372-peer-message-delivery-worker.md)). That is the same conclusion this
record reaches from the other side: what crosses a node boundary is a message with a
contract, never a reference that resolves. A library moves *content* between nodes; the
epic moves *messages* between them. Neither is a remote call activity, and the two do
not overlap.

### Folders are a path, never a namespace

An artifact record gains an optional `Folder string`: a display path within its
application, `/`-separated, with no leading or trailing separator. It is
presentation and organisation only.

The identity of an artifact stays its globally unique id. This is the load-bearing
half of the decision: the moment a folder took part in identity, the store key
would change, and with it the draft store's key function, every `zeebe:calledElement`
in every deployed model, every form binding, and the id-availability probe — a
migration across all of them for a tree view. A folder is a label; two artifacts in
different folders still cannot share an id, and the existing collision refusal
(ADR-0222) is unchanged.

The source tree absorbs it without a new concept: `sourceProcess.Path` already
carries a per-artifact path, today flat as `processes/<slug>.bpmn`. A folder
extends it to `processes/<folder>/<slug>.bpmn`, and the byte-stable round trip
asserts that it survives export and import.

### Slices

1. **The library flag.** `Library bool`, the read grant in `effectiveRole`, the
   un-mark refusal, the import guard correction, and the picker grouping. Ships
   alone and is useful alone: shared content stops being somebody's personal space.
2. **The dependency manifest.** The manifest section, the publish preflight, and
   the import/promote refusal. This is the slice that repays the container.
3. **Folders.** The field, the tree in the Modeler, the path mapping in the source
   tree.

### Consequences

- **Positive:** shared content gets an owner, a name, a version and a release
  history instead of being a bucket that means "unfiled" and enforces "private".
  A cross-application dependency becomes visible at publish time rather than at
  runtime on another server. Nothing an author can reach today becomes unreachable;
  the change is that the reach is now stated. The sharing, export, release,
  promotion and git mechanics are inherited whole rather than rebuilt. The
  invariants are untouched: no event, no hot path, no change to what the engine
  resolves.
- **Negative / trade-offs accepted:** a library must be published, so a shared form
  now has a deploy step where an ungrouped one had none, and a consuming
  application has an ordering constraint on a fresh server. The manifest gains a
  second kind of entry and another way for an export to be refused. A library is a
  read grant to every authenticated principal, which is a genuine widening — it is
  opt-in and per application, but it is real. And because a node serves one domain, a
  library is a **domain's** library: content meant for the whole estate exists as one
  copy per domain, which can drift between them.
- **Follow-ups / risks to watch:** whether a consuming application's release should
  **pin** a library release or merely record the one it saw, and what a pin means
  when the library moves on; how a library is deprecated when something still
  depends on it; whether the un-mark refusal needs a dependency index to be
  answerable cheaply; and whether "library" should later mean "library within a
  tenant" if multi-tenancy arrives, which would make the flag a scope after all.

## Pros and cons of the options

### Option 1 — a `scope: "global"` field on the artifact
- Good: no container to create and no publish step; an author marks one form and
  it is available. Matches what the engine already does at runtime, and the
  Modeler's pickers would need no change at all.
- Bad: the shared content has no version, no release history and no identity that
  survives a clone, so the dependency it creates cannot be written into a manifest
  or pinned — which is the very problem this record exists to fix. It also spends
  the word "scope" a third time, and it invites the migration that must not happen
  (reading today's ungrouped artifacts as the new global ones).

### Option 2 — a library application (chosen)
- Good: one concept instead of two; membership, export, releases, promotion and git
  binding are inherited; the dependency has a key and a version; the pattern is
  proven by the system application (ADR-0122); additive and migration-free.
- Bad: a publish step and a deploy ordering that a flag would not have; a library
  is a broad read grant; one more reason an export can be refused.

### Option 3 — keep it implicit
- Good: nothing to build.
- Bad: leaves the silent dangling export in place, and leaves *Not assigned*
  meaning "unfiled" in the UI and "private" in the enforcement — the mismatch that
  prompted the question.

### Option 4 — a container above applications
- Good: a clean home for content that is genuinely organisation-wide.
- Bad: two nested grouping layers and the immediate question of which one publishes;
  ADR-0034 rejected the same shape as its option 3, and nothing since has changed
  the argument.

### Option i — local materialisation (chosen)
- Good: resolution stays deterministic, local and free of network failure at
  runtime; honours I5 and the ADR-0076 child-instance model; reuses the push
  mechanism ADR-0129 already defines and the credential discipline ADR-0069/0070
  establish; a target server's operator keeps the overrides ADR-0105 gives them.
- Bad: content must be moved deliberately, so the same library exists as a copy per
  server and can drift between them; "available everywhere" is true per instance,
  not per estate.

### Option ii — references become URIs resolved over the network
- Good: one identifier everywhere, no copies to keep in step, and nothing to
  publish twice.
- Bad: does not make a remote process callable at all, because a call activity's
  callee is a child instance in the caller's partition; puts a remote server's
  availability and version on the processor's path against I5; turns a reference
  inside a model into an outbound request the server makes on the model author's
  behalf, discarding the `safeModelRef` separation between operator-configured base
  URL and model-supplied handle; and delivers addressability where the requirement
  was availability.

## Links

- extends [ADR-0034](0034-projects-and-artifacts.md) (the project, the optional
  tag, and the *Ungrouped* bucket this record deliberately leaves alone; its
  deferred "nested folders" question is the one answered here)
- extends [ADR-0128](0128-process-applications.md) (the application a library is a
  flag on, and its release version)
- refines [ADR-0071](0071-sharing-scopes.md) (the personal-space meaning of an
  ungrouped artifact, which this record keeps rather than reinterprets, and the
  `effectiveRole` a library widens)
- follows [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)
  (the server-wide, visible-to-all application whose pattern a library reuses)
- relates to [ADR-0134](0134-git-backed-applications.md) (the source tree the
  dependency section is added to, and the portable key that names a library)
- relates to [ADR-0129](0129-remote-deployment-targets.md) (the push that is this
  record's answer to "available on another server")
- relates to [ADR-0076](0076-call-activities.md) (the child-instance model that
  makes a remote call activity unimplementable) and
  [ADR-0105](0105-per-server-call-activity-target-overrides.md) (the per-server
  resolution a push preserves and a remote URI would bypass)
- relates to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) (the resolver
  seam, the one place a model is already fetched remotely, and the handle/base-URL
  separation this record declines to collapse) and
  [ADR-0050](0050-temis-decision-connector.md) (a remote decision as a modelled
  service call)
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) (a decision
  as a versioned runtime artifact, which a library publishes like any other)
- relates to [ADR-0222](0222-artifact-id-renames.md) (the global id namespace a
  folder must not disturb)
- relates to [ADR-0186](0186-embed-public-forms-cross-origin.md) and
  [ADR-0204](0204-hosted-apps-on-an-isolated-origin.md) (the origin questions both
  answered restrictively, which a URI-valued reference would reopen)
- relates to [ADR-0230](0230-process-information-model.md) (the per-application
  vocabulary behind this record's open question)
- relates to [ADR-0305](0305-business-capabilities-and-value-streams.md) (the business
  architecture, where an application is the unit of a capability — so a library, which
  realises no capability, is a resource beneath that map rather than a record in it)
- relates to the cross-instance epic,
  [ADR-0369](0369-cross-instance-message-addressing.md),
  [ADR-0371](0371-participant-binds-a-published-interface.md),
  [ADR-0372](0372-peer-message-delivery-worker.md) and
  [ADR-0373](0373-published-process-interface.md) (how a node reaches a process on
  another node — a contract and a message, which is why a reference never has to)
