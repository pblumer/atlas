# ADR-0262: Discord as an inbound event source — a channel is a log, and a snowflake is its sequence

- **Status:** Accepted (amended 2026-09-07: the Google row watch this record established as unfixable-here is fixed, and needed no migration — [ADR-0264](0264-row-watch-mark-per-watch.md))
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0258](0258-discord-worker.md) gave Atlas an *outbound* Discord Worker Type: a
modeled service task sends a message, edits one, deletes one, reads one, lists a
channel, or opens a thread. Every one of those seven operations begins inside a process
instance that already exists.

That record deferred the complementary direction and said why: **Discord's real-time API
is the Gateway**, a persistent WebSocket with a heartbeat, a session resume and its own
sequence numbering, and Atlas's inbound bridge
([ADR-0075](0075-clio-inbound-event-bridge.md),
[ADR-0214](0214-jira-inbound-issue-watch.md),
[ADR-0234](0234-google-inbound-watch.md)) is built around *polled* sources. It also said
what would fit: `list-messages` with an exclusive `after`, because Discord's message ids
are snowflakes and snowflakes are monotonic by construction.

The question this record answers is **whether that polled shape is actually correct for
a channel, and what its idempotency mark is** — not whether Atlas should eventually
speak the Gateway.

### A channel is not a search result

The two sources added since clio were both queries, and both had to work around the same
thing. A Jira watch reads a JQL, "whose order is the query's and whose contents are an
index's" (ADR-0214). A Drive folder watch reads `files.list`, which is the same shape.
Neither has a sequence of its own, so both scope the engine's high-water mark **per
item** and hold the cursor deliberately behind the tip with a lag knob, so an item the
index publishes late is still inside the next window.

**A Discord channel is none of that.** It is a log:

- A message id is a **snowflake**: a 64-bit integer whose high bits are a timestamp,
  assigned by Discord when the message is created. It is monotonic across the channel by
  construction.
- It **never changes**. Editing a message does not move its id, and there is no
  "updated" ordering to confuse with a "created" one — so the cursor-field distinction
  ADR-0214 needed does not arise here at all.
- `GET /channels/{id}/messages?after=<snowflake>` is an **exact bound on that
  sequence**, not a query against an index that lags the write. A message is readable as
  soon as it exists.

So Discord is the first source since clio whose read is a log read, and it wants clio's
mechanism, not Jira's: **the watch's own scalar mark, with the snowflake as the
sequence**. There is no lag knob, no cursor field, and no per-item mark.

### Why per-item marks would be wrong here specifically

Per-item marks are not merely unnecessary for a channel — they are the wrong shape. The
engine keeps one durable high-water entry per `SourceID`, so a mark scoped per message
grows durable state with the channel's traffic, without bound and without a point at
which it stops. A Jira project produces issues at human speed; a chat channel does not.
One mark per watch is both correct and bounded.

### The scalar mark has to be per *watch*, and today's composition is not

`inboundSourceID` composes the scalar mark as `kind + ":" + ConnectorID + ":" +
WatchedSubject`. `WatchedSubject` is clio's field, and ADR-0214 requires it stay
byte-identical so no existing clio watch replays its backlog.

