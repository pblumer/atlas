# ADR-0211: Panorama's derived landscape mesh and notation projections

- **Status:** Accepted (amended 2026-08-31 — §11 splits P2.5c's P4 dependency per
  state instead of holding the whole stage behind it; amended 2026-09-01 — §7 lays
  the graph out in a world of its own size rather than in the viewport, and names
  by magnification rather than by node count; amended 2026-09-02 — §7 lets the
  reader arrange the landscape by hand, and sizes a node by its connectivity as well
  as by its kind)
- **Date:** 2026-08-31
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) established
Panorama as a standards-based architecture workspace: an ArchiMate 3.2 model held in
the Open Group Exchange format, Atlas bindings as namespaced properties, and a
separate observation projection for runtime facts. Its delivery slices run P1
(architecture model, shipped), P2 (ArchiMate editor, read-only canvas shipped), P3
(bindings), P4 (live observations), P5 (landscape intelligence).

Everything ADR-0189 describes is **drawn**. A view exists because a person placed
elements on it. That leaves three questions unanswered, and they are the ones a user
actually arrives with:

1. **What does Panorama show on an instance where nobody has modeled anything?**
   Today: nothing. The value of the whole surface is gated behind a modeling effort
   that has to be paid up front, before any of it pays back. Every standalone
   architecture tool has this same cold-start problem, and Atlas has the one thing
   that could avoid it — it already knows its own process applications, deployed
   processes, call activities, workers, job types, releases, and deployment
   targets. The structure is *present*, not hypothetical.

2. **How does a user get from "the landscape" to "this instance is stuck"?** Atlas
   has a live BPMN overlay (Operations) and a token replay
   ([ADR-0065](0065-multi-token-process-replay.md)), but nothing above them. There
   is no navigational path from a whole-system view down to a running token, and no
   statement of which view owns which altitude — so the next feature that needs a
   process picture is as likely to build a second one inside Panorama as to reuse
   the one that exists.

3. **Can a user see the landscape in a notation other than ArchiMate?** C4 in
   particular is what a large share of the audience reads. ADR-0189 §7 forbids
   treating notations as renderer themes over one model, and is right to: a C4
   Container and an ArchiMate Application Component are not the same concept, and a
   toggle that pretends otherwise exports semantically false diagrams under a
   standard's name. But "forbidden as a theme" was never meant to settle "forbidden
   entirely", and the question has not been answered either way.

A fourth question is created by answering the first. A derived whole-instance graph
crosses the sharing boundary that ADR-0189 deliberately reused: Panorama models are
owned by one process application and inherit its scope
([ADR-0071](0071-sharing-scopes.md), [ADR-0128](0128-process-applications.md)). A
graph "of the whole instance" either shows a viewer resources they may not see, or
shows each viewer a different, silently incomplete graph. An incomplete mesh is
worse than a small one, because nothing in the picture says it is incomplete.

The question this record settles: **does Panorama gain a derived, whole-instance
landscape graph in addition to its drawn views — and if so, where does it get its
structure, its colors, its authorization, and its notations?**

## Decision drivers

- Be useful on an instance where no architecture model exists yet.
- Keep declared intent, Atlas bindings, and runtime observation separate — the
  three-way distinction ADR-0189 exists to protect — while showing them together.
- Never let a viewer's sharing scope silently subtract from a picture that claims to
  be complete.
- Do not weaken the honesty of ADR-0189 §6's seven observation states in order to
  get a simple traffic light.
- Terminate the drilldown in the views Atlas already has, rather than growing a
  second BPMN renderer inside Panorama.
- Stay buildless and CDN-free ([ADR-0012](0012-web-ui-app-shell.md)) at whatever
  graph size we claim to support.
- Preserve the engine invariants: nothing here enters the WAL, `applyToState`, or a
  processor.
- Let a second notation be read without letting it be authored, and without claiming
  a fidelity we have not tested.

## Considered options

### Where the mesh gets its structure

1. **Render the ArchiMate model as a graph.** One source of truth, no new
   derivation. But it inherits the cold-start problem whole: an unmodeled instance
   renders an empty mesh, and the edges are only as true as the last person to draw
   them.
2. **Derive the graph from Atlas's own resources; overlay the model onto it.** The
   graph exists on first boot, and its edges are facts (a call activity *is* a
   dependency) rather than claims. The model becomes an annotation layer that says
   what the resources *mean*.
