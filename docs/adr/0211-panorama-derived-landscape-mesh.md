# ADR-0211: Panorama's derived landscape mesh and notation projections

- **Status:** Accepted (amended 2026-08-31 — §11 splits P2.5c's P4 dependency per
  state instead of holding the whole stage behind it; amended 2026-09-01 — §7 lays
  the graph out in a world of its own size rather than in the viewport, and names
  by magnification rather than by node count; amended 2026-09-02 — §7 lets the
  reader arrange the landscape by hand, and sizes a node by its connectivity as well
  as by its kind; amended 2026-09-02 — a drag moves the neighbourhood rather than
  the landscape, and a filter keeps one hop of context around every match; amended
  2026-09-02 — §7 gains saved views, a findings list, and a heartbeat on the nodes
  that have one, and §4's node carries its incident count; amended 2026-09-02 — a
  node can be drilled into, and a finding names the element the work is parked on;
  amended 2026-09-02 — §7 gives each kind its own outline; amended 2026-09-02 — the
  landscape draws deployment targets, so §4's unreachable and stale are producible
  on it and what a payload declares unproducible is derived from what it drew; amended
  2026-09-02 — §10 gains the landscape's own export, which has only one class, and
  the mesh payload carries the observation time that export has to render; amended
  2026-09-02 — §6's impact answer gains a severity breakdown, the names of the
  nodes in it, and a ranking of every node's radius, and states why "is this the
  only way" is not a question this graph can be asked; amended 2026-09-03 — §7's
  legibility half gains canvas ink, a fit that frames the picture rather than the
  world, and a reachable chrome corner, and §8's projection is extended to the
  derived landscape under the same four constraints; amended 2026-09-03 — §8's
  ArchiMate projection becomes an exportable document under stated conditions,
  replacing the previous amendment's blanket refusal, and the mapping moves to the
  server)
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

> **Amendment (2026-09-02, second): a drag is local, and a filter keeps its
> context.**
> Three corrections to the two changes above, each of them a defect the first
> version shipped rather than a new idea.
>
> - **A drag moves the neighbourhood, not the landscape.** Dragging re-ran the
>   layout's own physics from the picture on screen. That looks reasonable and is
>   not: the picture has been *fitted* since it was simulated, and the fit rescales
>   every distance — so it sits nowhere near the simulation's equilibrium, and
>   restarting it there reorganises the whole landscape the moment a node is touched.
>   The reader lost the arrangement they were reading in order to move one node in
>   it. A drag now pulls only the edges that touch what is held, toward the lengths
>   those edges had at the instant of the grab, and resolves whatever the movement
>   ran into. Nothing else moves at all.
> - **The gesture belongs to the graph, not to the browser.** Three things went to
>   the browser instead: the canvas selected its own labels under a drag, a touch
>   drag was taken for a page scroll, and a drag that crossed the canvas edge dropped
>   the node at the boundary. Selection is off on the canvas, a node takes no
>   `touch-action`, and the pointer is captured — but only once the gesture is a
>   drag rather than a press, because capturing on the press retargets the
>   compatibility mouse events behind it and the click that *selects* a node is one
>   of them. For the same reason the press is never `preventDefault`ed.
> - **A filter keeps one hop of context.** A match on its own is a circle in an empty
>   field: it answers "does this exist" and nothing else, when the question somebody
>   types a name to ask is nearly always "and what is it attached to". The filter now
>   keeps the immediate neighbourhood around every match — one hop, because nearly
>   everything hangs off some hub and reaching through one drags most of the graph
>   back onto the screen. Context is marked as context, drawn more faintly and counted
>   separately in the header: a search that presented things which do not match the
>   search as matches would be a worse answer than the empty field it is fixing.
>
> Adjacency on hover (the 2026-09-01 amendment) also gained colour: a ring in the
> accent on each neighbour, outside the circle rather than a recolouring of it,
> because the body's own stroke is carrying §4's severity and painting over a red
> node to say "connected" would trade a finding for a hint.

