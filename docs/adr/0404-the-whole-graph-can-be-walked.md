# ADR-0404: The whole graph can be walked, in a projection with a stated scope

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-19
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0403 states, as its rule 2, that runtime
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
nodes each — 2.4 edges per node, so an average degree of 4.8 (measured, §5) — hanging off
a handful of **supernodes**: the
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
3. **A rebuildable CSR projection**, never maintained authoritatively, with dense
   ordinals, frontier bitmaps and a precomputed component membership — held on the heap or
   mapped from files, which §10 settles as an operator's choice rather than the record's.
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

### 2. The structure is a CSR, and it is never authoritative

Compressed Sparse Row: an offset array per node, a concatenated array of neighbour
ordinals. Two flat, pointer-free arrays — which is what lets §10 decide *where* they live
without changing anything about the structure or the walk. For the year-scale numbers above:

The figures below are **measured**, not estimated — see §5's W0 results. An earlier draft
of this table halved the target array by counting each edge once; an undirected CSR that
supports a walk in either direction, which impact analysis needs, holds every edge in both
endpoints' lists.

| | Size at 110 M nodes / 266 M edges |
|---|---|
| Offsets, (n+1) × uint32 | 420 MB |
| Targets, **2 × m** × uint32 | 2,028 MB |
| Raw CSR total | **2,448 MB** (measured) |
| Peak RSS through build and component pass | **3,369 MB** (measured) |
| Frontier / visited bitmap, 110 M bits each | 14 MB each |
| Union-find parent array | 420 MB |

Delta and varint compression of the adjacency lists is what Neo4j's GDS applies for the
same reason, and would bring the target array down at the cost of decompression on every
traversal. It is **not** costed here, because at an average degree of 4.8 the lists are
four or five entries long and there is little delta structure to exploit — the earlier
claim of "under 1 GB compressed" was an extrapolation from graphs that are far denser than
this one, and it is withdrawn rather than corrected.

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

### 5. One pass computes membership; a query is a lookup. The cloud is an aggregation, not a clustering

The whole-graph walk is a batch job, not a request. One pass over the CSR computes
**connected components** by union-find, which for this topology *is* the instance-family
decomposition. Queries are then lookups against that membership, not walks.

The cloud is a **group-by along dimensions the nodes already carry** — definition,
element, worker, incident state, time bucket — and not a community detection. An earlier
draft of this section said hierarchical Louvain; the measurement below replaced it, and
the replacement is better on every axis that matters.

**Community detection cannot serve this picture, and the reason is not empirical
disappointment — it is arithmetic, twice over.**

*Without the supernodes the graph is disconnected.* Modularity only changes when a node
has edges into both candidate communities, and across components there are none; merging
two edge-disjoint communities strictly lowers Q. So every community is a *subset* of a
component. Louvain can subdivide one 30-node lineage; it can never group two instances.
Communities collapse to components — which union-find already computes, deterministically,
with no resolution parameter, in the time recorded below.

