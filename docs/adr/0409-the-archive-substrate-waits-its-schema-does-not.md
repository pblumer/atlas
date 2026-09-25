# ADR-0409: The archive substrate waits, its schema does not

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-09-22
- **Deciders:** Atlas maintainers
- **Open question:** whether anybody will walk a year-old run graph. The answer decides
  whether the substrate refused below is ever owed, and it is a demand question rather than
  a technical one — nothing in this repository can settle it.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0404](0404-the-whole-graph-can-be-walked.md) §7 limits the run graph to "live instances
plus history within retention" and §8 removes the obstacle everyone expected in front of an
archive: erasure as key destruction ([ADR-0314](0314-portal-personal-data.md)) means the
archive may use exactly the layout a sequential CSR build wants and stay subject-erasable, so
**no archive-layout decision is owed**. §8 then hands two findings to "the archive substrate
record, for whoever writes it". This is that record.

What it found first is that the question it was convened to answer is the wrong one. "We need
history" is four different questions with four different costs:

| Question | Data needed | Served today? |
|---|---|---|
| How did traffic over this path develop? | daily aggregate per (definition, flow) — kilobytes/day | decided, not built: [ADR-0400](0400-an-edge-that-was-taken-is-not-an-edge-that-was-declared.md), `Accepted` / `Not started` |
| Which variants ran last quarter? | one path per instance | derivable from the export: every element-instance event is indexed with `ProcessInstanceKey`, `ElementId` and `Position`, so a path is a group-and-order, not a graph walk |
| Show me case 4711 from March | one instance's records | **yes** — [ADR-0247](0247-instance-archive-search.md) is `Accepted` / `Landed`, and `api/instancearchive.go` reads the export back for purged instances |
| Walk the whole graph of last year | ~275 M edges | no |

Only the last row needs a substrate. Three of the four are answered, decided, or a query away
— and the row that answers *what actually happens in reality*, the first, is kilobytes per
day. So the substrate is the one slice to challenge hardest before funding it, and the
challenge is a question about demand: if the honest answer to "walk last year" is "an auditor,
for one case", that is a point lookup, which row three already serves.

Meanwhile something else is true and was not on anybody's list. **The export is already a
schema, and it is already being read back by name.**

## Decision drivers

- Nothing may be built on an assumed requirement; §7's limit is stated and holding it costs
  nothing.
- A projection whose meaning depends on code that can be renamed is not an archive.
- ADR-0114 chose an uncurated projection deliberately; a change here must not overturn that
  choice for a benefit a cheaper measure also gives.
- Anything that makes a five-year read possible must be in place *before* the five years, not
  after — this is the one class of debt that cannot be paid retroactively.

## Considered options

1. Build the archive substrate now (S3-columnar objects, time-partitioned, CSR built on read).
2. Refuse the substrate, record and guard the export's schema (chosen).
3. Do neither: leave both the substrate and the schema as they are.

## Decision outcome

Chosen option: **"refuse the substrate, record and guard the schema"**, in two parts.

### 1. No archive substrate is built, and the reversal condition is named

The whole-graph walk stays scoped to §7's "this installation's current data". Nothing about
that limit is discovered at runtime: the projection states the position it is true as of
(ADR-0404 §4) and the cloud states what it is dense in, so a picture over retained data cannot
be mistaken for a picture over all data.

What would reverse this: a named consumer for the fourth row who cannot be served by the
third. Not "an archive would be useful" — a question somebody asks that a point lookup and a
daily aggregate cannot answer. Until then the substrate would be a store to back up, compact
and restore (the cost [ADR-0179](0179-worker-job-history-in-clio.md) refused) in service of a
question nobody has asked.

### 2. The export's payload column names are recorded, and a change to them fails a test

This is the part that was genuinely owed, and §8 understated it. Its wording is that the
implicit schema "is right for search and a liability for a graph source read in five years".
Measured against the code, the liability is present-tense:

- `opensearch/exporter.go` marshals a record's value as `any` with no `json` tags, so **every
  column name in the index is a Go identifier**: 122 of them across 17 value types.
- Atlas queries eight of those identifiers back *by name*, as strings, in
  `api/instancearchive.go` and `api/panoramacontext.go` — `value.Name.keyword`,
  `value.Text.keyword`, `value.ScopeKey`, `value.ProcessDefKey`, `value.State`,
  `value.CreatedAt`, `value.CompletedAt`, `value.CorrelationKey`.
- The document's **envelope** is guarded — `document`'s own fields carry `json` tags and a
  test pins them. The **payload** had neither tags nor a test.

So a rename compiled, kept every test green (the query builder still emitted the same literal
string), and broke the feature in production: documents after the rename carry the new column,
documents before it keep the old one, and a query matches exactly one of the two. ADR-0247's
own words describe what an operator then sees — an empty list, "indistinguishable from *no such
instance ever existed*".