3. **Two unrelated surfaces — a derived graph and a drawn model, side by side.**
   Avoids all correlation questions, and forfeits the entire point: knowing that a
   modeled component has no deployed implementation is the answer users want.

Option 2 is selected.

### How a second notation is offered

1. **Notation as a renderer theme.** Rejected by ADR-0189 §7, and rightly.
2. **A full second authoring notation** with its own metamodel, palette, validation,
   and interchange — what ADR-0189 §7 reserves for UAF.
3. **Read-only projection from ArchiMate under a declared mapping, with loss
   reported.** Not a theme, because it is one-directional, never authored, never
   round-tripped, and states what it dropped.

Option 3 is selected for C4 only.

### Where the mesh lives in the shell

1. **Replace the app's start page.** Maximum prominence.
2. **Panorama's own entry view, plus a Console tile, plus a per-role start-page
   setting.**
3. **A tab inside the existing model library.** Minimum disruption, minimum
   discoverability.

Option 2 is selected.

## Decision outcome

### 1. The landscape mesh is derived, and the model annotates it

Panorama gains a **landscape mesh**: a force-directed graph of the Atlas instance,
computed from resources Atlas already holds. It is a projection, never a stored
document, and it is never written back to any ArchiMate XML.

The first derivation covers:

| Node kind | Source |
|---|---|
| Process application | the process-application store ([ADR-0128](0128-process-applications.md)) |
| BPMN process | deployed processes, grouped under their application |
| Worker | the configured workers (the connector store, ADR-0203) and their placements |
| Job type / Worker Type | job type registry and Worker Type metadata |
| Deployment target | the target store |
| Release | the release store |
| DMN decision | the DMN registry |

and the following edges, each of which is a **fact Atlas can point at**, not a drawn
assertion:

- process → process, from call activities;
- process → worker / job type, from service-task placements;
- process → decision, from business-rule-task bindings;
- application → release → deployment target, from release and target records;
- application → process / worker / model, from artifact ownership.

The mesh has **no persistence of its own**. It is computed on request from
design-time stores and existing query endpoints, all reached through the API run
loop ([ADR-0147](0147-splitting-the-api-server-object.md)), and held in a bounded,
expiring cache. It does not enter the WAL, `applyToState`, snapshots, or recovery.
Remote calls, when live status is layered on, follow ADR-0189 §6 exactly: server-
side, outside the run loop, bounded concurrency and deadlines, per-target isolation.

An ArchiMate model **annotates** this graph through the P3 bindings. A bound element
contributes its name, layer, documentation, and relationships to the node Atlas
derived. This produces the two comparisons that justify the whole feature:

- a **modeled but not present** element — declared architecture with no
  corresponding Atlas resource; and
- a **present but unmodeled** resource — ADR-0189 §6's *discovered but unmodeled*,
  now with a place to appear.

### 2. Derived and modeled content are visually distinct, always

A node's provenance is part of its rendering — derived, modeled, or both — and is
never inferable only from context. A single legend states the three cases. The mesh
also carries an explicit mode control: *what Atlas found*, *what was declared*, or
*both, compared*. A blended picture with no provenance marking is exactly the
conflation of declared intent and observed fact that ADR-0189 was written to
prevent, and is refused here for the same reason.

### 3. Authorization: a filtered mesh must say that it is filtered

The mesh is computed **per requesting principal** against the existing sharing scopes
(ADR-0071), never once and shared across viewers. Resources the principal may not see
are excluded from node and edge data — no names, no ids, no counts that would let
them be inferred.

Where exclusion breaks a path the viewer *can* otherwise see — a visible process
whose call activity targets a process in an application they cannot see — the mesh
renders an explicit **restricted** placeholder node carrying no identifying
information beyond its kind. The edge is drawn to it. The alternative, dropping the
edge, produces a graph that looks complete and is not; a viewer would read "this
process depends on nothing" from an absence that means "you may not see what it
depends on". The count of restricted nodes is shown in the legend so the incompleteness
is a stated fact rather than a discovery.

### 4. Status: three severity classes over seven states, never instead of them

ADR-0189 §6's seven observation states remain the model and the API contract:
healthy, degraded, not ready, unreachable, stale, unbound/unknown, discovered but
unmodeled. The mesh does not replace them with a traffic light.