*With the supernodes the graph is connected, and the resolution limit forbids what we
would want.* Modularity optimisation cannot reliably resolve a community holding fewer
than about **√(L/2)** edges, where L is the whole graph's edge count
([Fortunato and Barthélemy, PNAS 2007](https://www.pnas.org/doi/10.1073/pnas.0605965104)).
At the measured L of 266 million that threshold is **≈11,500 edges**. An instance has
**74**. Components are ~156× below the limit, so they provably cannot appear as
communities; what Louvain returns instead is a handful of giant communities organised
around the hubs — the single blob the old text predicted as a rendering accident, which is
in fact the algorithm working correctly on the wrong question.

Turned around, the limit gives the sentence worth remembering: a 74-edge instance is
resolvable as a community only while the whole graph holds at most 2 × 74² ≈ 11,000 edges
— **about 150 instances.** The mechanism works only at the scale where nobody needs it.

So the cloud groups by attribute, and gains three properties clustering cannot give:

- **It names itself.** *"4,812 instances of `order-intake` parked on `Approve`"* is a cloud
  somebody can act on. A community carries no label, and an unlabelled blob is not a
  finding — it is a shape that has to be investigated before it says anything.
- **It is deterministic and parameter-free.** No resolution to tune, and therefore no
  picture that changes because a constant was nudged.
- **It is stable across rebuilds**, which is what makes §9's saved views keep their
  meaning; a Louvain partition is not stable under a rebuild on changed data.

This also removes the last reason to take on gonum: union-find and a group-by are a page
of code each, and the algorithms that would have justified the dependency are the ones
this section just refused.

Two rules on the rendering, both inherited rather than invented:

- **A density without a named reference is a picture of nothing.** A dense region must say
  dense *in what*, and as of *when*.
- **The supernodes are excluded before aggregating and overlaid after layout.** They are
  the hubs every instance touches, so any grouping that keeps them puts everything in one
  cell. This is the visual return of §1's arithmetic.

#### What W0 measured

A dependency-free spike built the structure this record describes at the year-scale size it
names — 3.67 million components of 30 nodes, 74 edges each, on a 16 GB / 4-core machine,
Go 1.26. A throwaway design spike under AGENTS.md's stated TDD exception; W1 re-does it
test-first against real state rather than a generator. The generator's parameters are given
above so the numbers can be reproduced from this description.

| | 1 M nodes | 10 M nodes | 110 M nodes |
|---|---|---|---|
| edges | 2.4 M | 24.2 M | 265.8 M |
| CSR build (count + fill) | 82 ms | 2.1 s | **23.1 s** |
| components (union-find) | 48 ms | 676 ms | **7.8 s** |
| whole-graph walk, every node | 33 ms | 327 ms | **3.7 s** |
| CSR size | 22 MB | 223 MB | **2,448 MB** |
| peak RSS | 34 MB | 333 MB | **3,369 MB** |

Three things it settled, two of them against this record's own earlier text:

- **The walk is confirmed cheap: 3.7 seconds for all 110 million nodes.** §1's claim that a
  whole-graph walk degenerates into a sequential pass was derived from the topology and
  marked as unmeasured. It is now measured.
- **The memory estimate in §2 was too low by more than a factor of two, and §2 is corrected
  below.** The error was conceptual rather than arithmetic: an undirected CSR that supports
  a walk in *either* direction — which impact analysis needs — stores every edge in both
  endpoints' lists. The earlier figure counted each edge once.
- **The build, not the walk, is the expensive phase**, and its fill pass is 4.5× its count
  pass: random writes into a 2 GB array are cache-hostile. A rebuild is therefore ~30
  seconds, not seconds, which is what §4's seeding and the "window during which the run
  graph is absent or behind" have to be sized against.

One wording error the measurement exposed: this record said "average degree ~2.5". That is
*edges per node* (2.42 measured); the average **degree** is twice it, 4.83. The arguments
above rest on component size and on disconnection, neither of which the mix-up touched, but
the number was wrong wherever it appeared.

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

**What the budget counts is the resident working set, not the projection's size.** §10
measured those apart: the year-scale graph is 2.4 GB of structure and runs its whole-graph
pass in 294 MB. So the estimate below produces two numbers — the file the build will write,
and the working set the query will hold — and only the second is what the limit governs. The
refusal still exists and still refuses; it simply refuses about eight times later than a
budget over the whole structure would.

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
  the server answers *this scope would need 2.4 GB against a 1 GB budget* and names the
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

### 10. Where the CSR lives is an operator's dial, not the data's decision

§2's figures are resident memory, and 3.4 GB in the process that executes business
processes is the cost §9 exists to bound. It does not have to be paid that way, and W0b
measured the alternative.

A CSR is two flat arrays with no pointers in them. Mapped from two files with `mmap`, the
page cache decides what is resident: the projection reserves *address space* rather than
memory, and its pages are evictable in a way heap pages are not. Both halves of that were
measured at the same year scale, on the same generated graph, on a 16 GB / 4-core machine
with the page cache dropped before every cold run.

**The build is cheaper in memory and dearer in time, and the difference is a detour.**
W0's fill pass scatters random writes across the whole 2 GB array, which is a page cache's
worst case on a mapped file. The standard external-memory answer is to sort rather than
scatter: distribute the directed pairs into 64 buckets by source, then stream each bucket
out in node order, so every write to the CSR file is sequential.

| Build at 110 M nodes / 266 M edges | on the heap (W0) | to files (W0b) |
|---|---|---|
| total | **23.1 s** | **31.3 s** |
| peak RSS | **3,369 MB** | **944 MB** |

35% more time for 72% less memory. The phases are degrees 3.8 s, offsets 3.1 s, distribute
18.2 s, stream out 6.2 s; the distribute pass writes 4.3 GB of bucket files which the
stream pass consumes and deletes, so the build's transient disk need is about three times
the CSR it produces.

**The walk off a cold disk costs nothing measurable, because the layout makes it
sequential.** This is what §3's persisted ordinals are quietly worth: assign them *in key
order* and a component's nodes are contiguous in ordinal space, so its adjacency lists are
adjacent in the target array. A pass in node order is then a stream, and readahead carries
it.

#### That premise is conditional, and the condition is arrival concurrency (measured, W1)

The sentence above was written as though key order were enough. **It is not.** W1 built the
ordinal map and the CSR from a real `ReadView` over state the engine wrote, and measured the
span of each component's ordinal range — 1.0 meaning a component occupies an unbroken range:

| Instances in flight when they arrive | Ordinal span |
|---|---|
| 1 — arrival spread over time | **1.00** |
| 8 | 5.67 |
| 64 | 43.00 |
| 512 | 341.67 |

The law behind those four points is exact, not fitted. The engine mints an instance's *k*
element instances across *k* batch phases, and with *c* instances in flight each phase lays
down *c* keys before the next begins, so one component's nodes end up one phase stride
apart:

    span = ((k − 1)·c + 1) / k

So the premise holds **perfectly** where instances arrive spread over time, which is the
10 000-a-day installation this record is sized for, and **degrades linearly** with a burst
— a bulk import, a message storm, a backlog drained after an outage. It is pinned as a law
rather than a number in `rungraph/locality_test.go`, so a change to the engine's batching
shows up there rather than as an unexplained slowdown of a walk.

What this costs in practice is smaller than the span suggests, and stating it needs both
numbers. At 30 nodes per instance and 100 in flight the span is ≈ 97 ordinals, whose
adjacency lists are ~470 target entries — under 2 KB, so a component's walk still touches
one or two pages and the table above stands. At 10 000 in flight it is ≈ 9 700 ordinals and
~185 KB, which is some 45 pages per component: that is where the capped, un-advised
scattered read this section measured at 74× would bite.

Two ways out, and this record deliberately picks neither yet because nothing depends on it
until a walk is wired: **assign ordinals grouped by component**, which costs a pass and
breaks §3's "the ordinal is the scan position" simplicity; or **leave the layout alone and
make `MADV_RANDOM` the dial's default above a measured concurrency**, which keeps §3 and
accepts the pages. The choice wants the walk's own profile, which W1 does not have.

| Whole-graph walk, 110 M nodes | time | resident |
|---|---|---|
| on the heap (W0) | 3.7 s | 3,369 MB |
| mapped, cold cache, uncapped | **3.5 s** | 2,464 MB |
| mapped, warm | 2.5–3.2 s | 2,464 MB |
| mapped, capped at 1 GB | 2.9 s | 1,019 MB |
| mapped, capped at 300 MB | **3.4 s** | **294 MB** |

So the resident footprint of the whole-graph pass is **a number an operator chooses**, not
one the data dictates: 294 MB instead of 3,369 MB, at the same wall time. The kernel evicts
behind the scan and reads ahead in front of it, which is the textbook streaming case.

**And then the trap, which is the finding that earns this measurement.** Scattered access —
the pattern a filter produces, "every instance with an incident", touching components spread
across the whole mapping — behaves completely differently, and *the naive implementation
fails in a way that looks like the disk being too slow*:

| 50 000 scattered components, 1.5 M nodes touched | time | resident |
|---|---|---|
| uncapped, default readahead | 2.9 s | 2,023 MB |
| uncapped, `MADV_RANDOM` | 3.6 s | 373 MB |
| capped at 300 MB, `MADV_RANDOM` | **4.0 s** | **287 MB** |
| capped at 300 MB, default readahead | **4 min 54 s** | 10 MB |

The last row is 74× the row above it, and the whole difference is one `madvise` call.
With readahead on and a small cap the cache thrashes: every touched node pulls a readahead
window that immediately evicts what the previous touch fetched, so each of the 50,000
touches re-faults repeatedly. With `MADV_RANDOM` the kernel fetches only the pages actually
touched, the working set is genuinely small, and nothing thrashes. Readahead costs memory
even uncapped — 2,023 MB to touch 1.5 M nodes, against 373 MB — for a 24% speedup.

**So the rule is that the readahead policy is part of the query, not part of the
mapping.** A whole-graph pass advises sequential; a scattered selection advises random.
Getting it wrong is not a tuning miss, it is a two-orders-of-magnitude difference that
will be misdiagnosed as "the disk variant does not work".

What this costs, stated rather than discovered:

- **The projection stops being invisible.** It is 2.4 GB of derived files in the data
  directory that an operator will see, may back up pointlessly, and will ask about. It stays
  disposable — delete it and it rebuilds — and it is still not ADR-0179's refused "second
  store", because it has no schema, no compaction and no retention of its own. But it needs
  a version stamp and an explicit "safe to delete at any time".
- **The numbers above are this machine's.** `/dev/vda` is a virtio device whose backing
  store is unknown; the cold walk implies SSD-class sequential throughput. The arithmetic
  for a cloud block volume is a different matter and is not measured here: 2.4 GB in 4 KiB
  pages is 626,688 page-ins, which at a gp3 baseline of 3,000 IOPS is 209 seconds *if* the
  access were random. The layout above is what keeps it from being random, and that is the
  property to verify on real ordinals in W1 rather than to assume.

**§9's budget therefore changes meaning, in the direction that helps.** It stops being
"how much memory may the projection own" and becomes "how much resident working set may it
have" — and the refusal threshold moves up by roughly the ratio between the file and the hot
set, which these measurements put at about 8×. The estimate §9 makes before allocating now
produces two numbers rather than one: the file the build will write, and the working set the
query will hold. Only the second is what the operator's limit governs.

### Consequences

- **Positive:** the walk is affordable *because* of the topology that makes a global
  instance graph a bad idea. One structure serves both asks — the walk and the cloud — and
  the clustering that makes the picture legible is the same pass that makes the walk cheap.
- **Positive:** nothing new to back up. The projection is disposable by construction, which
  is the property that distinguishes it from the persistent graph store option 2 proposes.
- **Positive:** no CGO and **no new dependency at all.** An earlier draft expected gonum for
  the layout and community algorithms; §5's refusal of community detection removed the
  reason, and union-find plus a group-by are a page of code each. The spike that produced
  §5's numbers was written dependency-free on purpose: one that needed gonum to decide
  whether gonum was needed would have answered a different question.
- **Negative / trade-offs accepted:** **memory — no longer the number that could make this
  undeployable.** Held on the heap the year-scale projection is 2.4 GB of CSR and 3.4 GB peak
  RSS, in a binary that also runs the engine. §10 measured the file-backed alternative and it
  changes the character of the cost rather than shaving it: **294 MB resident for the same
  whole-graph pass at the same wall time**, with the build at 944 MB instead of 3,369 MB for
  35% more time. What remains is a real but ordinary cost — 2.4 GB of derived files in the
  data directory, and a readahead policy that has to match the access pattern or lose two
  orders of magnitude. The earlier framing, that a large enough installation simply cannot
  have the feature, was a consequence of assuming residency. It is withdrawn: what a large
  installation needs is a working-set budget and the right `madvise`, not more RAM than the
  engine has. The accepted consequence, stated precisely because the loose
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

### Option 3 — a rebuildable CSR, heap- or file-backed (chosen)
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

- precises rule 2 of ADR-0403; uses the identity
  scheme of ADR-0401; complements
  ADR-0400, which aggregates the
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