> **Amendment (2026-09-02, third): a landscape somebody comes back to.**
> §7 has been about making one look at the landscape readable. These three are about
> the second look, and the tenth.
>
> - **Views can be saved.** Watching one node means filtering to it, zooming in and
>   arranging what is around it — and a reload puts all of that back to the whole
>   landscape. A saved view is that setup with a name on it: the filter, the
>   direction and depth, the node being watched, the magnification and the
>   arrangement. It is stored in **this browser**, where every other piece of
>   remembered UI state in Atlas lives, and the panel says so: these views are not
>   shared and do not follow anybody to another machine. A stored resource with an
>   owner, an access rule and a sharing scope is a decision about the product; this
>   is a way of not re-zooming, and conflating the two would have shipped the first
>   under cover of the second.
>
>   What is *not* saved is the graph. The landscape is derived and changes as things
>   are deployed, so a view is a way of looking rather than a snapshot of what was
>   there — which is why what it stores is fractions of the world rather than
>   coordinates, and why a view watching a node is re-centred on wherever that node
>   is *now*. A view whose node has since gone opens and says so, rather than framing
>   the empty space where it used to be.
>
> - **The findings are a list as well as a picture.** The mesh marks every node with
>   something wrong with it, and on four hundred circles that is not the same as being
>   able to read them: finding three red dots means hunting, and hunting is what
>   somebody does instead of noticing. The same findings are now an index beside the
>   picture, worst first, each with its state, its incident count and the sentence
>   behind it, and clicking one selects and frames it. An empty list says explicitly
>   that it is not a claim that everything is well — most nodes in a young landscape
>   are unobserved, and §4 has always required that distinction of the colours.
>
>   §4's node gains **Incidents**, the count the engine already holds against a
>   definition. It is the number behind the reason rather than a second opinion about
>   it: two degraded processes are not equally degraded, and the count says which to
>   look at first. It rides only on a process — an incident belongs to a token and
>   only a process has tokens — and is absent everywhere else, because "no incidents"
>   and "cannot have incidents" are different facts. A collapsed application sums what
>   is parked behind all of its children rather than reporting the worst one's number.
>
> - **A node with a finding beats.** Motion is the one channel left once colour, size,
>   shape and a glyph are each carrying something, and it is the channel the eye finds
>   without being pointed at it — which is what a view somebody glances at needs. The
>   worse the state, the *less* pulse, which is the metaphor read honestly rather than
>   backwards: a degraded process is still working and beats quickly and twice, like
>   something under strain; one that cannot do work beats once, slowly and heavily. A
>   landscape of quick pulses is busy and coping; a landscape of slow ones is in
>   trouble.
>
>   Each class keeps its own colour instead of both going red, because "it is broken"
>   and "something inside it went wrong" are the two findings this view exists to tell
>   apart. The beat is a ring of its own under the node, so the body's stroke goes on
>   carrying §4's severity and the fill goes on carrying the kind. And it is bounded:
>   past eighty beating nodes, and for anybody whose system asks for less movement,
>   the rings stay and stop moving. A landscape where two hundred things are wrong is
>   a picture of an outage, and two hundred simultaneous animations say less than a
>   still frame while costing far more to paint.

> **Amendment (2026-09-02, fourth): going into a node, and saying where it broke.**
> Two more, and they are the same act at two altitudes: getting from "something is
> wrong in this landscape" to "this task, this message".
>
> - **A node can be drilled into.** Double-clicking one reduces the landscape to it
>   and whatever is within the depth already on screen. The complaint it answers is
>   the one every large graph has: you find the thing you came for and it is still
>   sitting in four hundred circles of everything else. It is the *same cut* a search
>   makes — one walk, one idea of what a neighbour is — because two implementations
>   of "and its neighbours" would eventually disagree, and the difference would show
>   up as a picture that answers a slightly different question depending on how you
>   reached it. Direction is not consulted: "what is this attached to" includes the
>   application it sits in and the worker it calls alike.
>
>   A drilldown and the search box are two ways of asking the same kind of question,
>   so only one is ever in force — entering one clears the other. Two narrowings
>   compounding invisibly is how a picture ends up showing something nobody asked for
>   and nobody can undo. The way out is stated in the header beside the picture it
>   describes, and on Escape.
>
>   The double-click had a previous job — releasing one hand-placed node — and it
>   moved into the panel beside the node it is about. A gesture is something you have
>   to be told; a button on the thing itself is something you can see.
>
> - **A finding names the element the work is parked on.** "Three tokens are parked"
>   says there is a problem. "Three on the service task `charge-card`, and the last
>   one said 502 Bad Gateway" says where to go, and it costs the same read: the engine
>   already knows which element each incident is stuck on, because that is how it
>   resolves one. §4's node therefore carries **sites** beside its count — element id,
>   BPMN type, how many, and the message.
>
>   Four rules keep it honest. The element is named by its id and type rather than by
>   a label, because only user tasks carry a title in a compiled process and an
>   identifier that is sometimes there is worse than one that is always there — and
>   because it is what Operations shows, so the two name the same thing the same way.
>   The message is the first one seen at that element, not a summary of several:
>   inventing a combined sentence would be writing a message nobody produced. The list
>   is the worst few and stops there, because a landscape view is where somebody
>   decides *which* process to open, not where they triage eleven broken tasks. And a
>   collapsed application keeps the summed count but drops the sites: an element id
>   without the process it belongs to is not something anybody can act on, and a list
>   of them from six processes reads as one broken diagram.
>
>   The model overlay gets the same answer in one field — the worst site, on the
>   observation's detail — so a panel that says "degraded" can say where without
>   sending anybody to a second view.