For zoom-out legibility the mesh aggregates them into three **severity classes** —
`ok`, `attention`, `critical` — under a mapping fixed in one place and shown in the
legend. Two rules govern it:

- *unreachable* and *stale* map to `attention`, never to `critical`. "I do not know"
  and "it is broken" are different findings, and a picture that renders them alike
  loses its credibility on the first network fault, at exactly the moment it is being
  relied on.
- *unbound/unknown* is its own neutral rendering, not a severity. Most nodes in a
  young landscape are unbound; coloring them as a problem makes the whole mesh a
  problem.

Aggregation to a parent node is **worst-of**, and is never shown without its reason:
a node that inherits severity from a descendant states which descendant and why on
hover and in its detail panel. An unattributed red parent tells a user that something
is wrong somewhere, which is not actionable and trains them to ignore it.

Severity is carried by border, badge, and shape. ArchiMate layer fills are not
recolored (ADR-0189 §6), and color is never the sole channel: every severity is
distinguishable without color perception, and the legend is text.

### 5. Four altitudes, two of which already exist

The drilldown is defined as four levels, and Panorama owns only the first two:

| Level | Content | Owner |
|---|---|---|
| L0 Landscape | the derived mesh across the instance | Panorama (new) |
| L1 Application | one application's processes, workers, data, decisions, releases | Panorama (new) |
| L2 Process | BPMN diagram with live overlay | **Operations, existing** |
| L3 Instance | token replay and causal lineage ([ADR-0065](0065-multi-token-process-replay.md)) | **Operations, existing** |

Panorama **must not** render a BPMN diagram. L1 links into the existing views; it
does not reimplement them. This is stated as a decision and not left to taste,
because the cheapest wrong turn available here is a second, diverging process
renderer that starts as "just a preview".

### 6. Navigation is search-first, not click-only

Filter and full-text search over the mesh (by kind, application, severity, binding
state, name) are acceptance criteria for the first slice, not later polish. A graph
of a few hundred nodes is not navigable by clicking, and the size at which the
feature stops working is not the size at which it stops being shipped.

**Impact analysis is part of the first mesh slice**, moved forward from ADR-0189's
P5: selecting a node reveals its dependents and dependencies to a chosen depth
("what breaks if this worker is down"). This is the capability a graph has and a
diagram does not, and the edges it needs are the ones §1 already derives — deferring
it would ship the cost of the mesh without its distinctive payoff. The remainder of
P5 (desired-versus-observed drift over time, Prometheus/OpenSearch history) is
unaffected.

> **Amendment (2026-09-01): the graph is laid out in a world, not in the window.**
> §7 below asks the mesh not to render "an unusable hairball". It was producing one
> well inside its own budget, and the cause was arithmetic rather than density: the
> layout settled inside the viewport and was then scaled to fill it, and that scale
> moves *positions* while radii stay fixed. Any graph whose settled extent exceeded
> the frame was therefore compressed into it with its circles left at full size —
> which guarantees overlap, and guarantees more of it with every node added.
>
> The graph is now laid out in a world sized from its own content, with each node
> allocated personal space beyond its circle, and the viewport is a window onto that
> world. How much of that world the nodes occupy was settled by measuring rather
> than by taste, and the two goals turn out to be the same one: tightening it until
> the space stops reading as empty is also what shrinks the world enough for the
> names below to be legible at the opening view. The opening view still shows the whole landscape, as it must; reading it
> closely is what the zoom and pan already built for §7 are for. The separation
> guarantee is re-established *after* the fit, in the coordinates the circles are
> actually drawn in, because that is the only place it means anything.
>
> Two consequences follow, and both replace a rule that was standing in for the
> crowding rather than for a real constraint:
>
> - **Names are decided by magnification, not by node count.** The old rule painted
>   every name below about twenty-five nodes and only the applications above it.
>   Names collided because the graph was compressed, so the count was a proxy for
>   the defect above. With room beside every node, the honest question is whether
>   the text is large enough on screen to read — so a name appears when it is, and
>   zooming is what brings the rest out. Applications cross the threshold first,
>   because "where is Billing" has to be answerable before the detail is.
> - **Adjacency is shown on hover.** §6's impact analysis answers what breaks if a
>   node goes down, transitively, and needs a click. "What does this one touch" is
>   asked far more often while reading, so pointing at a node lifts its immediate
>   neighbours and the edges to them and lets the rest fall back. One hop
>   deliberately: the transitive answer is what selecting is for, and a hover that
>   lit half the landscape would be a worse version of it rather than a second tool.
>   Nothing is re-laid-out and nothing is re-rendered, so the picture cannot move
>   under the pointer while it is being read.

