# ADR-DRAFT: What a mail reply proves, and what a decision may rest on

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-27
- **Deciders:** Atlas maintainers
- **Open question:** three things this record could not settle here. Whether Gmail
  (`format=raw`) and Microsoft Graph (`/messages/{id}/$value`) hand back a signed message
  byte for byte, so that an S/MIME signature verifies, is written from documentation, not
  from a signed message read at a live tenant. The legal reading in the decision matrix —
  that a plain mail does not meet a written-form requirement under Swiss law, and that only
  a qualified electronic signature with a qualified time stamp is equated with a
  handwritten one (Art. 14 para. 2bis CO, ZertES) — is the maintainers' understanding,
  not legal advice, and an installation has to have it confirmed for its contracts. And
  whether the Swiss e-ID, adopted in 2025, is available to a relying party in a form Atlas
  could verify is not known here.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-draft-mail-reply-routing](draft-mail-reply-routing.md) routes a customer's reply to
the instance that wrote to them, with a signed token the customer's client carries back.
The token proves that the mail **answers** an Atlas mail. It does not prove **who** wrote
it: a customer who forwards the mail hands the token on, a shared mailbox has many
readers, and a `From` header is a claim anyone can write.

That is enough for a question answered or a document sent in. It is not enough for an
approval, a termination or a payment release, and nothing in Atlas today says where the
line runs. A modeler who can correlate a reply will use it for whatever the process waits
for, and the first time it matters is the first time somebody other than the customer
answered.

The question: **what can Atlas establish about who sent a reply and what they agreed to,
how does a model state what a decision needs, and where does the line run between what a
mail may decide and what needs another channel?**

## Decision drivers

- **Four properties, not one.** A decision rests on *authenticity* (who), *integrity*
  (exactly what), *binding* (to which request) and *evidence* (provable later). Mail
  offers each to a different degree, and a level that names only one of them misleads.
- **The requirement belongs to the decision.** The process knows whether it waits for an
  answer or an approval; the mailbox does not. Where it is stated is where a reviewer
  reads it.
- **A level is a fact Atlas established, never a claim it received.** Nothing a sender
  writes may raise it, and nothing but the mail watch may set it.
- **Fail closed.** A check that cannot finish — a revocation responder that does not
  answer — yields the lower level, not the higher.
- **Invariants.** Signature checks and revocation lookups are network I/O in the watch
  read, off the run loop (I3). The level is frozen into the publish (I6), and comparing it
  at correlation is an integer comparison in `applyToState`, identical on replay (I4).

## Considered options

1. **Facts only:** expose what was verified and let the model branch on it.
2. **A minimum on the watch:** each watch publishes only replies at or above a level,
   under its own message name.
3. **A minimum on the receiving element:** the catch event, receive task or boundary
   event states the level it needs, and the engine delivers nothing below it.
4. **No reply decides anything:** every decision moves to a form behind sign-in.

## Decision outcome

Chosen: **3, with the facts of 1**, and the matrix below as the handbook's default.

### The levels

| Level | Name | Atlas has established | It does not establish |
|---|---|---|---|
| 0 | `none` | nothing about the sender | anything |
| 1 | `domain` | the `From` domain's infrastructure sent it: DMARC `pass`, read from the receiving server's topmost `Authentication-Results` (ADR-0438) | which person; a compromised or shared mailbox |
| 2 | `signed` | an S/MIME signature that verifies, by a person whose identity a CA checked, whose address is the `From`, over content that quotes the request it answers | that the signer meant what a lawyer would read into the words |

A message's level is set by the mail watch and by nothing else. A publish through
`POST /api/v1/messages`, a throw event and every other watch publish at level 0, so an
operator or a process cannot manufacture a signed reply.

#### Level 2 in detail

Each condition closes a specific gap:

- **The signature verifies** over the signed content: `multipart/signed` and opaque
  `application/pkcs7-mime` signed-data, SHA-256 or stronger, RSA or ECDSA. SHA-1 is
  refused.
