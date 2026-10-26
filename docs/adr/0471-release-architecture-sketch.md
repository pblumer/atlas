# ADR-0471: A release carries its architecture sketch, and a worker publishes it to Confluence

- **Status:** Accepted (2026-10-23; amended 2026-10-23 — public start links belong to the
  physical part, the Atlas node is composed of its System Software, and job types belong to
  the logical part; see the notes below)
- **Implementation:** Partial
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** The Confluence contract this record builds on is not established from
  anything in this repository: that both Cloud and Data Center accept a create-or-update
  attachment upload by file name on a page through their REST API, which authentication
  each takes, and whether both render an SVG attachment embedded in a page. §4's file
  naming and §3's choice of SVG over PNG rest on that gap.
- **Question checked:** 2026-10

> **Implementation status and amendment (2026-10-23): S1 has landed.** A release records
> its snapshot on publish and on import (`api/releasearchitecture.go`), the three routes
> serve it, and the Console's release history shows it. The snapshot model and both
> renderings are `api/archsketch`; the per-element message enumeration is
> `compiler.CompiledProcess.MessageReceipts`. S2 to S4 are not started.
>
> Two details of the text below changed while building it, both for the reason the
> record itself gives for the split between the two parts:
>
> - **Public start links are physical, not logical.** §1 listed them among the entry
>   points of the logical part. A link is created on an installation, independently of
>   any release, and one that exists on DEV need not exist in PROD — exactly the kind of
>   fact §1 assigns to the physical part. They are recorded with it, in S2. The logical
>   part's entry points are the messages the release's processes receive.
> - **The Atlas node is composed of its System Software.** §3's table said the System
>   Software is assigned to the node. ArchiMate assigns active structure to behaviour or,
>   in the technology layer, to what is deployed on it; a node and its system software
>   are both active structure, so the relationship is composition, which is also what
>   Panorama's own authoring rules accept (`panorama.MayConnect`). The release's Artifact
>   is still assigned to the node.

> **Implementation status and amendment (2026-10-23, second): S2 has landed, without the
> worker protocol extension.** A snapshot now carries the physical part §1 describes —
> the installation's name and environment, its external URL, the public start links, and
> per worker name its placement, the configured endpoint reduced to scheme, host and port
> (`archsketch.ParseEndpoint`), and the worker instances the registry saw holding the
> name. The renderings draw each connection from the side that opens it (§2), and a read
> withholds an endpoint from a caller who may not see that worker's configuration and the
> instances from a caller without the operator role (§3, §5). The snapshot's format and
> mapping versions are 2; a version-1 snapshot is drawn as before. What remains of S2 is
> the additive `endpoints` field on job activation, so a worker that keeps its own
> configuration can report where it connects; until then such a worker is drawn as
> holding its endpoint itself.
>
> **Update (2026-10-23): the worker protocol extension has landed, and S2 with it.**
> `atlas worker` reduces the endpoint each held name's configuration names
> (`internal/endpoint`, shared with the server so both mean the same by "scheme, host
> and port") and sends it in the additive `endpoints` field of job activation. The
> server reduces it again, keeps it only for names the same poll reported, and bounds
> it. A snapshot draws a reported endpoint for a worker the installation holds no
> configuration for, marked as reported; instances that disagree are recorded as
> disagreeing; and a reported endpoint, being information about instances, is read by
> operators only. Format and mapping version 3; a version-2 snapshot is drawn as before.
>
> One addition to §1's logical part, found while building it: **job types.** A service or
> send task with a job type hands its work to whichever worker subscribes to that type
> (ADR-0007) — the plain external job worker, which §1 did not name because it is reached
> by a type rather than a worker name. It is a way out of the application all the same,
> so the logical part records each job type with the elements that state it, and the
> physical part records the instances seen leasing it. Its connection is always opened
> by the worker, so it is drawn as one in both views.

## Context and problem statement

Every Atlas process application needs an architecture sketch that shows how it
communicates with its environment: who calls into it, which workers it depends on, where
those workers connect, and over which protocol and port. Today such a sketch is drawn by
hand, outside Atlas, and it drifts from what is deployed on the first release after it was
drawn. The people who read it — operations, network and security reviewers, the owners of
the systems an application calls — read it in Confluence, and most of them have no Atlas
account.

Atlas already holds most of the facts, but nothing assembles them for this purpose:

- **Panorama** models ArchiMate 3.2 ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md))
  and derives a whole-instance landscape with an ArchiMate projection and an Open Exchange
  export ([ADR-0211](0211-panorama-derived-landscape-mesh.md)). That landscape is
  instance-wide rather than per application, laid out in the browser, unversioned, and it
  withholds endpoints on purpose: a worker's endpoint and credential reference never reach
  a node (`api/panorama/mesh.go`, `type Worker`), because a landscape is opened by anyone
  with modeler access and an internal hostname is what ADR-0211 §10 keeps from leaving the
  server.
