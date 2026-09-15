# ADR-DRAFT: An incident flood is read by cause and resolved in bulk

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas engine team

## Context and problem statement

Every incident surface built so far answers one question — *what is stuck here* — and
answers it one incident at a time. That is the right shape when a handful of tokens are
parked: the Operations incidents table lists them, the live view badges them on the
diagram, the replay shows them beside the element, and each is resolved (ADR-0061), its
variables corrected (ADR-0158/0098), its worker reconfigured (ADR-0160/0287) or its
repair form filled in (ADR-0169) by itself.

One process that fails in a loop breaks all of it at once. A worker whose endpoint stops
answering parks every instance that reaches the task, so a few thousand incidents are one
cause with one fix, and the surfaces behave as if they were a few thousand unrelated
problems:

- **`GET /api/v1/incidents` is the only reading available, and it returns rows.** The page
  cap is 5000 (`maxTaskListMax`), so a flood transfers several megabytes of near-identical
  JSON per refresh.
- **The Operations *Instances* overview pulls that same list on every refresh** — the
  whole thing — only to count incidents per definition for one column.
- **The incidents table renders every returned row**, each with an actions dropdown, then
  hands the result to the shared table enhancer (`api/web/table.js`), which sorts and
  filters those rows in the DOM on every keystroke and re-runs on every rebuild. Several
  thousand rows is tens of thousands of nodes; the page becomes slow to open, slow to
  filter, and slow to refresh after each resolve.
- **There is no bulk action at all.** Having fixed the one cause, an operator clears the
  flood by clicking *Resolve…* once per incident — through a modal that asks for a retry
  budget each time — or by writing a script against the single-key endpoint. Nothing in
  the product says "these 3 000 are the same failure".

The engine side is not the problem. Incidents are a column family keyed by element
instance, the listing already runs off the run loop against a snapshot (ADR-0266), and
`ResolveIncident` is one queued command. What is missing is a *reading* that is smaller
than the population and an *action* that is bigger than one row.

## Decision drivers

- **A flood must be readable in constant size.** What an operator needs first is how many
  are stuck, on which element of which process, and with what message — not the rows.
- **One cause, one action.** The fix for a flood is applied once (a worker, a credential,
  an endpoint); clearing what it parked must then be one operator gesture, not N.
- **Bounded turns on the single writer (I3).** Resolving thousands of incidents must not
  hold the loop for the whole set, and must not be a client-side loop of single calls
  either.
- **No new durable state (I6).** An incident already leaves state two ways — resolved, or
  dropped with the element instance it sits on — which is why `IncidentCount` is a scan
  and not a maintained counter (ADR-0061). A per-group counter would have the same drift
  and worse: it would be wrong per *element*, not just in total.
- **Reuse the shapes that exist.** Bulk termination (ADR-0090) already decided what a
  bulk operator action looks like in Atlas: two mutually exclusive modes, a bounded
  batch, `remaining=true` to repeat.

## Considered options

1. **Page the incident list** (a `?after=` cursor like the task inbox) and leave
   everything else alone.
2. **A grouped reading plus a bulk resolve**: one endpoint that aggregates the incident
   family by cause, one that resolves a selected set or a matching scope.
3. **A maintained per-element incident counter** in state, so the grouped reading is O(1).
4. **Client-side grouping**: keep pulling the rows, group them in the browser.

## Decision outcome

Chosen option: **"a grouped reading plus a bulk resolve"**.

### The reading: `GET /api/v1/incidents/summary`

One scan of the incident family, off the run loop against a snapshot exactly like the
listing, returning **groups rather than rows**. A group is the triple an operator can act
on as one thing:

    (process definition, BPMN element, incident type)