> **Amendment (2026-09-02): the arrangement is the reader's, and size says how
> connected a node is.**
> Two more things §7's legibility half was missing, and both of them are about what
> the eye does with a few hundred circles before it reads any of them.
>
> - **Nodes can be picked up.** The layout answers "where does this graph want to
>   sit", which is the right first answer and never the last one: the person reading
>   it knows things the simulation does not — that these four belong together, that
>   this hub should be out of the way — and until now had no way to say so. Dragging
>   a node moves it and the graph rearranges itself around it, which also makes
>   adjacency visible in the motion rather than only in the lines. A dropped node
>   stays dropped: this simulation settles once rather than running continuously, so
>   releasing a node back into it would put it straight back where it started and
>   make the whole gesture pointless. It is *pinned* instead — marked on the node,
>   because a node that is not where the layout put it is a fact about the picture
>   and hiding it would make the arrangement look like the simulation's own answer —
>   and undone one node at a time or wholesale. While anything is pinned the fit is
>   skipped, because fitting rescales every position and would slide the pins off the
>   spots they were dropped on; that is the trade, and the arrangement is worth more
>   than the last few percent of margin. The arrangement survives filtering and
>   selecting, and it is not stored: nothing on this view is (§2). The same step is on
>   the arrow keys for a focused node, because a convenience only some people can have
>   is not one.
>
> - **Size carries connectivity as well as kind.** Kind alone made every process the
>   same size, so the one that half the landscape hangs off looked exactly like a leaf
>   — and finding it meant clicking through until the impact panel said so. A node is
>   now drawn at its kind's floor plus however far its dependency count carries it up
>   that kind's band, logarithmically, because the difference between one dependency
>   and four is the one worth seeing and the difference between forty and fifty is not.
>   The bands are closed: the largest process is smaller than the smallest application,
>   so rank survives connectivity rather than competing with it, and size says two
>   things at once without either overwriting the other. Containment is not counted,
>   for the same reason §6 does not walk it — an application does not depend on the
>   processes it holds, and counting them would make every application a hub by
>   construction and say nothing. The reference degree is fixed rather than taken from
>   the graph on screen, so a node does not change size when a filter removes some
>   other node: its size describes the node, not the current screen.
>
> One consequence, and it was overdue: selecting a node no longer re-lays-out the
> graph. Impact analysis is an answer *about the picture on screen*, so the picture
> must not move while the answer is being given — and re-running a two-hundred-
> iteration simulation to toggle two classes was costing a few hundred milliseconds
> per click at the top of the budget.

### 7. A stated size budget, and a server-side fallback

The mesh declares a **supported node/edge budget**, verified by a browser performance
test at that size, and degrades honestly beyond it: it clusters by application and
says it has done so, rather than rendering an unusable hairball or freezing the tab.

Layout runs in the browser within the budget, subject to ADR-0012's buildless,
CDN-free, no-Node-toolchain rule. If the measured budget falls short of realistic
instances, the fallback is server-side layout — the pipeline in `api/layout`
([ADR-0124](0124-server-side-diagram-auto-layout.md),
[ADR-0127](0127-layered-layout-pipeline-and-invariants.md)) already exists for BPMN
— not a bundler and not a CDN dependency.

### 8. C4 is a read-only projection, not a theme

Panorama may render a **C4 projection** of an ArchiMate model. It is constrained so
that it cannot become a theme:

- **ArchiMate remains the only authored notation.** The projection is never editable
  and never imported back. There is no C4 source document.
- **The mapping is explicit and versioned**, published alongside the notation ids
  ADR-0189 §7 requires (`archimate-3.2`, and a projection id such as `c4-projection`
  that is not a peer notation).
- **Loss is reported, not hidden.** Every element and relationship the projection
  cannot express is listed in the UI and in the export. A projection that silently
  drops what it cannot map is the failure mode the theme ban exists to prevent.
- **Exports are labeled** as a projection of the named source model at a named
  revision.

