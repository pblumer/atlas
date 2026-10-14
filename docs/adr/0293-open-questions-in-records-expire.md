# ADR-0293: A record that rests on an open question says so, and the question expires

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers

## Context and problem statement

A decision record states what was decided and why. Some records are right only
*while* something nobody could answer stays unanswered, and nothing in the format
said so.

[ADR-0292](0292-mim-import-worksheet.md) is the case that prompted this. It decides
that the importer renders a MIMWAL table's cells **by position** — `column="1"` says
where a cell sat, never what it means — because the meaning of those columns is
established by no reference anyone could find, and the one real workflow checked
against MIMWAL's own editor labels contradicts them. The decision follows from the
gap. The day somebody reads MIMWAL's source and settles the grid semantics, the
right answer changes: naming the columns becomes an additive improvement rather than
an unverified claim in machine-readable markup.

Written like any other record, that gap is invisible a year later. The record reads
as a settled decision, because the format offers no way to distinguish "we decided
this" from "we decided this for now, on something we do not know". The person who
knew it was provisional is the author, and the author is exactly who will not be
reading it in two years.

This is the same failure [ADR-0289](0289-worker-type-setup-in-the-panel.md) addressed
for the Worker Type setup steps: text that goes on looking authoritative after it has
silently stopped being true, where the reader least able to tell is the one relying
on it. That record's answer was to have each entry state when it was last checked
against the real thing, and to fail the test once a statement has stood for a year.

## Decision drivers

- A provisional decision must be legible as provisional, by a reader who was not there.
- The mechanism has to fire **without anybody touching the file** — the file not being
  touched is the condition it exists to catch.
- It must not be defeatable by writing a number nobody will look at.
- One convention in this repository, not two: the Worker Type steps already solved
  the same problem, and a second, differently-shaped answer to it costs more to
  learn than it gains.
- It must cost nothing for the ordinary record, which rests on no such gap.

## Considered options

1. **Nothing.** Rely on authors to revisit records they know are provisional.
2. **A forward-looking `Recheck by: YYYY-MM`** — the record sets its own deadline.
3. **A backward-looking `Open question` + `Question checked: YYYY-MM` pair**, with a
   uniform maximum age enforced by a test — the shape ADR-0289 uses.
4. **Expire every record**, not only the ones resting on an open question.

## Decision outcome

Chosen option: **3**.

A record whose reasoning rests on something unresolved carries two lines in its front
matter: what the question is, and the month somebody last looked at it. They come as
a pair — `parseRecord` reports one without the other — and a guard test fails once
the month is more than twelve months old, or is dated in the future, which is the one
typo that would make the check quieter instead of louder.

**Why backward-looking rather than a self-set deadline (option 2).** A `checked`
month is a statement of *fact*: somebody looked, in that month. A `recheck by` month
is a *promise*, and the author sets it freely, so `2099-01` makes the mechanism a
no-op that still looks enforced — the exact failure being fixed. Closing that would
mean a second rule capping how far out a deadline may be, which is a rule to explain
where the backward-looking form needs none: the horizon is one constant in one place.

**Why not every record (option 4).** Most decisions do not decay. An expiry on all of
them would make the check routine, and a routine check is bumped rather than acted
on — which is how a freshness date becomes a lie. Only a record that names what it
does not know earns the maintenance.

**Twelve months**, matching ADR-0289. Long enough that re-reading is not busywork,
short enough that a question cannot outlive everyone who knew it was open.

### Consequences

- **Positive:** a provisional decision is legible as provisional, and the question is
  in the record rather than in the author's head.
- **Positive:** when the check fires it names the record *and* the question, so the
  reader knows what to go and look at without reading the whole record first.
- **Negative / trade-offs accepted:** the guard reads the wall clock, which the
  testing conventions in `AGENTS.md` otherwise forbid, and it will one day turn CI red
  on a change that has nothing to do with the record. That is the same cost ADR-0289
  accepted and for the same reason: a check that can only fail when somebody edits the
  file would never fire.
- **Negative:** the horizon is uniform. A question that genuinely cannot be looked at
  for three years still comes due yearly, and the honest response is to re-date it
  saying that nothing has changed — a minute's work, and a record of having looked.
- **Follow-ups / risks to watch:** the pair is declared per record by its author, so a
  record that rests on a gap and does not say so is not caught by anything. The 291
  records that predate this convention were **not** swept for open questions; doing
  that is a judgement call per record and is deliberately left as separate work.
- **The obvious way this fails:** somebody bumps the date without looking. Nothing can
  prevent that, and the test's message says so in as many words. What the convention
  buys is that bumping it is now a deliberate act with a diff, rather than the absence
  of one.

## Pros and cons of the options

### Option 1 — nothing
- Good: no mechanism, no maintenance.
- Bad: the only thing keeping the gap visible is the memory of the person who wrote it.

### Option 2 — a self-set deadline
- Good: a question that genuinely needs a long horizon can say so.
- Bad: the deadline is a promise the author sets freely, so it needs a second rule to
  stop it being set past the heat death of the repository.

### Option 3 — a checked date with a uniform maximum age (chosen)
- Good: a statement of fact, one horizon in one place, and the same shape a reader of
  this repository has already met in the Worker Type setup steps.
- Bad: reads the wall clock; uniform horizon.

### Option 4 — expire every record
- Good: nothing to decide per record.
- Bad: makes the check routine, and a routine check gets bumped rather than acted on.

## Links

- follows [ADR-0289](0289-worker-type-setup-in-the-panel.md) — the same failure, the
  same shape of answer, and the same accepted cost of a clock-reading test
- applies to [ADR-0292](0292-mim-import-worksheet.md), the first record to carry the pair
- relates to [ADR-0170](0170-adr-numbers-assigned-at-merge.md) — the other convention
  this directory guards with its own tests
