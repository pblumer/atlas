# ADR-DRAFT: The whole graph can be walked, in a projection with a stated scope

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-19
- **Deciders:** Atlas maintainers
- **Open question:** whether community detection over a graph of average degree ~2.5 that
  decomposes into millions of ~30-node components produces clusters that mean anything.
  Louvain optimises modularity, and a forest of tiny components may have no modularity
  structure worth finding — in which case the "clusters" are simply the components, which
  is either exactly right (one cloud per instance family) or a smear. The cloud rendering
  in §5 rests entirely on that, and it is decidable in a day with a prototype over real
  data. Nobody has run it.
- **Question checked:** 2026-09

## Context and problem statement

ADR-draft-the-estate-graph-is-derived-from-the-log states, as its rule 2, that runtime
facts reach the structural graph as properties and never as nodes. The reason given is
arithmetic: the starmap's measured budget is 400 nodes and one day of runtime facts is
four orders of magnitude past it.

The requirement that arrived afterwards is that **the whole graph be walkable** — every
instance, every element instance, every job — accepting a cloud or cluster rendering for
the parts that cannot be drawn node by node. That is not a rule 2 violation to be argued
away; it is a different altitude that rule 2 did not anticipate, and it needs its own
structure, its own name and its own costs written down.

### The numbers

Per instance, for a modest process (15 elements, 3 service tasks, 8 variables): about 30
nodes and 75 edges. This is an estimate from a model profile, not a measurement, and
multi-instance activities and loops can raise it three- to fivefold.

| Rate | Nodes | Reached |
|---|---|---|
| 10 000 instances/day | 300 000/day | 1 million in ~3.5 days |
| the store ADR-0080 observed (~529 000 parked instances) | ~16 million | already, today |
| 10 000/day over a year | ~110 million nodes, ~275 million edges | — |

### The topology, which decides everything

The run graph is **not one graph**. It is a forest of millions of components of about 30
nodes each, average degree about 2.5, hanging off a handful of **supernodes** — the
definition, the worker, the decision — whose degree is in the millions.

Two consequences follow, and they point in opposite directions:

- **Including the supernodes, a global traversal is semantically empty and unaffordable.**
  Every instance is two hops from every other, through the definition node. "A is two
  hops from B" is true for all pairs and means nothing, and any walk touching that node
  expands into millions.
- **Excluding them, a whole-graph walk degenerates into a sequential pass.** There is
  nothing deep to traverse: millions of independent, tiny, embarrassingly parallel walks.

So the requirement is cheaper than it sounds, for exactly the reason a global graph over
instances is a bad idea. That is the finding this record is built on.

### What exists already

- **The edges are in state.** `ElementInstanceValue` carries `ProcessInstanceKey`,
  `FlowScopeKey`, `TokenID`, `ParentTokenID` and `SourceFlowId` (`model/value.go`), and
  `RecordHeader.SourcePos` is the causal edge through the log.
- **The background-consumer shape is built.** `wal.Tailer` with its `Cursor`, and the
  OpenSearch exporter ([ADR-0114](0114-opensearch-event-exporter.md)) as the worked
  example: off the hot path, reading only records at or below the durable watermark, with
  a persisted high-water mark.
- **Cross-instance edges are indexed.** `cfChildByParent`
  ([ADR-0238](0238-child-instance-index.md)) is the call-activity parent-to-child edge;
  `cfInstanceByElement` ([ADR-0261](0261-instances-on-an-element.md)) is the instances on
  an element.
- **Pure-Go algorithms exist.** `gonum.org/v1/gonum/graph` carries strongly connected
  components, `graph/community.Modularize` the hierarchical Louvain modularisation, and
  `graph/layout.EadesR2` a force layout with Barnes-Hut global repulsion. No CGO
  ([ADR-0010](0010-go-and-no-cgo.md)).

## Decision drivers

- **No random I/O per edge.** 275 million edges at one random read each, at 100 µs, is
  7.6 hours. This single number rules out the obvious implementation and dictates the
  rest.
- **Not on the run loop.** I3, and the lesson of ADR-0080,
  [ADR-0239](0239-off-loop-queries.md) and
  [ADR-0382](0382-whole-store-reads-leave-the-writer.md): a cost that grows with the data
  never holds the single writer.
- **A second thing to back up, compact and restore is a real cost**
  ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)), and it was decisive once
  already ([ADR-0179](0179-worker-job-history-in-clio.md)).
- **Retention already deletes** ([ADR-0085](0085-process-instance-ttl.md),
  [ADR-0115](0115-history-retention-hard-delete.md)). Whatever "whole" means, it cannot
  silently mean more than Atlas holds.
- **A picture states its reference or says nothing** (ADR-0211 §4's discipline, and the
  radius comment in `panorama-mesh.js`: a size means something only against a stated
  reference).

## Considered options

1. **Random-access traversal over adjacency keys in the state store.** One prefix scan per
   hop, straight off Pebble.
2. **A persistent graph store beside the state store**, maintained incrementally by a WAL
   tailer.
