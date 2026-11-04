# ADR-0527: A capability vocabulary for CRM, ERP and finance, with idempotent writes as its admission rule

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-04
- **Deciders:** atlas maintainers
- **Open question:** Whether the systems this vocabulary is meant for (SAP S/4HANA, Microsoft
  Dynamics 365, Abacus, bexio, Salesforce, HubSpot) can each hold an external reference
  of up to 64 characters in a field the provider itself keeps unique, per object kind
  below, is not verified for any of them. The write contracts rest on it: a provider
  that cannot is left without a verifiable write claim (section 3, rule I5), not given
  a weaker contract.
- **Question checked:** 2026-11

## Context and problem statement

atlas has no Worker Type, no Repository package and no technical contract for a CRM, an
ERP or an accounting system. The status report of 2026-10-10 (§7.2) settled the vendor
question only in part: Salesforce is to be a package over the REST Worker Type, Abacus is
deferred, and SAP over RFC/BAPI is blocked by the no-CGO rule. Under
[ADR-0299](0299-worker-type-admission-criteria.md) most of these systems fail gate 2,
because the REST Worker Type already reaches them with Basic, Bearer, API key or OAuth2
client credentials ([ADR-0152](0152-rest-connector-oauth2.md)).

What none of those answers addresses is ADR-0299's gate 3, question 2: *is running it
twice safe?* A job is executed at least once and completed at most once. The `LeaseEpoch`
fence ([ADR-0007](0007-job-worker-protocol.md), [ADR-0274](0274-in-process-job-leases.md))
rejects the *outcome* of a round whose lease elapsed; the side effect of that round has
already happened. Two shipped Worker Types say so in their package documentation: Jira
([`connector/jira/client.go`](../../connector/jira/client.go)) and Remedy
([`connector/remedy/client.go`](../../connector/remedy/client.go)) can each create a
duplicate on replay, send the job key as an `X-Request-ID` for a downstream de-duplicator,
and leave a real idempotency key as a follow-up.

For a ticket, a duplicate is noise somebody closes. For a receivable invoice or a journal
entry, a duplicate is a wrong ledger: the customer is claimed twice, the tax on the
invoice is declared twice, and the bookkeeping rules atlas's users work under generally
require a posted entry to be corrected by a traceable reversal rather than deleted. The
cost of a duplicate is not the same across these domains, and the existing contract
envelope does not make the difference visible to anyone: [ADR-0485](0485-technical-capability-semantics.md)
admits `idempotency: non-idempotent`, which is honest and leaves every model to protect
itself.

A second problem is naming. [ADR-0480](0480-technical-capability-contract.md) exists
because similar verbs on similar-looking objects are not the same operation. The contract
fixture `io.atlas.identity.account.create` already uses the resource kind `account` for an
authentication account. A CRM "account" is a business partner. Using the same word would
build exactly the false equivalence ADR-0480 was written to prevent.

The question this record answers is therefore: **which technical capabilities does atlas
name for CRM, ERP and finance, and what must a write in that vocabulary guarantee when it is
run twice?**

## Decision drivers

- **Gate 1 of ADR-0299.** Each operation is a step a business process takes, from a short
  closed list. "Update any field of any object" is the REST Worker Type, not a capability.
- **The cost of a duplicate.** In finance a duplicate is a statutory correction, not a
  cleanup. The vocabulary must make a duplicate impossible by contract, not unlikely by care.
- **At-least-once stays.** The engine's delivery guarantee (ADR-0007) is correct and is not
  reopened here. Exactly-once across an external side effect is not something the engine
  can provide on its own; only the receiving system can refuse the second write.
- **Business identity belongs to the model.** Whether two writes describe the same fact
  ("invoice for order 4711") or two facts ("the first and second instalment") is known to
  the process author, not to the engine.
- **No invariant moves.** Everything here is design-time metadata in the `workertype`
  package. Nothing in the processor, the job protocol, events, the WAL or `applyToState`
  changes (invariants 1–6; I5 in particular).
- **No equivalence and no routing.** Claims stay descriptive ([ADR-0480](0480-technical-capability-contract.md),
  [ADR-0482](0482-capability-aware-binding-and-routing.md)); verification follows
  [ADR-0483](0483-technical-capability-conformance-governance.md).
- **Provider-neutral targets.** A Worker Type admitted under ADR-0299 (for example SAP
  S/4HANA over OData) and a Repository package over the REST Worker Type (for example
  Salesforce) should be able to claim the same contract.

## Considered options

