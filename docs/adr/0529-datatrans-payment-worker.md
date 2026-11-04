# ADR-0529: Card payments through a Datatrans Worker Type

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-04
- **Deciders:** atlas maintainers; the business owner of `zahlung-entgegennehmen` (not yet named)
- **Open question:** Three provider facts this record depends on were not checked against Datatrans's current documentation: which endpoints accept an idempotency key and how long Datatrans remembers it, how webhook calls are signed, and the field limits (for example the length of `refno`). Section 3 says what the Worker does if no key is honoured; sections 4 and 5 assume the webhook is signed with a shared secret. If any of these is wrong, the affected section changes before acceptance.
- **Question checked:** 2026-11

## Context and problem statement

External customers will pay for orders by credit card and TWINT. The payment service provider (PSP) is Datatrans. Atlas has no Worker Type for payments. The only payment in the tree is the `zahlung.eingegangen` message in `examples/mahnwesen`, which comes in from outside.

A card payment differs from every integration Atlas has so far, in four ways that follow from the engine itself:

1. **Everything a process holds is kept.** A variable is written into an event, the event stays in the log, and replay restores it (I2, I4, I6). A card number that reaches a variable once is in the WAL, the checkpoints, the instance views and every export for as long as the history exists. That would bring all of Atlas into PCI DSS scope, and it cannot be undone after the fact.
2. **A job runs at least once.** The `LeaseEpoch` fence ([ADR-0007](0007-job-worker-protocol.md)) refuses the outcome of a round whose lease expired; it does not undo that round's call to the provider. [ADR-0299](0299-worker-type-admission-criteria.md) gate 3 names this. For a capture or a refund, a second execution is a second booking.
3. **The outcome is asynchronous.** With 3-D Secure, TWINT or any redirect, the customer pays on a page that is not Atlas's, and the result arrives later as a webhook. A service task cannot wait for it.
4. **Providers are not interchangeable.** Partial and multiple captures, how long an authorization lasts, refund windows and status models differ between PSPs. This is the same finding [ADR-0480](0480-technical-capability-contract.md) made for Entra, LDAP and Google Contacts.

**Question:** How does a process take, capture, release and refund a card payment through Datatrans without card data entering Atlas and without a retried job booking twice?

## Decision drivers

- No card data (PAN, CVC, expiry, full magnetic or chip data) in any variable, event, log line or job. The target is PCI DSS SAQ A for the merchant: the card form is the PSP's.
- A retried job must not move money twice.
- The six invariants hold. Nothing in the processor, `applyToState` or the WAL format changes.
- The credential lives in the vault and nowhere else ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md), [ADR-0168](0168-connector-work-on-a-worker.md)).
- No claim that Datatrans operations are equivalent to another PSP's (ADR-0480, [ADR-0485](0485-technical-capability-semantics.md)).
- A Worker Type is admitted on ADR-0299's five gates, not on convenience.

## Considered options

1. **One generic "payment" Worker Type** that normalizes several PSPs behind one set of operations.
2. **The REST Worker Type plus element templates** for the Datatrans API.
3. **A Datatrans Worker Type with a short, closed list of operations**, using the PSP's hosted payment page.
4. **A card form served by Atlas** (for example in a public start form), tokenized by Atlas or forwarded to the PSP.

## Decision outcome

Proposed: **option 3, a Datatrans Worker Type**, with the PSP's hosted payment page (redirect or lightbox) as the only place a card is entered.

### 1. Card data never reaches Atlas

The customer enters card details only on Datatrans's payment page. Atlas sees the amount, the currency, the merchant reference, the Datatrans transaction ID, a status, and the URL of the payment page.

The technical contracts enforce this, not only describe it. Every input and output schema is closed (`additionalProperties: false`), and there is no card field in any of them. `workertype/payment_fixtures_test.go` checks that each contract refuses `cardNumber`, `pan`, `cvv`, `cvc`, `expiry` and a nested `card` object in both directions.