3. **An in-memory CSR projection**, rebuilt rather than maintained authoritatively, with
   dense ordinals, frontier bitmaps and precomputed component and community membership.
4. **Depend on a graph engine** — embedded or external — and let it own the walk.

## Decision outcome

Chosen: **option 3 — a rebuildable in-memory CSR projection, named separately from the
starmap, with a stated scope.** Eight points.

### 1. Rule 2 is precised, not repealed

T1 keeps its ban. **No runtime node ever appears on the structural starmap.** What this
record adds is a *second surface* with its own name — the **run graph** — and the two are
never mixed in one picture. The starmap's node carries a number; the number links into the
run graph. A reader always knows which of the two they are looking at, because they are two
views and not one view with a zoom level.

This is the same separation ADR-0396 made for the catalogue: a second *subject*, not more
nodes on the landscape. The precedent is deliberate.

### 2. The structure is an in-memory CSR, and it is never authoritative

Compressed Sparse Row: an offset array per node, a concatenated array of neighbour
ordinals. For the year-scale numbers above:

| | Size |
|---|---|
| Offsets, 110 M nodes × uint32 | 440 MB |
| Targets, 275 M edges × uint32 | 1.1 GB |
| Raw total | ~1.5 GB |
| With delta and varint compression of adjacency lists | under 1 GB |
| Frontier bitmap, 110 M bits | 14 MB |
| Visited bitmap | 14 MB |
| Union-find parent array | 440 MB |

This is not a novel design; it is what the field does. Neo4j's Graph Data Science
projects the stored graph into an in-memory structure in the CSR layout, with the
adjacency list of a node compressed by a combination of variable-length and delta
encoding, precisely so that more graph fits in the same memory, trading decompression
time for it. **Even a graph database, to walk the whole graph, builds a CSR copy in
RAM.** That is the argument for building the copy without the database, given that
Atlas already holds the data.

The projection is **disposable**: it is never a source of truth, never backed up, and a
lost or stale one is rebuilt. That is what keeps it from being the second store ADR-0179
refused.

### 3. Dense ordinals are the price of admission, and they are persisted

CSR and bitmaps need dense integer node ids. Atlas keys are sparse 64-bit
(`[16 bit partition][48 bit counter]`), so a key-to-ordinal map is required. Every graph
engine does this internally; here it is explicit.

The map is **persisted**, not rederived. Without that, every restart renumbers every node,
and any saved finding, exported picture or bookmarked walk refers to nothing.

### 4. Seeded from the state store, kept current from the tailer

Not from genesis. The exporter's `Cursor` is valid within one process run and a restart
resumes from genesis by design (ADR-0114); over 110 million nodes that is untenable, and a
compacted log no longer holds genesis at all (ADR-0131). So:

- **seed** from a consistent snapshot of the state store, off the loop under ADR-0239 and
  ADR-0382's discipline;
- **keep current** from the tailer, starting at the snapshot's position;
- **rebuild** whenever the two cannot be reconciled, because the projection is disposable.

### 5. One pass computes membership; a query is a lookup

The whole-graph walk is a batch job, not a request. One pass over the CSR computes:

- **connected components** by union-find, which for this topology is the instance-family
  decomposition;
- **communities** by hierarchical Louvain, which yields several coarsening levels at once.

Queries are then lookups against that membership, not walks. The **cloud is the coarse
level** — a cluster is a node, inter-cluster edge weights are sums — and semantic zoom
expands a cloud into its members, at which point the existing 400-node budget governs
again.

Two rules on the rendering, both inherited rather than invented:

- **A density without a named reference is a picture of nothing.** A dense region must say
  dense *in what*, and as of *when*.
- **The supernodes are excluded before clustering and overlaid after layout.** Louvain will
  otherwise place the definition node's cluster at the centre of everything and the cloud
  will be one blob. This is the visual return of §1's arithmetic.

### 6. Supernode edges are an excludable class, excluded by default

Without this the walk is both meaningless and unaffordable (see the topology above). The
edge classes that reach a shared structural node — instance-to-definition,
job-to-worker, evaluation-to-decision — are a named class that a walk switches off, and
the default is off. A walk that includes them says so in its result.

### 7. "Whole" means this installation's current data

Decided, not left open: the run graph covers **live instances plus history within
retention**. It is not the archive.

This is a real limit and it is stated rather than discovered. A walk is complete with
respect to what Atlas holds, and Atlas deletes (ADR-0085, ADR-0115). A view must therefore
render the scope alongside the picture — the same requirement ADR-0211 §10 already puts on
every exported starmap, for the same reason.

### 8. Extending the scope to an archive is blocked on a decision nobody has made

The blocker is **not** the data. The export already carries every edge the runtime
produces: `opensearch/exporter.go`'s `document` holds `Position`, `SourcePosition`, `Key`,
`Partition` and the record's value marshalled generically as `any` — which for an element
instance means `FlowScopeKey`, `TokenID`, `ParentTokenID` and `SourceFlowId` all travel.
An archive-scale run graph is reconstructible from what is already being written.

The blocker is governance, and it determines the archive's *layout*:

