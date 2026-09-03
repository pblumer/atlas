# ADR-0234: Google Sheets and Drive as inbound event sources — a polled row watch and a polled folder watch

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0235](0235-google-sheets-worker.md) gives Atlas the
*outbound* half: a service task reads a range, appends a row, creates or deletes a
spreadsheet. Every one of those eight operations begins inside a process instance that
already exists.

The direction people ask for next is the other one, and they ask for it in two shapes
that turn out to be the same shape:

- **A row appears in a spreadsheet and a process should start.** This is almost always a
  Google Form: the form writes each response as a row, and the triage, the approval,
  the onboarding that follows it is the process. A form response sheet is the cheapest
  intake channel an organization has, and today Atlas cannot see one.
- **A file lands in a Drive folder and a process should start.** An invoice is dropped
  in `Eingang`, a scan appears in `Posteingang`, a contract is uploaded by a partner —
  the folder *is* the queue, and it is a queue people already use.

Atlas has the destination. Message correlation
([ADR-0020](0020-message-correlation.md)) takes one published message and both starts
every matching message-start process ([ADR-0035](0035-message-start-events.md)) and
wakes every waiting subscription. It also has the inbound *shape*:
[ADR-0075](0075-clio-inbound-event-bridge.md) built a bridge, and
[ADR-0214](0214-jira-inbound-issue-watch.md) generalized it to a second source behind
an `inboundSource` interface, taking with it the two hard parts — the durable
per-source high-water mark that makes an at-least-once source safe, and the
`MarkKey` escape hatch for a source whose order is a query's rather than a log's.

So the question this record answers is not "how does an inbound event reach a
process" — that is settled. It is **what a Google event's sequence is**, because the
bridge's entire correctness rests on one, and the two shapes above answer it
differently.

### The two answers

**A spreadsheet row has a real sequence: its row number.** Rows are appended after the
last one, the number rises, and it never rises twice for the same row. That is exactly
the property a clio event id has, and it means a row watch can use the *scalar*
watch-level mark — the simple case — with no per-item mark at all.

It has one hole, and it must be named rather than discovered. **Deleting a row
renumbers the tail.** Delete row 40 of a 100-row sheet and every row below moves up
one; the next append lands on 100, which the mark says is already delivered, and that
response is silently lost. There is no repair inside the mechanism: a row carries no
identity but its position. What makes this acceptable is that the sheets people watch
are the append-only ones — a form response sheet is written by Google and edited by
nobody — and what makes it honest is saying so in the handbook next to the watch, and
refusing to pretend a general spreadsheet is a queue.

**A Drive file has no sequence at all.** `files.list` is a query, its order is an
index's, and a file indexed late would be permanently lost behind a scalar mark. This
is precisely the wall ADR-0214 hit with Jira, and the fix transfers whole: the mark is
scoped **per file id** (`MarkKey`), the sequence is the file's own `createdTime` or
`modifiedTime` in milliseconds, and the cursor is held deliberately behind the newest
file seen so a late-indexed file is still inside the next window. Re-reading then costs
one skipped publish rather than one duplicated process.

## Decision drivers

- **Reuse the bridge, do not fork it.** A Google event funnels into `correlateMessage`
  through `PublishInbound` like every other inbound event.
- **Invariants.** The Google call is network I/O: off the processor goroutine (I3),
  never inside `applyToState` (I4). The publish is durable before it is acted on (I2).
- **Correctness under at-least-once.** A re-read must not double-start a process — and
  must not silently drop an event either, which is the half that is easy to get wrong.
- **No new credential surface.** The watch names a Worker; the credential stays in
  the vault.
- **The loop guard applies unchanged.** A process started by a row that writes a row is
  a feedback loop, and [ADR-0225](0225-inbound-watch-budget.md)'s per-hour ceiling
  is what stops it. A Google watch is a watch.

## Considered options

1. **Two polled watches on the existing bridge** — a sheet row watch and a Drive folder
   watch, sharing one Worker Type and one credential.
2. **Google Drive push notifications** (`changes.watch`), which POST to a public HTTPS
   endpoint Atlas would have to expose.
3. **A Google Apps Script** on the sheet, posting to `POST /api/v1/messages`.
4. **Drive's `changes.list` change feed** as the single source for both shapes.

## Decision outcome

Chosen option: **1 — two polled watches on the existing bridge**, distinguished by which
target the subscription names, with the row watch on the scalar mark and the folder
watch on a per-file mark.

A subscription on a `googlesheets` Worker names **exactly one** of:

- `spreadsheetId` (with an optional `watchRange` and `headerRow`) — a **row watch**.
  The cursor is the number of rows seen; each row beyond it is one event, sequenced by
  its absolute row number. With `headerRow`, the first row's cells become the column
  names and each event's fields carry the row by column name, so a correlation key can
  say `Antragsnummer` rather than index into a list.