- **Releases** ([ADR-0128](0128-process-applications.md)) are a manifest of what shipped
  together: members by kind, logical id, version and definition key
  (`api/releasestore.go`). A release is recorded when an application is published
  (`api/releases.go`) and again on a target when a promotion imports it there
  (`api/appimport.go`). A release references its definitions by key only, and a
  definition can be removed later — promoting such a release fails with "references
  definition …, which is no longer stored" (`api/promote.go`).

Three facts shape any answer, and a sketch that ignores them is wrong in a way its readers
will act on:

1. **Worker communication is two hops in opposite directions.** A worker opens the
   connection *to* Atlas to lease its jobs (`POST /api/v1/jobs/activate`, long-poll,
   [ADR-0007](0007-job-worker-protocol.md)), and opens a second connection to the system
   it integrates. For an offloaded Worker Type the Atlas server opens no connection to that
   system at all. A sketch with an arrow from Atlas to the LDAP server is wrong for a
   firewall request and wrong for an operator diagnosing it.
2. **The sketch has two axes of change.** *Which* entry points and dependencies an
   application has is fixed by the release's content. *Where* they are — which worker
   serves a name, at which endpoint, and under which address this server is reached —
   belongs to the installation, differs between environments, and changes without a
   release.
3. **The server does not know every endpoint.** A managed worker's endpoint is in the
   connector store (`api/connectorkinds.go`). An external worker owns its configuration
   ([ADR-0168](0168-connector-work-on-a-worker.md)) and reports only the *names* of the
   connectors it holds, on each poll (`worker/worker.go`, `activate`), into a registry a
   restart empties (`api/workers.go`). The server's listen address is not its reachable
   address; the public origin is known only when an operator sets `--external-url`
   ([ADR-0200](0200-mcp-oauth-resource-server.md)). And a worker name authored as FEEL
   ([ADR-0172](0172-entra-id-connector.md)) names no fixed worker at all
   (`api/panoramamesh.go`, `workerRefs`).

The question is how a release comes to carry a sketch of its communication that is true by
construction, and how that sketch reaches Confluence.

## Decision drivers

- **Derived, never drawn.** The sketch is computed from what was deployed; nobody
  maintains it.
- **Versioned with the release.** A page about v7 keeps showing v7 after v8 ships.
- **Readable outside Atlas** — in Confluence, by people without an Atlas account, and
  without making Atlas reachable from wherever Confluence runs.
- **Connections drawn as they are opened**, because that is the fact operations and
  network reviews act on.
- **No secret ever leaves**, and endpoints leave only where an application explicitly
  allows it (ADR-0211 §10).
- **Unknown is not none.** A worker nobody has observed since a restart is reported as
  unobserved, not as absent.
- **Integrations run on workers** ([ADR-0164](0164-no-in-process-service-tasks.md),
  ADR-0168).
- **The engine is untouched.** Nothing here enters the WAL, `applyToState`, a processor
  or recovery.

## Considered options

How the sketch is produced:

1. **P1 — Compute on demand** from the release's definitions whenever it is asked for.
2. **P2 — Snapshot by value** when the release is recorded.

How it reaches Confluence:

1. **D1 — Pull:** serve the rendering under a revocable public token and let a page embed
   it as an image ([ADR-0029](0029-public-process-start-links.md),
   [ADR-0143](0143-process-documentation-export.md)).
2. **D2 — Push from the server:** a Confluence client inside the API server, called the
   way promotion calls a deployment target.
3. **D3 — Push from a worker:** a Confluence Worker Type, driven by a process Atlas ships.
4. **D4 — Leave delivery to the customer:** Atlas serves the renderings; a CI pipeline
   uploads them.

## Decision outcome

