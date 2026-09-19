# ADR-0403: The estate graph is derived from the log, never the log from the graph

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Open question:** whether every runtime property tier T2 wants can be served without
  a scan. The counters that exist (ADR-0080's three, ADR-0261's `piByEl`, the incident
  and parked-work readings the starmap already takes) were each built for a different
  view, and nobody has enumerated what a graph wants against what they hold. If a
  wanted property needs a walk of the instance population, the T2/T4 boundary below is
  what has to move, and it is the tier rule that gets re-argued rather than the query.
- **Question checked:** 2026-09

## Context and problem statement

Atlas has grown graph-shaped subjects one at a time, each with its own record and its
own store, and nothing states what relationship they have to each other or to the
write-ahead log:

| Subject | What its nodes are | Where it lives today |
|---|---|---|
| Compiled process ([ADR-0004](0004-compile-bpmn-to-indexed-graph.md)) | BPMN elements and sequence flows | in memory, per deployed definition |
| Starmap ([ADR-0211](0211-panorama-derived-landscape-mesh.md)) | applications, processes, workers, decisions, peers | derived per request, cached 30s |
| Catalogue arrangement ([ADR-0396](0396-catalogue-on-the-starmap.md)) | catalogues, products, composition/aggregation/requires | derived per request, from the item store |
| Capability register ([ADR-0305](0305-business-capabilities-and-value-streams.md)) | capabilities, value streams, stages | a design-time sidecar store |
| Information model ([ADR-0230](0230-process-information-model.md)) | classes, members, relationships | a design-time sidecar store |
| Decision references | decisions and the decisions they call | derived from the DMN registry |
| Token lineage ([ADR-0065](0065-multi-token-process-replay.md)) | element instances and token lines | a fold of the log, per instance |
| Instance object graph | one instance's data objects and their references | derived per instance |

Read together, this is one observation: **almost everything Atlas holds is a graph,
and the differences between these eight are differences of population, retention and
authorization rather than of shape.** That observation immediately raises two
questions that have been asked and never answered:

1. **Does architecture content belong in the WAL?** The log is the one place in Atlas
   with a durability story, a recovery story and an ordering story. It is the obvious
   home for anything that matters, and several of the stores above have none of those
   three.
2. **Can several WALs be merged into one graph?** Atlas is meant to run one instance
   per domain ([#986](https://github.com/pblumer/atlas/issues/986)), a node runs one
   partition today (`api/node.go:193`), and the starmap draws exactly one server.

Neither is answerable on its own. Both are really asking the same prior question:
*which direction does derivation run, and how many tiers are there?* This record
answers that and nothing else. The identity scheme a merge needs is
ADR-0401; what the log contributes to the picture
is ADR-0400; how the
subgraphs are stitched is ADR-0402.

## Decision drivers

- **The WAL's contract is the engine's, and it is deliberately narrow.** Every entry is
  folded by one function, identically live and on recovery (I4), holds only facts the
  processor produced (I6), and is visible only after one fsync (I2). Those are promises
  about execution, not about content management.
- **Retention already differs by orders of magnitude between the populations.** An
  instance is deleted by TTL ([ADR-0085](0085-process-instance-ttl.md)) and by history
  retention ([ADR-0115](0115-history-retention-hard-delete.md)). A capability, a
  catalogue and an ArchiMate document must outlive every instance that ever touched
  them.
- **Authorization differs too.** The starmap is computed per requesting principal
  against the sharing scopes ([ADR-0071](0071-sharing-scopes.md), ADR-0211 §3). The
  WAL knows no principals and never will.
- **Cardinality differs by six orders of magnitude.** The starmap's measured budget is
  400 nodes (`meshMaxNodes`, ADR-0211 §7, measured in Chromium). One busy day's
  runtime facts are millions of records.
- **A cost that grows with the data does not go on the run loop** (ADR-0080,
  [ADR-0239](0239-off-loop-queries.md),
  [ADR-0382](0382-whole-store-reads-leave-the-writer.md)) — and that lesson was learned
  twice, the second time on work nobody called a query.
- **The single-binary posture counts a second store as a real cost**
  ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)). It was decisive once
  already: [ADR-0179](0179-worker-job-history-in-clio.md) refused a sidecar store for
  job history on exactly this ground.

