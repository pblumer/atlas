# ADR-DRAFT: A wikiwikibangbang watch publishes the tasks a space finishes, so a process can wait for its task

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-27
- **Deciders:** Atlas maintainers
- **Open question:** two points this record could not settle. (1) **A finished task
  nobody waits for yet is not kept.** An inbound message carries no time to live, so one
  that matches no waiting instance is dropped ([ADR-0370](0370-durable-message-buffer.md):
  TTL defaults to zero). A process that does something else between opening a task and
  waiting for it can miss its task. Keeping inbound messages for a while would close
  that for every watch, not just this one, and is the buffer's question rather than this
  source's. (2) **Polling rather than being told.** The wiki has no webhook, and its feed's
  long poll (`wait`) is not used, because the bridge reads every watch one after another
  and a held request would hold all the others. A wiki that pushed, or watches read on a
  worker under a lease ([ADR-0187](0187-postgres-change-events.md)), would change both.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0498](0498-wikiwikibangbang-worker.md) gave processes a wikiwikibangbang Worker Type:
a process opens a task in the wiki for a person and moves it, comments on it and reads it.
What a process usually wants next is to **wait until the task is done** — the laptop is
prepared, the account is set up — and go on. ADR-0498 left that as a follow-up: the
Worker Type can only be asked, so a model that waits polls `get-item` in a timer loop.
That costs a job per poll per waiting instance, its latency is the loop's interval, and
every model builds the loop again.

Atlas already has the receiving half. A message catch event waits for a message by name
and correlation key, and the inbound bridge ([ADR-0075](0075-clio-inbound-event-bridge.md),
[ADR-0214](0214-jira-inbound-issue-watch.md)) turns what an external system reports into
messages, deduplicated in the engine against a durable mark per source and sequence.
What is missing is a source that says when a task in the wiki is done.

The wiki, walked at commit `928fec0`, offers:

- **A change feed** (its ADR-0121): `GET /api/changes?after=N&limit=L` lists the changes
  of its log after `N`, oldest first, each as `{seq, item, at}`, and `last`, the cursor
  to continue from. Without `after` it lists nothing and answers the newest sequence. It
  lists a change only when the account **watched** the item, an ancestor of it, or its
  space **at the time of the change**, and names no item the account may not read now.
- **Watching a space**: `PUT /api/spaces/{space}/watch`, which a token of scope `read`
  may make — the one write such a token may. A space the account holds no role in
  answers 404.
- **A query language** with `finished = true` — the workflow's last state, or one no
  transition leaves — `id IN (…)` and `space = "…"`. An item answers `stateSince`, when it
  reached its state. It answers no time of its last change.
- **Tokens of scope `read`**, which every other write refuses with 403.
- No webhook.

ADR-0498 made the Worker Type worker-only: the engine runs none of its tasks and builds no
client, so a token that writes as the wiki account is used by the supervised worker
alone. Every inbound watch Atlas has, though, is read by the engine.

## Decision drivers

- **A token that can write is used by the worker alone** ([ADR-0498](0498-wikiwikibangbang-worker.md),
  [ADR-0172](0172-entra-id-connector.md)).
- **At least once, deduplicated in the engine** ([ADR-0075](0075-clio-inbound-event-bridge.md)):
  each event needs a mark and a sequence that a replay repeats exactly.
- **Forward-only by default**: enabling a watch must not publish the history of a space.
- **Cheap when nothing happens**: a quiet space costs one request per read.
- **The loop guard applies** ([ADR-0225](0225-inbound-watch-budget.md)): a process that
  finishes tasks the watch then reports must not run without end.

## Considered options

1. **An engine-side watch on the change feed, with a token of scope read only**: the
   feed names the items that changed, one question asks which of them are finished tasks
   of the space, and each is published with one mark per task, sequenced by when it was
   finished.
2. **An engine-side watch by query alone**: ask for the space's finished tasks on every
   read.
3. **A worker-side watch**: the supervised worker, which holds the token anyway, reads
   the feed and publishes messages through the engine's API.
4. **Polling in the model**: a timer loop around `get-item`.
5. **Waiting for the wiki to push** a change to Atlas.

## Decision outcome

Chosen option: **1, an engine-side watch on the change feed, with a token of scope read
only**, because it is the bridge Atlas already runs for every other source, with its
cursor, marks, priming, health and loop guard, and because the wiki lets a token that
cannot write do everything the watch needs.

### The token

The watch reads with a wikiwikibangbang Worker of its own, whose bundle holds a token of
scope `read`. Before it reads, the connector reads the account's standing (`GET /api/me`,
again every ten minutes, as ADR-0498's operations do) and refuses a token of any other
scope — and, as ADR-0498 does, an account with the
system role `admin` or `erasure` — naming the fix: a second Worker, with a read token of
an account that is a viewer of the space. The refusal shows as the watch's `lastError`.

So ADR-0498's "the engine builds no client for this kind" holds for every token that can
write. The engine builds one client, for this watch, and only a token that cannot write
reaches it. The client is kept per Worker and the credential resolved again at most every
five minutes — a vault read on the run loop is not something a watch due every few
ticks should make on every one — or at once when the Worker's endpoint or reference
changes.

### What one read does

1. Make sure the account watches the space (`PUT …/watch`), once and again every ten
   minutes, because somebody may stop the watch in the wiki and a feed gone quiet that
   way looks like a quiet space.
2. Read the feed after the cursor, at most 100 changes.
3. Ask which of the items it named are finished tasks of the space —
   `(<wikiQuery>) AND space = "S" AND finished = true AND id IN (…)` — and keep only the
   answers that were asked for, in that space.