Two guards close it from both ends, and each was made to fail before being kept:

- `model/archiveschema_test.go` records the 122 column names in `model/testdata/archive-columns.txt`
  and compares them against what `encoding/json` actually produces, per value type. A rename
  reports as one added and one removed name. A second test scans `value.go` for `encode`
  methods, so a value type added later cannot arrive with unrecorded columns.
- `api/archiveschema_test.go` checks that every `value.X` path the query builders emit still
  names a field the corresponding type exports, and that no path in those two files is missing
  from its table.

The column names live in a **data file** rather than in a Go literal on purpose: a repo-wide
rename over `*.go` would rewrite a Go literal along with the fields it guards and the guard
would pass. The two guards also divide the work by failure mode — the API guard catches the
rename that leaves the query string behind (what a refactoring tool does), the model guard
catches the rename that carries the query string with it (what a text substitution does).

**`json` tags were considered and not taken.** Tagging all 122 fields with their current names
would pin each column against the Go identifier outright, which is stronger. It would also
overturn ADR-0114's deliberate choice of an uncurated projection, and add nineteen structs'
worth of tags that read as redundant, to obtain at nineteen places what one recorded table
obtains at one. The guard does not make a rename impossible; it makes it deliberate, which is
what was missing.

### 3. One further finding, stated rather than decided

**An archived element identity is a dangling index.** `ElementInstanceValue.ElementId` is an
index into a compiled process graph, not a stable identifier — the same property W3's cloud had
to respect by scoping an element cell to its definition. A deployed definition can be deleted
as soon as no instance of it is running (`api/handlers.go`, `handleDeleteProcess`, refused only
for running instances and platform processes), while the export keeps its documents. So the
index can already contain paths whose elements cannot be named.

Today's shipped feature is unaffected: ADR-0247's search selects instance-level fields and
never resolves an element. The rows this breaks are the second and the fourth — exactly the two
an archive is for. The options are to carry a stable element identity in the exported document,
to refuse a definition's deletion while exported documents reference it, or to accept that an
archived path is readable only while its definition is deployed. Which one is right depends on
the demand question in the front matter, so this record names the finding and chooses nothing.

### Consequences

- **Positive:** the export becomes a schema with a written-down contract, at the cost of one
  data file and two tests, and independently of whether an archive is ever built.
- **Positive:** nothing is built for an unasked question, and the condition that would change
  that is written down rather than left to be argued again.
- **Negative / trade-offs accepted:** the fourth question stays unanswerable. An installation
  that wants last year's graph walked cannot have it, and will be told so by the scope the
  picture states rather than by a wrong picture.
- **Negative:** the guards make a rename visible, not impossible. A reviewer still has to read
  the diff of a file whose only purpose is to be read.
- **Follow-ups / risks to watch:** the dangling element identity above; ADR-0400's counter,
  which is the cheap answer to the first row and is still `Not started`; and the fact that
  OpenSearch remains the wrong substrate for a bulk pass (no join, no traversal; a CSR build
  means scrolling the whole index on the cluster that also serves searches) if anybody reaches
  for it when the fourth row does acquire a consumer.

## Pros and cons of the options

### Option 1 — build the substrate now
- Good: the fourth row becomes answerable, and the layout question is genuinely settled (§8).
- Bad: a second store to back up, compact and restore, for a question nobody has asked; and it
  would be built on top of a payload schema that nothing guards, which is the defect this
  record found.

### Option 2 — refuse the substrate, guard the schema (chosen)
- Good: pays the debt that cannot be paid retroactively, at negligible cost, and leaves the
  expensive decision to the evidence that would justify it.
- Bad: somebody who does want the year-scale walk waits, and waits without a date.

### Option 3 — do neither
- Good: nothing to review.
- Bad: leaves a shipped feature depending on Go identifiers that any refactor may rename, with
  the failure showing up as an empty search result rather than as a red test.

## Links

- relates to [ADR-0404](0404-the-whole-graph-can-be-walked.md) §7 and §8 — this record is the
  archive substrate record §8 asks for
- relates to [ADR-0114](0114-opensearch-event-exporter.md) — the uncurated projection this
  record records rather than replaces
- relates to [ADR-0247](0247-instance-archive-search.md) — the shipped feature that reads the
  export back by name
- relates to [ADR-0115](0115-history-retention-hard-delete.md) and
  [ADR-0144](0144-per-definition-history-ttl.md) — why Atlas's own store cannot answer a
  year-scale question at all
- relates to [ADR-0400](0400-an-edge-that-was-taken-is-not-an-edge-that-was-declared.md) — the
  cheap answer to the first of the four questions
- relates to [ADR-0314](0314-portal-personal-data.md) — the erasure mechanism that dissolved
  the archive-layout conflict
