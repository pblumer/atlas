# ADR-DRAFT: Finding an instance — a key lookup, a per-definition index, and a read view

- **Status:** Accepted
- **Date:** 2026-09-03
- **Deciders:** Atlas engine team

## Context and problem statement

An operator's most common question about a running system is about **one instance**:
"where is MT-1998?", "what happened to the instance this ticket names?". Atlas's
answer to it cost, in every case, a walk of every instance in the engine.

Three surfaces made that walk:

- `GET /api/v1/instances/search?q=` scanned the live instance family and then the
  whole terminal history, reading every scope's variables on the way — and it did
  it **inside `s.do`**, on the single-writer run loop. At a few hundred thousand
  instances that is millions of decodes during which the processor folds no
  commands at all. A search did not just take a long time; it stopped the engine
  for as long as it took.
- `GET /api/v1/instances?process=<key>` filtered **after** the scan, so listing a
  version with three instances cost the same walk as listing the busiest one.
- The finished half was collected in full and then `sort.Slice`d by completion
  time in memory, so the cost of showing the ten most recent completions was the
  size of the whole history.

The live view's instance panel then fetched all of a version's instances — with
every variable on every row — and rendered one card each, on a 1.5 second poll.

None of this is a defect in the handlers; it is the absence of an index. The state
store keys process instances by instance key (`pi:<piKey>`, `piHist:<piKey>`) and
variables by scope (`var:<scopeKey>:<name>`). There is no index from a definition
to its instances, none from a completion time to an instance, and none from a
variable value to anything. Every question that is not "this exact instance key"
therefore degrades to a scan.

The question this record answers: **what does Atlas index, and what does it
decline to index, so that finding an instance costs the answer rather than the
store — without breaking the single-writer model or the one-`applyToState` rule?**

## Decision drivers

- **The common case must be a point read.** "I have the key" and "I have the
  business value" are the two ways an operator arrives. Neither should be
  proportional to the instance count.
- **Never scan on the run loop (I3).** A read whose cost grows with the store must
  not occupy the single writer. ADR-0157 moved handlers off the loop and
  `state.ReadView` exists precisely so an off-loop read is still *consistent*.
- **Derived state is folded, not computed (I4/I6).** Any index must be maintained
  in `applyToState` from the event alone, so replay rebuilds it identically.
- **Don't index what cannot be sought.** An ordered key-value store answers
  equality and prefix. Substring and full text are not accelerable by it, and
  pretending otherwise is how a "fast" search quietly becomes a scan again.
- **An upgraded store must not read empty.** A missing counter reads low; a
  missing index reads *nothing*, which is worse — a list that silently shows zero
  instances is believed.

## Considered options

1. **Leave the scans, cap harder.** Lower the page caps and accept that a search
   is slow. Rejected: the cap bounds the *response*, never the read, and the read
   is what holds the loop.
2. **Index by definition, and page by cursor (chosen for the listing).** Two
   valueless column families maintained in the fold; the operator's actual
   navigation ("this version's instances, newest first") becomes a bounded range
   scan.
3. **Index every variable value (deferred).** A `varIdx:<name>:<value>:<piKey>`
   family would make `identityId=MT-1998` a seek. Deferred, not rejected — see
   *Follow-ups*.
4. **Push all of it to OpenSearch (rejected as the primary answer).** Atlas
   already exports its event stream (ADR-0114), and that is the right home for
   full text over cold history. It is the wrong home for "which instances of this
   version are running", which must be answerable from the engine's own state,
   with no optional component deployed and no export lag.

## Decision outcome

Chosen: **option 2, plus a point-read path and a read view — three layers, each
answering a different shape of the question.**

**A bare instance key is a point read.** `?q=` that is nothing but digits is
resolved against `pi:<key>` and then `piHist:<key>` and returns that one instance
with its whole variable set. Two reads, no scan, live or finished. A number that
resolves to no instance falls through to the content search, so `3098` still finds
`zip=3098`.

**Reads happen off the run loop, against a `state.ReadView`.** The handler visits
the loop once, briefly, to take a Pebble snapshot and copy the deployment labels
it will need; everything after that runs on the request goroutine. A search no
longer stalls the processor, and what it reports is one coherent state rather than
a state that moved underneath it mid-scan.

**Two new column families index instances by their definition:**

```
piByDef:<procDefKey>:<piKey>                    → nil
piDoneByDef:<procDefKey>:<completedAt>:<piKey>  → nil
```

