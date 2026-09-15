# ADR-DRAFT: A number an operations view states is a counter or a walk, never the length of a page

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-draft-the-live-diagram-counts-every-parked-token](draft-the-live-diagram-counts-every-parked-token.md)
fixed one number. It did not fix the mistake behind it, and the mistake has a shape
that can be searched for:

> a list is fetched with a page cap, the console counts its rows, and the count is
> rendered as a fact about the population.

When the population fits inside the page the two agree, which is why this survives
review and testing: the defect is invisible until an installation is busy enough to
need the number. Past the cap the count is a floor. And when the page is *ordered* —
every capped list in Atlas is, by key or newest-first — the rows that fall off are not
a random sample but a contiguous slice, so a whole class of subject can be missing
together and the count reads **zero**. Zero is not a floor. Zero is a claim that
nothing is there.

So the surfaces were audited one number at a time: what does this figure assert, and
what produced it. Four kinds of answer came back.

### What the audit found

**Exact, from a maintained counter or a complete walk — no change.** The instances
overview's Running and Finished columns (per-definition counters, ADR-0083); the
Incidents view's cause table and its totals (a complete walk, ADR-0337); the
Operations nav badge (`/stats`); the live diagram and the replay (the record above).

**A floor, and says so — no change.** The Workers view's queue depth (`parked`, with
a `+`); the mock directory and the mock database (both print "showing n of m held");
the incidents row table ("the first n"); the saved task folders' badges (the counting
scan's own `truncated`); the instance search's result count ("showing first 200").
These are the pattern done right: a bound that bites is visible at the number it
bounds.

**A page counted as a population, with no sign that it was — fixed here.** Two, both
reproduced before being touched:

1. **The instance search's "stuck" flag.** The console fetched `GET /api/v1/incidents`
   — the server's whole list, capped at 5 000 rows — bucketed it by instance, and
   flagged a search hit whose key turned up in the bucket. Past that cap the bucket is
   a page. The response says so in `X-Incidents-Truncated`; the console never read the
   header. Measured on a store holding 5 200 parked instances: the bucket covered
   5 000, and **200 running instances that were each parked behind an incident
   rendered as a plain "active"** — on the surface an operator opens to debug one.

2. **The task inbox's fixed folder badges.** "All tasks", "Assigned to me",
   "Unassigned" and "Group tasks" were counted in the browser, off the rows of the
   newest-first page it had already loaded (500 by default). The saved folders beside
   them were counted by the server, over the whole open-task population, and carried a
   truncation flag. Measured: with 700 open tasks, claiming the **oldest** one for a
   user left their "Assigned to me" badge reading **0** while the task sat in their
   inbox.

**Exact, but bought with the run loop — fixed here.** The inverse trade, and worth
naming because it is the same confusion viewed from the other side: rather than
bounding a read to protect the single writer and quietly bounding the truth with it,
these two took the truth and spent the writer on it. `incidentsByJobType` walks the
whole incident family and does a point read per parked token, and it ran **inside a
run-loop turn** — once for the Workers view and once for every Starmap page load. On
a flooded engine that dispatches tens of thousands of reads onto the goroutine that
executes process instances, which is exactly what ADR-0266 removed from `/stats`.
Measured at 5 200 incidents: `/api/v1/workers` at 5.8 ms against a 2.1 ms `/stats`
baseline, all of the difference on the loop, and growing linearly with the incident
population.

## Decision drivers

- **Zero is a claim.** A number that can silently be zero cannot be used to conclude
  anything, and an operations view exists to be concluded from.
- **A count and a page are different questions.** "How many are there" and "which ones
  can you show me" have different right answers and different costs. Deriving the first
  from the second makes it the size of the second.
- **One predicate, one place.** Where the console and the server both decide
  membership, they must decide it from the same definition or they will drift.
- **Bounded turns on the single writer (I3).** Exactness is not a reason to walk a
  family on the run loop. The reading moves off the loop; it does not get shortened.
- **Say it where it is claimed.** A bound that bites belongs on the number it bounds,
  not in a note elsewhere on the page.

## Considered options

1. **Read the truncation headers and mark the affected numbers.** The console already
   receives every signal it would need.
2. **Move each number onto the thing that owns it** — a per-row count for a per-row
   question, a server-side scan for a population question — and keep option 1 as the
   fallback.
3. **Leave them and document the caps.** The numbers are right on small installations,
   which is most of them.

## Decision outcome

Chosen option: **"Move each number onto the thing that owns it"**.

- **The search's flag became part of the row.** `instanceResp.Incidents` is counted
  through that instance's own element index — bounded by the tokens the instance holds,
  paid only on the ≤200 rows a search returns, and exact. `GET /api/v1/incidents` is no
  longer fetched by the overview at all, which also removes a 5 000-row transfer from
  every search. The field is a pointer: *absent* means this response did not answer the
  question, and zero means it answered "none". The console acts on that difference.