1. **No vocabulary.** Each Worker Type or package names its own operations, as today.
2. **A vocabulary with idempotency declared per contract.** ADR-0485's envelope as it
   stands; a create may be `non-idempotent`.
3. **A vocabulary whose write contracts must be idempotent through a model-supplied
   external reference, checked when a contract is admitted.**
4. **An engine-derived idempotency key on every job.** The engine computes a stable key
   from the instance and the element and hands it to the Worker.

## Decision outcome

Chosen: **option 3**.

### 1. Namespaces, scopes and resource kinds

| Namespace | `identityScope` | Resource kinds |
|---|---|---|
| `io.atlas.crm` | `tenant` | `crm-organization`, `crm-contact`, `crm-opportunity`, `crm-case`, `crm-activity` |
| `io.atlas.erp` | `company` | `erp-customer`, `erp-supplier`, `erp-sales-order`, `erp-purchase-order`, `erp-item` |
| `io.atlas.finance` | `company` | `finance-receivable-invoice`, `finance-payable-invoice`, `finance-journal-entry`, `finance-open-item` |

`company` is the legal entity whose books are kept (a client, *Mandant*, company code),
not the provider's tenant. Every resource kind carries its domain as a prefix on purpose: a
CRM organisation, an ERP customer and a debtor in the books are three objects with
different obligations (payment terms, a control account, a consent record), and this
record claims no equivalence between them. The bare word `account` is not used.

### 2. The vocabulary, version 1

A closed list. A contract not on it is not part of this vocabulary until a later version
of this record adds it.

| Contract id | Resource kind | Verb | `idempotency` | The step |
|---|---|---|---|---|
| `io.atlas.crm.organization.upsert` | `crm-organization` | `upsert` | `idempotent` | Create or update an organisation by its reference |
| `io.atlas.crm.contact.upsert` | `crm-contact` | `upsert` | `idempotent` | Create or update a person at an organisation |
| `io.atlas.crm.opportunity.stage.set` | `crm-opportunity` | `set-stage` | `idempotent` | Move an opportunity to a named stage |
| `io.atlas.crm.case.create` | `crm-case` | `create` | `conditional` | Open a customer case |
| `io.atlas.crm.activity.record` | `crm-activity` | `record` | `conditional` | Record an interaction on an organisation or contact |
| `io.atlas.erp.customer.upsert` | `erp-customer` | `upsert` | `idempotent` | Create or update customer master data |
| `io.atlas.erp.supplier.upsert` | `erp-supplier` | `upsert` | `idempotent` | Create or update supplier master data |
| `io.atlas.erp.sales-order.create` | `erp-sales-order` | `create` | `conditional` | Enter a sales order with its lines |
| `io.atlas.erp.sales-order.status.get` | `erp-sales-order` | `get` | `read-only` | Read an order's fulfilment status |
| `io.atlas.erp.purchase-order.create` | `erp-purchase-order` | `create` | `conditional` | Enter a purchase order with its lines |
| `io.atlas.erp.item.availability.get` | `erp-item` | `get` | `read-only` | Read available quantity for an item |
| `io.atlas.finance.receivable-invoice.create` | `finance-receivable-invoice` | `create` | `conditional` | Create an **unposted** customer invoice |
| `io.atlas.finance.receivable-invoice.post` | `finance-receivable-invoice` | `post` | `idempotent` | Post it to the ledger |
| `io.atlas.finance.receivable-invoice.status.get` | `finance-receivable-invoice` | `get` | `read-only` | Read open amount and payment state |
| `io.atlas.finance.payable-invoice.register` | `finance-payable-invoice` | `register` | `conditional` | Register a supplier invoice, unposted and unreleased |
| `io.atlas.finance.payable-invoice.release` | `finance-payable-invoice` | `release` | `idempotent` | Release it for payment |
| `io.atlas.finance.journal-entry.post` | `finance-journal-entry` | `post` | `conditional` | Post a balanced manual entry |
| `io.atlas.finance.open-item.find` | `finance-open-item` | `find` | `read-only` | List a partner's open items (`cursor`, `maxResults` 200) |

**Create and post are separate on purpose.** Posting is the step that cannot be undone by
deletion. Separating it lets a process place an approval user task between the two, and
it makes posting idempotent by state: posting an invoice that is already posted changes
nothing. A provider whose API can only create an invoice already posted cannot claim
`receivable-invoice.create` with the empty profile; it claims it under the profile
`posted-on-create`, which is a different contract identity under ADR-0485.

**Deliberately absent from version 1:** deleting anything; credit notes and reversals,
whose semantics differ too much between systems to name without evidence; executing
payments, which belongs to banking, not to the books; computing tax; and maintaining the
chart of accounts.

