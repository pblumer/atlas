# ADR-0433: The event feed is pushed to a CloudEvents endpoint

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5 built the event
feed as a pull: `GET /api/v1/events` answers the facts after a cursor the consumer keeps. It
left push delivery "prepared and not built", and said how a later slice would build it:
"through a Worker (ADR-0203) with a server-held cursor per subscription, the retry ladder of
the task and a circuit breaker per endpoint (ADR-0340). It reads the feed; it adds no fact and
no second path into `applyToState`."

A pull asks something of every consumer. It must run a poller, keep its cursor durable, page
until `more` is false, and handle a 410 when it falls behind the retention. Many of the
systems that want the feed cannot do that. A billing SaaS or a CMDB usually offers an inbound
webhook and nothing that polls. For them the feed exists only if Atlas sends it.

Three things are fixed before this decision:

- **The feed is the one source.** It is a projection folded by `applyToState`, keyed by log
  position, pruned by a fact of its own (`api/eventfeed.go`, `state/feed.go`). Its events,
  their ids and their narrowing by catalogue
  ([ADR-0432](0432-the-event-feed-is-narrowed-by-the-catalogue-that-maintains-the-product.md))
  are already decided.
- **A Worker is a configuration** ([ADR-0203](0203-worker-execution-model.md)): an endpoint,
  a credential reference, an owner, and an enabled flag. Network side effects must happen
  after the fact they act on is durable.
- **The server already makes outbound calls off the run loop.** The inbound bridge polls
  Jira, Google and Discord for a Worker's watches (ADR-0214). The OpenSearch exporter pushes
  the log with a persisted position (ADR-0114). A promotion pushes a bundle to a deployment
  target.

## Decision drivers

- **The same events by both doors.** A subscription and an `events` token with the same reach
  are given the same events with the same ids, so a consumer's code does not depend on which
  door it uses.
- **Nothing is skipped.** A batch the endpoint refuses is never passed over. A billing system
  cannot detect a gap it was not told about.
- **Durable before visible.** Only rows the feed holds are sent, and they are durable before
  they can be read.
- **No new fact.** Push adds nothing to the log and nothing to `applyToState`, so replay and
  the record format are untouched.
- **A failing endpoint costs little.** An endpoint that is down is asked rarely, and the other
  subscriptions keep moving.
- **Credentials stay references** (I6), and the feed travels only over TLS.

## Considered options

1. **The server delivers.** A feed subscription names a `cloudevents` Worker, which holds the
   endpoint and the credential. A loop in the server process reads the feed off the run loop
   and POSTs batches.
2. **A system process per subscription.** A BPMN loop of a task that reads a page and a REST
   service task that posts it, so retries, incidents and the breaker come with it.
3. **An external worker.** `atlas worker --connector cloudevents` leases deliveries over a new
   protocol and posts them.
4. **The subscription carries its own URL and credential**, with no Worker.

## Decision outcome

Chosen: **option 1.**

**The Worker.** A new managed Worker Type, `cloudevents` ("CloudEvents endpoint"):
- Its endpoint is the absolute URL batches are POSTed to. It must be `https`; plain `http` is
  accepted for a loopback host only, the deployment-target rule (`api/targetstore.go`). It
  may carry no credentials in the URL.
- Its optional `credentialsRef` names the vault key sent as `Authorization: Bearer`.
- No task names it, so it has no job type, no client registry and no in-process handler
  (`api/connectorkinds.go`). Disabling it pauses every subscription it carries.
- Deleting it deletes them.

