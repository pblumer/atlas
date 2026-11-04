# ADR-DRAFT: Signed webhook ingress — a provider pushes, Atlas verifies and publishes a message

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-04
- **Deciders:** atlas maintainers
- **Open question:** Three provider facts are not settled. (1) How long Datatrans keeps retrying a webhook that was not answered with 2xx. Section 5 sets the duplicate window to the message's time to live, so the endpoint's TTL must be at least that long; the number is unknown. (2) Datatrans's exact signature scheme (header name, signed string, timestamp format). Section 3 assumes an HMAC-SHA256 over a timestamp and the raw body, which is the common shape but has not been checked against current Datatrans documentation. (3) Whether a provider stops delivering after a run of 4xx answers. Section 4 answers an unknown endpoint with the same 401 as a bad signature, and some providers disable a webhook that keeps failing.
- **Question checked:** 2026-11

## Context and problem statement

Some systems report what happened by calling a URL. A PSP calls when a payment is authorized ([ADR-0529](0529-datatrans-payment-worker.md)), Jira calls when an issue changes, and many SaaS products work the same way. Atlas has no route such a system can call.

Today Atlas learns about outside events in three ways, and none of them fits:

- **Polled watches** ([ADR-0214](0214-jira-inbound-issue-watch.md), [ADR-0234](0234-google-inbound-watch.md), [ADR-0262](0262-discord-inbound-watch.md)) read a source on a timer. They need a source that can be listed in order. A PSP has no such list for one payment; the process can only ask about one transaction at a time, on a timer of its own (ADR-0529 §4).
- **`POST /api/v1/messages`** publishes a message, but it needs an `operator` bearer token. ADR-0214 option 3 lists why that is not a product: the token reaches far more than this one route, it lives outside the vault, and the wiring is invisible from Atlas. A PSP also does not send a bearer token. It signs its request with a secret.
- **Public start links** ([ADR-0029](0029-public-process-start-links.md)) accept a form submission and start a process. They do not correlate to a waiting instance, and they authenticate nobody.

ADR-0214 considered a webhook receiver for Jira and kept polling, because many installations cannot be reached from the provider's cloud. It named the receiver as a follow-up that is **additive**: it does not replace polling, and it still needs the per-event idempotency polling already has. That reasoning stands. This record adds the receiver for installations that can be reached, and it does not remove anything.

**Question:** How does Atlas accept a call from an outside system, establish that the system sent it, and turn it into exactly one message, without opening a broad public write path and without keeping the request's content beyond what a process needs?

## Decision drivers

- **Authenticity is a signature, not a token.** Providers sign with a shared secret (usually HMAC-SHA256) or with a key pair (for example Ed25519). Atlas checks what the provider sends. It cannot ask the provider to fetch an OAuth token ([ADR-0512](0512-connected-systems.md)).
- **Durable before visible (I2).** The provider retries until it gets a 2xx. Atlas answers 2xx only after the message is on disk. Otherwise a crash between the answer and the fsync loses the event, and the provider never retries.
- **At-least-once in, exactly-once into processes.** Providers deliver at least once and out of order. Duplicates must be recognized by id, because a webhook carries no sequence a high-water mark ([ADR-0075](0075-clio-inbound-event-bridge.md)) could use.
- **Nothing new in the engine.** The engine already publishes a message with a `messageId` and a time to live, and already treats a second message with the same id as a no-op while the first one lives ([ADR-0370](0370-durable-message-buffer.md), *As built*). This record should need no new event type and no change to `applyToState` (I4).
- **One narrow public door.** A public route is a new access class ([ADR-0199](0199-route-access-classes.md)). It must accept one thing and reveal nothing.
- **The request is untrusted data.** A valid signature proves who sent it. It does not prove that what it says is final. A process that acts on money or access re-reads the fact through an authenticated API (ADR-0529 §4).
- **Data minimization.** Payloads may hold personal data or truncated card numbers. Only fields the mapping names may reach a variable. The raw body is never stored or logged.
- **Signing secrets live in the vault** ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md), [ADR-0168](0168-connector-work-on-a-worker.md)), never in a model, a variable or an event.

