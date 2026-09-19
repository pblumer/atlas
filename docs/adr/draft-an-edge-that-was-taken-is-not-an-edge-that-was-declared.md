# ADR-DRAFT: An edge that was taken is not an edge that was declared

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Open question:** whether a traversal count belongs in the engine's own state at all,
  given that the same number is derivable from the export sink wherever one is
  configured ([ADR-0114](0114-opensearch-event-exporter.md),
  [ADR-0179](0179-worker-job-history-in-clio.md)). The counter chosen below already
  exists, so this record spends nothing new — but the *roll-up across versions* it asks
  for next does, and if most installations run an exporter then that roll-up is paying
  for something the sink gives away. Nobody has measured how many installations run one.
- **Question checked:** 2026-09

## Context and problem statement

Every edge on the starmap today is **declared**. A `calls` edge exists because a call
activity names a target; a `uses` edge because a service task names a worker or a
business-rule task names a decision; an `offers` edge because a catalogue lists a
product. All of it is known at deploy time, and all of it is a fact the server can point
at — which is the test ADR-0211 §1 sets and which these edges pass.

The log knows something the compiler cannot: **which of those edges was ever taken.**
`applyToState` has folded a cumulative visit counter per `(definition, element)` since
[ADR-0080](0080-runtime-aggregate-counters.md) — `cfElementVisitAgg`, a Pebble merge
counter, incremented in `engine/apply.go` on element activation and read in
O(elements). It is never decremented and never deleted, so it survives instance TTL
([ADR-0085](0085-process-instance-ttl.md)) and history retention
([ADR-0115](0115-history-retention-hard-delete.md)).

Two facts about the starmap's edges make that counter immediately relevant:

1. **Every derived edge is anchored on exactly one compiled element.** A `calls` edge is
   a call activity, a `uses` edge to a worker is a service task, a `uses` edge to a
   decision is a business-rule task. The edge and the element are one-to-one.
2. So the counter that already exists is, without any new fold, **the traversal count of
   that edge**.

What the picture does with this today is nothing. A worker nobody has ever used and a
call path carrying the whole business are drawn identically. The result is that the two
questions an operator actually brings to a landscape — *what can I retire* and *what
actually carries the load* — are the two it cannot answer, while it answers questions
nobody asked about the shape of the estate.

There is a second, sharper gap. Some edges are resolved **late**, after the compiler is
done:

- a call-activity target overridden per server
  ([ADR-0105](0105-per-server-call-activity-target-overrides.md));
- a decision bound to `latest` rather than to a version
  ([ADR-0379](0379-dmn-version-follows-the-document.md),
  [ADR-0385](0385-a-decision-is-addressed-by-both-of-its-names.md));
- a worker resolved by name against operator configuration
  ([ADR-0256](0256-the-model-is-authored-the-provider-is-configured.md)).

For those, what the log recorded can differ from what the model declared, and that
difference is the single most actionable thing an estate graph can say. Today it is
invisible.

## Decision drivers

- **Provenance is already the mechanism.** ADR-0211 §2 requires derived and modeled
  content to be visually distinct, always, with a legend stating the cases. A further
  word in that vocabulary is far cheaper than a further picture.
- **No scan** — tier T2's rule in
  ADR-draft-the-estate-graph-is-derived-from-the-log: a graph property comes from a
  maintained counter or a bounded index, or it does not come.
- **A picture that overstates is worse than one that says less.** ADR-0211 §4 already
  refuses to render *unreachable* and *stale* alike, because a view that loses
  credibility on the first network fault loses it at the moment it is being relied on.
  The same standard applies to "nothing has used this".
- **I1/I4:** whatever is folded must be deterministic in the one `applyToState` and must
  not allocate on the hot path. The chosen counter already satisfies both, because it
  already exists.

## Considered options

1. **Derive traversal per request from live and history state.** No new writes. Wrong by
   construction: retention deletes the instances, so the answer is "recently" wearing
   the label "ever".
2. **A WAL tailer maintaining an edge-traversal table** beside the state store, in
   ADR-0114's shape.
3. **Join the starmap's declared edges to `cfElementVisitAgg`, which already exists.**
4. **Ask the export sink** (tier T4) for the number.

## Decision outcome

Chosen: **option 3 for the number, option 4 for the long horizon**, and a fourth
provenance word for what the number means.

### The edge gains a traversal fact, not a new store

A derived edge carries `takenCount` and `lastTakenAt`, read from the counter keyed by
the edge's anchoring `(procDefKey, elementId)`. The read is O(edges) on a graph already
bounded at 400 nodes, off the run loop like the rest of the starmap's status half, and
**nothing new is folded**: this is a join between two things Atlas already maintains.

### A fourth word: `taken`

Provenance today is `derived` / `modeled` / `both` (ADR-0211 §2). Traversal is a second,
orthogonal axis on the same edge, and the four combinations are four different findings:

| Declared | Taken | Reading | Rendered as |
|---|---|---|---|
| yes | yes | ordinary | the line as drawn today |
| yes | no | **nothing has walked this path** in the window the counter covers | a subdued line, with the window stated |
| no | yes | **drift** — something resolved to a provider the model does not name | a marked line, and a finding |
| no | no | nothing | no line |