## Considered options

1. **Put architecture into the WAL** — event-source the design-time stores through the
   engine's log, so a capability edit is an event and `applyToState` folds it.
2. **Materialize one graph store** beside the state store, maintained by a WAL tailer
   in the shape of the OpenSearch exporter
   ([ADR-0114](0114-opensearch-event-exporter.md)), and serve every graph question from
   it.
3. **Adopt a graph database**, embedded or external, as the home of all eight subjects.
4. **Keep every graph a projection, and state the tiers and what each may not do.**

## Decision outcome

Chosen: **option 4 — every graph in Atlas is a projection, in four tiers, and the
tier boundary is a rule rather than a preference.**

### The four tiers

| Tier | Population | Cardinality | Computed | Retention |
|---|---|---|---|---|
| **T1 Structure** | what the estate *is*: applications, definitions, workers, decisions, catalogues, products, capabilities, classes, peers | the estate (hundreds) | per request from design-time stores and compiled definitions, per principal, budgeted and cached | as long as the record exists |
| **T2 Runtime weight** | how much and how well T1's nodes and edges are doing | still the estate | maintained counters and bounded indexes only | the counter's, not the instance's |
| **T3 Causal lineage** | one instance's, or one conversation's, actual history | one instance | a query over the log, on demand | the instance's |
| **T4 History** | the estate and its traffic over a long horizon | unbounded | in the export sink | the sink's |

T1, T3 and T4 all exist today: T1 is the starmap and its siblings, T3 is ADR-0065's
timeline and the instance object graph, T4 is ADR-0114's OpenSearch exporter and
ADR-0179's clio job history. T2 exists in fragments — the severity classes of
ADR-0211 §4, the live-token count badged on a shape
([ADR-0366](0366-the-live-diagram-counts-every-parked-token.md)), the incident count a
starmap node carries. What is new here is the boundary, not the tiers.

### The three rules that make it a decision

**Rule 1 — nothing that is drawn, authored or edited enters the WAL.** The engine's log
holds facts its processor produced. A capability definition, a catalogue, an ArchiMate
document, a saved starmap arrangement and a form are none of those: they are authored
content, edited by people, with a retention and an authorization story of their own.
This is the answer to question 1, and it is a refusal with a reason rather than a
habit.

**Rule 2 — runtime facts reach T1 as properties, never as nodes.** A process instance,
an element instance, a job and an incident do not become nodes on the estate graph.
They reach it as numbers and states *on the nodes and edges that already exist*: this
definition has 412 live instances, this edge was taken 9,000 times, this worker has
served nothing for three days. The reason is arithmetic, not taste — T1's budget is
400 nodes because that is where browser layout stops painting in about a second, and
one day of instances is four orders of magnitude past it. A picture that cannot be laid
out is not a smaller picture, it is no picture.

**Rule 3 — a T2 property comes from a maintained counter or a bounded index, or it
does not come.** ADR-0080 is the precedent and the mechanism: three merge counters
folded in the one `applyToState`, read in O(elements). A graph property that would need
a walk of the instance population is refused at the door; if it is worth having, it is
worth an index, and that index is its own decision with its own write-amplification
cost. "It is only one scan, and only when somebody opens the view" is exactly the
sentence ADR-0382 was written about.

### What follows immediately

- **Question 1 is answered: no.** The graph does not go into the WAL. The direction is
  the other one, and it already runs that way — the starmap is a projection, and so is
  every other picture above.
- **Question 2 is answerable, and the answer is "yes, at T1, and only with an identity
  scheme".** Merging logs is not what a merged *graph* needs: what it needs is that two
  nodes' derived subgraphs can be named in one namespace. That is
  ADR-0401, and the stitching is
  ADR-0402. Merging the *logs themselves* is refused for the
  same reason rule 2 exists: it would produce a T3-population graph and call it T1.
- **No tier is promoted by adding a node kind.** A future slice that wants instances on
  the starmap is not a node-kind change; it is a tier change, and it needs its own
  record arguing against the budget and the retention split above.

### Consequences