> **Amendment (2026-09-02, fifth): each kind gets its own outline.**
> Colour on this view already carries the kind *and* ADR-0189 §7's layer meaning;
> size already carries rank and connectivity. Form was the one channel left, and a
> landscape of four hundred identical circles was spending it on nothing — so an
> application is a circle, a process a rounded square, a decision a triangle, a
> worker a hexagon.
>
> It is also the channel that survives what the others do not: a printout, a
> projector, and a reader who does not separate the hues. §4 already required that
> colour never be the only channel for *severity*; this extends the same discipline
> to kind, which had been relying on colour and size alone.
>
> Every shape is **inscribed in the circle the layout reserved for it** — no vertex
> further from the centre than the radius the simulation kept clear. That is what
> makes the change free rather than a new source of overlap: the separation guarantee
> is stated in circles, so a shape that never leaves its circle cannot break it. The
> shapes come out slightly smaller than the circles they replace, which is the right
> way round; the application stays the largest thing on screen.
>
> Two placeholders take the rule the other way. A **restricted** node stands for
> something real whose kind we may never learn, so it takes the one shape that is not
> any kind's — drawing it as a process would be a guess wearing the same clothes as a
> fact. An **unresolved** one is drawn in the silhouette of the thing that is
> *missing*: its id names the kind, the dashes already say it is not there, and the
> shape says what should have been.
>
> The legend is rendered by the same function the nodes are, so it cannot come to
> disagree with the picture it explains.

> **Amendment (2026-09-02, sixth): the landscape asks its peers.**
> §4 had two states it could never produce — *unreachable* and *stale* — and the
> reason was structural: everything the mesh drew was read from this server's own
> state while the request was being served, so nothing could fail to be contacted or
> go out of date. That was true and useless. An operator scanning the landscape for
> trouble could not see that a whole peer had gone away.
>
> The landscape draws **deployment targets** now. They are asked off the run loop,
> through the same cache and the same rules ADR-0189 §6's observation projection
> uses, so the two surfaces cannot come to disagree about whether a peer is stale or
> unreachable — they read one answer through one set of rules. A peer that stops
> answering is a node with a finding on it, and it beats like any other finding.
>
> Three things this pins down:
>
> - **The reach happens off the loop, and the shape enforces it.** The collector
>   hands back the landscape *and a closure* holding what it resolved while it was on
>   the loop — the target list, the credential each presents. Holding the single
>   writer across an eight-second timeout would stop every other design-time request
>   on the server (I3), so the test asserts the loop is free while the closure runs
>   rather than merely that it was called.
> - **What a payload declares unproducible is derived from what it drew.** A
>   landscape with a peer on it produces both states and declares neither; one
>   without still declares both, and names the deployment target that would change
>   that rather than only saying it cannot. A response that went on claiming
>   "unreachable cannot happen here" beside a target reporting exactly that would be
>   a contract nobody could rely on again.
> - **No edge is derived to a target.** A promotion is an act, not a stored
>   relationship: this server does not record which of its applications is running
>   over there, so a line from one to a target would be an assertion nobody made. It
>   sits beside the landscape rather than in it, which is what it is.
>
> A target carries its operator's name for it and nothing else — never the base URL
> or the credential reference. Those are this operator's map of where their
> infrastructure lives, and a landscape is opened by anybody with modeler access.