Three rules govern it, and each exists because the obvious rendering is wrong:

**A never-taken edge is never labelled dead.** Never-taken is not evidence of
unreachable. A quarterly reconciliation path, a compensation branch, an error handler:
each is correctly zero for months and each is load-bearing. A picture that says "dead"
about one of those is believed once and distrusted afterwards, which costs more than the
feature is worth. The rendering states the *window*, not a verdict.

**The window is part of the fact.** The counter is keyed by `procDefKey` — the deployed
definition — so a redeployment starts a fresh counter. That is the honest limit of this
mechanism and it must be shown rather than smoothed: a zero on a definition deployed
this morning says nothing at all, and the picture says so by naming the deployment's age
beside the count. A roll-up across versions of the same process id is a follow-up and
not free: element identity inside a compiled process is an **integer index interned per
process** (`compiler/builder.go`), so index 7 in v2 need not be the element index 7 was
in v1, and rolling up means mapping both back through their interned BPMN string ids —
possible at read time, exact only while nobody renames an element.

**`taken and not declared` is only producible where resolution is late.** For an
ordinary local call activity the compiler declares the edge, so the combination cannot
arise and a picture offering it would be teaching a distinction that has one answer —
the mistake ADR-0211 §6 names about "is this the only way". The combination is drawn
only for the three late-resolution kinds above, and for peers once delivery exists
([#986](https://github.com/pblumer/atlas/issues/986),
[ADR-0372](0372-peer-message-delivery-worker.md)). Everywhere else the axis is
`declared` with a count, and the legend says which kinds can drift.

### What this explicitly does not do

- **It does not put instances on the graph.** The count is a property of an edge (tier
  T2), not a population of nodes. That boundary is
  ADR-draft-the-estate-graph-is-derived-from-the-log's rule 2.
- **It does not answer "how long did it take".** Duration is not in the counter, and
  adding it is a second counter with a second write on the hot path — a separate
  decision with a separate cost.
- **It does not replace the drift journal.** ADR-0189 P5a records *transitions* in
  observation state. This records *traffic* over declared structure. They answer
  different questions and neither is the other's history.

### Consequences

- **Positive:** the two retirement questions become answerable, with no new fold, no new
  store, no hot-path cost and no scan. The first genuinely new thing the log tells the
  estate graph, and it costs a join.
- **Positive:** drift on late-resolved edges becomes visible, which is the one finding
  here worth an alert rather than a colour.
- **Negative / trade-offs accepted:** the counter's zero point is unknowable in general.
  It is seeded once by `backfillRuntimeCountersIfNeeded` from state that already existed
  at upgrade, and that state had already lost whatever retention deleted. So "never" is
  precisely "not since this definition was deployed, or since the backfill, whichever is
  later" — and the picture has to say that rather than say "never".
- **Negative:** a redeploy resets the evidence. Until the roll-up lands, the feature is
  weakest on exactly the processes that are changed most often.
- **Follow-ups / risks to watch:** the cross-version roll-up through interned BPMN ids;
  duration as a second counter if anyone asks; the peer edge once ADR-0372 is built;
  and whether `lastTakenAt` needs a counter of its own — the merge counter holds a total
  and not a timestamp, so "last taken" is not in it and would be the one genuinely new
  write this record's follow-ups incur.

## Pros and cons of the options

### Option 1 — derive per request from live and history state
- Good: no writes at all, available immediately.
- Bad: the answer is wrong in the direction that matters. Retention has deleted the
  instances that prove an edge was used, so a heavily used path on an old definition
  reads as never taken. A wrong retirement recommendation is worse than none.

### Option 2 — a tailer-maintained edge table
- Good: independent of the engine's fold; could carry durations and first/last seen
  without touching `applyToState`.
- Bad: a second store to back up, compact and restore for a number that is already
  being maintained — the cost ADR-0179 refused, paid twice over.

### Option 3 — join the existing counter (chosen)
- Good: zero new writes, zero new state, O(edges) read, recovery-correct because the
  fold it reads is ADR-0080's and already proven live and on replay.
- Bad: keyed per definition version, so a redeploy resets it; holds a total and not a
  timestamp; the roll-up that fixes the first is not exact.

### Option 4 — ask the export sink
- Good: exact, long-horizon, survives redeployment, and the data is already being
  shipped where an exporter runs.
- Bad: only available where an exporter is configured, and a first-class picture that is
  empty on installations without one has to say so in a way readers will not mistake for
  "nothing has run". Kept for the long horizon, where its retention is the whole point.

## Links

- honors I1, I4, I6; adds no fold
- builds on ADR-0080 (`cfElementVisitAgg`, the counter this joins), ADR-0022 (the
  per-instance visit history it aggregates), ADR-0211 §2 and §4 (provenance, and the
  refusal to render different findings alike)
- bounded by ADR-draft-the-estate-graph-is-derived-from-the-log (tier T2's rule)
- relates to ADR-0105, ADR-0379, ADR-0385, ADR-0256 (the late-resolution kinds that can
  drift) and to [#986](https://github.com/pblumer/atlas/issues/986) for the peer edge