- **Positive:** no new store, no new invariant surface, and nothing in the engine
  changes. Each graph keeps the authorization and retention of the population it is
  derived from, which is what makes per-principal filtering (ADR-0211 §3) possible at
  all — a shared materialized graph would have to be re-filtered per reader, which is
  the work it was built to avoid. And the two questions above stop being matters of
  opinion.
- **Positive:** the tiers give the eight subjects one vocabulary. "Where does this
  belong" now has an answer before the code is written.
- **Negative / trade-offs accepted:** T1 has **no history by construction**. "What did
  the estate look like in June" is not answerable, and cannot be made answerable
  without leaving this tier. That is deliberate and consistent with what is already
  decided — ADR-0189 P5a keeps a journal of *transitions* rather than a store of
  samples, precisely so Panorama does not become a time-series database — but it is a
  real limitation and it will be asked for.
- **Negative:** T2 is only as good as the counters that happen to exist, and adding one
  costs write amplification on every instance lifecycle event (ADR-0080's own accepted
  trade). A graph question whose counter nobody wants to pay for simply does not get
  answered.
- **Negative:** T4 means the long-horizon answer depends on a component an installation
  may not run. A view that silently degrades is worse than one that says it is
  degraded, so anything reaching T4 has to state that the sink is absent rather than
  render an emptier picture.
- **Follow-ups / risks to watch:** the good idea inside option 1 **survives its
  refusal**, and it should not be lost in it. The design-time stores have no history and
  no audit trail: `api/sidecar` is atomic-write-plus-fsync with one version per record,
  diagram version history is still
  [ADR-0031](0031-diagram-version-history.md) (`Not started`), and "who changed this
  capability, and to what" has no answer anywhere. That want is legitimate. It needs *a*
  log, not *the* WAL — a per-store append-only journal with the store's own retention,
  which is a separate record and a separate cost. Writing it into the engine's log
  instead would put content edited by people behind an invariant written for a
  processor.

## Pros and cons of the options

### Option 1 — architecture in the WAL
- Good: one durability, ordering and recovery story for everything; design-time content
  gains history and an audit trail for free; one backup covers the estate.
- Bad: `applyToState` would have to fold content whose schema moves at the pace of UI
  features, under a promise (I4) that it is the identical deterministic function live
  and on recovery. Retention collides head-on — checkpoints and WAL compaction
  ([ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)) are designed
  around instance lifecycle, and a compacted log must not be able to lose a capability
  definition. Authorization has nowhere to live: the log has no principals, and the
  pictures derived from it are per-principal. And a boot-time recompile of every stored
  definition already showed what happens when the load path is asked to be a validation
  path ([ADR-0393](0393-a-rule-added-later-is-a-gate-on-deploy.md)).

### Option 2 — one materialized graph store, tailer-maintained
- Good: real graph queries including paths; no per-request recomputation; the tailer
  pattern is proven and already off the hot path (ADR-0114).
- Bad: a second thing to back up, compact and restore — the cost ADR-0179 refused. A
  materialized graph is a cache that can drift from the log, which means a repair path
  nobody has designed. Retention gains a second owner, and per-principal filtering has
  to be re-applied on read anyway, so the expensive half of T1 is not avoided.

### Option 3 — a graph database
- Good: the query language the harder questions want, and somebody else's problem to
  scale.
- Bad: contradicts the single-binary constraint outright, or makes a first-class view
  depend on an external component. It also answers a question nobody has yet been
  unable to answer: none of the eight subjects above is currently blocked on query
  power.

### Option 4 — projections in stated tiers (chosen)
- Good: nothing new to operate; every graph inherits the retention and authorization of
  its source; the boundary is checkable in review.
- Bad: no history at T1; T2 bounded by the counters that exist; path queries across the
  whole estate are not available and are not planned.

## Links

- honors I2 (durable before visible), I3 (single writer), I4 (one `applyToState`), I6
  (events are facts)
- builds on ADR-0211 (the derived starmap this generalizes), ADR-0080 (the counter
  mechanism T2 rests on), ADR-0114 and ADR-0179 (T4's sinks), ADR-0065 (T3), ADR-0071
  (why authorization cannot be materialized once)
- companion records: ADR-0401,
  ADR-0400,
  ADR-0402