C4 is chosen because it is notation-poor — person, system, container, component,
relationship — so a subset of ArchiMate maps onto it with describable loss. The
inverse does not hold, which is precisely why the projection is one-directional.

Any *authorable* second notation, UAF included, remains what ADR-0189 §7 says it is:
a separate profile with its own metamodel and its own record. This decision does not
open that door.

### 9. Placement in the shell

The mesh becomes Panorama's landing view, reached from the app switcher. The Console
start page gains a mesh summary tile. The application start page becomes a per-role
setting rather than a global change.

Panorama does not become the product's start page. Most Atlas users are task workers
and operators for whom a landscape is not the first question of the day, and a
start page whose content depends on a modeling effort and a sharing scope is a
start page that is empty or partial for a large share of the people who see it.

### 10. Export: model exports are safe, live exports are a disclosure surface

Two export classes, and they are not interchangeable:

- **Model export** — Open Exchange XML, and rendered SVG/PNG/PDF of a view or the
  mesh's structure. Governed by ADR-0189 §4: no secrets, ids only.
- **Live export** — anything carrying observation data. This is a new disclosure
  surface that the drawn views did not have: deployment-target hostnames, incident
  detail, version and topology facts. Live exports are **redacted** to the same rules
  the observation API already applies to the requesting principal, and they carry a
  visible **observation timestamp and source instance** rendered into the artifact
  itself.

The timestamp is not decoration. An undated "all green" picture circulates inside an
organization long after it stopped being true, and is believed because it looks like
evidence.

### 11. Delivery position

This work sits between ADR-0189's P2 and P4, as **P2.5**, with staged dependencies:

1. **P2.5a — derived mesh.** Structure, provenance rendering, scope filtering with
   restricted placeholders, search/filter, impact analysis, size budget, L0/L1
   navigation into the existing L2/L3 views. Depends on nothing from P3 or P4: it
   ships value on an instance with no architecture model at all.
2. **P2.5b — model overlay.** Modeled-versus-present comparison. Requires P3
   bindings.
3. **P2.5c — severity on the mesh.** The three-class aggregation over live
   observations. Requires P4 — *amended*: it requires P4 for two of the seven states,
   not for the stage.

   > **Amendment (2026-08-31).** Stating the dependency at the stage was too coarse.
   > Three of §4's seven states are things the engine already knows about itself
   > without contacting anything: work parked behind an unresolved incident
   > (*degraded*), a configured worker that cannot serve work (*not ready*), and the
   > absence of either (*healthy*). Only *unreachable* and *stale* need what P4
   > brings — a source outside the process, and a freshness contract to exceed —
   > and synthesizing them from a timeout invented here would make the mesh cry wolf
   > on a schedule nobody chose. Holding the whole stage for them would withhold the
   > answer an operator opens this view for, in exchange for two states that are
   > *reported as unavailable* either way.
   >
   > So P2.5c ships the mapping, the aggregation and the states this build can
   > observe, and **every mesh payload declares the states it cannot produce, with
   > the reason**. That declaration is load-bearing, not a footnote: without it an
   > instance nothing is watching renders as uniformly healthy, and a green picture
   > that has no way to go red is worse than no picture — which is the same failure
   > §4's neutral rendering of *unbound* exists to prevent, arriving one level up.
   > It is the same discipline P3 applies to a binding it cannot resolve, where
   > *unsupported*, *missing* and *forbidden* stay three different answers.
   >
   > Aggregation is also fixed here to **containment only**. Propagating severity
   > along `calls` and `uses` would make a node's class a function of transitive
   > reachability, so one unserved worker would repaint most of a few-hundred-node
   > landscape — and a mesh that turns mostly red on a single fault is the mesh
   > nobody believes on the second one. §6's impact analysis already answers the
   > dependency question exactly, on demand and with its path.
4. **C4 projection** rides with P2.5b, since it projects a model.

Each stage ships end to end — API, embedded UI, authorization, tests, documentation,
OpenAPI — per ADR-0189's slice rule.

### Consequences

- **Positive:**
  - Panorama is useful on first boot, on an instance nobody has modeled. The
    cold-start problem that limits every standalone architecture tool does not apply.
  - The mesh's edges are derived from Atlas's own records, so they cannot drift from
    reality the way a drawn dependency does.
  - Modeled-versus-present becomes visible in both directions, which is the
    correlation ADR-0189 named as the differentiator and left without a surface.
  - Impact analysis arrives with the graph rather than three slices later.
  - The three-way separation of declared, bound, and observed data survives, because
    provenance is rendered rather than assumed.
  - C4 readers are served without a semantically dishonest notation toggle.
  - Nothing here touches the WAL, `applyToState`, or a processor.