- **The fixed folder badges became part of the folder scan, when they need to be.**
  The four predicates moved into `taskfolder.BuiltinFolders`; the console filters its
  rows with them and the server counts with the same four, from the walk that already
  counted the saved folders, so the two readings cannot say different things.

  Where the badge comes from follows what is loaded, and that is not a hedge: an
  *uncapped* page holds every open task, so counting its rows is counting the
  population — exactly, and for free. Only once the page is capped is it a
  newest-first slice, and only then does the badge come from the server's walk. That
  distinction is worth keeping because the walk is not cheap: measured at ~6 000 open
  tasks it is ~100 ms, against ~2 ms for the page itself, which is not a price to put
  on every inbox load for a number the page can already answer exactly. The badge shows
  an em dash while the walk is outstanding, because a zero is a claim and "not counted
  yet" is not that claim.
- **The two run-loop incident walks moved off the loop.** `incidentsByJobType` takes a
  read view (ADR-0266) and both callers take their tally *before* their loop turn and
  hand it in. The Starmap's `collectLocalFacts` now receives it as a parameter, which
  also makes the constraint checkable: a function that must not grow a
  population-sized read has that read passed to it.

Option 1 is kept where it belongs — as the fallback when a reading genuinely cannot be
taken — and option 3 was rejected on the strength of the two measurements: both
failures are reachable on one server with one broken worker, which is not an exotic
installation.

### Consequences

- **Positive:** a running instance parked behind an incident can no longer render as
  healthy in the search. A folder badge counts the inbox rather than the page. Nothing
  walks the incident family on the run loop any more — not the overlay, not the Workers
  view, not the Starmap.
- **Negative / trade-offs accepted:** the task-folder counting scan now runs for a
  viewer with no saved folders once their inbox outgrows a page, where before it was
  skipped for them entirely. That reverses a property an existing test asserted ("an
  empty sidebar costs nothing"); the test is rewritten to assert the opposite and to say
  why. The scan is off the loop and bounded by `maxFolderScan`, and ~100 ms at 6 000
  open tasks is the number to watch if that bound is ever raised.
- **Negative:** the Workers view and the Starmap now read two moments — a queue depth
  from their loop turn, an incident tally from just before it. For a polled view of a
  moving engine that is the same trade the overlay makes, and it is worth more than a
  consistent answer nobody can get while the loop is held.
- **Follow-ups / risks to watch:**
  - The Workers view's per-type queue depth (`ActivatableJobs`, capped at 10 000 per
    type) is still counted on the run loop. It is bounded and it says `+` when the bound
    bites, so it is honest; moving it off is the same change made again, for a number
    that is already truthful.
  - The Starmap's own per-definition incident scan (`maxStatusIncidentScan`) remains
    bounded and on the loop, carried over from the previous record.
  - The folder-counting walk costs ~100 ms at ~6 000 open tasks against ~2 ms for the
    task page, because it enriches every task it counts the way the listing does.
    Counting needs far less than listing; making the walk carry only what its matchers
    read is the obvious next saving, and is why the console still avoids the walk
    whenever its own page can answer.
  - The mock database's "n with no answer" counts failures among the statements on the
    page, beside a `held` that is the real total. It is a development tool and the two
    numbers sit side by side, so it is left as it is — noted so the next reader does not
    have to rediscover it.

## Pros and cons of the options

### Option 1 — read the headers, mark the numbers
- Good: small, local to the console, and removes every false *zero* by turning it into
  a visible "at least".
- Bad: it makes the console honest about not knowing, and stops there. "Assigned to me:
  0+" is not an answer to how many tasks are assigned to me, and the operator has no
  way to get one.

### Option 2 — move the number onto what owns it
- Good: the number becomes exact and cheap at the same time, because a per-row question
  asked per row is bounded by the row. It also deletes work: a 5 000-row transfer per
  search, and a predicate implemented twice.
- Bad: it touches a service interface (`CountFunc` → `Tally`) and reverses a documented
  cost property of the folder scan.

### Option 3 — document the caps
- Good: nothing to build, nothing to regress.
- Bad: the two failures were reproduced on a single server with a single broken worker.
  A defect that appears exactly when the number starts to matter is not one to document.

## Links

- follows [ADR-draft-the-live-diagram-counts-every-parked-token](draft-the-live-diagram-counts-every-parked-token.md)
  — the same mistake, found on one surface and generalized here
- builds on [ADR-0266](0266-stats-and-incidents-off-the-loop.md) — reads that grow with
  the population leave the run loop
- builds on [ADR-0337](0337-incident-floods.md) — the cause-shaped reading the overview
  and the incidents table already use
- relates to [ADR-0268](0268-task-folders-are-saved-filters.md) — the saved folders whose counting scan
  the fixed ones now share
- relates to [ADR-0083](0083-o1-instance-summary.md) — the maintained counters the
  overview's Running and Finished columns come from, which the audit confirmed exact