- `folderId` (with an optional `cursorField` of `created` or `modified`) — a **file
  watch**. Each file is one event, marked by its file id and sequenced by that
  timestamp in milliseconds.

Naming both, or neither, is refused when the watch is created — the same place a clio
watch is refused a `jql`. Which set of fields applies is decided by the Worker's type
plus the target, never by a discriminator column, so no stored record needs a
migration.

### Why polling, again

ADR-0214 chose polling over a webhook for reachability: an Atlas behind a firewall
cannot receive one. Drive's push channels make that worse, not better. A channel is
addressed to a public HTTPS endpoint whose domain must be verified with Google, it
**expires** (a week at most) so something must re-register it forever, and its
notification carries no payload — it says "something in the resource you watch changed"
and the receiver must then call `changes.list` anyway. So the push option is the polled
option, plus a public endpoint, plus a renewal daemon, plus a domain verification. It
buys latency, and a watch whose cadence an operator already sets in seconds is not
short of latency.

The Apps Script option is genuinely good and is not being argued against — a sheet
owner who can write one gets sub-second delivery with no Atlas change at all. It is
rejected as *the* answer because it puts the integration in a script attached to one
spreadsheet, owned by whoever created it, invisible to the Console, and it needs an
Atlas API token to exist inside a Google document. What is built here is what an
operator can configure, see, disable and rate-limit from the same place they configure
every other watch.

### Why not `changes.list` for both

Drive's change feed has the one thing this record spends most of its length working
around: an opaque `pageToken` that is a genuine monotonic cursor, with no lag window
and no per-item mark needed. It is the better mechanism, and it is still not chosen,
for two reasons. It is scoped to a whole drive rather than a folder, so a folder watch
would read every change in the account and filter client-side — on a shared drive that
is most of the traffic being paid for and thrown away. And it does not answer the row
question at all: a row appended to a spreadsheet is one `modifiedTime` bump on the
file, with no way to learn *which* row, so the row watch would have to read the values
anyway. Adopting it would mean two mechanisms instead of one, and the second would be
this one. It stays a follow-up for the folder half, if a large-drive installation ever
makes the filtering cost real.

### Consequences

- **Positive:** the two intake channels people actually have — a form response sheet and
  a drop folder — start processes, with no public endpoint, no script inside a Google
  document, and no credential outside the vault.
- **Positive:** the bridge takes a third source with no change to the engine's
  idempotency mechanism; `MarkKey` was built for exactly this and is now used by two
  sources, which is what shows it was the right seam.
- **Negative / trade-offs accepted:** **a row watch loses rows if rows are deleted from
  the watched range.** Stated above, documented next to the feature, and not repairable
  within a mechanism whose only identity for a row is its position.
- **Negative:** a row watch reads the range on every poll. Sheets bills per read and
  the whole range is returned, so a large sheet polled fast is real cost; the cadence
  and the range are the knobs, and both are on the watch.
- **Negative:** the folder watch's `createdTime` window is Drive's, and Drive's index
  can lag its writes. The lag knob makes that survivable rather than absent; setting it
  to zero on a busy shared drive is how a file gets missed.
- **Follow-ups / risks to watch:** `changes.list` for the folder half; a watch on a
  named sheet within a spreadsheet rather than a range; and whether a deleted-row
  detection worth having exists at all (a key column plus a per-key mark would be one,
  at the cost of reading the whole sheet every poll).

## Pros and cons of the options

### Option 1 — polled watches on the bridge
- Good: works behind a firewall; configured, visible and rate-limited where every other
  watch is; reuses the durable mark that makes at-least-once safe.
- Bad: latency is the poll interval, and every poll costs a Google API call whether or
  not anything happened.

### Option 2 — Drive push notifications
- Good: near-real-time, and Google does the work of noticing.
- Bad: needs a public verified HTTPS endpoint, channels expire and must be renewed
  forever, and the notification carries no payload — so the poll happens anyway.

### Option 3 — Apps Script posting to the API
- Good: no Atlas change, immediate delivery, and a sheet owner can do it themselves.
- Bad: an Atlas API token lives inside a Google document; the integration is invisible
  to the Console and dies with whoever owns the script.

### Option 4 — `changes.list` for both
- Good: a real monotonic cursor, no lag window, no per-item mark.
- Bad: drive-scoped rather than folder-scoped, and it cannot say which row changed — so
  the row watch would still need this record's mechanism, and there would be two.

## Links

- relates to [ADR-0235](0235-google-sheets-worker.md) — the outbound half
- relates to [ADR-0075](0075-clio-inbound-event-bridge.md) — the bridge and the durable mark
- relates to [ADR-0214](0214-jira-inbound-issue-watch.md) — the second source, and `MarkKey`
- relates to [ADR-0225](0225-inbound-watch-budget.md) — the per-hour ceiling
- relates to [ADR-0020](0020-message-correlation.md) — where an inbound event lands
