# ADR-DRAFT: A capped listing answers with a page, not with an array

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0365](0365-a-number-is-a-counter-or-a-walk.md) fixed five numbers that were the
length of a page presented as the size of a population, and left three text rules in
`api/pagecount_internal_test.go` to catch the sixth. What it did not do is take the
wrong number out of reach.

Every capped list endpoint answered with a bare JSON array:

```
GET /api/v1/tasks    →  [ {...}, {...}, … ]        (at most 500 rows)
                        X-Tasks-Truncated: true
                        X-Tasks-Next-Cursor: 281474976710661
```

Two properties of that shape produced all five defects:

1. **`response.length` exists, is a number, and is wrong.** It reads as "how many
   tasks are there" and is in fact "how many fitted". There is no moment at which a
   caller is told it asked the wrong object.
2. **What the response knows about itself is somewhere else.** The cap and the cursor
   were in headers, which `fetch` hands back in a separate object. Atlas's console
   kept two wrappers for exactly this reason — `api()` returning the body and
   `apiRaw()` returning body and headers — so the cheap call was the one that dropped
   the signal.

The second point is not a theory about how mistakes happen. `mcp/` held two builders
and a client method (`getWithHeaders`) whose entire job was to fold a bare array and
its truncation header back into one object, because the MCP tools could not hand an
agent a list that did not say whether it was complete. A consumer rebuilding the
envelope on its own side is the API saying the envelope belongs in the response.

The trigger was concrete: on a store with 5 200 parked instances, 200 running
instances parked behind incidents rendered as plainly active, because the console
bucketed a 5 000-row page and asked it a population question. The header said
`X-Incidents-Truncated: true` throughout.

## Decision drivers

- **A wrong answer should be unavailable, not discouraged.** A guard that fires in CI
  is worth less than a shape in which the mistake does not typecheck. `.length` on an
  object is `undefined`; `.map` on it throws. Both are loud where a short count is
  silent.
- **One response, one truth.** A caller that has the rows should not need a second
  object to learn whether it has all of them.
- **Say where the number came from.** "How many are there" is sometimes answered by a
  maintained counter and sometimes only by "what I saw". A single `total` that does not
  distinguish the two would be the original defect with a new field name.
- **Do not buy exactness with the run loop (I3).** Where a count would cost a walk the
  request was not already making, the honest answer is a floor that says so.
- **Breaking on purpose, once.** The alternative to a break is two shapes for the same
  endpoint, which is a permanent tax on every reader.

## Considered options

1. **Keep the arrays and the headers; rely on ADR-0365's text rules.** No client
   changes, no break.
2. **Keep the arrays and add the total as another header.** Answers the counting
   question without changing the body's type.
3. **Answer with an envelope: `{items, total, totalExact, truncated, nextCursor}`.**
   Breaking for every client.
4. **Version the endpoints** — `/api/v2/tasks` alongside the existing ones.

## Decision outcome

Chosen option: **"Answer with an envelope"**, applied to the six capped listings:
`/api/v1/tasks` (global, per-instance and folder-filtered), `/api/v1/instances`,
`/api/v1/instances/search`, `/api/v1/incidents`, `/api/v1/approvals` and
`/api/v1/audit`.

`api/httpapi.Page[T]` is the type, and its constructors make the caller say where the
total came from rather than letting it default to the flattering answer:

- `Rows(items, total, truncated)` — something already knows: a maintained counter
  (ADR-0080/0083) or a walk this request was making anyway. Exact even when the cap bit.
- `PageOf(items, truncated)` — the page is all there is to go on. `totalExact` is
  `!truncated`, which is the step every one of the defects skipped: an **un-truncated**
  page holds the whole population, so counting its rows is counting the thing, free and
  exact. Only a truncated page is a slice.
- `FloorRows(items, seen, truncated)` — a floor either way, for a listing that filtered
  rows out after reading them and cannot even count what it returned as the population.

