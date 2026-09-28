# ADR-DRAFT: One graph model, several projections, and a successor view built beside the starmap

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-28
- **Deciders:** Atlas maintainers
- **Open question:** whether the structured traversal of §6 is enough, or whether a
  textual query language (an openCypher subset) is needed on top of it. The answer
  depends on the questions people actually bring to the graph, and nobody has
  collected them. §6 is shaped so that a textual front-end can be added later without
  a second engine, which is what lets this record leave the question open rather than
  guess at it.
- **Question checked:** 2026-09

## Context and problem statement

The aim this record serves is stated plainly: **everything Atlas knows about should be
one graph that can be examined** — an Atlas installation, an application, a process
and its deployments, a worker, a decision, a catalogue, a capability, a value stream,
a requirement, and down to what actually ran.

Atlas does not have that graph. It has several pictures, each correct for its own
altitude, and nothing they are all pictures *of*:

| Surface | What it holds | Record |
|---|---|---|
| ArchiMate model in Panorama | capabilities, value streams and other authored elements, bound to Atlas resources by `atlas.` properties | [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md), [ADR-0308](0308-panorama-binds-the-capability-register.md) |
| Capability register | capabilities, value streams and their stages, realizations, `requires` | [ADR-0305](0305-business-capabilities-and-value-streams.md) |
| Landscape mesh (starmap) | domain, application, process (deployment), definition, worker, decision, draft, target, catalogue, product | [ADR-0211](0211-panorama-derived-landscape-mesh.md), [ADR-0402](0402-one-estate-several-nodes.md) |
| Information model | classes, associations, and where a process uses a class | [ADR-0230](0230-process-information-model.md) |
| Run graph | live element instances, as a library (`rungraph/`) with no API and no view | [ADR-0404](0404-the-whole-graph-can-be-walked.md) |
| Single-instance graphs | object graph, decision graph, timeline | [ADR-0065](0065-multi-token-process-replay.md), [ADR-0066](0066-decision-evaluation-records.md) |
| Requirements | nothing yet — the record is proposed | [ADR-0407](0407-requirements-as-design-time-records.md) |

Three facts about this landscape decide the shape of the answer.

**Each surface has its own vocabulary and its own identity.** The mesh names kinds in
`api/panorama/mesh.go` (`KindApplication` … `KindProduct`, edges `contains`, `calls`,
`uses`, `offers`, `composition`, `aggregation`, `requires`, `deploys`, and `promotes`
in `estate.go`). The register names capabilities by `Key` and realizations by
`applicationKey`/`processId`/`workerRef` (`api/capability/model.go`). The information
model names classes per model. Nothing states that a `processId` in a realization and
a `process` node on the mesh are the same thing; each reader of both re-derives it.

