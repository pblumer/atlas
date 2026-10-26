# ADR-0370: A published message waits for its subscriber

- **Status:** Accepted (amended 2026-09-16 — the buffer ships as a standalone local slice, ahead of and independent of any cross-node traffic; amended 2026-09-17 — expiry at an entry point that states a commitment is a breach rather than a silent outcome; amended 2026-10-13 — what the first slice decided where this record was silent: the buffer feeds subscriptions and not message starts, an id is remembered across delivery, one catch per event-based gateway takes a message, and a message that may be kept carries no personal data; amended 2026-10-23 — a subscription whose element no longer waits is no delivery; see the amendment notes below)
- **Implementation:** Partial
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers

> **Amendment (2026-09-16): this is a local fix first, and it has to earn its place as
> one.** The record below argues the buffer mainly from the cross-node need, because
> that is the need that makes it unavoidable. The delivery order is the other way
> round: the buffer is built **before** anything can send to it from another node, and
> is justified on its own.
>
> Two consequences follow, and both bind the first slice:
>
> - **The only writer is a local publish that declares a TTL.** The inbound peer
>   endpoint belongs to [ADR-0372](0372-peer-message-delivery-worker.md) and is not in
>   this slice. So the slice is worth building only if the opt-in fix for the
>   publish-before-subscribe race is worth it *by itself* — and that is the claim it
>   has to demonstrate, on a model that today has to be shaped around the race and
>   afterwards does not.
> - **"A message can arrive from the past" is the behaviour under test**, not a side
>   effect noted in passing. The first failing test states it directly: publish with a
>   TTL, subscribe afterwards, correlate exactly once. Because this adds a column
>   family, the recovery test comes with it and is written up front
>   ([ADR-0018](0018-test-driven-development.md)) — process, restart, replay, assert
>   the buffer and its dedup window rebuild identically.
>
> Nothing in the decision changes. TTL still defaults to 0, so ADR-0020's no-op stands
> for every deployed model, and the cross-node case still consumes the same buffer
> when ADR-0372 is built.

> **Amendment (2026-09-17): expiry is silent only where nothing was promised.** This
> record decided that a buffered message expiring unconsumed is an ordinary outcome and
> not an incident, because a fan-out nobody consumed would otherwise flood the incident
> list ([ADR-0337](0337-incident-floods.md)). That stands wherever nothing was promised,
> which is the default.
>
> [ADR-0373](0373-published-process-interface.md) now lets an entry point state a
> **commitment** — a duration within which an accepted message will be correlated. Where
> one is stated, expiry at that entry point is a **breach of it**, and the decision below
> reads differently in exactly two places:
>
> - the incident this record declines to raise is raised after all, in the domain that
>   made the promise. ADR-0337 still governs it, but as a bound on volume rather than as
>   a reason for silence: breaches are rate-limited and aggregated per interface and per
>   peer, because a rule that lets a remote sender open incidents here is otherwise a
>   denial of service against this domain's operations view.
> - `expiresAt` stops being only a cleanup deadline. It is the durable evidence that lets
>   a breach which happened while this node was down be reconstructed on recovery rather
>   than lost, and that lets a window which passed unobserved be recorded as
>   **unmeasured** rather than as met.
>
> Nothing changes for an entry point without a commitment. TTL still defaults to 0 for a
> locally published message, so ADR-0020's no-op stands for every deployed model.