and carries `count`, `oldestRaisedAt`, `newestRaisedAt`, a representative `message` (the
oldest incident's — stable across polls), `messageVaries` when the group holds more than
one distinct message, and the worker / repair-form context the single incident already
carries (ADR-0160/0169), so the fix is reachable from the group and not only from a row.

Groups are ordered by `count` descending, so the flood is the first line. The group list
is capped (`maxIncidentSummaryGroups`, 500) with `groupsTruncated` and an `ungrouped`
count saying what the cap left out; `total` always counts every incident scanned, so the
number is never a lower bound in disguise. `?process=` and `?instance=` scope it the same
way they scope the listing.

Deliberately *not* a maintained counter (option 3): the drift argument that kept
`IncidentCount` a scan applies here unchanged, and the cost is a scan of a family an
operator is expected to keep near zero — the same walk `/api/v1/stats` already does every
five seconds for the nav badge.

### The action: `POST /api/v1/incidents/resolve`

Two mutually exclusive modes, deliberately the shape of ADR-0090:

- **`{keys:[…], retries}` — an explicit set.** Each key is a point lookup
  (`GetIncident`); a key with no incident is reported as `notFound` rather than failing
  the call. This is what the table's tick-boxes produce.
- **`{processDefKey?, processInstanceKey?, elementId?, elementIndex?, type?, message?,
  retries?, limit?}` — a matching scope.** The same predicate the listing filters by, so
  what the table shows and what the bulk action touches cannot disagree. At least one
  selector is required: an unscoped "resolve everything on this server" is not something
  to reach by leaving a field out. `type=job` alone is the deliberate
  everything-that-parked-a-job scope, which is what a shared outage leaves behind.

  `elementIndex` is there for the one thing the BPMN id cannot say. An instance whose
  definition is no longer deployed has no compiled process, so its incidents resolve to
  no id at all — and a scope that simply left the element out would widen from the line
  an operator clicked to the whole definition. The index is what the incident actually
  stores; it is meaningful only inside its own definition, which is why the UI hides
  "Resolve all" entirely on the rarer group that has no definition either.

Selection runs off the loop (a snapshot scan, or point reads); the resolutions for one
call are then queued in a single `s.do` turn, bounded by `limit`
(`bulkResolveBatchDefault` 500, `bulkResolveBatchMax` 5000) and answered with
`{resolved, notFound, remaining, stats}` — repeat while `remaining` is true, like every
other bulk drain. Jobs the call re-activated are driven **off** the loop afterwards
(ADR-0157 step 6), and the batch bound is what keeps that drive from becoming an
unbounded outbound-call storm in one request.

Resolving is not destructive — a retry that fails parks the token again with the new
reason — so the bulk path needs no type-the-count gate. It does need the operator to have
fixed something first, which is why the UI puts the group's worker and repair-form
actions *beside* its Resolve all, not after it.

### The listing: two more filters

`GET /api/v1/incidents` gains `?element=`, `?elementIndex=`, `?type=` and `?message=`
(substring, case-insensitive) beside the existing `?instance=` / `?process=`. They are what makes a
group's rows readable as a page — and they are the same predicate the bulk resolve's
filter mode evaluates, so *preview* and *act* are one query written twice.

### The UI

Operations → Incidents opens on the grouped reading (**By cause**), with the rows below
it scoped to the selected group and fetched with an explicit `?limit=200` rather than the
API's generous default. Each group row carries its count, its message, the worker chip,
and the three actions that belong to a whole cause: *Resolve all N…*, *Configure
worker…* / *Create worker…*, and *Show incidents*. The row table keeps every per-incident
action it had, and gains tick-boxes that feed the keys mode.

The Instances overview reads its incident column from the summary instead of the row
list: the same column, from ~200 bytes instead of megabytes.

### Consequences

- **Positive:** a flood reads in constant size and clears in one gesture per cause; the
  Instances overview stops transferring the whole incident population; the grouped view
  names the *cause*, which is the thing an operator has to fix and the thing no list of
  rows ever said out loud.
- **Negative / trade-offs accepted:** the summary is a full scan of the incident family
  per call — acceptable for a family that is meant to be near zero and already scanned
  for `/stats`, but it is O(incidents) and would need an index if incidents ever became a
  population rather than an exception. The group cap can elide the tail of a very wide
  flood (many elements, many definitions); `groupsTruncated` and `ungrouped` say so
  rather than quietly undercounting.
- **Follow-ups / risks to watch:** a bulk resolve against an unfixed cause re-parks
  everything it touched, with new incidents and new keys — the operator sees the count
  return, which is the honest outcome, but it also means the retry budget granted in bulk
  is worth keeping at 1 unless there is a reason. Cursor paging of the flat list is
  deliberately **not** added: the answer to ten thousand rows is the grouped reading, not
  a way to walk them.

## Pros and cons of the options

### Option 1 — page the list
- Good: smallest change; the cursor machinery exists (ADR-0241's `?before=`).
- Bad: answers the wrong question. Paging lets an operator *walk* ten thousand identical
  failures; it never tells them there is one cause, and it adds no way to act on it.

### Option 2 — grouped reading plus bulk resolve (chosen)
- Good: constant-size reading, one action per cause, reuses ADR-0090's bulk shape and
  ADR-0266's off-loop scan; no new durable state.
- Bad: one more endpoint pair to keep in step with the listing's filters (the shared
  predicate is what keeps that honest).

### Option 3 — maintained per-element counter
- Good: O(1) reading, no scan.
- Bad: the exact drift ADR-0061 refused for the total count, multiplied per element — an
  incident dropped with its element instance announces no resolution, so the counter
  would go wrong in the one situation (a cancelled flood) where it is read hardest.

### Option 4 — group in the browser
- Good: no server change.
- Bad: keeps the megabyte transfer and the thousands of DOM rows, which is half the
  reported problem; and it cannot group what the page cap already cut off.

## Links

- builds on [ADR-0061](0061-incident-model.md) (the incident model and its resolve)
- follows [ADR-0090](0090-bulk-terminate-instances.md) (what a bulk operator action looks like)
- reads the way [ADR-0266](0266-stats-and-incidents-off-the-loop.md) decided incidents are read
- carries the context of [ADR-0160](0160-fix-the-connector-from-the-incident.md),
  [ADR-0287](0287-create-the-worker-from-the-incident.md) and
  [ADR-0169](0169-incident-repair-forms.md) onto a whole cause