**The one traversal that exists runs in the browser.** Impact analysis — "what breaks
if this worker is down" — is `impactOf(graph, startIds, {direction, depth})` in
`api/web/panorama-mesh.js`, over the at most 400 nodes the mesh has sent. A question
that crosses into the register ("which capabilities lose a realization if this worker
is down") cannot be asked at all, because the register's edges never reach the page
that walks.

**The separations were each decided for a reason, and the reasons still hold.**
ADR-0211 §2 keeps derived and modeled content visually distinct. ADR-0308 keeps
capabilities off the mesh, because the mesh compares model against landscape and a
capability there would be reported as drift against a landscape never meant to hold
one. [ADR-0403](0403-the-estate-graph-is-derived-from-the-log.md) rule 2 keeps runtime
facts off the structural graph as nodes. ADR-0404 §1 builds the run graph as a second
surface rather than a zoom level of the starmap.

This record does **not** revisit any of those. What is missing is not permission to
mix the pictures. It is the **model** every picture is a projection of, a traversal
that runs over that model rather than over one picture's payload, and a view able to
show more than one layer of it at once without erasing the distinctions above.

## Decision drivers

- **A question that crosses layers must be answerable in one request.** "Which
  requirements constrain a process that currently has incidents", "which value-stream
  stages depend, transitively, on this worker", "which capabilities have no deployed
  realization". Each is a join across two or three of the surfaces above, and today
  each is a manual one.
- **ADR-0403 rule 2 stands.** On the structural graph an instance is a number, not a
  node. That is accepted as sufficient by the maintainers for this aim.
- **ADR-0404 §1 stands.** The run graph is a second surface; instances are examined
  there, entered from a number on the structural graph.
- **The starmap is not rebuilt.** A more capable view is built *beside* it and
  replaces it only once it matches it, in function and in appearance.
- **The engine always has priority** while the graph is served from the same process
  (I3; [ADR-0239](0239-off-loop-queries.md), ADR-0382). Anything whose cost grows
  with the data is estimated before it runs, and refused above a budget.
- **A layer's truth conditions survive the union.** Authored, derived and observed
  facts mean different things; a graph that joins them must say which is which on
  every node and every edge, always.
- **Authorization is per node.** A graph spanning every area inherits every area's
  visibility rule ([ADR-0071](0071-sharing-scopes.md),
  [ADR-0275](0275-instance-visibility.md), [ADR-0278](0278-object-authorization.md)),
  and a filtered answer says that it is filtered (ADR-0211 §3).
- **No new store, no CGO, one binary**
  ([ADR-0010](0010-go-and-no-cgo.md), [ADR-0011](0011-single-binary-distribution-and-web-ui.md)).
  Every surface above resolves its facts at read time from the store that owns them;
  a graph that copied them would be a second, stale copy within a week (the rule
  ADR-0189 §4 and ADR-0305 already apply to bindings and realizations).

## Considered options

1. **Keep the surfaces separate and add cross-links** — a capability page links to
   the mesh node of its realization, and so on.
2. **Extend the starmap in place** until it draws every kind.
3. **One graph model — a declared catalogue of kinds, layers and identities, resolved
   at read time — with a server-side traversal over it, and a successor view built
   beside the starmap.**
4. **Export everything into an external graph database** and use its view and its
   query language.

## Decision outcome

Chosen: **option 3.** Nine points.

### 1. The graph model is a catalogue, not a store

Every node kind and every edge kind in Atlas is declared **once**, in one Go package
(working name `api/graphmodel`), with:

| Attribute | Meaning |
|---|---|
| kind | the name, reused from the surface that already has one (`application`, `worker`, …) rather than invented anew |
| layer | modeled, derived or observed — see §2 |
| tier | T1–T4 of ADR-0403 |
| identity | how an instance of the kind is named estate-wide — see §3 |
| source | the store or function that resolves it, e.g. the deployment registry, `capability.Store`, `infomodel` usage |
| cardinality class | *estate-sized* (bounded by what people author and deploy) or *population-sized* (grows with traffic) |
| visibility rule | the existing rule of the area that owns it; never a new one |

Nothing is copied. A request resolves the nodes it needs from their owning stores, as
`collectCapabilityLandscape` and the mesh collector already do. The catalogue is what
makes those resolutions agree on names.

A Go test enforces the catalogue in both directions: every kind a projection emits is
declared, and every declared kind has a resolver. That is the guard pattern the ADR
directory already uses on itself, and it is what stops a new surface from inventing
another vocabulary.

### 2. Layer and provenance are mandatory attributes, and layers are never merged

Three layers, on every node:

- **modeled** — authored by people: capability, value stream, stage, requirement,
  ArchiMate element, information-model class, draft;
- **derived** — what the server reads off deployed artifacts and configuration:
  domain, application, definition, deployment, worker, decision, catalogue, product,
  target;
- **observed** — what ran: counts and states on T1 nodes and edges (§4), and the run
  graph's element instances behind them.

Edges carry the provenance ADR-0211 §2 and
[ADR-0400](0400-an-edge-that-was-taken-is-not-an-edge-that-was-declared.md) already
use — declared, derived, taken — extended by one word, **authored**, for an edge a
person asserted in a record (a realization, a stage's capability, a requirement's
target, a capability's `requires`).

**Where two layers speak about the same relation, neither wins silently.** A
realization that names a process nobody has deployed, or a deployed process that no
realization names, is not resolved into one edge: both facts stay, and the
disagreement is a **finding** — the pattern ADR-0305's gap report and ADR-0189's drift
already follow. A comparison is made only between kinds that both layers can hold,
which is ADR-0308's binding allowlist: a capability has no observed counterpart, so no
drift is ever computed for it. This is the answer to "what does the graph say when the
model and the runtime disagree": it says both, and that they disagree.

### 3. Identity reuses what exists

- Design-time records keep their portable key: capability, value stream, requirement
  (ADR-0305, ADR-0407). Stages are `(valueStreamKey, stageKey)`.
- Artifacts keep their artifact id, and renames follow
  [ADR-0222](0222-artifact-id-renames.md).
- Definition and deployment follow
  [ADR-0401](0401-graph-identity-across-several-logs.md) §2; runtime entities follow
  its rule 1, `(runtimeId, key)`.
- A BPMN element is `(definition, elementId)`. Its stability is ADR-0407's open
  question and is inherited, not answered, here.

A node id in the model is `kind:identity`. No new identifier is minted.

### 4. Runtime reaches the structural graph as properties (ADR-0403 rule 2, unchanged)

On T1 a process carries its live and completed counts, an edge carries `taken` and
`takenSince`, a worker and a deployment carry their incident severity — all from the
maintained counters that already exist ([ADR-0080](0080-runtime-aggregate-counters.md),
[ADR-0261](0261-instances-on-an-element.md), ADR-0400). No instance becomes a node
here.

Every such number is also an **entry point into the run graph** with that node as the
scope — the "entry scope follows the question" rule of ADR-0404 §9. Instances are
examined there, on the second surface ADR-0404 §1 describes, and nowhere else.

### 5. Traversal moves to the server

The structural graph across all three layers stays estate-sized: hundreds of
processes, workers and decisions, plus what people author — capabilities, stages,
requirements, classes. That is small enough to traverse whole on every request once
it is resolved, and far too large a join to leave to each page.

So traversal becomes a server function over the resolved model: resolution of
estate-sized facts happens where the owning stores require (the run loop for the
deployment registry and counters, as today), and the walk itself runs off the loop on
the resolved values. `impactOf` in the browser stops being the source of truth; the
page may keep one-hop hover adjacency (ADR-0211 §6 amendment), which needs nothing the
page does not already hold.

### 6. Queries are structured traversals, estimated before they run

A query is a JSON document with four parts:

1. **start** — a selection: kind, key, tag, layer, or text search;
2. **steps** — each an edge-kind set, a direction and a depth;
3. **filters** — on layer, provenance, severity, state, or a property;
4. **result** — nodes, paths, or counts grouped by an attribute.

It is served over HTTP and, from the start, as an MCP tool
([ADR-0016](0016-mcp-server-over-http-api.md)), because an agent asking "what depends
on this" is as likely a reader as a person.

Two rules make it safe to offer:

- **Every query is estimated before it runs.** A query that stays on estate-sized kinds
  is bounded by the estate and always runs. A step into a population-sized kind — the
  run graph — becomes a scope for ADR-0404 and falls under its §9 budget: above a
  stated share of the budget the answer is a **warning** naming the estimated cost,
  and above the budget it is a **refusal** naming the nearest scope that fits. A
  warning alone would let the view outrun the engine, which the engine's priority
  forbids.
- **The shape is the `MATCH`-pattern subset of openCypher, written as data.** A start,
  typed steps with direction and length, and filters are exactly what a Cypher pattern
  expresses. Choosing that subset, rather than inventing a vocabulary, means a textual
  front-end can later compile into the same plan — one engine, two syntaxes — if the
  open question above is answered "yes". What the structured form refuses by
  construction is what makes estimation and authorization hard: unbounded variable-
  length paths through population-sized kinds, and arbitrary joins with no start.

### 7. The successor view is built beside the starmap, and replaces it at a stated bar

A new view — working title **Atlas Graph** — renders projections of the model. The
starmap stays as it is, receiving fixes only, until the new view passes the bar below;
then the starmap's route points at the new view and its code is retired in the same
change.

The view opens at the altitude the starmap shows today. **Layer switches** add the
modeled layer (capabilities, value streams and stages, requirements once ADR-0407
lands, classes) and the observed numbers; each layer keeps its own visual grammar so
that a reader always sees which layer an element belongs to (ADR-0211 §2).

**The bar for replacing the starmap**, every item of it:

- everything the starmap does: search and filter (ADR-0211 §6), impact to a chosen
  depth, hover adjacency, provenance distinction (§2), severity classes (§4), the
  400-node budget with its server-side cluster (§7), the C4 projection (§8), exports
  (§10), saved views;
- the estate view of ADR-0402 and the traversal counts of ADR-0400;
- the same opening paint time at the 400-node budget, measured in the same browser
  test;
- a visual review in which the maintainers state that it looks at least as good as the
  starmap. That is a judgment, and it is recorded as one rather than dressed as a
  metric.

### 8. Authorization: a walk stops at what the caller may not see, and says so

Every node is resolved under the rule of the area that owns it. A node the caller may
not see is returned as the existing `restricted` kind, and **a traversal does not
continue through it**. Continuing would disclose the structure behind a node whose
existence is all the caller may know; stopping makes the answer incomplete, so the
result states every point at which it stopped (ADR-0211 §3).

Across installations, the estate rules of ADR-0402 apply unchanged. ADR-0402's open
question — an estate reader who may not see all of it — is inherited: this record makes
it more pressing, because a path is a disclosure a single node is not.

### 9. What this record deliberately does not do

- **No instance on the structural graph** (ADR-0403 rule 2) and **no mixing of the two
  surfaces in one picture** (ADR-0404 §1).
- **No authored content in the WAL** (ADR-0403 rule 1) and **no new fold** in
  `applyToState`.
- **No new store.** The model is resolved at read time and nothing new is persisted.
- **No graph database dependency.** Option 4 stays available as a later, *optional*
  exporter in the shape of [ADR-0114](0114-opensearch-event-exporter.md), never as the
  view itself — see its pros and cons below.
- **No change to the starmap** beyond fixes until §7's bar is met.

### Build order

1. **The catalogue** (§1) with its test, covering the kinds the mesh and the register
   already have. No new behaviour; the value is agreement on names.
2. **Server-side traversal and the query endpoint** (§5, §6) over those kinds, with
   the MCP tool. This alone answers the cross-layer questions above, without a new
   picture.
3. **The modeled layer**: capabilities, value streams and stages, information-model
   classes, each with its findings (§2).
4. **The successor view** (§7), first behind a feature flag beside the starmap.
5. **Requirements**, when ADR-0407 is accepted and built.
6. **The drill into the run graph** from a number (§4), when ADR-0404's view and §9
   budget exist.
7. **Parity review and replacement** of the starmap (§7).

### Consequences

- **Positive:** cross-layer questions become one request — by a person in the view, or
  by an agent over MCP — without mixing layers and without breaking rule 2 or §1.
- **Positive:** every surface stops re-deriving that a realization's `processId` and a
  mesh `process` node are the same thing. That derivation lives once, and is tested.
- **Positive:** the starmap stays usable throughout; nothing is taken away before its
  successor has been judged at least as good.
- **Negative / trade-offs accepted:** two views of the landscape exist for a while, and
  every fix to the starmap in that period is paid for twice or deferred.
- **Negative:** stopping a walk at a restricted node makes answers incomplete for
  readers with partial access. That is chosen over disclosure, and stated in every
  answer it affects.
- **Negative:** without a textual language, ad-hoc questions need a JSON document or
  the view's controls. Accepted until the open question is answered from real use.
- **Follow-ups / risks to watch:** the size of the modeled layer on a large
  installation is not measured — if authored records reach tens of thousands, the
  "estate-sized, walk it whole" assumption in §5 needs its own budget; the stability
  of BPMN element ids (ADR-0407); an optional graph exporter if external analysis is
  asked for; and the estate reader of ADR-0402's open question.

## Pros and cons of the options

### Option 1 — separate surfaces with cross-links
- Good: cheapest; no new concept.
- Bad: a link answers "where is this over there", never "what depends on this across
  both". The joins stay manual, and each page keeps its own vocabulary.

### Option 2 — extend the starmap in place
- Good: one view, no period with two.
- Bad: ADR-0308 already argued why capabilities do not belong on the mesh — they
  would read as drift. Adding layers into a view built around one comparison breaks
  that comparison, and it puts the working view at risk throughout the change, which
  the maintainers explicitly do not want.

### Option 3 — one model, server-side traversal, a successor view beside the starmap (chosen)
- Good: keeps every existing separation, adds the missing agreement on names, makes the
  traversal available to people and agents alike, and lets the new view earn its place
  before it replaces anything.
- Bad: a catalogue to maintain; two views for a period; a query format that is not a
  standard language until and unless the open question says it should be.

### Option 4 — an external graph database
- Good: a mature query language on day one, and history beyond Atlas's retention if it
  is fed from the log.
- Bad: a first-class view that depends on an external component, against ADR-0011; the
  database holds everything and enforces none of Atlas's visibility rules, so it is a
  disclosure surface of its own; and ADR-0404 already found that such an engine would
  build the same projection internally. As an *optional* exporter beside the view it
  remains a reasonable follow-up; as the view it is refused.

## Links

- keeps ADR-0403 rule 1 and rule 2 and ADR-0404 §1 unchanged; uses ADR-0404 §9 for
  every step into the run graph
- builds on ADR-0211 (the mesh, its provenance, filtering, budget and rendering rules),
  ADR-0402 and ADR-0401 (estate and identity), ADR-0400 (taken edges), ADR-0305 and
  ADR-0308 (the register and why it is not on the mesh), ADR-0230 (classes and their
  use), ADR-0407 (requirements, when built)
- bounded by ADR-0239 and ADR-0382 (nothing data-sized holds the writer), ADR-0071,
  ADR-0275 and ADR-0278 (visibility)
- relates to ADR-0016 (the MCP tool) and ADR-0114 (the shape of an optional exporter)