### 3. The idempotency rule

**I1 — Admission.** Every contract in `io.atlas.crm`, `io.atlas.erp` and
`io.atlas.finance` declares `idempotency` as `read-only`, `idempotent` or `conditional`. A
contract in these namespaces declaring `non-idempotent` or `unknown` is refused when it is
parsed, at design time, in `workertype`. Contracts in other namespaces are unaffected.

**I2 — Meaning in these namespaces.**
- `idempotent`: the operation sets a state or upserts by key. Repeating it with the same
  input leaves the provider in the state the first run left it in.
- `conditional`: the operation creates an object. A second call carrying an
  `externalReference` that already exists in the same scope does not create a second
  object.

If ADR-0485 or its successor defines `conditional` for all namespaces, this record follows
that definition and is amended.

**I3 — The external reference is the model's, and it is required.** The input schema of
every contract that is not `read-only` requires `externalReference`: a string of 1 to 64
characters matching `^[A-Za-z0-9][A-Za-z0-9._:/-]*$`. It names the *business fact* (the
order number, the case number, the instance's own business key), and it is unique per
resource kind within one `identityScope` (per company for invoices and orders, per tenant
for CRM objects). `post`, `release` and `set-stage` address their target by the provider's
identifier or by its `externalReference`. A provider mapping that cannot store the whole
reference refuses it with `REFERENCE_UNSUPPORTED`. It never truncates the reference,
because truncation would merge two facts into one.

**I4 — The answer says what happened.** The output of every write contract carries the
provider's identifier and an `outcome` of `created`, `existing`, `updated` or `unchanged`.
For a `conditional` contract, a second call with the same reference and materially
different input fails with `REFERENCE_CONFLICT` (`retryable: false`). It never returns the
existing object as a success. Each contract's description names the fields it compares
(for an invoice: partner, currency, lines and totals). The envelope is not extended for
this.

**I5 — What a provider must show to be verified.** Under ADR-0483 a provider operation's
claim on a write contract can become verified only if its mapping rests on one of:

1. an idempotency key the provider honours natively, for at least as long as a job can be
   retried;
2. a native upsert, or a uniqueness constraint the provider enforces, on the field that
   holds `externalReference`;
3. find-by-reference-then-create, **only** where the uniqueness of (2) holds on the
   provider's side.

Find-then-create without provider-side uniqueness is not enough. Two rounds of the same job
(the lease of the first elapsed and was handed on while it was still running) can both find
nothing and both create. Serialising inside one Worker Instance does not help, because the
rounds may run on different instances. A provider with none of the three keeps an
unverified claim; the vocabulary is not weakened for it.

**I6 — The job key is not the idempotency key.** Worker Types keep sending the job key as a
request identifier for diagnostics, as Jira and Remedy do. It identifies one job, not the
fact the job records: a new job for the same fact (after a process modification, an
instance migration, a cancelled and restarted instance, or a duplicate start message)
carries a new key, and the provider would accept the duplicate.

### 4. Common value shapes

- **Amounts** are decimal strings (`^-?[0-9]+(\.[0-9]+)?$`) paired with an ISO 4217
  `currency`, never JSON numbers, so no provider mapping parses cents through a binary
  float.
- **Dates** are ISO 8601 calendar dates. The document date and the posting date are
  separate fields.
- **Tax** is the provider's `taxCode`, passed as a string. Version 1 has no neutral tax model.
- **Ledger accounts and cost centres** are the provider's identifiers, as strings.
- **References to other objects** (the customer on an invoice, the organisation on a
  contact) use the provider identifier or that object's `externalReference`.
- **`providerFields`** is an optional object passed through unchanged, for tenant-specific
  custom fields. It is not portable: a model that uses it is bound to its provider in
  practice. It is never compared for `REFERENCE_CONFLICT`.

### 5. What this record does not do

- It admits no Worker Type and ships no package. ADR-0299 still decides each vendor.
- It claims no interchangeability and routes nothing (ADR-0480, ADR-0482).
- It does not validate inputs at runtime (ADR-0485, option 4).
- It changes no job protocol, event, WAL record or `applyToState`.
- It publishes no contract as verified. Version 1 contract files are candidates until
  ADR-0483's evidence exists for them.

### Consequences

- **Positive:** a duplicate invoice, order or journal entry is excluded by the contract
  rather than left to each model. A provider that cannot guarantee it is identified at
  verification, not in production.
- **Positive:** the SAP S/4HANA and Dynamics 365 admissions that ADR-0299 still has to
  decide, and packages for Salesforce, HubSpot and bexio, get one target to claim against
  instead of each inventing operations.
- **Positive:** splitting create from post gives every finance process a natural place
  for an approval step.
- **Negative / trade-offs accepted:** every write in a model needs an `externalReference`
  expression, and its author has to decide what the business fact is. This is intended,
  and it is effort.
- **Negative:** some providers will end up with no verifiable write claim. That outcome is
  correct and will be unpopular.
- **Negative:** eighteen contracts are a real review load, and a process needs two tasks
  where some APIs offer one call.
- **Follow-ups / risks to watch:**
  - The contract JSON files and their positive and negative schema cases, beside the
    existing fixtures in [`workertype/testdata`](../../workertype/testdata), until a
    published corpus exists (CDA3).
  - Rule I1 as a check in `ParseTechnicalContractJSON` or `ValidateTechnicalContracts`,
    limited to the three namespaces.
  - Conformance cases under ADR-0483 for every write contract: the same request twice
    (exactly one object, second `outcome` is `existing` or `unchanged`), two concurrent
    identical requests (exactly one object), and the same reference with different input
    (`REFERENCE_CONFLICT`).
  - The ADR-0485 envelope is `v1alpha1` and may still change before CDA3. A change there
    may require this record's contracts to be rewritten, not just re-validated.
  - Version 2 candidates: credit notes and reversals, receipt of payment, goods receipt.
  - Jira and Remedy could adopt `externalReference` for their create operations. That is
    out of scope here and belongs to their own records.

## Pros and cons of the options

### Option 1 — No vocabulary
- Good: no review load now; each integration fits its vendor exactly.
- Bad: every Worker Type and package invents its own operations for the same steps, and
  nothing records what a write guarantees on replay.
- Bad: the first finance integration sets the precedent by accident.

### Option 2 — Vocabulary, idempotency declared per contract
- Good, at its strongest: it is honest. A provider that cannot de-duplicate says so, the
  claim is still useful for read and update steps, and nobody is excluded. A model author
  who knows the risk can guard against it with a lookup task before the create.
- Bad: the guard the model author would write is find-then-create, which section 3, I5
  shows to be insufficient without provider-side uniqueness. Declaring the risk moves it
  into every model rather than removing it, and in finance a model author who misses it
  produces a statutory correction.

### Option 3 — Mandatory idempotency through a model-supplied reference (chosen)
- Good: makes the property that matters most in these domains a condition of being in the
  vocabulary at all, and makes the failure visible at verification time.
- Good: design-time only; no engine change.
- Bad: places a modelling obligation on every write, and excludes providers that cannot
  meet it from a verified write claim.

### Option 4 — Engine-derived idempotency key
- Good, at its strongest: no burden on the model author. The engine can derive a
  deterministic key from the process instance, the element and a loop or multi-instance
  counter, write it into the job's events so replay reproduces it (invariant 6), and every
  Worker receives one without anybody thinking about it. This is the shape several
  workflow engines use for activity ids.
- Bad: it identifies the execution step, not the business fact. A migration, a process
  modification, a restarted instance or a duplicate start message yields a new key for the
  same invoice, which is the duplicate this record exists to prevent.
- Bad: the hard part does not move. The provider still has to refuse the second write
  (I5), and a key nobody on the provider's side enforces de-duplicates nothing.
- Bad: it changes the job record and the events that carry it, an engine change bought
  without gaining the hard part. A model that has no business key can still build its
  `externalReference` from values of its own instance; that is a modelling choice, not an
  engine feature.

## Links

- relates to: [ADR-0480](0480-technical-capability-contract.md) (claims),
  [ADR-0485](0485-technical-capability-semantics.md) (envelope and `idempotency` values),
  [ADR-0483](0483-technical-capability-conformance-governance.md) (verification),
  [ADR-0482](0482-capability-aware-binding-and-routing.md) (routing, not decided here)
- admission of vendors: [ADR-0299](0299-worker-type-admission-criteria.md)
- at-least-once delivery and the fence: [ADR-0007](0007-job-worker-protocol.md),
  [ADR-0274](0274-in-process-job-leases.md)
- duplicates on replay today: [ADR-0201](0201-jira-connector.md),
  [ADR-0106](0106-bmc-remedy-connector.md)
- the generic path: [ADR-0067](0067-service-task-connector-catalog.md),
  [ADR-0152](0152-rest-connector-oauth2.md)
- business capabilities, which this record does not touch: [ADR-0305](0305-business-capabilities-and-value-streams.md)
- plan: [`docs/planning/technical-capability-contract.md`](../planning/technical-capability-contract.md)