> **Who deletes from the archive, and on what request?**

Atlas hard-deletes instances; the archive keeps them. A deletion request for a person
therefore reaches nothing by deleting in Atlas. And the answer dictates the layout before
the first byte is written:

- deletion **by time only** → time-partitioned immutable objects, dropped wholesale;
- deletion **by subject** → large immutable objects mixing many subjects are ruled out
  entirely.

Rewriting an archive is the one thing that is not cheap, so this decision cannot be
retrofitted. Until it is taken, the archive extension is not designed, and this record's
scope stands at §7.

Two further findings belong with it, for whoever takes it:

- **OpenSearch is the wrong substrate for a bulk pass and the right one for a search.** It
  has no join and no traversal; building a CSR from it means scrolling the entire index —
  estimated hours at 10–50k documents per second, on the cluster that is also serving
  searches. Its strengths are selection with predicates and point lookup, and Atlas already
  reads it back (`opensearch/search.go`, ADR-0189 P5b).
- **The archive's schema is implicitly Go field names.** `document.Value` is `any`,
  marshalled generically with no `json` tags — deliberately "a search/archival projection,
  not a curated per-value-type schema" (ADR-0114). That is right for search and a liability
  for a graph source read in five years: renaming a Go field silently renames an archive
  column.

### Consequences

- **Positive:** the walk is affordable *because* of the topology that makes a global
  instance graph a bad idea. One structure serves both asks — the walk and the cloud — and
  the clustering that makes the picture legible is the same pass that makes the walk cheap.
- **Positive:** nothing new to back up. The projection is disposable by construction, which
  is the property that distinguishes it from the persistent graph store option 2 proposes.
- **Positive:** no CGO and no new service. The heaviest dependency is gonum, and even that
  is optional if the algorithms are written directly against the CSR.
- **Negative / trade-offs accepted:** **memory.** Under 1 GB compressed at the year-scale
  estimate, and three to five times that if the per-instance node count is higher than
  estimated — in a single binary that also runs the engine. This needs an explicit budget,
  a refusal above it, and possibly a separate process. It is the one cost that can make
  this undeployable.
- **Negative:** rebuild time after a restart, and a window during which the run graph is
  absent or behind. It must report that state rather than answer from a partial structure.
- **Negative:** the projection can drift from the state store. Disposability is the
  mitigation, not a repair path — there is no reconciliation, only a rebuild.
- **Negative:** a second surface is a second thing to learn. §1 accepts that cost to avoid
  the larger one of a picture that is sometimes structure and sometimes traffic.
- **Follow-ups / risks to watch:** measure `Modularize` at scale before anything else is
  built — gonum's algorithms work over the interface-based `graph.Graph`, and interface
  dispatch per edge across 275 million edges is where a hand-written pass may become
  unavoidable; the archive substrate record, blocked on §8; the ordinal map's own
  compaction as instances are deleted underneath it.

## Pros and cons of the options

### Option 1 — random-access traversal over the state store
- Good: no new structure, no memory, always current, and the adjacency keys are the
  technique the store already uses everywhere.
- Bad: 7.6 hours for one pass. Correct and unusable. It remains the right implementation
  for a *bounded* walk — a call tree, one instance's lineage — which is why nothing here
  replaces it there.

### Option 2 — a persistent graph store maintained by a tailer
- Good: no rebuild, incremental, survives restart, could outlive retention.
- Bad: a second store to back up, compact and restore — ADR-0179's refused cost. It is a
  cache that can drift with no repair path, and it acquires a retention story of its own
  that will disagree with the engine's.

### Option 3 — a rebuildable in-memory CSR (chosen)
- Good: sequential build, fast passes, disposable, no persistence to operate, and the
  industry's own answer (Neo4j GDS projects exactly this).
- Bad: memory; rebuild latency; persisted ordinals as a hard requirement; and it crosses
  the tier boundary rule 2 drew, which is why this record exists.

### Option 4 — depend on a graph engine
- Good: somebody else's problem to scale, and a query language for the questions that are
  genuinely traversals.
- Bad: contradicts the single-binary constraint, or makes a first-class view depend on an
  external component. And the engine would immediately build the same CSR internally, so
  the dependency buys the copy rather than avoiding it.

## Links

- precises rule 2 of ADR-draft-the-estate-graph-is-derived-from-the-log; uses the identity
  scheme of ADR-draft-graph-identity-across-several-logs; complements
  ADR-draft-an-edge-that-was-taken-is-not-an-edge-that-was-declared, which aggregates the
  same facts onto structure instead of walking them
- builds on ADR-0114 (the tailer shape and the export document), ADR-0131 (why not from
  genesis), ADR-0080/ADR-0239/ADR-0382 (what may not hold the writer), ADR-0238 and
  ADR-0261 (cross-instance edges already indexed), ADR-0211 §§4/7/10 (the rendering and
  export discipline it inherits), ADR-0396 (a second subject rather than more nodes)
- bounded by ADR-0085 and ADR-0115 (retention), ADR-0010 (no CGO), ADR-0011 (single binary)