- **Negative / trade-offs accepted:**
  - Per-principal graph computation is more expensive than one cached graph, and
    caching is per-scope. Accepted: a shared graph cannot be made correct here.
  - Restricted placeholder nodes disclose that *something* exists. Accepted as
    strictly better than an edge silently dropped, and bounded to kind alone.
  - A derived graph must be extended whenever a new resource kind is added, or it
    quietly under-reports. The derivation needs a test that fails when a store gains
    a kind the mesh does not know.
  - The C4 mapping is a public contract with its own maintenance and loss-reporting
    burden, for a projection nobody can author in.
  - A worst-of severity rule makes a large landscape red easily. Attribution is what
    keeps that actionable, and attribution is not optional.
- **Follow-ups / risks to watch:**
  - The browser size budget is a real risk against ADR-0012's buildless rule. Measure
    it before publishing a supported size, not after.
  - Pressure to make the mesh editable ("just drag this node") will arrive. It is
    derived; edits belong in the ArchiMate model or in the Atlas resource itself.
  - Pressure to make the C4 projection round-trip will arrive from the same place.
    §8 is the answer, and changing it needs a new record.
  - The mesh makes an unmodeled instance look sparse, which may push users toward
    modeling for coverage rather than for meaning. Worth watching in the UI's framing.

## Pros and cons of the options

### Structure: render the ArchiMate model as a graph
- Good: one source of truth; no derivation to keep current; no new authorization
  surface beyond the model's own.
- Bad: empty on an unmodeled instance, which is every instance on day one; edges are
  only as accurate as the last modeling session; cannot show what Atlas has and the
  model does not.

### Structure: derive from Atlas resources, overlay the model (chosen)
- Good: works with zero modeling effort; edges are facts; enables comparison in both
  directions; uses data Atlas already holds and already authorizes.
- Bad: derivation must track every new resource kind; per-principal computation;
  needs explicit provenance rendering to avoid conflating declared and observed.

### Structure: two unrelated surfaces
- Good: no correlation semantics to design; each surface stays simple.
- Bad: forfeits the comparison that motivates the feature; two navigations for one
  question; users perform the correlation by eye, badly.

### Notation: renderer theme
- Good: cheap; one model, many pictures.
- Bad: rejected by ADR-0189 §7; produces diagrams that are semantically wrong under a
  standard's name, and those diagrams get exported and believed.

### Notation: full second authoring notation
- Good: honest and complete; what UAF would require.
- Bad: a metamodel, palette, rules, and interchange per notation — far beyond the
  payoff for C4, whose audience wants to *read* a diagram.

### Notation: read-only projection with declared mapping and loss (chosen)
- Good: serves C4 readers; one-directional so no round-trip fidelity claim; loss is
  stated rather than hidden; no second source of truth.
- Bad: a mapping contract to maintain; users will ask to edit the projection and be
  told no.

## Links

- extends [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) —
  adds a derived landscape altitude above its drawn views, and answers the C4
  question its §7 left open without reopening the notation-as-theme ban
- respects the observation states and overlay rules of ADR-0189 §6, the ownership and
  sharing boundary of [ADR-0071](0071-sharing-scopes.md) and
  [ADR-0128](0128-process-applications.md), and the API service shape of
  [ADR-0147](0147-splitting-the-api-server-object.md)
- constrained by [ADR-0012](0012-web-ui-app-shell.md) (buildless, CDN-free shell)
- reuses [ADR-0124](0124-server-side-diagram-auto-layout.md) /
  [ADR-0127](0127-layered-layout-pipeline-and-invariants.md) as the layout fallback
- terminates its drilldown in the live overlay and the replay of
  [ADR-0065](0065-multi-token-process-replay.md)
- leaves [ADR-0099](0099-archimate-enterprise-architecture-view.md) untouched: Atlas's
  own architecture documentation keeps Markdown as its source of truth
- historical context may later come from
  [ADR-0142](0142-prometheus-metrics.md) and
  [ADR-0114](0114-opensearch-event-exporter.md), as ADR-0189's P5 already provides