> **Amendment (2026-09-02): the impact answer says how bad and which, and the
> landscape ranks its radii without being asked about a node first.**
> The answer above was a count and a highlight, and both leave out what somebody
> acts on. Three additions, and one deliberate non-addition:
>
> - **How bad.** The radius is broken down by §4's severity classes: "twelve depend
>   on this — one critical, one attention, ten OK" is a different morning from twelve
>   quiet ones, and the count is identical in both. It is stated as triage and never
>   as cause. A node's class is what that node reports about itself; that three of a
>   worker's dependents are critical may be the worker's fault, may be why the worker
>   looks busy, or may be unrelated — and a panel that inferred causation from
>   adjacency would be wrong the first time anybody checked, which is once.
> - **Which.** The impacted nodes are named, worst first, direct before transitive
>   within a class. This is the findings list's argument applied to the impact
>   answer: on four hundred circles, "twelve depend on this" means finding twelve lit
>   dots, and hunting is what somebody does instead of noticing. Direct and
>   transitive are kept apart because they are different facts — the direct
>   dependents are the ones whose owners get told.
> - **Which node, out of all of them.** Impact analysis needed a selection, which
>   assumes the reader already suspects the right node; before a change, or on an
>   instance somebody has just been handed, that assumption is exactly backwards. The
>   walk therefore runs from every node and the results are ranked. It is O(N·E) over
>   the graph already in the browser, which §7's budget bounds — past it the payload
>   arrives collapsed to applications, so N is a few dozen precisely where it would
>   have cost the most. The ranking follows the direction and depth controls rather
>   than fixing its own question: two blast-radius numbers on one page measured
>   differently would be worse than one.
>
> **What is deliberately not built is "is this the only way".** The obvious next
> question — of the twelve, how many would *stop* rather than merely be affected,
> because this node is their only path — has one answer on this graph, for every
> node, always: yes. Every edge §1 derives names exactly one resolved provider. A
> call activity resolves through the deployment overrides to a single process, a
> service task names a single worker, a business-rule task a single decision; where
> the resolution fails the mesh draws *unresolved* rather than a second candidate.
> There is no fan-out to alternatives anywhere in the derivation, so "does this
> dependent have another way" is a label that never varies — and a label that never
> varies teaches a reader that the distinction exists when it does not. It becomes a
> real question the day the landscape derives something with more than one provider,
> and not before.
>
> One correctness fix travels with this, found while ranking every node: asking about
> a **restricted placeholder itself** used to report zero dependents and call the
> answer complete. Arriving at a boundary and standing on one are different — the
> nodes that point at a placeholder are in this caller's own picture, so they are
> walked — and "nothing depends on this" is the single claim §6 says a boundary must
> never be allowed to make. It now reports them, as a floor.

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

> **Amendment (2026-09-03): the picture is legible, it fills the sheet, and every
> node on it can be reached.**
> Three defects in §7's legibility half, and all three had the same shape: a rule
> that was right about one thing measured against the wrong thing.
>
> - **The canvas has its own ink.** The unremarkable outlines and every edge were
>   drawn in the token used for a hairline between two panels — correct at a hand's
>   width, invisible on a canvas — so a landscape showed its findings sharply and
>   its structure barely at all. Two canvas tokens now carry them, on the same
>   argument the amber status mark already rests on: a border is read, a mark is
>   noticed, and the two want different values. Context nodes and the nodes outside
>   an impact set were also faded past the point of being seen at all; an impact set
>   is a part of a whole, and the whole has to stay legible for the part to mean
>   anything.
> - **"Fit" frames the picture, not the world.** The world is an area budget the
>   layout settles in, and the layout normally spreads the content across it — so
>   the two coincided and framing the world framed the picture. They stop coinciding
>   the moment a node is pinned, because the fit is skipped then to keep pins under
>   the spots they were dropped on. From the first drag onward, "Fit" showed the
>   picture in a corner of a mostly empty sheet. It now frames what is drawn.
> - **A node under the zoom controls could not be picked up.** The panel floats over
>   the canvas, so it is also in front of it: the press landed on chrome. That is not
>   a coincidence but a consequence of fitting — the fit pushes content to the edges
>   by construction — and it bit hardest in a filtered or drilled picture, where
>   there are few enough nodes for one of them to be the one being reached for. The
>   fitted view now holds that corner clear, and the panel passes pointers through
>   except on its own buttons. Reserving it costs one dimension rather than two: the
>   largest rectangle avoiding a corner gives up either its width or its height, and
>   subtracting both would hand a quarter of a wide canvas to a panel a few hundred
>   pixels across.
>
> The framing is a pure function of the drawn box, the viewport and the chrome's
> footprint, and it is *stored* rather than recomputed on demand. Every screen-to-
> world conversion goes through it and a drag moves the content it is computed from,
> so a view derived per call would shift the coordinate system under the pointer as
> the node crossed it.

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