Integration modes that place card fields in a page Atlas serves (Datatrans Secure Fields or anything like it) are **not** covered by this record. Whether they keep SAQ A is a question for the PCI assessor, not for this design.

### 2. Operations

Proposed job type: `io.atlas.datatrans`. Operations:

| Operation | What the process does | Technical capability claim | Moves money |
|---|---|---|---|
| `create-checkout` | Opens a payment session and returns the page URL | `io.atlas.payment.checkout.create@1.0.0` | yes, when `captureMode` is `automatic` |
| `get-transaction` | Reads the state of a transaction | `io.atlas.payment.transaction.read@1.0.0` | no |
| `capture` | Captures an authorized amount, in full or in part | `io.atlas.payment.transaction.capture@1.0.0` | yes |
| `cancel` | Releases an authorization that was not captured | `io.atlas.payment.transaction.cancel@1.0.0` | no |
| `refund` | Refunds a captured amount, in full or in part | `io.atlas.payment.refund.create@1.0.0` | yes |

Every claim uses the profile `datatrans`. Statuses are mapped to five values (`pending`, `authorized`, `captured`, `canceled`, `failed`); Datatrans's own status names do not pass through. Amounts are whole minor units (`4990` for CHF 49.90), and currencies are ISO 4217 codes.

**Left out of the first version:** stored cards and recurring payments (alias handling), subscriptions, payouts, chargebacks and the Datatrans reconciliation reports. Each has a different data and risk profile. Chargebacks stay manual in the Datatrans merchant portal; the business capability `zahlungsstreitfall-bearbeiten` records that.

### 3. Idempotency

`create-checkout`, `capture` and `refund` require an `idempotencyKey` of 16 to 64 characters. The contract declares them `conditional`: idempotent given the key.

- **The key is derived, not generated.** It is built from the process instance key, the element and, inside a multi-instance body, the loop counter. All of these are already frozen in the job (I6), so every retry of the same job carries the same key, and replay never produces a new one.
- **The Worker passes the key to Datatrans** on every endpoint that accepts one.
- **Where Datatrans honours no key** (see *Open question*), the Worker reads the transaction first and treats an existing capture or refund for the same reference and amount as the result, instead of calling again. This narrows the window but does not close it: two Worker Instances that both read before either writes can still both book. If this is the case, a capture or refund must run with a lease long enough that a second round cannot start while the first is in flight, and the record must say so before acceptance.

`cancel` is `idempotent`: the Worker reports a transaction that is already canceled as success. `get-transaction` is `read-only`.

### 4. The asynchronous result

The process models the wait. A sketch:

```text
service task  create-checkout        → transactionId, paymentPageUrl
send task     payment link to the customer (portal, mail)
event-based gateway
  ├─ message  zahlung.autorisiert    (correlation: merchantReference)
  └─ timer    session lifetime       → get-transaction → failed / canceled
service task  get-transaction        (the webhook triggers; the API decides)
service task  capture                (only on status authorized)
```

The webhook triggers the next step, but it is not the evidence for it. Before `capture`, the process reads the transaction through the authenticated API. A forged or replayed webhook can therefore at most cause an unnecessary read; it cannot cause a capture.

**Gap:** Atlas has per-kind inbound watches (Jira, Google, Discord), but this record did not find a generic ingress that verifies a webhook signature and publishes a message. Until one exists, the process polls with `get-transaction` on a timer. Webhook ingress is a separate decision; it should not be built into this Worker Type.

### 5. Credential

There is one credential shape: the Datatrans merchant ID and its API password (HTTP Basic), plus the environment (test or production) as Worker configuration. The webhook signing key is a second secret held by the same Worker; that is the "tolerable" case in ADR-0299 gate 4. Both live in the vault under the Worker's name. Neither appears in a model, a variable or an event.

### 6. Admission against ADR-0299

