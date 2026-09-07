# ADR-0261: The diagram is the query — filtering instances by the element they sit on

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

The Operations live view puts two things side by side: a diagram badged with where
tokens are, and a panel listing instances. On a production process they do not
connect. A user task reads **25 205 here now**; the panel beside it lists the
newest fifty of fifty thousand instances. The operator's next question after "how
many are waiting here" is invariably "**which** ones", and the only way to ask it
was the variable search — which needs a value they would have to know already.

So the number on the shape is a dead end. An operator who can see that a thousand
identities are stuck before *Eintritt verbuchen* cannot get from that to the
thousand instances, and the shape they are pointing at is the most precise
statement of what they want.

Making the diagram the query is a small change to the click. Answering it is not.
The obvious implementation — walk this version's live instances and keep the ones
holding a token on that element — is a scan whose cost grows with the instance
population, and the live view re-reads its listing **every 1.5 seconds** for as
long as it is open. ADR-0080 removed exactly that shape from the runtime overlay
by maintaining counters; a filter that reintroduced it would undo that work at the
one moment the population is large enough for the filter to matter.

The engine already records where every token is: an `ElementInstanceValue` carries
its process instance, its definition and its element index. What it does not keep
is the direction this question reads — element → instances. `elByProc` gives
instance → element instances; there is no index the other way.

## Decision drivers

- **The answer must cost the answer.** A page of instances on an element must cost
  that page, not a walk of the version, because it is re-read on a 1.5-second poll
  and because the versions where operators need it are the ones holding hundreds of
  thousands of instances.
- **Derived state must be event-driven.** Anything added has to be written from the
  event that causes it, so replay rebuilds it identically (I4/I6) and `applyToState`
  stays the one place state changes.
- **No behaviour may depend on the upgrade.** A store written before the index
  exists holds live tokens the index never saw. A missing index does not read low
  here — it reads *empty*, and "no instance is sitting on this task" is an answer an
  operator acts on.
- **The filter must be legible.** A short list that is short because it is filtered
  must not read as a process with nothing in it.
- **One interaction per element.** A click on a shape can mean one thing. Whatever
  it means now, the meaning it had before has to keep an affordance.

## Considered options

1. **Filter in the browser.** Ask for the same page and keep the rows whose element
   matches.
2. **Walk the version on the server.** Add `?element=` to the instances list and,
   for each live instance of the definition, test whether it holds a token there.
3. **A `piByEl` column family (chosen).** Index every live element instance under
   the `(definition, element)` its token sits on; answer the filter as a prefix scan.
4. **Answer it from the task inbox.** For user tasks, the open jobs already name
   their element, so the filter could read those.

## Decision outcome

Chosen: **option 3**, a `cfInstanceByElement` column family keyed
`<procDefKey>:<elementId>:<piKey>:<elKey>` with no value, plus `?element=` on
`GET /api/v1/instances` and a click on the diagram that sets it.

- **Written from `applyToState`**, in `PutElementInstance` and dropped in
  `DeleteElementInstance` — the same two calls that already move the ADR-0080
  live-token counter. That makes the index and the badge on the shape two readings
  of one fact, and an engine test asserts they agree. Both come off the event, so
  replay rebuilds the index (I4/I6).
- **The instance key before the element-instance key**, so the range walked
  backwards yields instances newest first — the order every other instance listing
  uses — and so a loop or multi-instance activity's several tokens on one element
  are adjacent and collapse to one row. The question is which *instances* are there.
- **Migration drops the old entry.** `MigrateInstance` re-puts a live element
  instance under the target version's `(definition, element)`; the record then no
  longer says where it was, so the source entry is named and deleted before the
  re-put (ADR-0162).
- **Seeded once on open** (`backfillElementTokenIndexIfNeeded`, marker
  `element_token_index_v1`), the same one-time migration shape as the ADR-0080/0083
  counters and the ADR-0238 child index.
- **Live instances only, by construction.** A token exists only in a running
  instance, so a filtered listing has no finished half — the view does not ask for
  one, and `?element=&state=finished` is empty rather than filtered.
