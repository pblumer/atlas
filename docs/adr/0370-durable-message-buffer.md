# ADR-0370: A published message waits for its subscriber

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers

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
