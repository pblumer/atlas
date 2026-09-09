# ADR-0298: A record has two states — whether the decision holds, and whether it is built

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers

## Context and problem statement

On 2026-09-09 the directory held 295 numbered records: 162 `Accepted`, 131 `Proposed`,
2 `Draft`, and not one `Superseded` or `Deprecated`. A reader's fair conclusion from
those numbers is that Atlas has an unusual amount of undecided architecture.

That conclusion is wrong, and the way it is wrong is the problem. Of the 131 records
filed as `Proposed`, 113 were cited from non-test production code, and 115 of the 133
`Proposed`/`Draft` records had at least four in five of the identifiers they name
present in the tree. ADR-0061 decides the incident model and
carries its own note that the job runner routes failures into it. ADR-0086 names
`bindInputsChain`, which is in `engine/conditional.go`. ADR-0285 describes the WAL's
frame, and `wal/wal.go` documents that exact layout in its package comment. ADR-0288
decides how an agent's commits are attributed, and `.claude/hooks/commit-identity.sh`
applies it on every session. All four read `Proposed`.

This was already known. [An audit](../audits/adr-implementation-audit-2026-08-25.md)
on 2026-08-25 covered the 180 records that then existed, found 28 implemented records
filed as `Proposed`, named every one of them, and recommended reconciling them as its
Phase 0. Two weeks later none of the 28 had moved and the `Proposed` count had grown
from 43 to 131. An audit that names the fix and changes nothing is evidence about the
mechanism, not about the people: nothing in the process ever goes back to a merged
record, so nothing ever will.

Underneath the count sit three specific failures:

- **`Status` is asked two questions and can answer one.** "Does this decision hold" and
  "is it built" are different questions with different answers, and readers use the
  field for the second because it is the more useful one. A record whose code shipped
  has no way to say so.
- **The vocabulary is documented and unenforced.** `template.md` has listed four values
  since the directory existed. `go test ./docs/adr` checked that the index and the
  record agree on the status *string*, never that the string is one of the four. Two
  records drifted to `Draft` — the word this package uses for "not numbered yet" — while
  numbered and shipped.
- **Supersession is described and unused.** `README.md` says a changed decision is
  superseded rather than edited. In 295 records it has never once been written down.
  ADR-0156 was revised in substance by ADR-0164, was named as such by the August audit,
  and sat beside it as an equal `Proposed` peer for three weeks.

## Decision drivers

- **The index must be answerable without reading the source.** That is what an index is
  for, and today the only honest answer to "is this built" is `grep`.
- **The fix has to survive the next 295 records.** A one-time reconciliation restores
  the truth and loses it again at the same rate, because the mechanism that lost it is
  untouched.
- **A record stays immutable in substance.** Front matter is state, not decision;
  reconciling state is not rewriting history, and nothing below edits an argument.
- **No new ceremony at authoring time.** The merge-time numbering (ADR-0170) earned its
  keep by removing a step. A governance field that costs an author a decision each time
  they write a record will be filled in wrongly.

## Considered options

1. **Ratify the implemented records and leave one field.** Flip the 115 to `Accepted`
   and move on.
2. **Add a second field, `Implementation`, and a rule tying the two together.**
3. **Derive implementation state automatically** from citations and identifier presence,
   and render it into the index from a generator.
4. **Drop `Status` and keep only implementation state.**

## Decision outcome

Chosen option: **2 — two fields, and one rule between them.**

**`Status`** says whether the decision holds: `Proposed`, `Accepted`,
`Superseded by ADR-NNNN`, `Deprecated`. A parenthetical after the word is allowed and
common; a different word is not.

**`Implementation`** says whether it is built: `Not started`, `Partial`, `Landed`,
`Superseded`. This field carries no parenthetical, because the index is rendered from
it; nuance belongs in the record, where a reader is.

