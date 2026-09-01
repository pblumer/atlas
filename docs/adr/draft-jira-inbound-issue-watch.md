# ADR-DRAFT: Jira as an inbound event source — a polled issue watch, deduplicated per issue

- **Status:** Proposed
- **Date:** 2026-09-01
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0201](0201-jira-connector.md) gave Atlas an *outbound* Jira connector: a modeled
service task creates an issue, reads one, updates it, moves it through its workflow,
comments on it, assigns it, or searches. Every one of those seven operations begins
inside a process instance that already exists.

The complementary direction is not served. The question asked was the ordinary one:
**an issue appearing in Jira should start a process.** A ticket lands in a project, and
the triage, the approval, the provisioning that follows it is the process — but today
nothing in Atlas notices the ticket.

Atlas has the destination already. Message correlation
([ADR-0020](0020-message-correlation.md)) takes one published message and both starts
every matching message-start process ([ADR-0035](0035-message-start-events.md)) and
wakes every waiting subscription. And it has the inbound *shape*:
[ADR-0075](0075-clio-inbound-event-bridge.md) built a bridge that consumes clio events
and republishes each as an Atlas message, with a durable engine-side idempotency mark
so an at-least-once source cannot double-start a process. The generic half of that —
`model.InboundDeliveryValue{SourceID, SourceSeq}`, `Processor.PublishInbound`, the
`cfInboundHighWater` column family, the `SourceSeq <= hw` guard in
`handleMessagePublished` — knows nothing about clio.

What is clio-specific is everything above it. `resolveInboundSubs` rejects any
subscription whose connector is not `connectorKindClio`; `pendingSub.client` is a
`clio.Client`; `pollInbound` and `primeInbound` call `clio.ReadEvents` directly; the
Console gates its "Events" button on `c.kind === "clio"`. So the bridge is not a bridge
with one source — it is a clio reader with the generic parts factored out below it.

### Two facts this record had to establish first

**The engine's guard is a scalar, and a scalar mark is only correct under
monotonically increasing delivery.** `handleMessagePublished` reads one high-water
value per `SourceID` and skips anything at or below it. For clio that is free: an event
id *is* the partition's sequence. Jira has no such thing. Issues have ids that rise
with creation, but the thing a watch reads is a *search result*, whose order is the
query's and whose contents are an index's. [ADR-0187](0187-postgres-change-events.md)
hit the same wall for PostgreSQL and named it precisely — insert order is not commit
order, and the mark loses a row permanently. Its fix was to assign the sequence at
claim time. Jira offers nothing to assign a sequence *in*, so that fix does not
transfer, and this record needs a different one.

**Jira Cloud has removed the endpoint the outbound `search` operation uses.**
`connector/jira/rest.go` posts to `/rest/api/2/search` with `startAt` paging.
Atlassian deprecated the `/rest/api/2/search` and `/rest/api/3/search` JQL endpoints in
2025 and progressively shut them down; a Cloud site that has been switched over
answers `410 Gone` with "The requested API has been removed. Please migrate to the
`/rest/api/3/search/jql` API." The replacement pages by opaque `nextPageToken` rather
than `startAt`, and returns no issue fields unless an explicit `fields` array is
passed. Data Center is unaffected — this is a Cloud change.

Whether the replacement is also served under `/rest/api/2/` is the one thing to
confirm before writing the migration, because ADR-0201 chose v2 deliberately: v3
requires an Atlassian Document Format tree where v2 takes a string, and no model author
should have to write ADF for one sentence. If `/search/jql` is v3-only, the search
operation moves to v3 alone while the six others stay on v2 — which is tolerable
precisely because a search *reads*. ADF only changes how a description or comment body
is shaped, so the split costs nothing on the write path, and on the read path it means
a `description` inside a searched issue arrives as a document tree rather than a
string. That is a reason for the inbound envelope below to name the fields it exposes
rather than flatten whatever came back.

That makes the shipped `search` operation broken on Cloud, independently of anything
inbound, and it is a **prerequisite** for this record rather than a consequence of it:
a watch built on a 410 reads nothing. (Corroborated from several independent integration
bug reports against unrelated products; Atlassian's own deprecation pages are not
reachable from the build environment, so both the rollout state and the v2 question
above should be confirmed against the target site rather than taken from a date here.)

## Decision drivers