- **The chain ends at a trust anchor configured on the Worker** — a PEM bundle, for
  example a public S/MIME CA or a business partner's own CA. The system's TLS roots are
  not used: a CA trusted to vouch for a web server is not thereby trusted to vouch for a
  person.
- **The certificate vouches for a person.** Under the CA/Browser Forum's S/MIME Baseline
  Requirements a *mailbox-validated* certificate proves only that its holder could
  receive mail at the address, which is no more than level 1 already says. Level 2
  requires a *sponsor-validated* or *individual-validated* policy, or an anchor the
  Worker's configuration marks as vouching for persons (a partner's own CA).
- **The signer is the sender.** The certificate's e-mail address equals the `From`.
- **Validity is judged at the receive time** the provider reports. The signing time
  inside the signature is the signer's own claim and is not believed.
- **Revocation is checked**, by OCSP (`golang.org/x/crypto/ocsp`) or CRL. A responder that
  cannot be reached yields level 1 with `revocation: unknown`, not level 2.
- **The signature binds the request.** S/MIME signs the body, not the headers, so the
  `In-Reply-To` that routed the reply is unsigned: anyone holding a signed "I approve"
  could resend it under the `In-Reply-To` of a different request. Level 2 therefore
  requires the request's token **inside the signed content**. A mail task that asks for a
  signed answer writes a reference line into its body (`replyTag="body"`), the customer's
  client quotes it, and the watch looks for it in what the signature covers. A client that
  does not quote yields `bound: false` and level 1.

### What the process receives

```
mail.assurance = { level, dmarc, signed, bound, revocation, signer, certificate }
certificate    = { subject, issuer, validation, notAfter, qualified }
```

`validation` is `mailbox`, `organization`, `sponsor`, `individual` or `unknown`.
`qualified` reports that the certificate carries the ETSI qualified-certificate
statements. It is a fact about the certificate, not a finding that the signature is
legally qualified: that needs a qualified signature-creation device and, under Swiss law,
a qualified time stamp, neither of which a mail shows.

### The requirement is stated on the receiving element

A message catch event, a receive task and a message boundary event gain
`atlas:minimumAssurance` (`none`, `domain`, `signed`; absent means `none`, today's
behaviour). Its rules:

- The subscription records the minimum, and the publish records the level. Both are
  appended fields, zero on older records.
- Correlation delivers a message only to subscriptions whose minimum it meets. A reply
  below the minimum is, for that subscription, a message nothing waits for: it waits in
  the buffer if the watch has a wait time, and otherwise shows as unmatched, exactly as
  the routing record decided.
- The buffer's hand-over to an opening subscription applies the same comparison.
- The model handles a reply that is not good enough the way it handles silence: a timer
  on the wait, then a reminder that asks for a signed answer or links to a form.

The requirement stands in the diagram, where the process owner and a reviewer read it,
and the engine enforces it. A modeler cannot meet it by configuring a watch differently.

### Where the line runs — the handbook's default

An installation decides this for itself; the handbook offers this as the starting point:

| The reply decides… | Minimum | Why |
|---|---|---|
| an answer to a question, a document sent in | `none` | the content is checked anyway; a wrong answer costs a correction |
| a confirmed appointment, contact data, a non-binding choice | `domain`, and the model compares `mail.from` with the address it wrote to | reversible, little damage |
| an approval within an existing agreement (an offer below a limit, an order release) | `signed`, or a form behind sign-in | binding: who and what must both be established |
| a termination or change of a contract, a waived right | not a mail reply: a form behind sign-in; where written form is required, a qualified electronic signature | legal form and evidence |
| a payment release, a change of bank details | never by mail: two people, in an authenticated channel | the target of business e-mail compromise fraud |

The last row is not about signatures. Mail is the channel that fraud uses to change
where money goes, so a process that pays on a mail's word has already lost the argument
whatever level the mail reaches.

### What Atlas offers above a mail

- **A form behind sign-in.** Where the counterpart has an account — the portal's own
  sign-in ([ADR-0399](0399-portal-carries-its-own-sign-in.md)), federated through an
  OIDC provider that enforces MFA ([ADR-0210](0210-federated-authentication.md)) — the
  reply is only the trigger, and the decision is a task completed by an identified person
  on a page that shows exactly what they approve. That establishes all four properties
  without a certificate on the customer's side, and is the recommendation wherever
  customers have accounts.
