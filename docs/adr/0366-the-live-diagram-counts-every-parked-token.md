# ADR-0366: The live diagram counts every parked token, not the ones a bounded scan reached

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0337](0337-incident-floods.md) made an incident flood readable: a summary whose
size is the number of *causes*, and a bulk resolve that clears one. The operations
overview and the incidents table were moved onto it. **The live diagram was not**, and
it is the surface an operator opens after the overview has told them a process has work
parked.

The three then disagreed, and the report that surfaced it reads as a catalogue of ways
to be wrong about the same number. One process, two deployed versions, both under the
same broken mockup worker:

| Surface | What it said |
|---|---|
| Operations overview, Incidents column | **10 910** |
| Live view of v2 (current), *All instances* | nothing — no pill, no red shape |
| Live view of v2, one instance selected | 2 incidents, correctly marked |
| Live view of v1 (previous), *All instances* | **50** on each of two tasks holding ~5 452 each |

Only the first was right. The reporter's own words for the result: *"Irgendwie finde ich
das eher irreführend"* — and the worst of it is not the wrong number, it is the absent
one. A process whose every running instance was parked rendered as a healthy diagram.

### Why the overlay could not see its own incidents

The overlay collected incidents on the run loop, so the collection was bounded twice —
`maxRuntimeIncidentScan` on how far it read, `maxRuntimeIncidents` on what it returned.
Both bounds were justified: I3 makes the loop the single writer, and this runs under a
1.5-second poll, so an unbounded walk of the incident family would stop the engine four
times a minute.

The defect is not the bounds. It is **what they bounded and in which order**. The walk
went over the whole incident family in key order and attributed each entry to its
definition *after* reading it, because `model.IncidentValue` carries its process
instance and not its definition. Incidents are keyed by element instance and those keys
ascend, so the budget was spent oldest-first:

- **v1**, deployed first and flooded first, holds every one of the first 2 000 keys.
- **v2**, deployed four days later, sits entirely past them.

So the scan attributed 2 000 entries to v1, returned the first 100 of them, and stopped
— having never looked at a single one of v2's. v2's overlay was then an empty list, and
an empty list under a truncated scan is indistinguishable from a healthy process. The
browser made that last step itself: it hid the incident pill whenever the list was
empty, `incidentsTruncated` or not.

And the counts, on both versions, were read off the returned list rather than
established separately. That is where "50" comes from: the response cap admitted 100
rows, those rows fell on two tasks, and the badge on each said how many rows it had
been handed. 50 is a property of the page size, not of the process.

Reproduced as a test before anything was changed:

```
parked on the older definition: 2100
incident summary for the newer definition: total=1   groups=1
aggregate runtime for the newer definition: incidents=0  truncated=true
  element review: tokens=1  incidents=0
aggregate runtime for the older definition: incidents=100  truncated=true
  element review: tokens=2100 incidents=100
```

## Decision drivers

- **An absent number is worse than an approximate one.** A count that can silently be
  zero cannot be used to conclude anything, and "this process is fine" is exactly what
  an operator concludes from a clean diagram.
- **The surfaces must not disagree.** An operator moves from the overview's count to the
  diagram expecting to find the same flood. Two numbers for one fact means neither is
  believed.
- **Bounded turns on the single writer (I3).** Whatever replaces the scan must not put
  a walk of the incident family back on the loop.
- **No new durable state (I6).** An incident leaves state two ways — resolved, or
  dropped with the element instance it sits on — which is why `IncidentCount` is a scan
  and not a maintained counter (ADR-0061). A per-element counter would drift per
  *element*, which is worse than drifting in total.
- **A count and a page are different things.** The diagram needs a number per element;
  the resolve panel needs rows. Deriving the first from the second is what made the
  first the size of the page.

## Considered options

1. **Tell the truth about the bound in the browser.** Show the pill when
   `incidentsTruncated` is set even with an empty list, and suffix the badges with `+`.
2. **Count exactly, off the run loop, cached.** Walk the incident family through the
   same `walkIncidents` the summary uses, off the loop against a snapshot (ADR-0266),
   and hold the result briefly so a 1.5-second poll does not pay for one each time.