- **`?element=` requires `?process=`.** A BPMN element id means nothing without the
  version that defines it, and the index is keyed by that pair. An id the version
  does not define is a 400, not an empty list: an empty list reads as a fact about
  the process.

In the view, clicking a **flow node** filters the panel to the instances sitting on
it (clicking it again clears); clicking anything that is **not** one — the canvas
around the shapes, a collaboration's pool or lane, a sequence flow — puts the whole
version back. The panel carries a chip naming the element with an × , the diagram
outlines the element in the accent colour, and an empty result says "no instance is
sitting on *X* right now" rather than the listing's "no instances yet". Under a
filter the bulk-terminate "All active" scope is withdrawn: it means every running
instance of the version, which is not what the panel is showing.

That click was the decision inspection's (ADR-0066). It moves to the ⚖ badge, which
was already the discoverable affordance for it, appears on exactly the tasks that
have a decision to inspect, and does not have to be guessed at. The bare click was
also useless in the state the filter is most used in — with "All instances"
selected, inspecting a decision has no instance to inspect.

### Consequences

- **Positive:** "How many are waiting here" and "which ones" become one gesture, and
  the diagram stops being a dead end at the exact scale it is most informative.
- **Positive:** The answer costs the answer. An element five instances sit on costs
  five reads whether the version holds five instances or five hundred thousand — so
  the filter survives the 1.5-second poll it lives under.
- **Positive:** The index makes "which instances are on this element?" a first-class
  question. Nothing else asks it yet; the migration preview and any future
  element-scoped bulk operation would.
- **Negative / trade-offs accepted:** One more derived family to keep consistent, and
  one more entry written per element-instance activation and deletion — a valueless
  key beside the `elByProc` entry the same call already writes.
- **Negative / trade-offs accepted:** Clicking a business rule task no longer opens
  its decision. The badge does, and the panel's "← Variables" still leaves it.
- **Follow-ups / risks to watch:** The index sizes with live element instances, so it
  grows and shrinks with the token population rather than with history — the same
  profile as `cfElementInstance` itself. The one-time backfill scans that family
  once at open, like its four predecessors.

## Pros and cons of the options

### Option 1 — filter in the browser
- Good: no server change at all.
- Bad: **wrong**, not merely slow. The panel holds one page of a version that may
  have hundreds of thousands of instances; the instances on an element are mostly
  not in it, so the filter would report a subset of a page as the answer — with no
  way to tell that from the truth.

### Option 2 — walk the version on the server
- Good: no new persistent state, and it reuses the existing by-definition index.
- Bad: O(live instances of the version) per request, on a request the view repeats
  every 1.5 seconds. Worst exactly when the filter is most wanted: a selective
  element on a busy version reads every instance to find the few. Off the run loop
  (ADR-0239) it would not stop the engine, but it would burn a core per open tab.

### Option 3 — a `piByEl` column family
- Good: the read is a prefix scan of the matching instances, newest first, cursor
  paged like every other instance listing.
- Good: symmetric with the ADR-0080 live-token counter — same two call sites, so the
  number on the shape and the rows in the panel cannot drift apart.
- Bad: a new derived family, a new backfill, and a migration path to keep right.

### Option 4 — answer it from the task inbox
- Good: no new state; open jobs already carry their element.
- Bad: only user tasks have jobs. A token waiting on a timer, a message catch, a
  gateway or an incident-parked service task has none — and those are a large part
  of what an operator points at. It would answer the question for some shapes and
  silently not for others.

## Links

- builds on ADR-0080 (maintained per-element counters) — same two call sites, same rule
- follows ADR-0238 (child instance index) for the shape of a derived reverse index
- relates to ADR-0239 (off-loop queries) — the listing already runs off the run loop
- relates to ADR-0241 (finding an instance) and ADR-0244 (searchable variables) — the
  other two ways into a version's instances
- changes an interaction from ADR-0066 (decision inspection): the click moves to the ⚖ badge
- relates to ADR-0162 (instance migration), which rebinds the indexed tokens
