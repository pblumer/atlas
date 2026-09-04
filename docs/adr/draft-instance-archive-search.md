# ADR-DRAFT: An instance that is gone is still findable

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0114 built the event exporter to make a bargain possible: history retention
(ADR-0115) may hard-delete a finished instance, because the instance's events are
already durable in an external index. "Delete the data corpses, but make sure the
data can still be exported."

The half that was never built is the half an operator needs. Nothing read the
index back for instances. The instance search asked this server's own store; for
a purged instance the store had nothing; and the answer came back as an empty
list — indistinguishable from "no such instance ever existed". The export was an
archive for somebody else's tooling, and the operator holding a customer number
from two years ago had no way in.

ADR-0241 and ADR-0244 made the *live* search fast. This
record is about the instances those paths cannot see at all.

## Decision drivers

- **A purged instance must be findable by the same question.** The operator types
  a business value; whether the instance is still resident here is not something
  they know or should have to.
- **An archived answer must not pass for a live one.** The instance cannot be
  opened, replayed or terminated, and what is known about it is what the log
  recorded, not what is true now.
- **Bounded cost.** The export holds every event of every instance. Neither the
  query nor the response may scale with that.
- **Empty must not swallow "not configured".** A server with no exporter, a
  cluster that refuses, and a cluster with nothing matching are three different
  facts, and only one of them is about the data.
- **Reuse the existing seam.** ADR-0189 P5b already reads this index for
  Panorama, through `opensearch.Searcher` and the same configuration. A second
  path would be two sources of truth about one index.

## Considered options

1. **Keep a local tombstone per purged instance (rejected).** A small record left
   behind at purge time, holding the searchable values. It answers instantly, but
   it reintroduces exactly the unbounded growth retention exists to stop — a
   tombstone per instance forever is the instance table again, only thinner.
2. **Index a per-instance document at purge time (rejected).** Write a purpose-
   shaped summary into OpenSearch as the instance is deleted. It would make the
   read trivial, but it puts a network write on the retention sweep and creates a
   second projection that can disagree with the event stream the exporter already
   wrote. It also cannot answer for instances purged before it existed.
3. **Query the exported event stream, in two steps (chosen).** Ask which
   instances a matching variable belonged to, then what those instances were.
   Nothing new is written, nothing is kept locally, and it answers for everything
   the exporter ever carried.

## Decision

**When the live search comes up empty, ask the exported log.**

The lookup is two queries because the export is a stream of events, not a table
of instances:

1. A `terms` aggregation over `value.ScopeKey`, filtered to `Variable` records
   with the name asked for and a value matching the pattern. A value written five
   times is five documents and one bucket, so this answers in distinct instances
   and stays small — small enough that the 1 MiB response bound the client
   enforces is never the thing that decides the answer.
2. A bounded page of `ProcessInstance` hits for exactly those keys, with an
   explicit `_source` list. Sorted newest-first and folded to one row per
   instance by log position, so what is reported is the instance's last recorded
   state rather than a narration of its life.

The aggregation is what bounds the second query. If it finds nothing, the second
query is not sent at all — asking it with no keys would drop the only clause that
bounds it.

**The archive is a fallback, not a second opinion.** It is consulted only when
the live store answered with nothing, and only for a structured `name=value`
query: the index is reached through a term filter, and a term with no name has
nothing to filter on, which would mean scanning the export.

**An archived row is marked, and the marking is load-bearing.** `archived: true`
on the wire; in the panel an "archived" pill, no Replay link, no task link, the
word in the picker, and a sentence in the single-instance view. The row carries
no element instance count, because the archive knows of no live tokens and a zero
meaning "none recorded" must not be dressed up as a measurement.

**The five outcomes travel on `X-Archive-State`:** `available`, `empty`,
`notConfigured`, `refused`, `unreachable`. The UI words each one itself rather
than rendering prose from a header.

Two details that cost a bug each to find. Instance keys are read from their JSON
literal via `json.Number`, never through `float64` — two neighbouring keys past
2^53 land on the same value, and a search that quietly returns the wrong instance
is worse than one that returns none. And bucket keys are *not* read from
`key_as_string`: a `terms` aggregation over a `long` does not emit that field at
all, so the first draft would have returned nothing in production while every
test passed.

## Consequences

- **Positive:** the bargain ADR-0114 and ADR-0115 struck is now complete. An
  operator can find an instance the engine deleted, and can see that it is gone.
- **Positive:** no new storage, no new write path, no new projection. Retention
  stays as cheap as it was.
- **Negative:** the answer is only as complete as the export. An instance purged
  from a server that never had an exporter is unfindable, and the surface says so
  (`notConfigured`) rather than reporting an empty result.
- **Negative:** it is eventually consistent by construction — the exporter lags
  the writer. That is right for cold history and wrong for live data, which is
  why the archive is never consulted while the live store has an answer.
- **Neutral:** full text over the archive is still out of scope. A term filter is
  a seek; free text is a scan, and the export is where the whole history lives.