**The rule:** `Implementation: Landed` requires `Status: Accepted`. Nobody merges code
against a decision that has not been made, so a record with landed code is a record
whose decision was taken, whatever the front matter still says. `parseRecord` refuses
the pair, so the check runs in the same `go test ./...` sweep that already guards the
numbering.

`Partial` is the word that needs a definition, because without one it absorbs
everything. An extension the record itself defers is not what makes a record partial:
ADR-0154 decided an LDAP connector and listed a delta cookie among its follow-ups, and
that record is `Landed`. A piece of the decision that is missing is what makes it
partial: ADR-0027 decided that an author selects and applies an element template, and
only the store behind it exists.

Option 1 is rejected because it treats the symptom. The 115 stale records are what the
missing mechanism produced, not the defect. They accumulated over the seven weeks
between the oldest and newest of them, under a process that is otherwise unchanged, so
ratifying them and changing nothing else restores the index for exactly as long as it
takes the next records to merge.

Option 3 is rejected on the audit's own methodology: an `ADR-NNNN` in a comment is a
navigation aid, never proof. The heuristics that make the derivation possible — citation
presence, identifier presence — are exactly the ones that call ADR-0242 built because
`route` appears in `app.js`, when the record's whole subject is that five lists describe
the same routes. A generated field would be confidently wrong and, being generated,
would never be corrected. The heuristics are good enough to *find* the records worth
looking at, which is how the reconciliation used them, and not good enough to *be* the
answer.

Option 4 is rejected because a decision can legitimately be taken before it is built,
and that state — `Accepted` / `Not started` — is one an architecture record exists to
express. ADR-0175's replicated partition cells is that state today.

### Consequences

- **Positive:** the index answers both questions; a record whose code shipped can no
  longer read as a suggestion; the status vocabulary is closed, so `Draft` cannot recur;
  supersession finally has a value that a test can require to name its successor.
- **Negative / trade-offs accepted:** one more line of front matter per record, and one
  more thing to get right in review. 295 records were touched in one pass to add the
  field, which makes `git blame` on those lines point at the reconciliation rather than
  at the decision — acceptable, because the line being blamed is state, and state is
  what was wrong.
- **Follow-ups / risks to watch:** the rule catches a landed record filed as proposed;
  it cannot catch a record whose `Implementation` says `Landed` and whose code was never
  written, because no test can read a record's prose and find the code it means. That
  gap is why the reconciliation is written down with its evidence rather than asserted.
  A periodic re-audit is the only thing that closes it, and it belongs on the same
  footing as the open-question freshness check of ADR-0293.

## Pros and cons of the options

### Option 1 — ratify and keep one field
- Good: smallest possible change; no new vocabulary; the count looks right immediately.
- Bad: the field still cannot say "decided but not built"; the drift resumes the next
  time a record merges, under the same process that produced these 115.

### Option 2 — two fields and a rule
- Good: each field answers one question; the rule is checkable and runs where the other
  ADR guards already run; the vocabulary becomes closed rather than advisory.
- Bad: one more field to fill in and to review; the cross-field rule is a convention
  somebody has to learn once.

### Option 3 — derive it
- Good: no author effort; cannot go stale between merges.
- Bad: the signals are proxies. A citation is a navigation aid, and an identifier match
  is a coincidence away from a false positive. It would be wrong exactly where the
  question is hard, and generated fields are not read critically.

### Option 4 — implementation state only
- Good: one field, and it is the one people actually want.
- Bad: loses the ability to say a decision was taken before it was built, which is a
  legitimate and common state for an architecture record.

## Links

- reconciles [the ADR implementation audit of 2026-08-25](../audits/adr-implementation-audit-2026-08-25.md), whose Phase 0 this is
- applied by [the status reconciliation of 2026-09-09](../audits/adr-status-reconciliation-2026-09-09.md), which carries the evidence per record
- relates to [ADR-0170](0170-adr-numbers-assigned-at-merge.md) — the other place this directory turned a convention into a mechanism
- relates to [ADR-0293](0293-open-questions-in-records-expire.md) — the other front-matter pair with a guard behind it