> **Amendment (2026-09-03): the derived landscape may be projected too, and it is
> the smaller case rather than a wider one.**
> This section was written for a projection *of an ArchiMate model*. The landscape
> asks the same question of something with no model behind it at all, and the answer
> is yes under the same four constraints — which bind more tightly here, not less.
>
> A projection of a model has a source document whose fidelity can be argued about.
> A projection of the derived landscape has none, so the only thing that can be
> misread is the vocabulary itself: a reader handed a C4-looking picture of Atlas's
> own resources may take it for a C4 model of the system. That is the whole risk, and
> it is what the constraints are pointed at:
>
> - **Nothing becomes authorable.** There is no ArchiMate and no C4 document behind a
>   projected landscape, none can be exported from one, and the projection is not
>   editable — as it never was, since nothing on this landscape was drawn to begin
>   with.
> - **The mapping is one table per notation, versioned**, and a mesh kind the
>   notation has no element for is simply *absent from it*: the node keeps its
>   derived shape rather than being dressed as something it is not. Inventing a row
>   would be the silent drop the theme ban exists to prevent. Restricted and
>   unresolved placeholders are the two — they are findings about the picture rather
>   than architecture, and no notation should have a word for them.
> - **The loss is listed beside the picture and inside the export**, in the
>   notation's own terms: untyped relationships, a worker's Application Service with
>   no Technology Service behind it, C4's levels shown on one canvas, C4's external
>   systems absent because Atlas holds no model of what is behind a worker.
> - **The vocabulary is stated wherever the picture goes.** The legend says "Atlas's
>   own resources, drawn in ArchiMate's vocabulary; nothing here was modelled", and
>   the export's stamp carries the same sentence with the mapping version — the file
>   travels, and there is nobody beside it to ask.
>
> Each notation's own idiom is honoured as far as a landscape can: ArchiMate's
> structure/behaviour rectangle split, C4's uniform box told apart by the annotation
> under the name. Two deviations are stated rather than papered over — ArchiMate's
> corner icon is written out, because an icon at this magnification is a smudge, and
> every shape stays inscribed in the circle the layout reserved, so a change of
> vocabulary can never make two nodes overlap that did not overlap before.

> **Amendment (2026-09-03): the ArchiMate projection is also a document, and the
> mapping is served rather than duplicated.**
> The amendment above said no document can be exported from a projected landscape.
> That was the safe answer and it was the wrong one, and this replaces it. What it
> was protecting against is real — a generated file taken for one somebody drew —
> but a picture that cannot leave the browser is a picture an architect cannot use,
> and the objection is answerable rather than fatal.
>
> The landscape can be exported as an **ArchiMate Open Exchange document**, under
> these conditions, which are what make the generated file honest rather than merely
> permitted:
>
> - **It says what it is, three times over.** The model's documentation opens with
>   `DERIVED, NOT AUTHORED: nothing in this model was drawn by a person`, names the
>   instance and the moment it was generated, states the mapping version, and says
>   that editing it changes nothing in Atlas. Every element carries `atlas.*`
>   provenance. The model identifier is derived rather than random.
> - **It carries no observation state.** No severity, no incidents, no reachability.
>   This is what settles §10's question about it: structure only makes it a *model*
>   export, the safe class, and not a live one. An architecture document that froze
>   this morning's health would go on asserting it — the undated green picture in
>   another wrapper. Health belongs on the image export, dated.
> - **The loss is in the file.** The same list the legend shows, written into the
>   model's documentation, plus what this particular export dropped: how many
>   dependencies point at resources the exporting reader may not see, and how many
>   relationships went with them. A model missing a third of the estate that did not
>   say so would be worse than no model.
> - **Nothing becomes authorable.** There is no round trip: the document is generated
>   from resources, never read back as one, and re-generating it reproduces it. It is
>   deterministic for that reason — the same landscape produces the same bytes, so it
>   can be committed and diffed and a change in it means a change in the estate.
> - **The bindings are ADR-0189 §4's own keys**, so a model exported here and
>   imported back into Panorama arrives with its bindings already resolving. That is
>   the one direction that *is* a round trip, and it is a useful one: it turns the
>   generated file into a starting point somebody can then model on top of.
> - **No diagram.** The landscape's arrangement is computed in the browser and
>   belongs to whoever arranged it; a server-side grid would be worse than the
>   importing tool's own layout.
>
> Two mapping decisions are worth naming because a reader would otherwise find them
> surprising. An application is **assigned to** the processes it holds rather than
> composing them — composition is for elements of one kind, and a component and a
> behaviour are not. And a `uses` edge is **reversed** on export: ArchiMate's Serving
> runs from provider to consumer while the landscape's edge points from the process
> to the worker it needs, so the document says "the mail worker serves the invoice
> process", which is the true statement in ArchiMate's terms.
>
> **The mapping moves to the server and is served to the browser.** It now has three
> readers — the picture's labels, the stamp on its image export, and this document —
> and a table each of them kept a copy of would end with the picture calling a node
> an Application Process beside a file that called it something else. This is
> ADR-0189's rule for the connection subset, applied for the same reason: a surface
> that offers what another surface contradicts is a promise the server breaks. Each
> row carries both what a person is shown and the notation's own machine token, since
> the two readers need different halves of one fact.

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