## Considered options

1. **Polling only.** Every push provider gets a polled watch or a per-instance status timer.
2. **The provider calls `POST /api/v1/messages`** with an Atlas credential.
3. **A generic signed endpoint**, bound to a configured Worker, verified by a scheme the Worker Type supplies.
4. **A webhook receiver per provider**, each in its connector package with its own route.
5. **An external relay** outside Atlas that verifies the signature and forwards through an authenticated API.

## Decision outcome

Proposed: **option 3, a generic signed endpoint**. Each verification scheme comes from a closed list that the Worker Type supplies. It is never assembled from configuration or a model. Polling stays the default; option 5 remains a valid way to deploy.

### 1. An endpoint is a record bound to a Worker

An operator creates a **webhook endpoint**. It holds:

| Field | Meaning |
|---|---|
| `id` | Opaque, random, 128 bits. It is part of the URL. It identifies the endpoint; it does not authenticate the caller. |
| `workerId` | The configured Worker whose vault entry holds the signing secret and whose kind supplies the verifier. |
| `messageName` | The message published for each accepted call. |
| `correlationKey` | A FEEL expression over the parsed body, compiled when the endpoint is saved (I5). |
| `variables` | A map from variable name to FEEL expression over the body. Only these values become process variables. |
| `deliveryId` | A FEEL expression naming the provider's own event id. If the provider sends none, the scheme supplies a fallback, see section 5. |
| `timeToLiveMs` | How long an unmatched message waits for its subscriber, and how long a retry of it is recognized as a duplicate (ADR-0370, section 5). The verifier declares its provider's retry horizon as the minimum; the default is that minimum, never less than 24 hours, and never more than `--message-ttl-max` (168 hours by default). |
| `maxPerHour` | The hourly budget of [ADR-0225](0225-inbound-watch-budget.md), applied exactly as for a polled watch. |
| `enabled` | An endpoint can be switched off without deleting it. |

The record lives in a sidecar store beside `inboundSubStore`. It is design-time configuration, not engine state.

Saving the record checks everything that can be checked before the first call:
- it compiles every expression;
- it asks the Worker's kind for its verifier, and refuses a kind with no verifier;
- it refuses a `timeToLiveMs` below the verifier's retry horizon or above `--message-ttl-max`;
- it applies the refusals `POST /api/v1/messages` already makes. A message that a catalogue product's action starts or waits at is refused (ADR-0429), because the order sends that message itself. A variable that a deployed process declares personal is refused, because a message with a TTL may be kept.

### 2. The route

```text
POST /hooks/{endpointId}
```

The route is mounted with `accessPublic` through `mount` (ADR-0199). It is outside `/api/v1`, so the authenticated API keeps its assumptions. It answers only `POST`, and it has no read, list or enumerate counterpart.

The handler works in this order. Each step is cheaper than the next, so a flood is turned away before it costs anything:

1. **Rate limit**, per endpoint id and per client address, with the existing `rateLimiter` (`api/ratelimit.go`). Over the limit: `429`.
2. **Size cap**, `http.MaxBytesReader` at 64 KiB by default, configurable per installation. Over the cap: `413`.
3. **Look up the endpoint.** Unknown or disabled: `401`, the same answer as a bad signature (step 4), so the route does not reveal which ids exist.
4. **Verify the signature** over the raw bytes as received, before any parsing. The comparison is constant-time. If the scheme carries a timestamp, a timestamp more than five minutes away from the server clock is refused. Failure: `401`, with no detail in the body.
5. **Parse and map.** The body must be JSON. The compiled expressions produce the correlation key, the variables and the delivery id. If parsing or mapping fails, or if the publish is refused for a reason that arose after the endpoint was saved (for example, a process deployed since then declares a mapped variable personal): `422`. The operator sees the reason in the endpoint's health; the caller does not.
6. **Publish** on the run loop with `messageId`, `timeToLiveMs` and the mapped variables, through the same path `POST /api/v1/messages` uses (`PublishMessageWithTTL`).
7. **Answer after the fsync:** `204` for a new message, also `204` for a duplicate (section 5). If the engine is not ready to write: `503`, and the provider retries.