| Gate | Assessment |
|---|---|
| 1 — operations are process steps | Passes. Five verbs that appear on a BPMN task. |
| 2 — generic path blocked or lossy | **Lossy, not blocked.** Basic authentication is in reach of the REST Worker Type (`connector/rest/worker.go`). The argument is that every model would otherwise repeat the derivation of the idempotency key, the conversion to minor units, the status mapping and the refusal of card fields, and a mistake shows up as a wrong booking, not as a 404. A reviewer may judge this insufficient; then option 2 applies, with element templates. |
| 3 — the work fits a job | Passes under section 3. Each call is short; results are small. |
| 4 — one credential shape, server-side | Passes, see section 5. |
| 5 — arrives complete | Owed by the implementing change: `managedConnectorKind`, the `api/releasedkinds.go` row, the Repository package, the setup entry and the handbook card. |

### Consequences

- **Positive:** Atlas stays out of PCI DSS cardholder-data scope by construction, and the contract tests keep it that way.
- **Positive:** A retry cannot book twice wherever Datatrans honours the key; elsewhere the remaining risk is named.
- **Positive:** The technical claims connect to the business capabilities `zahlung-entgegennehmen` and `zahlung-rueckerstatten` without asserting that another PSP is a drop-in replacement.
- **Negative:** A second PSP needs its own Worker Type and its own profile. That is deliberate.
- **Negative:** Without webhook ingress, a payment's completion is noticed only as fast as the polling timer.
- **Follow-ups:** a record on signed webhook ingress; checking the three facts in *Open question*; reconciliation (`zahlungseingang-abgleichen`) as its own design; conformance evidence for the five contracts under [ADR-0483](0483-technical-capability-conformance-governance.md) before any claim is marked verified.

## Pros and cons of the options

### Option 1 — One generic payment Worker Type

- Good: a process author models "take payment" once, and switching PSP is configuration. That is valuable at the moment of a PSP change.
- Bad: it claims an equivalence nobody has shown. Captures, authorization lifetimes, refunds and statuses differ, and the abstraction either hides those differences or grows a provider-specific escape hatch for each. ADR-0480 rejected the same shape for directories.

### Option 2 — REST Worker Type plus element templates

- Good: no new code, and the REST Worker already reaches Datatrans with Basic authentication and custom headers.
- Bad: nothing validates what a model sends. Each model would derive its own idempotency key, convert amounts and map statuses, and nothing would stop a model from putting card data into a request body. The failure mode is money, not an incident.

### Option 3 — A Datatrans Worker Type (proposed)

- Good: card data and idempotency are enforced in one place and tested. The operations are named, and the claims are versioned.
- Bad: one more kind to own, with gate 2 resting on the lossy limb only.

### Option 4 — A card form served by Atlas

- Good: a seamless page for the customer.
- Bad: Atlas would receive or render cardholder data and enter PCI DSS scope, with its event log in that scope permanently. Refused.

## Links

- [ADR-0007 — Job worker protocol](0007-job-worker-protocol.md)
- [ADR-0069 — Engine-internal encrypted secret vault](0069-engine-internal-encrypted-secret-vault.md)
- [ADR-0168 — Connector work on a Worker](0168-connector-work-on-a-worker.md)
- [ADR-0299 — Worker Type admission criteria](0299-worker-type-admission-criteria.md)
- [ADR-0480 — Technical capability claims](0480-technical-capability-contract.md)
- [ADR-0483 — Technical capability conformance governance](0483-technical-capability-conformance-governance.md)
- [ADR-0485 — Technical capability semantics](0485-technical-capability-semantics.md)
- Contract fixtures: `workertype/testdata/technical-payment-*.json`
- Business capabilities on the reference installation: `zahlung-entgegennehmen`, `zahlung-rueckerstatten`, `zahlungseingang-abgleichen`, `forderung-mahnen`, `zahlungsstreitfall-bearbeiten`