- **Reuse the correlation path.** A Jira event must funnel into `correlateMessage`
  through `PublishInbound`, not into a parallel start mechanism.
- **Invariants.** The Jira call is network I/O: off the processor goroutine (I3), never
  inside `applyToState` (I4). The publish is durable before it is acted on (I2).
- **Correctness under at-least-once.** A re-read must not double-start a process, and —
  the harder half — must not *silently drop* an issue either.
- **No new credential surface.** Nothing in a model, nothing in a watch record.
- **Reachability.** An installation behind a firewall must be able to use this.
- **Converge with where connectors are going.** [ADR-0203](0203-worker-execution-model.md)
  moves worker types out of the engine process; whatever is built here must move with
  the Jira worker type rather than anchor it.

## Considered options

1. **A polled Jira issue watch on the existing inbound bridge**, generalized to a second
   source kind.
2. **A webhook receiver** — Jira Cloud posts issue events to a public Atlas route.
3. **Jira Automation posting to the existing `POST /api/v1/messages`** — no Atlas change
   at all.
4. **Wait for ADR-0187's leased worker-watch protocol** and build Jira as its second
   consumer.

## Decision outcome

Chosen option: **1 — a polled Jira issue watch on the existing bridge, with the
idempotency mark scoped per issue rather than per watch.**

### Why the bridge and not a worker

ADR-0187 rejected running its reader in the engine process for one stated reason: "the
engine would need the DSN, and a DSN is a credential." It was careful to add that this
is the *only* thing wrong with that option — "the clio bridge's structure is right, and
the worker-side reader copies it almost line for line."

That objection does not apply to Jira **today**, because the Jira credential is already
in the engine process. The Jira worker type is built in and served in-process: its
`managedConnectorKind` entry carries a `newRegistry` and a `registerHandlers` and is
not `workerOnly`, so the server builds the client and subscribes the handler itself,
and the `{email, apiToken}` or `{token}` bundle is resolved from the vault by
`jira.NewProviderClient` there. An inbound reader beside it adds no
credential to any process that does not already hold it, and reuses the same registry,
the same `jira.Client`, the same error mapper.

When ADR-0203 moves the Jira worker type out of process, the reader moves with it —
into ADR-0187's `POST /api/v1/inbound/watches/lease` + `POST /api/v1/inbound/deliveries`
protocol, unchanged in substance, because that protocol was designed as exactly this
loop across an HTTP boundary. Building the reader now against the bridge and moving it
later is strictly cheaper than blocking on a protocol that has no implementation
(ADR-0187 is Proposed; neither endpoint exists in the tree).

### The cursor is not the mark

This is the part a reader should not have to rediscover, and it is what makes option 1
correct rather than merely convenient.

A poll needs two different things, and conflating them is the bug:

- **A cursor**, so each poll asks Jira for less than everything. JQL's date comparison
  has *minute* granularity (`created >= "2026/09/01 06:54"`), and Jira's search is served
  from an index that lags the write. A cursor is therefore approximate in both
  directions: it re-reads, and — if advanced eagerly — it can step past an issue that was
  not yet indexed.
- **A mark**, so a re-read publishes nothing twice.

Take the mark **per issue**, not per watch:

```
SourceID  = "jira:" + connectorID + ":" + watchID + ":" + issueID
SourceSeq = the issue's timestamp in the watch's own cursor field, epoch milliseconds
            (created for a watch on new issues; updated for a later watch on changes)
```

The sequence must be the **cursor field**, not `updated` unconditionally. On a watch for
new issues, `updated` moves whenever somebody edits the issue — so an edit inside the
lag window would arrive with a higher sequence than the mark, pass the guard, and start
a *second* instance for a ticket already handled. `created` never moves, so the same
re-read is skipped exactly as it should be. Pairing the sequence with the field the
query filters on is what keeps "the same issue, seen again" and "this issue changed"
from being confused for one another.

Every other consequence follows from scoping the mark per issue:

- **Ordering stops mattering.** Two issues never share a mark, so no delivery order can
  make one suppress the other. ADR-0187's trap cannot bite, because there is no shared
  scalar to race for.
- **Overlap becomes free.** The cursor can be held deliberately *behind* the newest issue
  seen — a lag knob, default 2 minutes — so an issue indexed late is still inside the
  next query's window. Re-reading it costs one skipped publish.