> **Amendment (2026-09-02): the derived landscape has one export class, and the
> artifact carries its own provenance.**
> The two classes above were written for the drawn views, where the distinction is
> real: an ArchiMate view exists because somebody placed elements on it, and a
> picture of that structure discloses only what they authored. The derived landscape
> has no such half. Its structure is *read off this server's resources* rather than
> drawn, and every node on it carries an observation state — so there is no version
> of this picture that is a model export, and offering a "structure only" variant
> would only produce a landscape that hides what it knows. **Every export of the
> mesh is a live export.**
>
> What that costs is nothing, because the redaction is *inherited rather than
> re-applied*: what is written to the file is the picture the server already built
> for this principal — scope-filtered, with §3's restricted placeholders where a
> scope cut a path — serialized in the browser from the SVG on screen. There is no
> export endpoint to authorize, no second walk over an unfiltered graph, and
> therefore nothing that could disclose more than the screen it came from. That is
> the argument §6's impact analysis already makes for running on the delivered
> graph, applied to the artifact rather than to the answer.
>
> What §10 demands in exchange is that the file stand alone, and that is the whole
> of the design:
>
> - **The observation time comes from the server**, on the mesh payload, and is
>   rendered into the image. The browser's clock dates the *save*, not the reading,
>   and the two are the same number only if nobody left the tab open. A payload
>   without one says so in the artifact rather than substituting a plausible time —
>   "old" and "unknown" are different, and only the second one is true there.
> - **Everything the picture is not showing travels inside it**: which landscape
>   this is (the whole of it, filtered by a term, drilled into a node), how many of
>   its nodes were drawn, how many are hidden by the reader's access, whether it is
>   collapsed over the size budget, whether counting parked work stopped at its
>   bound, and which of §4's states this build cannot produce at all. Beside the
>   canvas all of that is in the legend; a file pasted into a ticket has no beside.
> - **The key travels too.** A hexagon nobody can name is a shape rather than a
>   worker. It is drawn from the same list the on-screen legend renders, which is
>   drawn by the functions that drew the nodes, so a third rendering cannot come to
>   explain a picture it no longer matches.
> - **The artifact is the whole landscape at full extent, not the viewport.** Pan and
>   zoom are reading aids; a file cropped to where somebody had scrolled would drop
>   nodes and say nothing about having dropped them.
> - **It is inert and self-contained.** The theme's custom properties are resolved to
>   literals so the file renders where nobody has Atlas's stylesheet; the styling is
>   harvested from the live stylesheet rather than restated for exports, because a
>   second copy would drift and a drifted export is wrong in a way only its recipient
>   can see; and every script and event handler is stripped, because an exported file
>   is opened by people who did not make it. The heartbeat is stilled — an animation
>   rasterizes at whatever phase the encoder caught, and severity is carried by the
>   ring, the badge and its glyph regardless.

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