> **Amendment (2026-10-13): what the first slice decided where this record was silent.**
> Building the local slice met five questions the decision below does not answer.
>
> - **The buffer feeds subscriptions, not message start events.** A message is consumed
>   when a catch event, a receive task, a boundary event or an event subprocess's trigger
>   opens a subscription on its name and key. A definition deployed or reactivated
>   (ADR-0119) after a message was buffered does not start from it: draining the buffer
>   into starts would start a burst of instances at once, past the singleton rule of
>   ADR-0094, and that is a decision of its own. The record's question about a
>   deactivated definition is answered this way: a message buffered while a message
>   start was down waits only for a catch. One consequence is welcome rather than
>   designed: a message a singleton start declines because an instance for its key is
>   live is buffered when it has a time to live, and reaches that instance if it later
>   waits for it — part of the follow-up ADR-0094 named.
> - **An id is remembered across delivery.** Idempotency covers a message delivered at
>   once as well as a buffered one: its id is recorded until the message would have
>   expired, so a sender's retry after an answer it never saw is dropped either way. The
>   id is scoped by message name and correlation key, the triple the mechanism names. An
>   id without a time to live is refused at the API, because nothing would remember it.
> - **One catch per event-based gateway takes a buffered message.** The catches of a race
>   (ADR-0110) arm one after another before the winner cancels the rest. The first in
>   flow order that finds a message takes it; the others leave theirs in the buffer
>   instead of spending them on a branch that is about to be cancelled.
> - **A message that may be kept carries no personal data.** Nothing on the message path
>   can seal a value: the data subject that would key it is known only once the message
>   correlates (ADR-0314). A buffered payload would therefore wait readable, out of reach
>   of an erasure. So a publish with a time to live is refused with 422 when one of its
>   variables is declared personal by any deployed process — any, because which process a
>   message reaches is decided when it arrives.
> - **A lifetime is bounded.** `--message-ttl-max` (default seven days) bounds what a
>   publish may ask for; more is refused with 400 rather than shortened, because a sender
>   that asked for a lifetime relies on it.

> **Amendment (2026-10-23): a subscription whose element no longer waits is no delivery.**
> "If it delivered to nothing — no subscription correlated" was read literally, and a
> subscription is correlated even when its element no longer waits: the losing branch of an
> event-based gateway and a boundary whose host finished leave their subscription behind,
> and the next matching send clears it (`deliverToSubscriptions`). Such a send counted as
> delivered, so a message with a time to live that only a left-behind subscription matched
> was not kept and was lost. It also wrote its payload into that instance, which may have
> moved on and now read values of a message it never received, and it left a message flow
> in the collaboration replay (ADR-0038) for a delivery that did not happen.
>
> - **Delivered means an element received it.** A correlation with a subscription whose
>   element instance is gone clears the subscription and nothing more: no payload, no
>   flow, no completion, and it does not count as delivered. A message with a time to live
>   that reached only such subscriptions is kept, as the record intended for a message
>   "nothing waits for".
> - **The same holds for a signal** (ADR-0088): a broadcast writes nothing into an
>   instance whose subscription was left behind.
> - **Replay is unchanged.** What earlier versions wrote stays on the log and replays as it
>   was; the rule changes what is written from this version on.
>
> Found while building the event log ([ADR-0472](0472-event-log.md)), which
> showed such a send as "reached 0" with no message kept. Tests:
> `engine/stale_subscription_test.go`.

> **As built (2026-10-13).** The local slice is built. `POST /api/v1/messages` and
> `atlas_publish_message` take `timeToLiveMs` and `messageId`; the engine's
> `PublishMessageWithTTL` buffers an unmatched message (`IntentMessageBuffered`), records
> the id of one delivered at once (`IntentMessageIdRecorded`), and `openMessageSubscription`
> (`engine/behavior.go`) hands the oldest live message to each subscription that opens
> (`IntentMessageConsumed`). The timer tick enqueues an expiry for every message past its
> time (`TriggerDueMessageExpiries`, `IntentMessageExpired`), and a message past its time
> is never delivered even before the sweep reaches it. The record rides `VTMessage`, the
> value type reserved for an incoming message, rather than a new `VTBufferedMessage`; the
> state lives in three families — the buffer, its expiry index and the id index
> (`state/message.go`). Tests: `engine/message_buffer_test.go` (including the recovery
> test this record asked for up front), `state/messagebuffer_test.go`,
> `api/messagebuffer_internal_test.go`.
>
> **What an operator sees.** `GET /api/v1/messages/buffered` (operator role, read off the
> loop per ADR-0239) lists what waits a page at a time, narrowed to a message name and,
> with it, a correlation key, and marks a message whose time is up but which the sweep
> has not reached. The MCP tool `atlas_buffered_messages` and the Console's Operations →
> Messages view read the same list. A message records when it was buffered
> (`MessageValue.BufferedAt`, appended to the record, zero on one written before it). The
> gauge `atlas_buffered_messages` reads a runtime total maintained in `applyToState` in
> ADR-0142's shape: up on `IntentMessageBuffered`, down when a delivery or an expiry
> removes a message that is still there, so an expiry after a delivery does not count it
> out twice; a store that buffered messages before the total existed is seeded once on
> open. Tests: `state/messagebuffer_test.go`, `state/messagebuffer_internal_test.go`,
> the count's replay in `engine/message_buffer_test.go`,
> `api/messagebuffer_internal_test.go`, `mcp/tools_operations_test.go`.
>
> **What is not built**, and keeps the record `Partial`: the breach of a commitment
> (amendment of 2026-09-17) has nothing to raise until ADR-0373 lets an entry point state
> one, and a message arriving from a peer is ADR-0372's. A model cannot yet give a throw
> or end event a time to live: only the API and MCP publish can.

