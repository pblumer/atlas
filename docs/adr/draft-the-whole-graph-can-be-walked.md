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

### 8. Extending the scope to an archive needs no archive-layout decision

The obvious reading is that an archive forces a layout choice — time-partitioned objects
that are dropped wholesale, or fine-grained objects so one subject can be removed — and
that this choice has to be made before the first byte is written, because rewriting an
archive is the one thing that is not cheap. That reading is wrong here, and it is wrong
because of a decision this repository has already taken.

**Neither the data nor the layout is the obstacle.**

The export already carries every edge the runtime produces: `opensearch/exporter.go`'s
`document` holds `Position`, `SourcePosition`, `Key`, `Partition` and the record's value
marshalled generically as `any` — which for an element instance means `FlowScopeKey`,
`TokenID`, `ParentTokenID` and `SourceFlowId` all travel. An archive-scale run graph is
reconstructible from what is already being written.

And [ADR-0314](0314-portal-personal-data.md) already decides how personal data is erased
from copies nobody can reach: a **reference by default**, and for the residue that cannot
be a reference, **ciphertext under a per-subject data key held in the vault**, erased by
destroying that one vault entry. Its own words on why that reaches an archive: every copy
— "the WAL segment, the state record, the checkpoint, the OpenSearch document, last year's
backup, the instance snapshot an operator exported" — holds the same bytes that the
destroyed key decrypted, so "nothing has to be found, coordinated or reached".

**The consequence for this record is that a conflict dissolves.** Subject-level erasure and
a bulk sequential read pull in opposite directions only while erasure means removing bytes:
one wants many small objects, the other wants few large ones. Erasure as key destruction
wants neither. So the archive may use exactly the layout the CSR build needs — large,
immutable, time-partitioned objects — and remain subject-erasable. No archive-layout
decision is owed, and this record does not ask for one.

What genuinely remains is smaller, and none of it is a vacuum:

- **ADR-0314's scope is the portal.** The run graph covers every instance of every process.
  The mechanism reads as general — which variables are personal is declared on the process
  as a compile-time attribute, "the same shape and the same place as the searchable-variable
  declaration" of [ADR-0244](0244-searchable-variables.md) — but extending it from portal
  processes to all of them is a decision, and it is not this record's.
- **ADR-0314 is `Implementation: Not started`.** So an archive that must be subject-erasable
  is ordered *after* it. That is a sequencing constraint, not an open question.
- **One switch is genuinely once-only, and it is not the layout.** Whether an installation
  runs the per-subject key machinery at all has to be settled before the first write and
  cannot be changed afterwards: ciphertext written under per-subject keys is unreadable to
  an installation that later switches the machinery off, and plaintext already written
  cannot retroactively become erasable. The posture shape already exists —
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md) is on by default with a
  generated key and one flag to disable — and an installation under an archiving duty
  rather than an erasure duty is the case for choosing differently. That switch belongs to
  the record that extends ADR-0314 beyond the portal.

**The run graph itself holds no payload, and that is not the same as holding nothing.** The
CSR carries keys, ordinals, flow ids and token ids — never a variable value — so ADR-0314's
ciphertext never enters it and erasing a subject changes nothing in it. But an instance key
is a pseudonymous identifier, and a picture that shows *one subject's instances* is
processing about that person whether or not a value is drawn. The run graph is therefore
pseudonymous structure rather than anonymous structure, and it inherits the sharing scopes
(ADR-0071) and the redaction discipline (ADR-0211 §3) for that reason, not merely by
analogy.

Two further findings belong with the archive substrate record, for whoever writes it:

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

### 9. A hard memory budget, a refusal above it, and narrowing as the way in

**The explorer may not displace the engine.** Atlas executes processes; a projection that
can exhaust the process it lives in is not a feature, it is an outage with a view attached.
So the budget is not a tuning parameter with a degradation curve behind it — it is a limit
with a **refusal** above it.

*"The run graph is too large for this installation"* is an honest answer. A slow server is
not an answer at all, and it is the one failure mode an operator cannot diagnose from the
outside: the engine that was executing work yesterday is executing it slowly today, and
nothing points at the view somebody opened.

Five properties make this a decision rather than an intention:

**The refusal is a prediction, not a recovery.** The size is estimated *before a byte is
allocated*, from counters that already exist: active instances per definition
(`cfDefInstanceCount`), finished instances per definition
([ADR-0083](0083-o1-instance-summary.md)'s `cfDefCompletedCount`), cumulative visits per
definition-element (`cfElementVisitAgg`), and the element count of each compiled process,
which is known at deploy time. The estimate is therefore O(definitions × elements) — the
same shape ADR-0080 made the runtime view — and never O(instances). An OOM caught by a
recovery path has already stalled the writer; a projection refused before it starts has
cost nothing.

**The budget is stated in bytes, not in nodes.** An operator knows how much memory the
machine has and cannot know what a node costs. Converting one to the other is the server's
arithmetic, and making the operator do it is how a limit gets set wrong in the direction
that hurts.

**It is off by default.** ADR-0070's posture — on by default with a generated key — is right
for a security feature, where being off is the worse state. Here being off is the *safe*
state: nothing that executes processes is at risk while no projection exists, and an
installation that never opens the run graph should not be paying for the possibility. The
opt-in is the operator saying how much memory the view may have.

**A subset is a whole graph, so narrowing is lossless.** This is where §1's topology pays a
second time: because the run graph is a forest of independent components, **a subset is a
complete graph of a subset, not a truncated graph.** One application, one definition, one
time window — each is a whole graph whose walk is sound, not a picture with the edges cut
off. Nothing about a narrower scope is an approximation, which is what makes the next point
possible.

And the build is **all-or-nothing**. A partially built CSR that answers is worse than none,
because its answers are wrong in a way no reader can see. A refused or failed build leaves
whatever projection already existed in place, stale and labelled stale, rather than
replacing it with something incomplete.

#### The entry is a narrow scope; the whole graph is the exception

A budget with a refusal above it, offered as *"show me everything"* with a *no* behind it,
removes the feature exactly where it is most wanted: the installation with half a million
instances is the one that needs impact analysis and the one that gets refused, while the
installation with three thousand gets the whole-graph walk and has no question it answers.
That is a limit doing its job and a product failing at its own.

So the interaction is inverted. **The first scope is narrow, and the reader widens until
refused.** The refusal then stops being a door that was shut before anyone started and
becomes a boundary found while exploring — the same thing as reaching the end of a list.

Four rules follow, and the first is the one that makes it work:

- **The entry scope follows the question, not the budget.** A reader arriving from a worker
  node on the starmap wants the definitions that use that worker; from a process, that
  process. That is context the click already carries, not a guess. Only when there is no
  context — the run graph opened directly — is a default needed, and it is a **recent time
  window**, because that is the scope a person means by "what is going on".
- **Every widening states its estimate before it runs.** The control is not widen-and-hope:
  the server answers *this scope would need 2.3 GB against a 1 GB budget* and names the
  nearest scope that fits. Over a few steps the reader learns the shape of their own estate,
  which is worth more than the picture they were denied.
- **The budget is visible while exploring**, as a share of it spent on the current scope.
  This is affordable precisely because the estimate is O(definitions × elements) — it can run
  on every step rather than once.
- **A saved view re-estimates on open.** ADR-0211 §7 already saves views. A saved run-graph
  view holds a scope, and a scope that fitted last month may not fit today; the view says so
  rather than failing to open.

**The estimator needs its own cap, and that is not a contradiction.** Scoping by application
or definition is free — the counters are per definition. Scoping by *time* is not: a count
within a window is a range scan over `cfInstanceDoneByDef`
(`piDoneByDef:<procDefKey>:<completedAt>:<piKey>`), so it costs in proportion to the window
asked about. A day is tens of thousands of valueless keys and cheap; a year is the whole
population. So the estimator is itself bounded, and above that bound the answer is *"too
large to estimate"* — which is a refusal too. If counting what you asked for is itself too
expensive, the answer is no, and saying so is cheaper than finding out by building it.

A picture that states what it cannot do, and what would work instead, is the discipline
ADR-0211 §7 applies over its node budget and §3 applies to a filtered mesh. This is that
discipline made into the primary interaction rather than the error path.

### Consequences

- **Positive:** the walk is affordable *because* of the topology that makes a global
  instance graph a bad idea. One structure serves both asks — the walk and the cloud — and
  the clustering that makes the picture legible is the same pass that makes the walk cheap.
- **Positive:** nothing new to back up. The projection is disposable by construction, which
  is the property that distinguishes it from the persistent graph store option 2 proposes.
- **Positive:** no CGO and no new service. The heaviest dependency is gonum, and even that
  is optional if the algorithms are written directly against the CSR.
- **Negative / trade-offs accepted:** **memory, bounded by refusal.** Under 1 GB compressed
  at the year-scale estimate, and three to five times that if the per-instance node count is
  higher than estimated — in a single binary that also runs the engine. §9 turns that from an
  open risk into a stated limit. The accepted consequence, stated precisely because the loose
  version is wrong: **on a large enough installation the *whole-graph* walk is unavailable**,
  and says so. The feature is not — every narrower scope is a whole graph of itself (§9), so
  what a large estate loses is the one question it could ask least usefully anyway, and what
  it keeps is every question about an application, a definition or a window. That is the cost
  of refusing to let a view slow the engine, paid in reach rather than in throughput.
- **Negative:** rebuild time after a restart, and a window during which the run graph is
  absent or behind. It must report that state rather than answer from a partial structure.
- **Negative:** the projection can drift from the state store. Disposability is the
  mitigation, not a repair path — there is no reconciliation, only a rebuild.
- **Negative:** a second surface is a second thing to learn. §1 accepts that cost to avoid
  the larger one of a picture that is sometimes structure and sometimes traffic.
- **Follow-ups / risks to watch:** measure `Modularize` at scale before anything else is
  built — gonum's algorithms work over the interface-based `graph.Graph`, and interface
  dispatch per edge across 275 million edges is where a hand-written pass may become
  unavoidable; the archive substrate record, which §8 no longer blocks; the ordinal map's
  own compaction as instances are deleted underneath it; and the key-backup tension in both
  directions — ADR-0314 names key loss as data loss, and the reverse is equally true once an
  archive is in play, because a vault backup that survives the erasure defeats it. Backing up
  too well and backing up too little are both compliance failures, of different laws.
  Neither record settles it. And §9's estimate has one hole: **variables are counted
  nowhere.** Instances, element visits and jobs have counters; variables per scope do not,
  so the estimate needs either a counter of its own or a conservative per-instance
  allowance — and a conservative allowance refuses installations that would in fact have
  fitted.

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
- rests on ADR-0314 for §8: erasure as key destruction is what lets the archive keep the
  layout a bulk read wants, and ADR-0244 is the declaration shape it reuses; ADR-0070 is the
  posture shape of the one switch that is genuinely once-only
- bounded by ADR-0085 and ADR-0115 (retention), ADR-0071 (the scopes the pseudonymous
  structure inherits), ADR-0010 (no CGO), ADR-0011 (single binary)