3. **A secondary index from incident to definition**, so a definition-scoped read is
   O(that definition's incidents) rather than O(all of them).

## Decision outcome

Chosen option: **"Count exactly, off the run loop, cached"**, with the browser's
honesty from option 1 kept as the fallback for a reading that could not be taken.

The overlay now reads what the summary reads, the way the summary reads it — one walk
of the incident family off the loop, through the same `walkIncidents` that attributes an
incident to its definition everywhere else. Three surfaces that disagreed now cannot,
because there is one attribution and one walk behind all of them.

The response separates what used to be conflated:

- `incidentTotal` — how many of this definition's tokens are parked. Exact.
- `elements[].incidents` — the same, per element. Exact.
- `incidents[]` — a bounded page of *details*, for the resolve panel. Still 100.
- `incidentsTruncated` — that the **page** is a page. Says nothing about the counts.
- `incidentCountsExact` — that the counts are the whole truth. False only when the
  reading could not be taken at all, in which case the browser shows `+` and the numbers
  are a floor.

`collectDefIncidents` and `maxRuntimeIncidentScan` are gone: nothing walks the incident
family on the loop any more.

### Why cached, and why five seconds

Measured, not assumed. At 5 000 incidents over 5 000 instances:

| Call | Cost |
|---|---|
| `/incidents/summary` (whole server) | 72 ms |
| `/incidents/summary?process=<flooded>` | 68 ms |
| `/incidents/summary?process=<quiet>` | 70 ms |
| `/stats` (nav badge, 5-second poll) | 2.4 ms |

Scoping the walk to a definition saves nothing — the family is read whole either way,
because attribution is a point lookup per instance. ~70 ms per 5 000 incidents is
affordable at the nav badge's five seconds and not at the overlay's 1.5, which is what
the cache is for. Five seconds is that same cadence: a flood pays for one walk per four
polls, however many browsers are watching.

The cache is keyed by nothing. One walk answers for every definition at once, so a
two-version process pays for one rather than two and twenty tabs pay for none of the
other nineteen. It is held off the loop under its own mutex, like `jobWaiters` and
unlike the Starmap's structural cache: the handlers already run off the loop
(ADR-0157 step 6) and the walk itself must, so routing the result back onto the loop
would buy two dispatches and nothing else. The mutex is held across the walk, which
makes it single-flight for free.

A resolve — single or bulk — drops the reading immediately. Waiting out a TTL is
tolerable for an incident *arriving* (nobody is watching for a failure they do not yet
know about) and not for one an operator has just cleared and is looking at the diagram
to confirm.

### Consequences

- **Positive:** the overview, the incidents table and the live diagram report one
  number. A healthy definition can say it is healthy, because "absent from the reading"
  now means "none" rather than "not reached". Badges carry the real count. The detail
  page is filled from the definition being drawn rather than from whatever the scan
  reached first, so the resolve panel works on a definition standing behind another's
  flood. The run loop no longer walks the incident family at all.
- **Positive, unrelated to the report:** the single-instance branch used to stop
  *counting* when its detail page filled, so an instance holding more than 100 parked
  tokens reported exactly 100 — on the one view whose counts nothing else corrects.
  It now counts every token it walks and pages only the details.
- **Negative / trade-offs accepted:** a definition under a flood costs a walk of the
  incident family every five seconds while somebody watches its diagram. That cost is
  proportional to the incident population, not to the instance population, and it lands
  off the loop. Counts can be up to five seconds stale; a resolve is exempt.
- **Negative:** the cache holds up to a detail page per definition that has incidents.
  Bounded by deployed definitions — design-time size, the same bound `defIndex` is
  content with — and unrelated to how many instances or incidents exist.
- **Follow-ups / risks to watch:**
  - The Starmap's own per-definition incident scan (`maxStatusIncidentScan`) is still
    bounded and still on the loop. It was deliberately set to the same order as the
    overlay's bound so it could not report a problem the drilldown would fail to find;
    that symmetry is now gone in the safe direction (the drilldown sees more), but
    lifting it is a derivation to move off the loop, not a cap to raise.
  - The live view's open-task badge has the same *shape* — a globally paged list
    filtered client-side — but not the same defect: the badge is drawn from the
    element's exact live-token count (ADR-0080), and the task page is used only to
    deep-link when there is exactly one. A definition whose tasks fall outside the page
    gets the inbox instead of the form, never a diagram that looks healthy.

## Pros and cons of the options

### Option 1 — tell the truth about the bound in the browser
- Good: a few lines, no new server cost, removes the false negative immediately.
- Bad: removes the false negative by replacing it with "unknown". The operator still
  cannot learn from the diagram how much is parked, and the overview's number still has
  no counterpart on the surface it links to. It is the honest version of not knowing,
  not an answer.

### Option 2 — count exactly, off the loop, cached
- Good: one attribution and one walk behind every incident surface; the run loop stops
  walking the family; "none" becomes a statement.
- Bad: a real cost per flood per five seconds, and a TTL to reason about — including
  the invalidation a resolve needs.

### Option 3 — a secondary index from incident to definition
- Good: makes a definition-scoped read proportional to that definition, so no cache and
  no TTL.
- Bad: an incident leaves state two ways, one of them silent (dropped with its element
  instance). The index would need maintaining on both, in `applyToState` (I4), and an
  index that drifts marks shapes on a diagram that are not stuck — the mistake that
  makes an operator stop believing the colour. That is a durable-state change and needs
  its own record; it is not the fix for a display that reports zero.

## Links

- builds on [ADR-0337](0337-incident-floods.md) — the cause-shaped reading this moves
  the diagram onto
- builds on [ADR-0266](0266-stats-and-incidents-off-the-loop.md) — reads that can grow
  with the population leave the run loop
- corrects [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) — which put
  incidents on the overlay so a parked token would not read as a waiting one
- relates to [ADR-0061](0061-incident-model.md) — why an incident count is a scan and not a
  maintained counter
- relates to [ADR-0080](0080-runtime-aggregate-counters.md) — the maintained per-element
  token counters the overlay's green numbers still come from