Both are valueless — the definition key, the instance key and the completion time
*are* the key, and the instance's own record holds everything a reader wants. They
are written by the same `Tx` methods that write the record they index
(`PutProcessInstance`, `PutProcessInstanceHistory`, `DeleteProcessInstance`,
`PurgeInstanceHistory`, `MigrateInstance`), so an index entry cannot be forgotten
by a caller and cannot disagree with the record it points at.

Completion time leads the history key because "most recently finished first" is
the order the operator reads history in; with it in the key that order is a
backwards range scan instead of sorting the whole history in memory. It is a pair
with the instance key because completion order is not key order — an instance
started first can finish last — which is also why the paging cursor for that half
carries both.

**`GET /api/v1/instances` gains `?state=active|finished` and `?before=<cursor>`.**
Scoped with `?process=`, each half is a bounded page off its own index and the
response carries `X-Instances-Next-Cursor`, mirroring the task inbox's newest-first
paging. Unscoped, the endpoint behaves exactly as before: capped, unpaged, and a
full family walk — there is no index to page, and saying so is better than
pretending. `?before=` without `?process=` is refused rather than ignored, because
a silently dropped paging parameter is how a client loops over the same page
forever.

**`GET /api/v1/instances/search` gains `?process=`**, which scopes the content
walk to one definition's index. The content search itself is still a walk — of
that version, not of the engine — and is still capped.

**A one-time backfill seeds both families** from the instances and history a store
already holds, marked in `cfMeta` like the ADR-0080 and ADR-0083 counters before
it, in one atomic synced batch so a crash mid-migration leaves nothing.

### Consequences

- **Positive:** a version's instances cost the page, not the store. The finished
  half comes back in completion order without an in-memory sort. An instance key
  is O(1) whatever the engine holds. No search occupies the run loop. The live
  panel fetches one page per half instead of every instance, and reaches the rest
  through the cursor and the search box.
- **Positive:** the operator is told what they are looking at — "80 of 150", not
  "80" — so a page is never mistaken for the whole truth.
- **Negative / trade-offs accepted:** two more index entries per instance
  lifecycle (one on activation, one on completion, both valueless). A read view
  held open for the length of a request holds back compaction of everything
  written since — bounded by the request, and the reason `Close` is deferred at
  the point the view is taken. An off-loop read is consistent but very slightly
  stale, which for an operator surface polling at 1.5 seconds is not a
  distinction that exists.
- **Negative:** unscoped `?state=finished` still walks and sorts the whole
  history. Adding a global completion-ordered family would fix it; it is not
  added because every surface that lists history is scoped to a version, and an
  index nothing reads is write amplification with no reader.
- **Follow-ups / risks to watch:** the **variable value index** (option 3) is the
  next layer and the one that makes `identityId=MT-1998` a seek rather than a
  walk. It should be **declarative** — a process states which variables are
  searchable, resolved and interned at deploy time (I5) — because indexing every
  value would double the write path and index JSON blobs; it should cover exact
  and prefix matches only; and its entries must be dropped by `DeleteVariable`
  and by the ADR-0146 history purge. Substring and free text over cold history
  belong in OpenSearch (ADR-0114), not in a new engine index.

## Pros and cons of the options

### Option 1 — cap harder
- Good: no new state, no migration.
- Bad: bounds the response, not the read. The run loop keeps paying for the scan,
  which is the actual failure.

### Option 2 — per-definition indexes with cursor paging
- Good: matches how operators actually navigate (by version, newest first). Small,
  valueless entries. Fits the existing fold, backfill and paging conventions
  exactly (ADR-0080, ADR-0083, ADR-0146, and the task inbox's `?before=`).
- Bad: does not help an unscoped question, and does not help a content search
  become a seek.

### Option 3 — variable value index
- Good: turns the operator's real query — a business key — into a seek.
- Bad: needs a declaration surface (which variables?), a compile-time resolution,
  a purge path, and a decision about value size. Enough design to deserve its own
  record rather than riding along in this one.

### Option 4 — OpenSearch for everything
- Good: already exported; genuinely the right tool for full text and cold history.
- Bad: optional, lagging, and external. "Which instances of this version are
  running" must be answerable from the engine's own state.

## Links

- builds on ADR-0157 (handlers off the run loop) and the `state.ReadView` it
  introduced
- follows the derived-index pattern of ADR-0080 and ADR-0083 (per-definition
  counters, folded and backfilled) and ADR-0146 (the due-date purge index)
- mirrors the newest-first cursor paging the task inbox uses
- relates to ADR-0017 (terminal history), ADR-0114 (OpenSearch export),
  ADR-0115 / ADR-0146 (history retention and purge)