The raw body is discarded after step 5. It is not written to a variable, an event, the request log or an error message.

### 3. Verification is code that ships with the Worker Type

A Worker Type that can receive webhooks implements one verifier:

```go
// Verify reports whether raw was sent by the holder of secret. It must not
// parse raw beyond what the scheme signs, and must compare in constant time.
type WebhookVerifier interface {
    Verify(header http.Header, raw []byte, secret []byte, now time.Time) (deliveryID string, err error)
}
```

The verifier for a kind is written, reviewed and tested with that kind. The scheme is not configurable data, such as a header name plus an algorithm chosen in a form. A configurable scheme invites `none`, a weak hash, or a signature computed over parsed JSON instead of the bytes received. These are the usual ways signature checks fail.

The first verifier is Datatrans's. *Assumed, see Open question:* an HMAC-SHA256 over the timestamp and the raw body, keyed with the webhook signing key from the Datatrans Worker's vault entry.

**Rotation:** the vault entry may hold a current and a previous signing secret. Both are accepted until the operator removes the previous one, so a provider can switch keys without a gap.

### 4. Answers reveal nothing

All authentication failures look alike: `401` with an empty body, whether the id is unknown, the endpoint is disabled, the signature is wrong or the timestamp is stale. The difference is recorded where an operator can act on it: in the endpoint's health, beside the polled watches' health (`api/inboundhealth.go`). Health shows the last accepted call, the last refused call with its reason, and counts per reason. It never shows a body.

### 5. Duplicates are recognized by id

The engine's `messageId` is `webhook:` + endpoint id + `:` + the delivery id. A provider retry carries the same delivery id, and the engine refuses a second message with that id (ADR-0370). The handler still answers `204`, because the event is already durable and a non-2xx answer would only make the provider retry again.

If a provider sends no event id, the scheme's fallback is a SHA-256 of the signed bytes. That recognizes a byte-identical retry. Two distinct events with identical bodies would collapse into one. A provider with that property needs a scheme that says so explicitly.

**The duplicate window is the message's time to live.** The engine treats a repeat of the same name, key and id as a no-op "for as long as the first one lives, delivered or not" (`PublishMessageWithTTL`, `engine/processor.go`). A retry that arrives after the TTL has run out is a new message. This is why section 1 ties the TTL to the provider's retry horizon instead of leaving it to taste.

### 6. The message is a trigger, not a proof

What the ingress establishes is: *the holder of this endpoint's secret sent this body, recently, once*. It does not establish that the content is final, complete or the latest. Providers send events out of order, and a signing secret can leak.

A model that acts on money, access or other side effects with consequences reads the fact again through the Worker's authenticated API before acting. This is ADR-0529 §4, stated here as the general rule. This record does not enforce it; process authors and reviewers must follow it.

### 7. What does not change

- The engine, its events, `applyToState`, the WAL and recovery. The publish is the existing one, and no recovery test is needed beyond those ADR-0370 already has.
- Polled watches. An installation that cannot be reached keeps polling, as ADR-0214 decided.
- `POST /api/v1/messages` and its authentication.

### 8. Surfaces

- **HTTP:** `GET/POST /api/v1/webhook-endpoints` and `GET/PUT/DELETE /api/v1/webhook-endpoints/{id}`, with the same role that manages inbound subscriptions. The public route `POST /hooks/{endpointId}` is declared in the route table as public.
- **MCP:** listing and reading endpoints and their health is a tool. Creating, changing and deleting are tools for the same role, limited server-side as for inbound subscriptions. The signing secret is never returned by any surface; it is set on the Worker, not on the endpoint.
- **Console:** the endpoint shows its full public URL, built from the installation's configured external address, ready to paste into the provider's settings.

### Consequences