Chosen: **P2 and D3** — a release carries an architecture snapshot taken by value when it
is recorded, and a shipped process uploads its renderings to Confluence through a
`confluence` Worker Type, on every installation that records the release.

### 1. The architecture snapshot is part of the release

When a release record is saved — on publish and on the import of a promoted release — the
server takes an **architecture snapshot** and stores it by value on the release record
itself, in the same atomic, fsynced write that records the release. A release therefore
never exists without its snapshot — a store of its own would leave a crash window between
the two writes, and a repair path for it — and the snapshot is backed up, restored and
deleted with its release. Nothing enters the WAL. The member definitions are deployed at
that moment by construction, so the snapshot never depends on a definition outliving its
release. It is never recomputed: its format version and mapping version are written into
it, and a later change to either produces different snapshots for later releases only.

The snapshot has two parts.

**The logical part** is environment-independent and is the same for a release on every
installation:

- **Entry points:** message start events and intermediate message catches by message
  name, and public start links configured for the member processes. Starting an instance
  over the API is possible for every process and is drawn once, as the Atlas API, rather
  than once per process.
- **Dependencies:** every worker reference with its name and Worker Type, by the same
  enumeration as `workerRefs`; FEEL-authored names as *decided at run time*; local
  decisions; call activities into processes of other applications.
- **Where each is used:** the element ids that state it, so a reader can trace a line on
  the sketch back to a task.

**The physical part** describes the installation that recorded the release, at that
moment:

- **The environment:** the node descriptor's operator-defined name and environment
  (ADR-0189 §6, `api/node.go`).
- **Inbound:** the configured external URL, or *not configured* — never the listen
  address.
- **Per worker reference:** what serves that name here — a managed worker with its stored
  endpoint, workers that reported the name since the server started, or nothing — as one
  of *configured*, *observed*, *not observed since restart* and *configured nowhere*.
- **Endpoints as scheme, host and port only.** The value is parsed as a URL and rebuilt
  from those three parts; user information, path, query and fragment are dropped by
  construction rather than trimmed. A value that does not parse is reported as
  *unparseable* and never echoed.

A release recorded before this lands has no snapshot and says so. A backfill is offered
only where every member definition is still stored, and the snapshot it writes is marked
with the moment it was taken, because its physical part describes that moment and not the
release's.

### 2. Connections are drawn as they are opened

The sketch is three columns: callers on the left, the Atlas node with the application in
the middle, workers and the systems they reach on the right. **An arrow points from the
side that opens the connection**, labelled with protocol and port. A worker's lease is
drawn from the worker to the Atlas API; its integration from the worker to the target
system. An in-process Worker Type, deprecated under ADR-0164, is drawn from the Atlas node
to its target directly, and marked deprecated. Which way the *data* flows is a different
fact; it is carried by the ArchiMate relationships in the export, not by the arrows.

### 3. One snapshot, three renderings

- **JSON**, the snapshot itself.
- **SVG**, rendered on the server with a fixed layout — not the landscape's force layout,
  which exists only in the browser. The same snapshot produces the same bytes, so a
  rendering can be cached, committed and compared. With no font metrics on the server,
  boxes have fixed widths, labels wrap and truncate by character count, and the full text
  rides in each element's `<title>`. The rendering carries a stamp: application, release
  version, environment, the moment the snapshot was taken by the server's clock, the
  mapping version, the sentence *Generated by Atlas from release vN; nothing here was
  drawn by a person*, and the list of what it does not show (§6).
- **ArchiMate Open Exchange XML**, under a versioned mapping:

  | Snapshot fact | ArchiMate 3.2 |
  |---|---|
  | Atlas installation | Node, with System Software *Atlas* assigned to it |
  | Release vN | Artifact realising the Application Component, assigned to the Node |
  | Application | Application Component |
  | Entry point (message, public start link) | Application Interface composed in the component |
  | Atlas API | Technology Interface composed in the Node |
  | Worker reference | Application Service, as ADR-0211 already maps a worker |
  | Worker process | System Software realising that service |
  | Target system | Node, marked as derived from an endpoint |
  | Connection | Path associated with the two Nodes it joins |

  Path is ArchiMate 3.2 but outside Panorama's authoring subset (`api/panorama/subset.go`).
  An imported document round-trips it under ADR-0189 §2; adding Path to the subset is a
  follow-up, not a prerequisite. The existing binding key `atlas.releaseId` identifies the
  release, and new descriptive properties carry the connection facts —
  `atlas.endpoint.scheme`, `atlas.endpoint.host`, `atlas.endpoint.port`,
  `atlas.connection.initiator` and `atlas.environment`. They describe and do not bind, and
  they are documented with the mapping version.