**The subscription** (`feedSubscription`, `api/feedsubstore.go`): the Worker, a `reach` of
catalogues (as an `events` token's: the minter must maintain each), a `cursor`, a batch size
(1–1000, default 100), `enabled`, and the reason delivery switched it off.
- It is created at the oldest row the feed holds (`from: "oldest"`, the default) or after its
  newest (`from: "now"`, `state.FeedLast`).
- An update can move the cursor the same two ways.
- It is operating state, not engine state.

**The routes** are admin-only:
- `GET`, `POST /api/v1/feed-subscriptions`;
- `PATCH`, `DELETE /api/v1/feed-subscriptions/{id}`.

Sending who holds what to a system beyond Atlas is the act minting an `events` token is, and
minting is an administrator's act ([ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md)).

**Delivery** (`api/feedpush.go`). Every 2 seconds, a goroutine started like the inbound
bridge resolves, on the run loop, the subscriptions that are enabled, on an enabled
`cloudevents` Worker, and not held. For each:
- it takes a snapshot of the feed off the loop;
- it reads the next batch through the same function as the pull route (`readFeedPage`),
  narrowed to the subscription's reach;
- it POSTs the batch in the CloudEvents HTTP binding's batched mode. The body is a JSON array
  of the pull route's envelopes, with `Content-Type: application/cloudevents-batch+json` and
  `Atlas-Feed-Subscription` naming the subscription.

A `2xx` moves the cursor. A batch the reach passes over entirely moves it without a request.

A subscription catching up sends at most 20 batches per round and writes its cursor once, on
the run loop. The write is skipped if an administrator moved or deleted the subscription
while the batch was out, so the administrator's change wins.

The client has the connectors' 10-second timeout and follows no redirect. The credential is
for the endpoint the Worker names, so a redirect is a refusal, not an address.

**At least once.** The cursor moves only after the endpoint accepted every event up to it. A
crash between the answer and the write delivers those batches again, and the receiver
deduplicates by `id`, as a reader of the pull feed does.

**A failing endpoint is held.** A refusal, a timeout or an unreachable endpoint leaves the
cursor where it was and holds the subscription:
- The next attempt waits 10 s, doubling to 5 min. This is the breaker's own ladder
  (`breakerCooldown`, `breakerMaxCooldown`).
- The next attempt is the probe: the first accepted batch lifts the hold.
- The hold is runtime state, like a breaker's. A restart tries again at once.
- The first failure of a streak logs `feed.push_failing`, and the recovery logs
  `feed.push_recovered`.
- The listing shows each hold with its failures, `failingSince`, `retryAt` and `lastError`.

**A subscription the retention passed is switched off.** If the feed's retention dropped rows
after a subscription's cursor, delivering on from the oldest row held would hide the gap. The
subscription is disabled with the reason, and `feed.subscription_disabled` is logged. An
administrator enables it again, choosing to resume from the oldest row held or from now.

**Surfaces.**
- **HTTP:** the four routes.
- **MCP:** a read tool, `atlas_feed_subscriptions`, so an agent can see why a system was not
  sent an event. Creating, changing and ending a subscription is administrator configuration
  and has no tool.
- **Console:** a `cloudevents` Worker's menu offers **Feed…**. It lists the Worker's
  subscriptions with their catalogues, state and hold, subscribes it, pauses, rewinds and ends
  a subscription. The Worker catalogue and the New worker form describe the type, and the
  handbook has its runbook (`runbook-cloudevents`).
- **Audit:** `feed.subscription_changed` records every change.

### Where this departs from ADR-0429 §5

**"Through a Worker".** The Worker is the configuration, as ADR-0203 defines one. Delivery
runs in the server process off the run loop, as the inbound bridge's polling does, not as a
job a Worker Instance leases. ADR-0203 makes a side effect job-backed so that it follows the
durability of the fact it acts on. A feed row is durable before it can be read, and the
subscription's cursor, written after the `2xx`, is the delivery's own at-least-once record. A
job would also have to belong to a process instance and an element, and there is none.

**"The retry ladder of the task".** A task's ladder ends: when its retries are spent, it
raises an incident and waits for a person. A stream that must not skip has no such end. Its
ladder only widens, and the stream resumes by itself when the endpoint answers.

**"A circuit breaker per endpoint".** This is a hold per subscription, on the breaker's ladder.
A breaker exists so that many parallel jobs to one target stop failing one after another.
A subscription is one serial stream: its next attempt is the probe, and its hold is the open
breaker. A Worker carrying several subscriptions is asked once per subscription per step.
That is the trade-off accepted for one subscription's refusal (say, a batch the receiver
rejects) never holding another.

### Why not the others

**Option 2** has the strongest case. It would be compiled rather than interpreted, retries and
incidents would come for free, and an operator would see each delivery as an instance. But
every batch would become facts in the log, with the payload in variables: the job, its
completion, and the batch as the request body. Delivering the feed would write the feed into
the log a second time, under the history's retention rather than the feed's, and pruning the
feed would leave those copies behind. A delivery loop is also an instance that never ends,
and a refused batch would end in an incident a person must resolve, where a hold resumes by
itself.

**Option 3** keeps the credential and the outbound call out of the engine's process, and it
scales by adding processes. But the worker protocol has no delivery lease; ADR-0187 proposes
one and it is not built. A relay outside Atlas can already do the same with an `events` token
and the pull route, today, with no change here. Push from the server exists so that no such
process is needed.

**Option 4** has the fewest moving parts. But it would give an endpoint and a credential a
second home beside the Worker. It would also lose what a Worker already has: enable and
disable, an owner, the credential-reference handling, and one place that names an endpoint
several subscriptions share.

### Consequences

- **Positive:**
  - A system that only accepts pushes can follow the feed, with no poller to run.
  - The events, their ids and their reach are the pull feed's, so a consumer that switches
    doors changes nothing but its transport.
  - The log and replay are untouched.
  - A failing endpoint is visible in the Console, over HTTP and MCP, and in one log line,
    and it resumes by itself.
- **Negative / trade-offs accepted:**
  - The server process makes outbound https calls that carry the feed, with the credential
    in its memory at call time. The inbound bridge, the exporter and promotion already do so.
  - Delivery is at least once, so a restart can repeat up to 20 batches of a subscription.
  - Subscriptions are delivered one after another, so a slow endpoint delays the others by up
    to its timeout per round.
  - A hold is lost on restart, which costs one immediate attempt.
  - An endpoint that refuses a batch forever holds its subscription forever. That is visible,
    but it is not resolved by anything but the endpoint or an administrator moving the cursor.
- **Follow-ups / risks to watch:**
  - **A signature** (an HMAC over the body) for receivers that verify one instead of a bearer
    token.
  - **`Retry-After`** honoured on a 429 or 503.
  - **Parallel delivery** if an installation runs many subscriptions.
  - **A Console view of every subscription**, beside the per-Worker panel.

## Links

- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5 — the feed and
  the push it prepared.
- [ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md) — the feed's role and
  token scope.
- [ADR-0432](0432-the-event-feed-is-narrowed-by-the-catalogue-that-maintains-the-product.md)
  — the reach a subscription shares with a token.
- [ADR-0203](0203-worker-execution-model.md) — Workers as configuration, side effects after
  durability.
- [ADR-0340](0340-worker-circuit-breaker.md) — the breaker whose ladder the hold uses.
- [ADR-0214](0214-jira-inbound-issue-watch.md) — the inbound bridge, the same shape run the
  other way.
