# ADR-0170: ADR numbers are assigned at merge, not on a branch

- **Status:** Accepted (amended 2026-08-24: the numbering commit also carries the regenerated What's New feed)
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

An ADR's number used to be chosen when the record was written — on a branch, by
looking at what `docs/adr/` currently held and taking the next free number. That
question has no stable answer on a branch. Two branches open at the same time both
look at the same main, both see the same highest number, and both take it. Whichever
merges second is wrong, through no fault of its own.

The repository has the scars. Numbers 0090, 0103 and 0105 were each shared by two
unrelated decisions before there was a test; the later record of each pair now lives
at 0139, 0140 and 0141. `docs/adr/adr_test.go` was written to catch exactly that, and
it does — but catching a collision is not preventing one. The cost simply moved from
"two decisions quietly share a number" to "every parallel record renumbers on every
merge". In the forty commits before this record, eight merges existed only to do
that:

```
625589d Merge main; renumber the connector-on-a-worker ADR to 0166
2aedb6e Merge origin/main; renumber to ADR-0165 and hold the coverage floor
e1bc907 Merge origin/main; renumber to ADR-0166 and adopt the new index format
cd3a162 Merge main; renumber to ADR-0167 and make the AD connector offloadable
9a66983 Merge origin/main; renumber the incident-form record to ADR-0167
8dde79c Merge origin/main; renumber the incident-form record to ADR-0168
5ef355c Merge main; renumber to ADR-0168 and keep main's restart test compiling
d46ea29 Merge origin/main; renumber the incident-form record to ADR-0169
```

One record walked 0164 → 0165 → 0166 → 0167 → 0168 → 0169 across six merges without
a word of its content changing. Each step cost a merge of main into the branch, a
conflict in the index table, and a full `go test -race ./...` run on a diff whose
substance had not moved.

Two things make the collision structural rather than accidental:

1. **The number is in the file name**, so it is claimed at the moment the file is
   created — the earliest possible point, and the one with the least information.
2. **The index in `README.md` is one ordered table**, so every new record appends a
   row to the same line. Two branches adding a record therefore conflict textually
   even when the records have nothing to do with each other.

There is a quieter problem underneath. A renumber is not just a rename: `ADR-0168`
appears in Go comments, `AGENTS.md`, the changelog, the roadmap and the UI — over
700 files in this repository cite an ADR by number. Nothing verified that a citation
still named the record it meant. The renumbers so far happened to involve records
that no code cited yet; the first one that does not will silently repoint a comment
at a different decision.

## Decision drivers

- **Parallel records must not touch a shared line.** Two branches each adding a
  decision is the normal case here, not the exception. Anything that leaves them
  editing the same file name or the same index row will keep conflicting.
- **A number, once assigned, is permanent.** That is what makes `(ADR-0168)` in a
  comment worth writing. Renumbering is the mechanism that can break it.
- **No step that depends on someone remembering.** A convention in a document is
  what we already had; it is not a mechanism.
- **The record itself must not wait.** A decision must be reviewable, mergeable and
  citable while it is in flight — a scheme that blocks writing until a number is
  available trades one queue for another.
- **Guards as tests, not as prose.** The directory already checks its own
  conventions in the mandatory test sweep; whatever replaces the convention should
  be checked the same way.

## Considered options

1. **Keep write-time numbering; automate the renumber.** A `make adr-renumber` that
   detects the duplicate, renames, fixes the heading, rewrites citations and repairs
   the index.
2. **Assign the number at merge; carry no number before that.** A record in flight
   lives at `draft-<slug>.md`; a command run on main gives it its number.
3. **Make numbers collision-free by construction** — derive them from the PR number,
   a date, or a hash of the slug.
4. **Claim the number on main before writing the record** — a small direct commit to
   main that reserves `NNNN` and its index row, after which the branch fills the body
   in.

## Decision outcome

Chosen option: **2 — the number is assigned when the record lands on main.**

A record in flight carries no number at all:

- it lives at `docs/adr/draft-<slug>.md`,
- it heads with `# ADR-DRAFT: Title` instead of `# ADR-NNNN: Title`,
- it has **no** row in the index, and
- anything that wants to cite it — prose, a Go comment, another record — writes
  `ADR-draft-<slug>` or links `draft-<slug>.md`.

Two branches each writing a record now touch two different files and no shared line
of `README.md`. There is nothing left to collide.

`make adr-number` (`go run ./docs/adr/cmd/adrnum`) closes the loop on main, where
"the next free number" finally has one answer. For every draft, in slug order so the
result never depends on directory iteration order, it:

- renames `draft-<slug>.md` to `NNNN-<slug>.md` and rewrites the heading to match,
- appends the record's row to the index — directly after the last row, because a
  Markdown table ends at the first line that is not one, and
- rewrites every `ADR-draft-<slug>` citation and `draft-<slug>.md` link in the tree
  to the number just assigned.

It refuses to run at all if `docs/adr/` is already inconsistent, and it is a no-op
when there are no drafts.

**`.github/workflows/adr-number.yml` runs it on every push to `main` and commits the
result.** This is the part that makes it a mechanism rather than a convention: no
person and no agent has to remember the step. If the push is refused — branch
protection, a revoked token — the job goes red and says to run `make adr-number`
locally, so the failure mode is visible rather than a record that quietly never gets
a number.

The guard tests in `docs/adr/adr_test.go` hold both halves:

- numbered records stay unique and gapless, and the index matches the directory
  exactly (unchanged);
- a draft is never in the index, and its heading and file name agree that it has no
  number;
- **every `ADR-NNNN` citation anywhere in the repository resolves to a record that
  exists** — the check that was missing, and the one that turns "a number is never
  reassigned" from an intention into a property; and
- no `ADR-draft-<slug>` citation is left behind pointing at a draft that no longer
  exists, which is what a rewrite missing a file type would look like.

### Consequences

- **Positive:** two records in flight cannot collide, because there is no shared
  name and no shared line. The renumbering merges disappear, and with them the full
  test runs they forced.
- **Positive:** a number is assigned exactly once and never moves, so a citation
  written today keeps its meaning. The repository-wide citation test now states that.
- **Positive:** the moment of numbering is also the moment the index row appears, so
  the two can no longer drift apart by hand.
- **Negative / trade-offs accepted:** a record has no number while it is under
  review, so a PR discusses "the draft on connector offloading" rather than a
  number. Code merged in the same PR cites `ADR-draft-<slug>` and only reads
  `ADR-NNNN` after the numbering commit lands.
- **Negative:** between a merge and the workflow's commit, main briefly holds a
  record that is not in the index. The window is one CI run, and the guard tests
  tolerate it by design.
- **Negative:** an automated commit on `main` is a new thing for this repository. It
  touches `docs/adr/`, files citing a draft, and the one file *generated from* those
  citations (see the amendment below), and it is a no-op on the overwhelming
  majority of pushes.
- **Follow-ups / risks to watch:** the citation rewrite works on an allowlist of
  file types (`docs/adr/number.go`). A citation in a type not on that list would be
  left behind — the draft-citation guard test is what catches it, and the fix is to
  add the extension.

### Amendment (2026-08-24): the numbering commit carries the regenerated feed

The rewrite reaches `CHANGELOG.md`, and the Console's What's New feed is *generated
from* that file and committed — ADR-0012 keeps the web UI buildless, so nothing
regenerates it at build or run time, and CI fails on a stale one.

Those two facts met exactly as the record's own reasoning predicts they would not:
the numbering job pushes with `GITHUB_TOKEN`, which deliberately triggers no
workflow run, so its commit is never checked by CI. A numbering run that rewrote a
changelog citation therefore left `main` one push away from red — and the push that
went red was an unrelated PR, whose author had no way to see the cause. That is
precisely the "an automated commit nothing checks" hazard this record already
guards against with its own `go build`/`go vet`/`go test` step; the feed was simply
a gate that step did not cover.

So the job now regenerates the feed after numbering, and its self-check covers what
CI covers: `gofmt` as well, and the api test that reads the feed. The regeneration
is conditional on there having been a draft to number — with a clean tree there is
nothing to commit, and regenerating unconditionally would sweep an unrelated
staleness into a commit whose message says it assigns ADR numbers.

The consequence for this record is narrow but real: the numbering commit is no
longer confined to `docs/adr/` and citing files. It also carries
`api/web/whats-new.json`, which is derived from a citing file rather than authored.

## Pros and cons of the options

### Option 1 — automate the renumber
- Good: no new concepts; the existing scheme keeps working; small change.
- Bad: treats the symptom. The index row conflict still happens on every parallel
  record, which still forces a merge of main and a full test run.
- Bad: keeps renumbering, so it keeps the risk that a citation is left pointing at
  what is now a different decision.

### Option 2 — assign at merge (chosen)
- Good: removes the collision rather than resolving it; nothing shared, nothing to
  conflict.
- Good: a number is assigned once and is permanent, which is what citations rely on.
- Bad: a record is unnumbered while in flight, and same-PR code must cite it by slug
  until the numbering commit lands.
- Bad: needs a tool and a workflow — more machinery than a convention.

### Option 3 — collision-free numbers by construction
- Good: no coordination needed at all.
- Bad: gives up the contiguous 0001..N sequence, which is how the directory reads as
  a chronology, and would strand 169 existing records in the old scheme.
- Bad: a PR number or hash is not a number a person can hold in their head, and every
  citation in the repository would carry it.

### Option 4 — claim the number on main first
- Good: numbers are stable from the first commit of the record; citations never move.
- Bad: requires a direct push to `main` before starting work. Agents work on
  designated branches and cannot do that, so the path that produces most records here
  is exactly the one it does not serve.
- Bad: serialises the start of every record behind a main push.

## Links

- supersedes the "take the next free number" rule in `AGENTS.md`, `CONTRIBUTING.md`
  and `docs/adr/README.md`
- the collisions that motivated the guard tests: ADR-0090/0139, ADR-0103/0140,
  ADR-0105/0141
- relates to [ADR-0018](0018-test-driven-development.md) — the guards are tests in
  the mandatory sweep, not documentation