- **The late-index case becomes recoverable instead of permanent.** Under a per-watch
  scalar on issue id, an issue surfacing after a higher id was marked is dropped
  forever. Under per-issue marks it is delivered on the next poll that covers it.
- **Watching updates later costs nothing.** `updated` is monotonic per issue by
  construction, so a watch on changed issues is the same mechanism with a different JQL
  and the cursor field moved along with it — no second design.

The cursor then carries no correctness weight at all. It exists so a poll is bounded and
makes progress, which is exactly the role `LastEventID` already documents for itself:
"best-effort … it only speeds a restart's resume." The field is reused verbatim as an
opaque, source-interpreted resume cursor.

**Compatibility, and it is not optional:** clio's `SourceID` must stay byte-identical to
today's `"clio:" + ConnectorID + ":" + WatchedSubject`. Any reshaping of that string
resets every existing clio watch's mark and replays its backlog as new process starts.
The generalization below adds a per-event key *beside* the existing composition rather
than replacing it.

### What the bridge grows

One interface, two implementations, and the polling, priming, cursor and publish logic
stay where they are:

```go
// inboundSource turns a watch plus its opaque resume cursor into a bounded page of
// events. The bridge never learns what a subject or a JQL is.
type inboundSource interface {
    Read(ctx context.Context, rec inboundSubscription, limit int) (page []inboundEvent, cursor string, err error)
    Prime(ctx context.Context, rec inboundSubscription) (cursor string, done bool, err error)
}

type inboundEvent struct {
    MarkKey string         // "" = the watch's own scalar mark (clio); else a per-event suffix
    Seq     uint64
    Fields  map[string]any
}
```

clio's implementation returns `MarkKey: ""` and its existing event id as `Seq`, which is
today's behaviour expressed through the interface — no observable change. Jira's returns
the issue id as `MarkKey` and the issue's cursor-field millis as `Seq`.

`Prime` keeps `StartFromTip`'s one-page-per-tick contract so clio's paged backlog skip is
unchanged; Jira's implementation is a single descending read of one issue, which reaches
the tip immediately. Priming matters more for Jira than for clio: pointing a watch at a
project with 500 existing issues must not start 500 instances, and defaulting
`StartFromTip` to true is what prevents it.

The connector's **kind is the discriminator** — `resolveInboundSubs` already loads the
connector record, so its `switch c.Kind` replaces the current `!= connectorKindClio`
rejection. No discriminator field, no migration, and a watch's shape is validated at
config time against the kind it names (a JQL for `jira`, a subject for `clio`).

### What Jira is asked, and what comes back

The adapter builds the query from the watch's JQL, and **owns the ordering**:

```
(<watch JQL>) AND created >= "<cursor>"  ORDER BY created ASC
```

A watch JQL that carries its own `ORDER BY` is rejected when the watch is created,
because the cursor's progress depends on the ordering being the bridge's. The call goes
through `jira.Client.Do` with the existing `search` operation — one Jira HTTP surface,
one credential path, one error mapper — which is why migrating that operation to
`/search/jql` is a prerequisite and not a parallel task. The migration is also a gift
here: `nextPageToken` is a real opaque cursor for paging *within* one poll, replacing
`startAt` arithmetic. It is **not** a watch cursor — a page token does not survive a
changing result set between polls — so `created >=` remains the resume mechanism.

What reaches the process is a bounded envelope plus the issue as one value, rather than
every field flattened into variables (a Jira issue with custom fields would otherwise
seed dozens):

| variable | |
|---|---|
| `eventType` | `"jira.issue.created"` |
| `issueKey` | `OPS-42` |
| `issueId` | Jira's numeric id |
| `projectKey`, `issueType`, `summary`, `status`, `reporter`, `created` | the fields a correlation key or a gateway actually reads |
| `issue` | the whole issue object, JSON |

The envelope names take precedence over anything of the same name inside the issue, the
same discipline `eventFields` already applies to a clio body. The typical correlation
key is `=issueKey`. The `fields` array the new endpoint requires is exactly this list
plus whatever the correlation key names, which bounds the response instead of fetching
every custom field on every poll.

### Cadence and ownership

clio polls every 2 seconds. Jira Cloud rate-limits per site, and a 2-second poll per
watch would spend that budget on empty answers. A per-watch `pollSeconds` with a
kind-specific default — 60 seconds for Jira — is what ships; the existing
`WithInboundPollInterval` stays the bridge's tick, and a watch is due when its own
interval has elapsed.