## Context and problem statement

ADR-0020 shipped correlation without buffering, and said so plainly: *"a message
published before its subscriber exists is lost (a race the demo avoids by subscribing
first)"*. `correlateMessage` (`engine/behavior.go:2522`) collects matching
subscriptions, delivers to each, instantiates every matching message start — and if
nothing matched, returns. The message is gone. Buffering has been the first-listed
follow-up of that record ever since.

Inside one node that is a modelling constraint: one hop, the requester is already
parked at its reply catch, so the race is avoidable by construction. **Across a node
boundary it is guaranteed loss**, for three independent reasons:

1. Delivery is retried, and a retry that arrives before the receiving instance
   subscribes is discarded as silently as the first attempt.
2. The receiving instance may legitimately not exist yet — the peer is restarting,
   the definition is being redeployed, an upstream step has not finished.
3. Nothing tells the sender that the delivery evaporated, so there is no incident, no
   retry ceiling and nothing on any screen. It simply did not happen.

No amount of transport reliability fixes this: a delivery that is accepted over HTTP
and then dropped by `correlateMessage` has been acknowledged and lost. The receiving
side needs somewhere durable to put a message that has nowhere to go **yet**.

## Decision drivers

- **Invariants hold.** Buffering and later matching must be a deterministic,
  side-effect-free `applyToState` mutation from the event alone (I4); the message
  identity and timestamps are frozen into events (I6); the batch cycle allocates
  nothing per command (I1).
- **Reuse the established shape.** `cfMessageSubscription` keyed by length-prefixed
  `(name, key)` already makes "which subscriptions match?" a single prefix scan
  (ADR-0020). The buffer should be its mirror image, not a new mechanism.
- **Do not change behaviour under deployed models.** A model that today relies on an
  unmatched publish being a no-op must keep getting a no-op.
- **Bounded by construction.** ADR-0017 and ADR-0022 both shipped unbounded history
  and both list retention as an open concern. A buffer that grows without limit is
  worse than those, because it is on the correlation path.

## Considered options

1. **No buffer — the sender retries until the receiver is ready.** Retry as buffer.
2. **A durable inbound buffer** keyed by `(messageName, correlationKey, messageId)`,
   consulted on publish *and* on subscribe, with a TTL.
3. **Buffer in an external log** (clio, ADR-0036/0075) and let the engine read from it.

## Decision outcome

Chosen option: **2 — a durable inbound buffer with a TTL.**

### The mechanism

- A new value type `VTBufferedMessage` and a column family keyed by
  `(messageName, correlationKey, messageId)` — the same length-prefixed shape as
  `cfMessageSubscription`, so matching stays one prefix scan.
- **On publish**: `correlateMessage` runs unchanged. If it delivered to nothing —
  no subscription correlated and no message start instantiated — *and* the message
  carries a TTL greater than zero, one `IntentMessageBuffered` event is appended.
- **On subscribe**: when a catch event opens a subscription, the buffer is scanned
  for `(name, key)`. A hit correlates immediately through the existing path and
  appends `IntentBufferedMessageConsumed`, which removes it.