The renderings are served at `GET /api/v1/applications/{id}/releases/{version}/architecture`
with `.svg` and `.xml` variants, under the application's sharing scope
([ADR-0071](0071-sharing-scopes.md)). The release list leaves the snapshot out; it is read
through these routes only. Endpoints appear in a response only to a caller who
may read the worker store; for anyone else they are reported as *withheld*.

### 4. Publication: a Confluence Worker Type, driven by a shipped process

- **The Worker Type.** A new `confluence` Worker Type, built worker-first under ADR-0164,
  with one operation to begin with: attach a file to a page, creating the attachment or
  adding a version of it by file name. It addresses Confluence Cloud and Data Center. The
  credential is the worker's own (ADR-0168); a supervised worker is provisioned with it
  from the connector store and the vault as the mail Worker Type is
  ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md)). It is an ordinary Worker
  Type, so any customer process can use it too.
- **The process.** A publication process ships in the protected system project
  ([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)). The server
  starts an instance once the release record and its snapshot are durable, the way the
  order service starts the processes of its positions
  ([ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)), and only
  for an application that has a publication destination. Retries are a task property
  ([ADR-0135](0135-retries-as-a-task-property.md)); a failure parks as an incident in
  Operations and is repaired from there ([ADR-0160](0160-fix-the-connector-from-the-incident.md)).
- **The bytes stay out of the log.** The job carries coordinates only — application,
  release version, page and file names. The worker fetches the renderings from §3's
  routes with its own credential, the same way it leases jobs. A document in a job
  variable would sit in the event log for the life of the instance's history, and an
  event is a fact about execution, not a place to keep a drawing. The worker's principal
  must therefore be able to read the application; when it cannot, the incident says so.
- **Each environment publishes itself.** Publishing on one installation uploads that
  installation's view; promoting the release records it on the target, and the target
  uploads its own. No installation reads another's physical facts for this, so ADR-0402's
  federation is not involved.
- **File names** are fixed per application slug and environment:
  - `atlas-<application>-v<N>-<environment>.svg` and `.xml`, written once per release;
  - `atlas-<application>-current-<environment>.svg`, replaced by each newer release, with
    Confluence's attachment history keeping the previous ones.

  A page embeds either the pinned release or the current one, and Atlas never needs to
  be reachable from Confluence.
- **The destination** is a design-time setting on the application: which `confluence`
  worker, which page, and whether endpoints may be published (§5). Changing it publishes
  nothing; a *publish again* action starts the process for a chosen release.

### 5. Disclosure

A published sketch is read by everyone who can open the Confluence page, which is an
audience Atlas does not control. So:

- **By default** the physical part is published without hosts, ports and the external
  URL: worker names, Worker Types and their states only.
- **With the application's explicit permission** it carries scheme, host and port.
- **Never:** credential references, user information, paths, query strings, or anything
  read from the vault.

This amends ADR-0211 §10 for this artifact only. The landscape keeps withholding
endpoints, because its audience is everyone with modeler access, and nothing here changes
that.

### 6. What the sketch does not show, and says so

The stamp lists each of these, because a sketch is believed for everything it does not
contradict:

- **The installation's own communication** — the outbound proxy, OAuth token endpoints,
  the OpenSearch exporter, Prometheus scraping, deployment targets, identity providers.
  None of it belongs to an application. **The sketch is therefore not a basis for firewall
  requests**, and says so.
- Worker names authored as FEEL.
- Addresses a task builds at run time beyond its worker's configured endpoint.
- External workers not observed since the last restart — reported as *not observed*,
  never as *none*.
- What sits behind a target endpoint: gateways, load balancers, the systems they forward
  to.

### Consequences

- **Positive:**
  - Every release gets a sketch that matches what was deployed, without anyone drawing
    it, and keeps it after later releases ship.
  - Connection direction is drawn as it is, so the sketch can be used in operations and in
    reviews without being corrected first.
  - Each environment documents itself in Confluence; nobody outside Atlas needs an
    account, and Atlas needs no inbound route from Confluence.
  - The `confluence` Worker Type is useful beyond this record.
  - Failed uploads are incidents, retried and repaired with the tools operators already
    use, rather than a status page of their own.
