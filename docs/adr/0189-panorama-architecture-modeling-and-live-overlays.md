# ADR-0189: Panorama architecture modeling and live operational overlays

- **Status:** Accepted (amended 2026-08-31 — a derived landscape mesh sits above these drawn views, and takes impact analysis out of P5; amended 2026-09-01 — P5's "over time" is a journal of transitions, not a store of samples, its historical context is a query rather than a copy, and arranging a view splices the document rather than re-serialising it; see the amendment notes below)
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

> **Amendment (2026-08-31): the landscape altitude is derived, not drawn.** Everything
> this record describes is authored — a view exists because a person placed elements on
> it. That leaves an instance nobody has modeled showing nothing, and leaves no altitude
> above a single view.
> [ADR-0211](0211-panorama-derived-landscape-mesh.md)
> adds one: a whole-instance graph computed from resources Atlas already holds — process
> applications, deployed processes, call activities, connectors, job and worker types,
> releases, deployment targets, DMN decisions — with the ArchiMate model of this record
> as an annotation over it rather than its source.
>
> Three things in the text below are affected, and none of them is reversed:
>
> - **§6's seven observation states remain the contract.** The mesh aggregates them into
>   three severity classes for zoom-out only, keeping *unreachable* and *stale* out of
>   the critical class and attributing every worst-of parent to the descendant that
>   caused it. It does not replace the states, and §6's rule that layer fills are never
>   recolored holds there too.
> - **§7's ban on notation-as-a-theme holds.** C4 is added as a one-directional,
>   read-only projection under a versioned mapping that reports what it drops — never
>   authored, never round-tripped, and not a peer notation. An authorable second
>   notation, UAF included, still needs its own record, exactly as §7 says.
> - **The delivery slices below gain P2.5**, between P2 and P3, and **P5 loses
>   dependency/impact analysis and discovered-but-unmodeled resources to it** — the
>   derived graph produces the edges both need, so they arrive with it rather than three
>   slices later. P5 keeps desired-versus-observed drift over time and the optional
>   Prometheus/OpenSearch adapters.

> **Amendment (2026-09-01): P5's "over time" is a journal of transitions.** This record
> allows Panorama to correlate and forbids it to become "a monitoring or time-series
> database" (see the non-goals below). P5's *desired-versus-observed drift over time*
> has to be delivered inside that, and a store of samples is exactly the thing the
> non-goal names. So none is kept. What is kept is a **transition**: when a bound
> value's observation state changes between two reads, that change is recorded once —
> both states, the reason, and the moment it was noticed. A hundred identical readings
> produce nothing; one release going stale produces one entry.
>
> Three properties follow, and all three are *published with every answer* rather than
> only recorded here — a history that hides what it cannot see is worse than no history,
> because without them "nothing changed" and "nobody looked" read alike:
>
> - **It sees only what was looked at.** Observations are computed when somebody asks
>   for them; nothing polls, and §6 is why. A state that changed and changed back
>   between two views leaves no trace. Continuous history is what the optional
>   Prometheus/OpenSearch adapters are for, and they remain optional.
> - **It does not survive a restart.** This is runtime state, like the worker registry
>   and the peer descriptor cache: never written to the log, never rebuilt by
>   `applyToState` (I4/I6). *When* a transient fact was noticed is not an architecture
>   fact, and the declarative model stays untouched by observation exactly as §6 says.
> - **It is bounded**, per model and across models, and each answer says from which
>   moment it can still speak.
>
> §6's seven states and its rule that layer fills are never recolored are unaffected: a
> transition is a pair of those states, and it is rendered as text beside a finding.

> **Amendment (2026-09-01): arranging a view splices the document.** §2 requires
> that unsupported-but-standard content round-trips without loss. Authoring makes
> that concrete for the first time, and the shape of the answer is the binding
> writer's: the server rewrites the four geometry attributes of the shapes that
> moved and reads nothing else, so comments, indentation, attribute order,
> namespace prefixes and any part of the schema Panorama does not model survive
> being edited.
>
> The corollary is that **the browser never writes the document.** The canvas
> parsed one to draw it and could serialise it back in a line — but a browser's
> `XMLSerializer` normalises, so that line would rewrite somebody's model every
> time a box was nudged. The canvas therefore sends what it actually knows, which
> is a list of moved shapes, on a route that can move a shape and do nothing else
> whatever is posted to it.
>
> Two rules follow from the same principle, that the canvas must not offer what it
> cannot honestly do:
>
> - **An editable canvas loads editing; a reader's does not.** Not a disabled
>   toolbar — a diagram-js with no modeling behaviour in it, so a reader's drag
>   pans the view rather than being refused.
> - **An explicit rules provider permits only what a slice implements.** With no
>   rules provider diagram-js allows everything, including operations that would
>   change the canvas and never reach the document. Creating elements and drawing
>   relationships arrive with their own slice and their own semantic rules; until
>   then they are refused, because an edit that cannot be saved is worse than one
>   that cannot be made.

> **Amendment (2026-09-01): P5's historical context is a query, never a copy.** The
> options above rejected "copy all remote metrics and logs into a Panorama-specific
> internal database" and selected the projection instead, noting that "historical
> charts may query dedicated backends such as Prometheus or OpenSearch later; they
> remain external sources of historical data." P5b is that later. It queries those
> stores when somebody asks and keeps nothing — a cache of somebody else's history
> is the rejected database with a shorter retention and no owner.
>
> What each store may be asked is decided by what it can **identify**, and the two
> answers are not symmetric:
>
> - The exported event log (ADR-0114) carries each record's process definition key,
>   so it answers about a process and about the application whose processes those
>   are. It stores a job's type as an interned index — a number meaningless outside
>   the process that wrote it — so it cannot answer about a connector or a job type.
>   An incident names its instance and not its definition, so no single query
>   attributes one; that gap is named in the answer rather than left as a silence.
> - Metrics (ADR-0142) carry no per-element labels *by design*: that record forbids
>   labelling by process id, instance key, or any other value the data can invent,
>   because one such label turns a metric into unboundedly many series. A metrics
>   store therefore answers about a node and never about one process — and it
>   identifies that node the only way it knows one, by the scrape target the series
>   came from. Atlas derives that from a deployment target's base URL. For the
>   server itself it cannot derive it at all, because how this process appears in
>   somebody's Prometheus is their scrape configuration; that one is configured, and
>   left unset the local runtime is reported unidentifiable rather than silently
>   matched to whichever series is nearest. Guessing it would answer a question
>   about a different process while looking exactly like an answer about this one.
>
> Neither limit is a gap to close later, so neither is reported as an absence of
> data. A source's answer for one bound value is one of six states —
> *not-configured*, *unidentifiable*, *unreachable*, *refused*, *empty*,
> *available* — because each sends an operator somewhere different, and only
> *empty* is a statement about the architecture rather than about the lookup. Every
> source answers for every value, including the ones it cannot help with: a row
> left out is indistinguishable from a store nobody thought to ask.
>
> Three further constraints hold. The route is scoped to **one element**, because
> every bound value costs a query against a system that did not agree to be
> browsed. The window is an **allowlist**, because an arbitrary range is an
> arbitrary query on somebody else's cluster. And the run-loop split is §6's: ids
> become definition keys on the loop under the caller's sharing scope, and the
> query itself runs off it (I3).

## Context

The web shell reserves **Panorama** for analytics over the exported event stream
([ADR-0012](0012-web-ui-app-shell.md)), but Panorama is still a placeholder. Atlas
also has a generated ArchiMate model of Atlas itself
([ADR-0099](0099-archimate-enterprise-architecture-view.md)). That model is a
documentation artifact: Markdown remains the source of truth, the exchange file is
generated, and there is no user-facing architecture editor.

Atlas users need a different capability in Panorama:

- model an application landscape using ArchiMate 3.2 concepts;
- describe an application's internal application services, interfaces, data, and
  artifacts;
- place those artifacts on nodes, system software, and technology services;
- connect the landscape to Atlas process applications, BPMN processes, connectors,
  releases, and deployment targets;
- relate business processes and application services to the capabilities they
  support; and
- overlay the declared architecture with current operational facts without turning
  the drawing into a second runtime database.

These needs introduce three different kinds of data that must not be conflated:

1. the **declared architecture model** (what should exist and how it is related);
2. **Atlas bindings** (which Atlas resource a model element refers to); and
3. **runtime observations** (what is currently deployed, healthy, ready, or
   unreachable).

ArchiMate also uses the term *Application Component*. Atlas uses *process
application* for a versioned, deployable unit ([ADR-0128](0128-process-applications.md)).
They are not the same concept: an ArchiMate component can be implemented by several
Atlas process applications, and one process application can contribute to several
components.

The design must preserve the engine invariants, the single-binary/buildless web
distribution, existing application sharing, credential indirection, and honest
partial-failure behavior for remote deployment targets.

## Decision drivers

- Use a recognized, interoperable architecture notation instead of an Atlas-only
  drawing format.
- Keep elements and relationships reusable across multiple views.
- Keep desired architecture separate from observed runtime state.
- Reuse existing Atlas applications, BPMN processes, deployment targets, releases,
  connectors, and credentials rather than duplicating them in Panorama.
- Keep secrets out of diagrams, exchange files, browser storage, logs, and API
  responses.
- Keep network I/O and editor persistence outside the engine's durable event path.
- Remain self-contained at runtime: no CDN and no frontend build tool is required to
  start Atlas.
- Report unavailable, stale, and partially failed observations explicitly.
- Leave a clean extension boundary for another notation such as UAF without
  pretending that one notation is a renderer theme for another.

## Considered options

### Product scope

1. **Event analytics only.** This preserves ADR-0012's placeholder description but
   does not provide a declared architecture model or application landscape.
2. **A standalone architecture editor.** This provides diagrams but misses Atlas's
   differentiator: correlating the model with deployed and running resources.
3. **Architecture modeling with live operational overlays.** This provides both
   design-time intent and a current, explicitly separate runtime projection.

Option 3 is selected. Time-series analytics remains a possible Panorama view, but
it is not the canonical architecture store and does not make Atlas a time-series
database.

### Model and editor

1. **Atlas-specific JSON and SVG.** Simple initially, but it creates a proprietary
   metamodel and poor interchange with architecture tools.
2. **ArchiMate 3.2 semantics with the Open Group ArchiMate Model Exchange File
   Format, edited with a custom `diagram-js` bundle.** This separates model from
   view, supports standard interchange, and reuses the same generic diagram canvas
   foundation as the BPMN editor without importing BPMN semantics.
3. **Embed a third-party ArchiMate editor unchanged.** Faster for a prototype, but
   its metamodel coverage, license, dependency chain, import/export fidelity, and
   compatibility with the Atlas buildless shell would become product constraints.
4. **Extend `bpmn-js` with ArchiMate shapes.** Rejected because BPMN and ArchiMate
   have different metamodels and relationship rules; visual similarity is not
   semantic compatibility.

Option 2 is selected. Third-party implementations such as `archimate-js` may be
used during a spike as references, but are not accepted as runtime dependencies by
this decision.

### Runtime correlation

1. Write health and deployment state back into the ArchiMate document.
2. Resolve bindings into a separate live observation projection.
3. Copy all remote metrics and logs into a Panorama-specific internal database.

Option 2 is selected. Historical charts may query dedicated backends such as
Prometheus or OpenSearch later; they remain external sources of historical data.

## Decision outcome

### 1. Panorama's product role

Panorama becomes Atlas's architecture knowledge and visualization surface. It owns
user-editable architecture models and views, resolves explicit bindings to Atlas
resources, and can render current operational observations on top of a selected
view.

Panorama is not:

- a replacement for the BPMN Modeler;
- an engine state store or a participant in command processing;
- a configuration source for deployments in the first release;
- a monitoring or time-series database; or
- a claim that every imported ArchiMate 3.2 concept is editable on day one.

The existing generated model in `docs/architecture/model/atlas.xml` remains a
documentation-owned artifact under ADR-0099. Panorama may import it, but importing
it does not change the source-of-truth rule for Atlas's own architecture
documentation.

### 2. Canonical architecture document

The canonical persisted and exported artifact is an XML document conforming to the
[Open Group ArchiMate Model Exchange File Format](https://www.opengroup.org/open-group-archimate-model-exchange-file-format).
The semantic target is ArchiMate 3.2. The implementation must pin the exact exchange
schemas and validate fixtures against them before the importer is declared
interoperable.

One model contains:

- reusable elements;
- reusable relationships;
- one or more views with node positions, connections, labels, and visual metadata;
- standard property definitions and properties used for Atlas bindings; and
- model metadata such as name, documentation, language, and organizations.

Views reference the reusable semantic elements; copying an element into a second
view must not create a second application component. A server-generated stable
model id and monotonic revision accompany the XML for optimistic concurrency, but
do not replace its standard identifiers.

The first editor slice supports a deliberately bounded palette:

- Capability and Business Process;
- Application Component, Application Service, Application Interface, and Data
  Object;
- Artifact, Node, System Software, Technology Service, Communication Network, and
  Path; and
- the ArchiMate structural, dependency, dynamic, and other relationships needed to
  connect those elements.

The relationship matrix is semantic validation, not just a visual hint. Invalid
connections are blocked while authoring and reported during import/validation.
Unsupported but valid standard content must either round-trip without loss or make
the operation fail with a precise diagnostic. Atlas must never silently discard it.
The UI states the implemented subset and must not claim complete ArchiMate 3.2
authoring until the full conformance scope is tested.

### 3. Ownership, persistence, and API boundary

Each Panorama model belongs to one Atlas process application, which remains the
unit of ownership, sharing, and bundle organization established by
[ADR-0071](0071-sharing-scopes.md) and ADR-0128. A landscape spanning
many process applications can live in a dedicated process application such as
"Enterprise Architecture" and bind to other visible applications. This reuses the
existing sharing boundary instead of inventing Panorama-specific ACLs.

Panorama models are design-time artifacts stored in a sidecar store under the Atlas
data directory and included in design-time backup and restore. Writes are atomic
and fsynced. They do not enter the WAL, `applyToState`, processors, snapshots, or
runtime recovery. Restart-persistence, import/export, conflict, and malformed-input
tests apply; engine recovery tests do not.

The HTTP area is implemented as an `api/panorama` service following
[ADR-0147](0147-splitting-the-api-server-object.md): it owns its handlers and store,
uses `sidecar.NewStore`, and schedules store access through the API run loop. The
central route table remains in `api/openapi.go`. The initial resource surface
supports list, create, read, update with expected revision, delete, import/export,
and validation. Request sizes and XML parser limits are bounded.

### 4. Atlas bindings

Bindings are standard ArchiMate properties carried by the exchange document. They
contain only stable, opaque Atlas resource ids; names and mutable operational facts
are resolved by the server. Repeated property values express many-to-many bindings.
At minimum the following bindings are defined:

| ArchiMate element | Atlas resource |
|---|---|
| Application Component | Process application id |
| Business Process | BPMN process id |
| Application Service | Connector id, job type, or API identifier |
| Node | Local Atlas runtime id or deployment target id |
| Artifact | Released process application id and version |

Binding property definitions use an `atlas.` namespace and are versioned as a small
public contract. Unknown namespaced properties round-trip. Credential references,
tokens, passwords, endpoint credentials, and secret values are forbidden. A binding
to a deployment target stores the target id only; the server resolves its base URL
and credential reference from the target store.

The editor presents resolved names and lets an authorized user select only resources
they may see. Missing or inaccessible resources remain explicit unresolved bindings
rather than being silently removed.

ArchiMate **Capability** keeps its standard Strategy-layer meaning: an ability that
an organization possesses. It is not an API endpoint or a runtime feature flag. A
typical trace is:

- a Business Process realizes a Capability;
- an Application Service serves that Business Process;
- an Application Component realizes that Application Service;
- an Artifact realizes the Application Component; and
- that Artifact is assigned to a Node.

Technical functions reported by an Atlas runtime are called **features**, not
capabilities, to preserve that distinction.

### 5. Editor packaging

The ArchiMate editor uses `diagram-js` directly with Atlas-owned palette, rules,
renderers, commands, import/export, property panel, and overlays. It is a separate
bundle from the BPMN Modeler and must not modify or fork `bpmn-js` behavior.

The browser bundle and its assets are vendored under `api/web/vendor/archimate/`
and embedded in the Atlas binary. The repository records the exact upstream
versions, licenses, checksums, and reproducible rebuild command. Building or running
Atlas does not fetch a CDN asset and does not require a Node.js toolchain. Any
third-party editor adoption requires a separate dependency/license review and
interchange-fixture proof.

Undo/redo, save/reload, multi-view switching, semantic validation, keyboard access,
and import/export are acceptance criteria, not post-MVP polish. The editor uses the
same revision check as the API so two browser sessions cannot silently overwrite
each other.

### 6. Runtime identity and observations

Live correlation needs a stable identity for every Atlas server. Product version
information from `/api/v1/info` is insufficient because it does not identify a
runtime across restarts. Atlas therefore adds an authenticated, read-only node
descriptor with:

- stable runtime id;
- operator-defined display name and environment;
- non-secret labels;
- product version, revision, and build information;
- partition count; and
- supported feature ids.

The stable id is operator-configurable or generated once and persisted under the
data directory. The descriptor never returns credentials, environment variables,
filesystem paths, or secret material. Remote access uses a least-privilege status
scope; existing deploy credentials are not automatically broadened.

Panorama resolves a selected view into an observation document keyed by ArchiMate
element id. Each observation includes source, status, `observedAt`, staleness, and a
bounded detail object. Initial sources are:

- local readiness, health, version, deployments, instances, jobs, and incidents;
- remote Atlas node descriptors and status through deployment-target bindings; and
- deployment/release facts already held by the process-application and target
  stores.

Prometheus and OpenSearch adapters may add metrics and log-derived observations in
later slices. The declarative XML is never mutated by polling. Observations are
computed on request or held in a bounded, expiring cache; they are not durable
architecture facts.

All remote calls are made by the Atlas server, never directly by the browser. They
run outside the API run loop with bounded concurrency, deadlines, response-size
limits, TLS validation, and per-target error isolation. Results return partial
success honestly. A failed target does not hide healthy targets or make the whole
model appear healthy.

The UI distinguishes at least:

- **healthy** — current observation satisfies the declared binding;
- **degraded** — reachable but warning, incident, or desired/observed deviation;
- **not ready** — reachable but unavailable for work;
- **unreachable** — the source could not be contacted;
- **stale** — the last observation exceeded its freshness contract;
- **unbound/unknown** — no runtime binding or no applicable observation; and
- **discovered but unmodeled** — Atlas found a resource not present in the model.

ArchiMate layer colors remain intact. Runtime state is shown with borders, badges,
icons, and an accessible text legend rather than recoloring semantic element fills.

### 7. Future notations

The Panorama resource envelope contains an explicit notation id such as
`archimate-3.2`, and internal editor type ids are namespaced (for example,
`archimate:ApplicationComponent`). This prevents ArchiMate assumptions from leaking
into generic model, view, and observation APIs.

UAF, if added, is a separate notation profile/projection with its own metamodel,
validation, views, and interchange rules. It is not a renderer toggle over an
ArchiMate model. Any conversion requires a separate ADR, an explicit mapping, loss
diagnostics, and retention of the original source model.

## Consequences

### Positive

- Architects get a standards-based, importable landscape rather than a proprietary
  picture.
- A semantic element can appear in several views while retaining one identity and
  one set of Atlas bindings.
- Atlas can show desired-versus-observed architecture without contaminating the
  model with transient health state.
- Existing application sharing, deployment targets, credentials, and runtime APIs
  remain authoritative.
- The engine event log and recovery path remain unchanged.
- The explicit notation boundary supports future UAF work without a misleading
  one-model/two-renderers design.

### Negative

- A conformant importer, relationship rules, property round-tripping, and a custom
  renderer are substantially more work than drawing Atlas-specific boxes.
- Full ArchiMate 3.2 authoring cannot ship in one slice; the supported subset and
  lossless behavior require visible limits and conformance fixtures.
- Live landscape views depend on remote permissions and network quality, so partial,
  stale, and unknown states are normal UI states.
- A dedicated process application may be needed as the ownership container for an
  enterprise-wide landscape.
- Vendoring a browser bundle adds dependency, license, and reproducibility
  maintenance.

## Delivery slices

1. **P1 — Architecture model:** sidecar resource, application ownership, Open
   Exchange import/export, bounded parser, revision conflicts, semantic validation,
   backup/restore, and interoperability fixtures.
2. **P2 — ArchiMate editor:** vendored `diagram-js` bundle, bounded palette,
   property panel, multi-view canvas, undo/redo, save/reload, and browser E2E tests.
3. **P2.5 — Landscape mesh** (added by the 2026-08-31 amendment): the derived
   whole-instance graph, computed per requesting principal with explicit restricted
   placeholders where a sharing scope cuts a path; search, filter, and impact analysis;
   a measured size budget; then the model overlay and the C4 projection on P3, and
   severity aggregation on P4.
4. **P3 — Atlas bindings:** namespaced properties and selectors for process
   applications, BPMN processes, connectors/job types, releases, runtimes, and
   deployment targets; no secret material.
5. **P4 — Live Panorama:** stable node descriptor, local and remote observation
   projection, freshness/partial-failure semantics, and accessible overlays.
6. **P5 — Landscape intelligence:** desired-versus-observed drift over time — a
   journal of transitions rather than a store of samples — and optional
   Prometheus/OpenSearch adapters for historical context, which query those stores
   rather than copying them; both per the 2026-09-01 amendments.
   Dependency/impact analysis and discovery of unmodeled resources moved to P2.5 in
   the 2026-08-31 amendment.

Each slice ships end to end: API, persistence where applicable, embedded UI,
authorization, tests, documentation, and OpenAPI contract. A slice is not complete
when only the renderer or only the API exists.