- **A qualified electronic signature** through a signing service (Skribble, Swisscom
  Trust Services or similar) on a document. That is a Worker Type of its own and a record
  of its own; this record only names it as the answer where written form is required.
- **The Swiss e-ID**, if it becomes verifiable by a relying party, belongs with the
  signed-in form, not with mail.

### Evidence

The mailbox remains where the signed message lives, and a mailbox is cleaned. A process
that relied on level 2 should keep the raw message, so the follow-up is an
`export-message` operation beside ADR-0478's `export-attachment`, writing the message as
`.eml` to an S3 Worker. Until it exists, the handbook says so.

### Implementation notes

- **CMS.** Go's standard library verifies certificate chains but does not parse CMS
  `SignedData`, and no module in `go.mod` does. The slice chooses between a maintained
  pure-Go library and a narrow subset written here, as the IMAP client was
  (ADR-0438): one signer, signed attributes with `messageDigest`, bounded parsing, no
  SHA-1. Pure Go either way ([ADR-0010](0010-go-and-no-cgo.md)).
- **Raw bytes.** The signature covers the exact bytes of the signed part. IMAP reads
  `BODY.PEEK[]`, Gmail `format=raw` and Graph `$value`; verification runs on those, never
  on a parsed and re-serialised message.
- **Cost.** A signed message costs a parse, a chain build and a revocation lookup in the
  watch read. Only a message that carries a signature pays it, and the watch's budget
  bounds the rate.

### Consequences

- **Positive:**
  - The line between what a mail may decide and what it may not is written down, and
    each process states where it stands on it.
  - The level is established by Atlas, enforced by the engine, and visible in the
    diagram.
  - A signed reply cannot be lifted from one request into another.
- **Negative / trade-offs accepted:**
  - Level 2 is out of reach for most private customers, who have no S/MIME certificate.
    For them the answer is the signed-in form, not the mail.
  - A level 2 reply depends on the customer's client quoting the request.
  - Every trust anchor is an installation's decision and an installation's maintenance.
  - A model that waits with a minimum receives nothing below it, so it needs a timer, or
    the reply waits unseen until its time runs out.
- **Follow-ups / risks to watch:**
  - `export-message` for evidence.
  - A signing-service Worker Type for qualified signatures.
  - The legal reading in the matrix, confirmed for the installation's contracts.

## Pros and cons of the options

### Option 1 — facts only
- Good: no engine change; the model sees everything.
- Bad: the requirement lives in a gateway condition a modeler may forget to draw, which is
  exactly the misuse this record exists to prevent.

### Option 2 — a minimum on the watch
- Good: no engine change; enforced before anything is published.
- Bad: the requirement sits with the mailbox, not with the decision. The guarantee holds
  only while every approval subscribes to the right message name, which nothing checks.

### Option 3 — a minimum on the receiving element (chosen)
- Good: stated where the decision is drawn, reviewed with the model, enforced by the
  engine.
- Bad: an engine change: two appended fields and a comparison at correlation and at the
  buffer's hand-over.

### Option 4 — no reply decides anything
- Good: the simplest rule, and the strongest.
- Bad: it gives up the cases where a reply is plainly enough — an answer, a document —
  and it leaves the customer without an account no channel at all.

## Links

- builds on [ADR-draft-mail-reply-routing](draft-mail-reply-routing.md) (the token and the
  reply watch) and [ADR-0438](0438-mailbox-worker.md) (the `Authentication-Results`
  verdict and the mailbox read)
- correlation as [ADR-0020](0020-message-correlation.md) decided, waiting as
  [ADR-0370](0370-durable-message-buffer.md) decided
- the channel above mail: [ADR-0399](0399-portal-carries-its-own-sign-in.md),
  [ADR-0210](0210-federated-authentication.md), [ADR-0028](0028-forms-and-the-tasks-app.md)
- evidence beside [ADR-0478](0478-mail-documents-leave-the-mailbox.md)
