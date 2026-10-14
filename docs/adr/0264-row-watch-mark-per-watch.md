# ADR-0264: A row watch's idempotency mark is its own, and the cursor is why that needs no migration

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0234](0234-google-inbound-watch.md) gave Atlas a Google Sheets **row watch**: the
rows appended to a spreadsheet are published as Atlas messages, so a form response starts
a process. It chose the watch's own **scalar** idempotency mark rather than the per-item
mark [ADR-0214](0214-jira-inbound-issue-watch.md) needed, and the reasoning was right:

> the row number *is* the watch's sequence, monotonic across the whole watch, so the
> scalar mark is correct — the clio case.

The *shape* is correct. The **composition** is not. `inboundSourceID` builds the scalar
mark as:

```go
kind + ":" + r.ConnectorID + ":" + r.WatchedSubject
```

`WatchedSubject` is clio's field. ADR-0214 requires it stay byte-identical so no existing
clio watch replays its backlog — but it also means that for **every other kind** the
scalar branch keys on the empty string. And `validateGoogleWatch` explicitly *refuses* a
non-empty `WatchedSubject` on a Google watch.

So every Google row watch on one Worker composes the same id. Confirmed directly:

```
watch A (spreadsheet A) sourceID = "googlesheets:conn1:"
watch B (spreadsheet B) sourceID = "googlesheets:conn1:"
```

Because the sequence is an **absolute row number**, this is not a near-miss. The watch
further down its sheet sets the shared mark high, and the engine then correctly discards
every row of the other one. Two row watches on one Google Worker means one of them
silently delivers nothing — no incident, no parked token, no failed job. Every part of
the system is behaving exactly as designed.

This record fixes that, and corrects a claim made about it.

## The claim being corrected

[ADR-0262](0262-discord-inbound-watch.md) established this defect while designing the
Discord channel watch around it, and said it could not be fixed cheaply:

> changing a live watch's `SourceID` resets its mark and replays its sheet as new process
> starts, which is the flood ADR-0075 exists to prevent. It needs its own record and a
> migration.

**The first half is true and the second does not follow.** That sentence applies the
general rule — a new `SourceID` starts at a zero high-water mark — without checking what
actually bounds a row watch's output. `sheetRowSource.Read` does:

```go
seen := sheetRowsSeen(rec)          // the watch's own cursor, from its own record
for i := seen; i < len(rows) && len(out) < limit; i++ {
```

**The cursor bounds what is produced; the mark only discards what was produced twice.**
A row watch never emits a row at or below its own cursor, so a live watch handed a fresh,
empty mark has nothing below that cursor left to replay. The mark's real job here is
narrower than it looked: it catches the page that was published and then re-read because
`drive()` failed and the cursor could not advance.

That is a property of the source, not an argument about it, so it is pinned by a test
(`TestRowWatchNeverEmitsAtOrBelowItsCursor`) rather than left in prose where the next
reader has to re-derive it — which is precisely what went wrong the first time.

## Decision drivers

- **A watch's mark is the watch's.** Two subscriptions are two subscriptions; nothing
  about one may silence the other.
- **No replay.** Whatever the fix, it must not turn a spreadsheet into a burst of process
  starts (ADR-0075).
- **Do not disturb clio.** ADR-0214's byte-identical requirement stands.
- **State the behaviour change rather than discover it.** A fix that quietly changes what
  a running installation delivers is worse than one that says what it changes.

## Considered options

1. **Key the row watch's mark on its spreadsheet**, routing it through the composition's
   existing non-empty branch.
2. **Change the scalar branch** to use the watch id for every kind but clio.
3. **Per-row marks**, as Jira and the Drive folder watch use.
4. **Seed the new mark from the old one** as a one-time migration.

## Decision outcome

Chosen: **option 1 — the row watch passes its spreadsheet id as the event's `MarkKey`.**

That routes the composition through the branch it already has for per-item marks:

```go
kind + ":" + r.ConnectorID + ":" + r.ID + ":" + markKey
```

which carries the watch's **own `ID`**. One watch, one mark. Two watches on one Worker —
even two on the *same* spreadsheet, which is a real configuration when one starts an
approval and another feeds a report — never share one.

The key is the spreadsheet rather than, say, a constant: any non-empty value would do the
scoping, and naming the spreadsheet is what makes the resulting id readable in a store
dump. It is the same move the Discord channel watch makes with its channel id
(ADR-0262), which is where the pattern came from.

**No migration.** Per the correction above, the cursor already bounds emission.

### The one real behaviour change, stated

A watch whose new mark starts empty loses one thing the old mark was doing by accident:
suppressing an append that landed on a row number already delivered *before rows were
deleted*. ADR-0234 documented that as a hole with no repair —

> deleting a row renumbers the tail, and a later append landing on a delivered number is
> not delivered again

— and after this change, such an append **is** delivered. That is a repair, not a
regression: a row's number is its position, not its identity, and suppressing a genuinely
new row because an unrelated older row once held its number was never right. It is
bounded by the page limit and by the watch's hourly budget, and it happens at most once
per watch whose sheet had shrunk.

### Rejected

**Option 2** would fix every kind at once and is the composition this should probably
have had from the start — but it changes the Google row watch *and* leaves the empty-key
trap for the next kind to fall into, while touching a function clio depends on. It is the
better long-term shape and belongs in the record that reworks `inboundSourceID`
deliberately, not in a bug fix.

**Option 3** would make ordering stop mattering, but a row watch has a real sequence and
would be paying an unbounded durable-state cost for nothing — the argument ADR-0262 makes
for a channel.

**Option 4** solves a problem that does not exist, and would be actively wrong: with the
marks collapsed, the shared value is the *maximum* across the Worker's watches, so seeding
each watch from it would freeze the watches that are behind at a position they never
reached.

## A related finding, deliberately not fixed here

clio has the same class of collision with a much narrower blast radius. Two clio watches
naming the **same connector and the same subject** compose the same id:

```
watch A (message "eintritt") = "clio:c1:/employees"
watch B (message "audit")    = "clio:c1:/employees"
```

Different subjects do not collide, so this bites only where one subject is deliberately
watched twice under two message names. It is not fixed here for the reason ADR-0214
gives: clio's composition is a compatibility contract, existing installations have live
clio watches, and changing it is a decision to take on its own terms rather than as the
tail of a Google fix.

## Consequences

- **Positive:** two row watches on one Google Worker both work. The property that makes
  the mark's scope safe to change is now a test rather than an assumption.
- **Negative / trade-offs accepted:** the marks written under the old shared id are
  orphaned — harmless, but they stay in the store. Rows lost to the collision are not
  recovered; the fix stops the loss, it does not replay it.
- **Follow-ups / risks to watch:** `inboundSourceID`'s empty-key trap still waits for any
  future kind that takes the scalar branch without a `WatchedSubject` (option 2), and
  clio's narrow collision is unfixed.

## Links

- [ADR-0234](0234-google-inbound-watch.md) — the row watch, and the mark it chose
- [ADR-0262](0262-discord-inbound-watch.md) — where this defect was established, and the claim corrected here
- [ADR-0214](0214-jira-inbound-issue-watch.md) — the per-item mark, and clio's byte-identical requirement
- [ADR-0075](0075-clio-inbound-event-bridge.md) — the bridge, the scalar mark, and the flood this avoids