4. Publish each with **its id as the mark key** and **`stateSince` in milliseconds as the
   sequence**. An edit of a finished task reads it again at the same time, which the mark
   skips; a task reopened and finished again is finished later, which it does not.

The cursor is the feed's sequence and a time, `<seq>|<since>`. `since` is the wiki's own
clock (its `Date`) when the watch began, less a minute for a clock set back a little: a
task finished before it is not published, even when a later edit puts it in the feed.

### Edges

- **Forward-only, always.** The feed lists only what the account watched at the time,
  so there is no history to replay; `startFromTip: false` is refused, as for Gmail
  ([ADR-0438](0438-mailbox-worker.md)).
- **A lost or unreadable cursor** starts at the tip again rather than at the beginning.
- **A feed shorter than the cursor** — the wiki restored from a backup — moves the cursor
  to the feed's end, makes the account watch the space again on the next read, since the
  watch was a change of the log too, and warns `inbound_watch.gap`.
- **The watch's own query** narrows and never widens: it is grouped before the watch's
  conditions, and one that closes a parenthesis it did not open, leaves one or a text
  open, or orders, is refused when the watch is created.

### What a process receives

One variable, `wikiEvent`, as the mail watch hands over `mail`: `eventType`
(`wikiwikibangbang.item.finished`), `itemId`, `itemKey`, `itemType`, `space`, `title`,
`state`, `finishedAt` and `item` — the task as `get-item` returns it, without its body.
A correlation key binds the flat names and the object's alike, so the watch is keyed
`= itemId` and the catch event `= aufgabe.id` when create-item kept its result in
`aufgabe`. A watch is read every 30 seconds by default.

### As built

- `connector/wikiwikibangbang/watch.go` — `WatchTip`, `FinishedSince`, `CheckSpace`,
  `CheckWatchQuery`; `client.go` records the token's scope with the standing and answers
  the headers of a read (`send`).
- `api/wikiwatch.go` — `wikiSource`, the event's fields, the engine's client per Worker
  and `validateWikiWatch`; the bridge, the cadence, the record's `wikiSpace` and
  `wikiQuery`, the descriptions, the API summary and the Console's **Events…** form.
- Tested against the connector's in-memory wiki (`watch_test.go`) and in `api`
  (`wikiwatch_internal_test.go`), where two instances wait for two tasks and only the one
  whose task is finished is woken. `TestAWatchAgainstARealWiki` (`live_test.go`) walks the
  watch against wikiwikibangbang at commit `928fec0` when `ATLAS_TEST_WWBB_READ_TOKEN` is
  set beside the variables of `TestAgainstARealWiki`; so did a server with a supervised
  worker, where an instance that opened a task was woken when the task was finished in the
  wiki.

### Consequences

- **Positive:** a process waits for its task without polling, woken within the watch's
  cadence. A quiet space costs one feed request per read.
- **Positive:** a token that writes stays with the worker; what the engine holds of the
  wiki can read, and nothing more.
- **Negative / trade-offs accepted:** a wiki a process both writes and waits on needs two
  Workers and two tokens. The read account reads every space it holds a role in, so it
  should be a viewer of the watched spaces only.
- **Negative:** a task finished while no instance waits for it is not kept (open question
  1), and a task finished and reopened between two reads is not seen, because the
  question reads the state at the time of the read.
- **Negative:** one durable mark per finished task per watch, as a Jira watch keeps one
  per issue; the marks grow with the number of tasks a space finishes.
- **Negative:** the account's watch of the space is visible in the wiki, and every change
  others make in the space is listed among the account's notifications there.
- **Follow-ups / risks to watch:** the open questions above; a webhook from the wiki;
  watches read on a worker ([ADR-0187](0187-postgres-change-events.md)).

## Pros and cons of the options

### Option 1 — the change feed, engine-side, read token only *(chosen)*
- Good: ordered and exact; a quiet space is one request; the dedupe, priming, health and
  loop guard are the bridge's.
- Good: no token that can write reaches the engine.
- Bad: a second Worker; the engine builds a client of a worker-only kind, for reading.

### Option 2 — a query alone
- Good: one request, no watch of the space.
- Bad: an item answers no time of its last change, so there is no cursor to page by; every
  read returns every finished task of the space, and the answer grows with the space.

### Option 3 — a worker-side watch
- Good: no wiki token reaches the engine at all.
- Bad: no protocol lets a worker own a watch yet ([ADR-0187](0187-postgres-change-events.md)
  is proposed, not built); the publish route has no mark, so a replay would start or wake
  a second time; cursor, health and loop guard would be built a second time.

### Option 4 — polling in the model
- Good: nothing to build.
- Bad: a job per poll per waiting instance, latency of the loop's interval, the same loop
  in every model.

### Option 5 — the wiki pushes
- Good: no polling.
- Bad: the wiki has no webhook, and it is not this repository's to build; Atlas would also
  need an authenticated inbound route for it.

## Links

- refines [ADR-0498](0498-wikiwikibangbang-worker.md) (its follow-up: waiting on a task by a
  message rather than by polling)
- relates to [ADR-0075](0075-clio-inbound-event-bridge.md),
  [ADR-0214](0214-jira-inbound-issue-watch.md), [ADR-0225](0225-inbound-watch-budget.md),
  [ADR-0370](0370-durable-message-buffer.md), [ADR-0438](0438-mailbox-worker.md),
  [ADR-0187](0187-postgres-change-events.md)