For any other kind the scalar branch therefore keys on an **empty** string, and every
watch of that kind on one Worker collapses onto one mark. That is not hypothetical: it
is the state of the Google Sheets **row** watch today, which takes the scalar branch
(ADR-0234: "the row number *is* the watch's sequence … so the mark stays the watch's
scalar one") with `WatchedSubject` refused as empty by its own validator. Two row
watches on one Google Worker share the mark `googlesheets:<connectorID>:`, and because
their sequence is an absolute row number, the one that is further down the sheet
suppresses the other's rows entirely.

That is a defect in ADR-0234's watch and is **not fixed here**: changing a live watch's
`SourceID` resets its mark and replays its sheet as new process starts, which is the
flood ADR-0075 exists to prevent. It needs its own record and a migration. What this
record does is refuse to reproduce it.

### Amendment (2026-09-07, ADR-0264)

The paragraph above is right that the Google row watch shares a mark, and wrong about what
that costs to fix. It says changing a live watch's `SourceID` "resets its mark and replays
its sheet as new process starts" — which applies the general rule without checking what
actually bounds a row watch's output.

`sheetRowSource.Read` emits only rows past the watch's **own cursor**, held on its own
record. The cursor bounds what is produced; the mark only discards what was produced
twice. So a live row watch handed a fresh, empty mark has nothing below its cursor left to
replay, and the fix needed no migration at all — it is one `MarkKey`, the same move this
record makes for a channel.

What survives unchanged is everything this record decided about Discord: a channel is a
log, the sequence is the snowflake, the mark is keyed on the channel so it is per watch.
Only the aside about the neighbouring defect was overstated, and it is corrected there
rather than rewritten here.

Discord scopes its mark by passing the **channel id as the event's `MarkKey`**. That
routes through the composition's existing non-empty branch —
`kind + ":" + ConnectorID + ":" + ID + ":" + markKey` — which already carries the watch's
own `ID`. The result is one mark per watch (a watch names exactly one channel), scoped
correctly, with no change to shared code and no risk to another kind's marks.

### Ordering is a correctness requirement, not a nicety

The bridge publishes a page in slice order (`for _, p := range pubs`), and the engine
skips anything at or below the mark. Under a scalar mark that makes **order load-bearing**:
a page delivered newest-first would set the high-water mark from its first element and
the engine would then correctly discard every older message behind it.

Discord returns `GET /channels/{id}/messages` newest-first. The source therefore sorts
each page **ascending by snowflake** before returning it. This is stated here because it
is invisible in the diff — it looks like tidiness and it is the difference between a
working watch and one that delivers one message in every hundred.

## Decision drivers

- **Reuse the correlation path.** A Discord message funnels into `correlateMessage`
  through `PublishInbound`, not a parallel start mechanism.
- **Invariants.** The Discord call is network I/O: off the processor goroutine (I3),
  never inside `applyToState` (I4); the publish is durable before it is acted on (I2).
- **Correctness under at-least-once.** A re-read must not double-start a process, and
  must not silently drop a message either.
- **Bounded durable state.** The mark must not grow with the channel's traffic.
- **No new credential surface.** The bot token stays where ADR-0258 put it.

## Considered options

1. **A polled channel watch on the existing bridge**, with the scalar mark and the
   snowflake as sequence.
2. **A Gateway connection** — a persistent WebSocket consuming `MESSAGE_CREATE`.
3. **A webhook receiver** — Discord posts to a public Atlas route.
4. **A per-message mark**, as Jira and the Drive folder watch use.

## Decision outcome

Chosen: **option 1 — a polled channel watch, one mark per watch keyed on the channel id,
sequenced by the message snowflake, pages sorted ascending.**

### What the watch reads

The watch reuses the outbound Worker's own `list-messages` operation rather than adding
a second read path, exactly as the Jira watch reuses `search`. The bridge therefore
needs no new method on `discord.Client` — which is the difference from the Drive folder
watch, whose `ListFiles` had to be added beside `Do` precisely because no authored
operation covered it.

Two bounds the bridge does not otherwise know about:

- **The page cap is Discord's, not the bridge's.** `defaultInboundBatch` is 256 and is
  operator-configurable; Discord's endpoint accepts at most 100 per call and answers a
  larger `limit` with a 400. The source clamps.
- **An empty cursor means the beginning, not the tip.** With no `after`, Discord returns
  the *newest* page — so an un-primed backfill watch would publish the newest hundred
  messages, set its cursor past them, and never see the history it was pointed at. The
  source sends `after=0` when it has no cursor, which is the Discord epoch and makes the
  first read forward-from-the-beginning like every read after it.

`StartFromTip` defaults to true, as for every other kind and for the same reason:
pointing a watch at a channel with ten thousand messages must not start ten thousand
instances. Priming is one descending read of a single message, which reaches the tip
immediately — Jira's shape.

### What a message exposes

The event's fields are a curated envelope plus the raw message object, following the
Jira watch: `eventType`, `messageId`, `channelId`, `content`, `authorId`, `authorName`,
`authorBot`, `timestamp`, and `message` for anything not named. `authorBot` earns its
place: the most common thing a channel watch must not react to is its own Worker's
outbound message, and a correlation key or a process condition needs to be able to say
so.

### The operational trap this must document

Discord gates message **content** behind the privileged *Message Content* intent. A bot
without it reads a channel successfully and receives every message object with an
**empty `content`** — not an error, not a permission failure, just a blank field. A
correlation key over `content` then evaluates to `""` and the watch quietly correlates
nothing.

This is the single most likely reason a correctly configured Discord watch does nothing,
so it belongs in the Console's hint beside the field and not only here.

## Consequences

- **Positive:** a message in a channel can start a process, over the mechanism already in
  place, with one bounded mark per watch and no lag knob to tune. The read is the
  Worker's own authored operation, so there is one definition of what listing a channel
  means.
- **Negative / trade-offs accepted:** latency is the poll cadence, not real time. A
  message deleted before the next poll is never seen — a channel watch reads what is
  there, not what happened.
- **Follow-ups / risks to watch:** the Gateway remains unbuilt and this record does not
  bring it closer. The Google row watch's shared scalar mark, established above, needs
  its own record.

## Pros and cons of the options

### Option 1 — polled channel watch (chosen)
- Good: fits the bridge exactly; the mark is bounded and correct; no new credential or
  transport.
- Bad: poll latency; a message that exists only between two polls is missed.

### Option 2 — Gateway
- Good: real time, and it is what a chat integration eventually wants.
- Bad: a persistent WebSocket does not fit a bridge built on polled sources with a scalar
  high-water guard; it needs a second inbound mechanism, a connection owned by something
  that is not the engine, and its own resume semantics. A decision to take deliberately.

### Option 3 — webhook receiver
- Good: no polling.
- Bad: needs a publicly reachable Atlas, which the installations this serves do not have;
  and Discord's outgoing webhooks do not deliver message events to arbitrary endpoints
  anyway — that is what the Gateway is for.

### Option 4 — per-message mark
- Good: order stops mattering, so the ascending sort would not be load-bearing.
- Bad: durable state grows with the channel's traffic, without bound. The whole reason
  Jira and Drive pay that cost is that they have no sequence of their own; a channel does.

## Links

- [ADR-0258](0258-discord-worker.md) — the outbound Discord Worker Type this completes
- [ADR-0075](0075-clio-inbound-event-bridge.md) — the bridge, and the scalar mark
- [ADR-0214](0214-jira-inbound-issue-watch.md) — the per-item mark, and why a query needs one
- [ADR-0234](0234-google-inbound-watch.md) — the second and third sources
- [ADR-0264](0264-row-watch-mark-per-watch.md) — fixes the row watch's mark, and amends this record's claim about the cost
- [ADR-0020](0020-message-correlation.md) — where a published message goes
- [ADR-0035](0035-message-start-events.md) — what starts from one