Ownership needs no new decision: [ADR-0205](0205-connector-ownership-and-event-delivery.md)
already governs who owns a connector and who may claim a message name, and it was
written with exactly this case in mind — its own framing is that inbound is clio-only
*today*. A Jira watch is a second inhabitant of a model that already exists.

### Consequences

- **Positive:** the answer to "start a process when a ticket is created" reuses the
  correlation path, the durability guarantee and the config surface that already exist.
  No inbound network exposure, so it works behind a firewall. The mark is per issue, so
  no delivery order can lose an event — which a scalar mark cannot promise for a source
  whose order is a query's rather than a log's (it is exactly right for clio, where an
  event id *is* the sequence).
  Watching *changed* issues later is a different JQL, not a different design. The
  generalization is what a third source (ADR-0187's PostgreSQL, a mailbox) lands on
  rather than beside.
- **Negative / trade-offs accepted:** latency is the poll interval — a minute by
  default, against a webhook's seconds. `cfInboundHighWater` grows one key per issue
  ever delivered, and nothing collects them — a key is one column-family byte plus
  `"jira:"` and two 16-hex ids and the issue id, so ~55 bytes plus an 8-byte value,
  about half a megabyte per 10,000 issues. Worth naming, not worth solving yet. A watch
  spends Jira API quota whether or not anything happened. JQL's minute granularity plus
  the safety lag means every poll re-reads a small window. And the outbound `search`
  operation must be migrated to `/search/jql` before any of this reads anything on
  Cloud.
- **Follow-ups / risks to watch:** the webhook receiver (option 2) as the low-latency
  path once an installation is reachable — it does not replace this one, because
  reachability is not universal. Moving the reader onto the Jira worker with ADR-0203,
  through ADR-0187's leased-watch protocol. A retention sweep for per-issue marks. A
  watch on `updated` for transitions and comments. And the rollout state of the Cloud
  search deprecation should be confirmed against the target site rather than a date.

## Pros and cons of the options

### Option 1 — polled issue watch on the bridge
- Good: reuses the whole inbound stack; no inbound exposure; the per-issue mark makes
  delivery order irrelevant; generalizes the bridge for the next source.
- Bad: poll latency and API quota; unbounded mark keys; needs the search-endpoint
  migration first.

### Option 2 — webhook receiver
- Good: seconds, not a minute; no polling load; Jira sends transitions and comments,
  not just creations, with no extra query.
- Bad: Atlas must be reachable from Atlassian's cloud, which many installations are
  not; a new public route and a new access class (ADR-0199); a shared secret and
  signature verification per watch; and Atlassian webhooks are themselves at-least-once
  and unordered, so the per-issue mark above is needed *anyway* — this option is
  additive to option 1's core, not an alternative to it.

### Option 3 — Jira Automation → `POST /api/v1/messages`
- Good: works today with no Atlas change; the rule can build the exact body.
- Bad: the token needed is role `operator`, which reaches 28 routes rather than this
  one; it lives in an Automation rule, outside the vault; there is no idempotency mark
  on that path (`SourceID` is empty and the publish is never deduplicated), so a
  retried rule double-starts; and the wiring is invisible from Atlas. A fine pilot,
  not a product.

### Option 4 — wait for ADR-0187's worker protocol
- Good: lands the reader in its final home once, with no move later.
- Bad: neither endpoint exists, ADR-0187 is Proposed, and its blocking reason — a
  credential the engine must not hold — is not true of Jira today. It makes the answer
  wait on an unrelated decision.

## Links

- builds on [ADR-0075](0075-clio-inbound-event-bridge.md) (the bridge, the mark, the
  at-least-once argument)
- extends [ADR-0201](0201-jira-connector.md) (the outbound connector, the client, the
  credential bundle) and depends on migrating its `search` operation off the removed
  Cloud endpoint
- converges with [ADR-0187](0187-postgres-change-events.md) (the ordering trap, the
  leased worker-watch protocol this reader moves into)
- governed by [ADR-0205](0205-connector-ownership-and-event-delivery.md) (connector
  ownership, the message-name claim)
- delivers into [ADR-0020](0020-message-correlation.md) /
  [ADR-0035](0035-message-start-events.md)
- moves with [ADR-0203](0203-worker-execution-model.md)