- **Positive:** Push providers deliver in seconds, without a polling timer per instance and without API quota spent on asking.
- **Positive:** The engine is untouched. Durability, deduplication and buffering are the ones already built and tested.
- **Positive:** Data minimization is structural: only mapped fields become variables, and the raw body has no place to be kept.
- **Negative:** A public write route exists. Rate limits, a size cap and uniform `401`s narrow it, but an internet-facing installation should put it behind a reverse proxy, as ADR-0029 and `/mcp` already expect.
- **Negative:** Deduplication lasts only as long as the TTL. A provider that retries longer than `--message-ttl-max` allows cannot be deduplicated fully by this route, and its verifier must say so.
- **Negative:** Mapped variables cannot be ones a deployed process declares personal, because a message with a TTL may be kept. A webhook that must carry personal data into a process needs a different design, for example carrying only an id that the process resolves through the Worker's API.
- **Negative:** Each provider needs a verifier written in Go. That is intentional, and it means a new provider is a code change, not a configuration change.
- **Follow-ups:**
  - Check Datatrans's retry horizon and signature scheme (Open questions 1 and 2) before the first verifier is written.
  - Decide whether a Jira verifier follows, as ADR-0214's option 2 had in mind.
  - The relay of option 5 can use the same mapping record later, if a firewalled installation asks for it.

## Pros and cons of the options

### Option 1 — Polling only

- Good: no public surface at all, and it works behind any firewall. Every piece exists.
- Good: the strongest form of the argument: since the process must re-read the fact anyway (section 6), a webhook only saves latency, and latency is rarely what a business process lacks.
- Bad: a payment has no feed to follow. Each waiting instance polls its own transaction, so the load grows with open payments and the latency is the timer's. For a customer waiting on a confirmation page, that latency is the experience.
- Bad: it does not scale to providers that offer no read API for the event at all.

### Option 2 — The provider calls `POST /api/v1/messages`

- Good: no Atlas change.
- Bad: providers sign; they do not send bearer tokens. Where a provider allows a custom header, the token is an `operator` token that reaches far more than this route, stored in the provider's console outside the vault. ADR-0214 already rejected this as a product.

### Option 3 — A generic signed endpoint (proposed)

- Good: one route, one durability path, one deduplication path. The provider-specific part is the verifier, which is small and testable.
- Bad: a public write route, and a verifier per provider.

### Option 4 — A receiver per provider

- Good: each receiver can parse its provider's payload in Go with full type checking, instead of through FEEL mappings.
- Bad: every receiver repeats the rate limit, size cap, uniform answers, fsync-before-answer and deduplication. Those are exactly the parts where a mistake is a security or durability defect. Option 3 keeps the provider-specific part to verification and leaves the rest shared.

### Option 5 — An external relay

- Good: Atlas never faces the internet. The relay verifies and forwards through an authenticated, narrow credential (ADR-0512).
- Good: for firewalled installations, this is the only push option, and the strongest argument against building anything inside Atlas.
- Bad: another component to deploy, secure and monitor. The signing secret leaves the vault. Deduplication and fsync-before-answer must be reimplemented in the relay or lost.
- Kept as a deployment pattern, not as the product answer.

## Links

- [ADR-0020 — Message events and correlation](0020-message-correlation.md)
- [ADR-0029 — Public process start links](0029-public-process-start-links.md)
- [ADR-0069 — Engine-internal encrypted secret vault](0069-engine-internal-encrypted-secret-vault.md)
- [ADR-0075 — clio inbound event bridge](0075-clio-inbound-event-bridge.md)
- [ADR-0168 — Connector work on a Worker](0168-connector-work-on-a-worker.md)
- [ADR-0199 — Every mounted route declares its access class](0199-route-access-classes.md)
- [ADR-0214 — Jira inbound issue watch](0214-jira-inbound-issue-watch.md)
- [ADR-0225 — Inbound watch budget](0225-inbound-watch-budget.md)
- [ADR-0370 — Durable message buffer](0370-durable-message-buffer.md)
- [ADR-0429 — Product actions are commands with published outcomes](0429-product-actions-are-commands-with-published-outcomes.md)
- [ADR-0512 — Connected systems](0512-connected-systems.md)
- [ADR-0529 — Datatrans payment Worker](0529-datatrans-payment-worker.md)