- **Negative / trade-offs accepted:**
  - The physical part is a moment, not a live view: an endpoint changed between two
    releases is not republished until the next release or a *publish again*.
  - External workers appear without endpoints until the worker protocol carries them.
  - Releases recorded before this lands have no snapshot, and a backfill describes the
    moment it ran.
  - Three moving parts — a Worker Type, a system process and a trigger — where D2 would
    have one.
  - The worker's principal needs read access to every application it publishes.
- **Follow-ups / risks to watch:**
  - **Worker protocol extension:** an additive `endpoints` field on job activation that
    maps each connector name to `scheme://host:port`, so external workers can be drawn
    with their endpoints. Older workers send nothing and stay *observed* without an
    endpoint.
  - Path in Panorama's authoring subset.
  - A PNG rendering, if a Confluence edition does not render SVG attachments. It needs a
    pure-Go rasteriser ([ADR-0010](0010-go-and-no-cgo.md)) and its own dependency review.
  - D1's public token URL for internal HTML pages, as a separate decision.
  - Republishing `current` when a worker's endpoint changes between releases.

## Delivery slices

1. **S1 — The snapshot:** the logical part taken on publish and import, its store, the
   three renderings, the routes, and the release view in the Console.
2. **S2 — The physical part:** managed workers, observed workers, the external URL, and
   the disclosure rule; then the worker protocol extension.
3. **S3 — The `confluence` Worker Type.**
4. **S4 — Publication:** the shipped process, the destination setting, the trigger on
   publish and import, and *publish again*.

Each slice ships end to end — API, persistence, embedded UI, authorization, tests,
documentation and OpenAPI — as ADR-0189's slice rule requires.

## Pros and cons of the options

### P1 — Compute on demand
- Good: nothing stored; always the current renderer.
- Bad: a release whose definition was removed has no sketch; a renderer change rewrites
  the sketch of every past release, so "the sketch of v7" stops being one thing.

### P2 — Snapshot by value (chosen)
- Good: immutable and answerable, the way ADR-0143 made documentation versions; survives
  the removal of a definition.
- Bad: storage grows with releases; the physical part is frozen at the moment it was
  taken.

### D1 — Pull through a public token
- Good: live; the simplest way into any HTML page.
- Bad: Atlas must be reachable from wherever the image is fetched, which for a hosted
  Confluence may be the internet; a leaked link discloses until revoked; endpoints on a
  public URL are exactly what ADR-0211 §10 guards against.

### D2 — Push from the server
- Good: fewest moving parts; promotion already makes outbound calls with a vault
  credential.
- Bad: puts an integration in the server's own process, against ADR-0164's rule that it
  "does not run integrations"; needs a retry loop and a status view of its own beside
  incidents; the credential sits where it is not otherwise needed (ADR-0168); a process
  cannot reuse it.

### D3 — Push from a worker (chosen)
- Good: follows ADR-0164 and ADR-0168; retries, incidents and repair come with the
  engine; each environment publishes itself; the Worker Type is reusable.
- Bad: the most moving parts; the worker needs read access to the application; nothing
  is published until a `confluence` worker is configured.

### D4 — Leave delivery to the customer
- Good: no integration code in Atlas; fits teams that already keep documentation as code.
- Bad: every customer builds the same pipeline; nothing triggers it on a release; each
  environment's view needs a pipeline with access to that environment.

## Links

- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — Panorama's ArchiMate model, node descriptor and slice rule
- relates to [ADR-0211](0211-panorama-derived-landscape-mesh.md) — the landscape's ArchiMate projection and §10's disclosure rule, amended here for this artifact only
- relates to [ADR-0128](0128-process-applications.md) — releases
- relates to [ADR-0143](0143-process-documentation-export.md) — snapshot by value, the precedent for P2
- relates to [ADR-0164](0164-no-in-process-service-tasks.md) and [ADR-0168](0168-connector-work-on-a-worker.md) — why delivery runs on a worker
- relates to [ADR-0007](0007-job-worker-protocol.md) — the lease connection drawn from the worker, and the protocol the endpoint extension adds to
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Type, Worker and Worker Instance