- **Idempotency**: a `messageId` already present in the buffer, or already consumed
  within the retention window, is not buffered or delivered twice. This is what makes
  the at-least-once delivery of ADR-0372 safe.
- **Expiry**: `expiresAt` from the envelope (ADR-0369)
  drives removal through a due-date index, the shape ADR-0146 already uses for history
  expiry. Expiry emits an event; it is never a clock read inside `applyToState` (I4).

### TTL defaults to zero, and that is the whole backwards-compatibility story

A locally published message has **TTL 0 unless it declares otherwise**, so it is not
buffered and ADR-0020's no-op semantics stand exactly as deployed models rely on
them. A message arriving from a peer always carries a TTL from its envelope, so it is
always bufferable.

This is deliberate and it is the reason this record does not supersede ADR-0020: the
old behaviour is not being corrected, a second mode is being added beside it, and a
model opts in by saying how long its message stays useful. "Forever" is not an option
the mode offers — a buffer entry without an end is a leak with a business meaning
attached.

### What an expiring message means

Expiry is **not** an incident by itself. A buffered broadcast that nobody consumed is
an ordinary outcome, and raising an incident for it would flood exactly the way
ADR-0337 describes. Whether a specific undelivered message deserves an incident is a
property of how it was *sent*, and is decided in
ADR-0372, where the sender's delivery mode lives.

### Consequences

- **Positive:** the oldest open follow-up of ADR-0020 closes. Cross-node delivery
  becomes safely retryable, because the receiver can accept a message before it can
  act on it and can recognise a duplicate. Local models gain an opt-in fix for the
  publish-before-subscribe race they currently have to model around.
- **Negative / trade-offs accepted:** a new column family, a new value type and a new
  encode/decode, plus a scan on every subscription open — a cost paid by every catch
  event, including those in models that never buffer anything. The dedup window binds
  retention to idempotency: shortening it to save space lengthens the window in which
  a late retry duplicates. And a buffered message correlates to a subscriber created
  *later*, which is a real semantic change for anyone who opts in — a message can now
  arrive "from the past".
- **Follow-ups / risks to watch:** the retention/compaction policy shared with
  ADR-0017 and ADR-0022 now has a third tenant, and this one affects correctness
  rather than only disk. Interaction with singleton message start (ADR-0094) needs a
  test written first: a buffered message must not start a second instance for a key
  that is already live. Interaction with a deactivated definition (ADR-0119) likewise
  — a message buffered while a definition is down and consumed after it returns is
  either the point of the feature or a surprise, and the record should say which.

## Pros and cons of the options

### Option 1 — retry as buffer
- Good: no new state on the receiver; the sender already has to retry anyway.
- Bad: the liability sits with the sender forever, and a receiver that will never
  subscribe turns a retry loop into sustained cross-node load. Without a receiver-side
  `messageId` index the retries also duplicate as soon as one succeeds ambiguously.
  It answers "the peer was down" and not "the instance does not exist yet", which is
  the more common case.

### Option 2 — durable inbound buffer (chosen)
- Good: mirrors the subscription index; one prefix scan; deterministic and replayable;
  makes idempotent delivery possible at all; opt-in, so nothing deployed changes.
- Bad: a new column family with its own retention and expiry; a scan added to every
  subscription open; "a message from the past" is a genuinely new semantic.

### Option 3 — external log
- Good: retention, ordering and replay are solved elsewhere, and clio is integrated.
- Bad: correlation state would live outside the engine log — the boundary ADR-0020
  rejected for the ADR-0001 reason, and rejecting it again here is consistent rather
  than new. It also makes a core correlation path depend on an optional component.

## Links

- closes the buffering follow-up of ADR-0020 (message events and correlation)
- relates to ADR-0035 (message start events) and ADR-0094 (singleton message start)
- required by ADR-0372 (at-least-once needs dedup)
- takes its envelope fields from ADR-0369
- borrows the expiry shape of ADR-0146; shares retention concerns with ADR-0017/0022
- relates to ADR-0337 (incident floods — why expiry is not an incident)
- honors the invariants in docs/architecture/invariants.md (I1, I4, I6)