`totalExact: false` therefore means: this is a floor, this endpoint cannot cheaply do
better, and the endpoint that can is named in the listing's API description. The
instances listing uses all three constructors within one handler: scoped to a
definition, or to the engine's live half, a counter answers exactly; spanning both
halves of the whole engine, or filtered to one element, nothing does.

**Option 1** was rejected on the evidence that produced ADR-0365: five instances of one
mistake, none caught in review. The rules stay — they are cheap and they fire at the
moment somebody writes the next reader — but a rule over the text of the console cannot
follow data flow, and the one defect that mattered most (the inbox's folder badges) was
two assignments away from its fetch and invisible to all three.

**Option 2** was rejected because it leaves `.length` in place. The count would be
correct in a header nobody is obliged to read, beside an array whose length is wrong
and free.

**Option 4** was rejected as the expensive version of option 1: two shapes to maintain,
two sets of tests, and the wrong one still reachable and still the shorter URL. Atlas
has one console, one MCP server and a small number of API consumers; a single break
with a changelog entry is cheaper than a permanent fork.

### What the break cost, measured

- **`api/web/`** — the console: the task inbox and its folders, the incidents table,
  the operations instance search, the live panel's listing and search box, the
  approvals page and the admin audit view. `apiRaw`'s reason for existing shrank to one
  header (`X-Archive-State`, which says the rows describe instances history retention
  has deleted, and is genuinely not about pagination); every pagination read now goes
  through `api()`.
- **`mcp/`** — an envelope-building function, an inline one, the two types they filled
  and the client method that existed only to feed them (`getWithHeaders`) are all gone.
  The tools return `asText(c.get(path))`.
- **Hosted pages** (`api/web/*.html`) — five unwrap `.items`.
- **Cost of the totals themselves**: 2.2 ms for the instances listing's `total` against
  21.7 ms for the page it accompanies — about +10%, and it is point reads of maintained
  counters, not a walk. Nothing exact was bought with the run loop.

### The fourth guard

`api/pagecount_internal_test.go` gains `TestACappedListingAnswersWithAPage`, and it is
the first rule in that file that proves something. It starts a server, asks each capped
listing, and refuses a body that is a bare array or that lacks `items`, `total`,
`totalExact` or `truncated`. It runs against a live server rather than the source, so a
listing added after the file was written is covered by adding one row to a table —
which is the failure mode of the three text rules, that they quietly stop covering the
code they name.

ADR-0365's rule 2 ("a raw read of a capped listing keeps its headers") is retired with
the headers it was about, and replaced by a rule against the shape that would replace
it: using a listing's response as if it were its rows (`(await api(…)).map`,
`.length`, `.filter`). That line now throws in production instead of lying, which is an
improvement and still a worse place to find out than in CI.

That replacement rule also reads the hosted pages, which ADR-0365's rules did not,
and the reason is not theoretical. Four of them — both order-to-cash apps and both
travel-booking wizards — were still filtering the instance listing as a bare array
after the server had been converted, and the first one was found by a browser test
failing, not by a guard. They are the callers furthest from this change and the ones an
outside customer opens, which is the worst combination for a rule that does not look at
them.

The third rule's path pattern is now built from the same table as the other two, rather
than written out beside it, and that alone found two more. `/api/v1/audit` had been in
the table while the pattern still named three endpoints, so the console's audit view
read a capped listing with no rule watching it — and read it as a bare array.
`/api/v1/instances/search` was newly in the table, and the rule's first run reported the
live panel's search box: it labelled its picker `Search results (200)` off the row
count, which is the original defect in its purest form, on the control an operator uses
to find one instance among many. Both are fixed here. The lesson is smaller than the
rules themselves: a guard with its own copy of the list it guards will drift from it,
and the drift is silent in the direction that matters.

### Consequences

- **Positive:** the wrong number is out of reach. A caller that wants a count reads
  `total` and is told whether it is exact; a caller that wants rows reads `items`. A
  client written against the old shape fails immediately and visibly rather than
  reporting a page size as a population.
- **Positive:** `items` is never `null`. An empty listing is `[]`, so a caller that
  iterates it does not need a guard for the empty engine.
- **Negative / trade-offs accepted:** this is a breaking API change for any client
  outside this repository. There is no compatibility mode and no versioned alias; the
  changelog says so under **Changed**. Anyone reading these six endpoints has to touch
  their code.
- **Negative:** `page.items.length` is still reachable and still wrong on a truncated
  page. The envelope makes the mistake explicit rather than impossible — somebody has
  to type `.items` first — and the display-site rule catches it where it matters
  (`fmtCount(x.items.length)` fails, because the guard compares the bare name).
- **Negative:** three constructors is one more decision per call site, and picking the
  flattering one is silent. It happened while this change was being written: the
  instances listing tracked its own exactness correctly and then handed every page to
  `Rows`, so the two floors it computes — the unscoped both-halves total and the element
  filter — went out claiming to be counts. Nothing failed; a floor reported as exact
  reads exactly like a truth. `TestTheInstanceListingSaysWhereItsTotalCameFrom` is what
  now holds each of that handler's six paths to the total it can actually justify, and
  it is the shape of test any listing with more than one total needs.
- **Follow-ups / risks to watch:**
  - The listings that are **not** capped still answer with bare arrays:
    `/api/v1/processes`, `/api/v1/users`, and the per-instance sub-resources
    (`…/instances/{key}/jobs`, `…/variables`, `…/data-objects`). That inconsistency is
    deliberate — their length *is* their population — but it means the shape of a
    response no longer follows from it being a list, and a listing that later grows a
    cap has to change shape at the same time. The guard's table is where that is
    recorded.
  - `/api/v1/approvals` reports a floor when its scan budget bites, and nothing counts
    open approvals today. If an operator starts reading that total as a population, the
    next step is a counting endpoint beside it, not a more expensive listing.
    (`/api/v1/audit` is the opposite case and worth naming as the pattern: its events
    are already in memory, so the `break` at the limit became a `matched++` past it, and
    the total is exact for the price of not stopping a loop.)
  - The instances listing's unscoped both-halves total is a floor only because the
    completed family has no engine-wide counter. Adding one would make it exact; it is
    not obviously worth a maintained counter for a query the console does not issue.

## Pros and cons of the options

### Option 1 — keep arrays and headers, rely on the text rules
- Good: no break, no client work, and the rules already exist.
- Good: headers are the HTTP-native place for metadata about a response.
- Bad: `.length` stays correct-looking and wrong, and it is the shortest thing to write.
- Bad: the rules cannot follow data flow, which is how the worst of the five defects got
  through.
- Bad: consumers keep rebuilding the envelope themselves, as `mcp/` did.

### Option 2 — arrays plus a total header
- Good: answers the counting question without touching the body's type.
- Bad: the wrong number stays free and the right one stays optional.
- Bad: two objects still have to be carried together by every caller.

### Option 3 — the envelope (chosen)
- Good: `.length` on the response is `undefined`; the mistake fails loudly.
- Good: rows, total, exactness and cursor travel together and cannot be separated.
- Good: the constructors make "where did this total come from" a decision at the call
  site rather than an assumption.
- Bad: breaking, with no transition period.
- Bad: one more level of nesting for callers that only ever wanted the rows.

### Option 4 — versioned endpoints
- Good: no break; clients migrate when they choose.
- Bad: two shapes, two implementations and two test suites for the same question.
- Bad: the wrong shape remains reachable indefinitely, which is the problem this record
  is about.

## Links

- extends [ADR-0365](0365-a-number-is-a-counter-or-a-walk.md) — the numbers; this record
  is the shape they are carried in
- relates to [ADR-0366](0366-the-live-diagram-counts-every-parked-token.md) — the defect
  that started the audit
- relates to [ADR-0266](0266-stats-and-incidents-off-the-loop.md) — why an exact total
  may not be bought with the single writer
- relates to [ADR-0080](0080-runtime-aggregate-counters.md) / [ADR-0083](0083-o1-instance-summary.md)
  — the counters that make `totalExact: true` affordable
