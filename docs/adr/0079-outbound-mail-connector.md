# ADR-0079: An outbound mail connector (SMTP first)

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

Processes routinely need to notify a human: "order shipped", "approval required",
"payment failed". Atlas already runs several service-task connector kinds through the
job path — a clio event store (ADR-0036), a temis decision service (ADR-0050), and an
HTTP-REST endpoint (ADR-0067) — behind one seam: a `TypeConnectorTask` that creates a
job carrying a reserved job type, picked up by an in-process worker off the hot path
(ADR-0007). ADR-0067 named "an email sender" as the archetypal *next* kind the
connector catalog should absorb. There was no way to send an e-mail from a process.

Two shapes were possible and the split matters:

1. **Where does the provider live?** A mail provider is a host plus credentials
   (Google, Microsoft 365, a corporate relay). Per the secret rule (ADR-0041, I6) a
   credential must never be authored in a BPMN file, which is shared, versioned and
   rendered. clio solves exactly this: the endpoint and token are managed server-side
   and a model names the connector by name only.
2. **Where does the message live?** The recipients, subject and body are per-task
   content an author writes in the model, exactly like a REST task's URL and headers
   (ADR-0067), and they benefit from the same FEEL "fx" toggle so a recipient can be
   `=customer.email`.

So a mail task is a hybrid of the two connectors that already exist, not a new
mechanism.

## Decision drivers

- **Reach the providers the user asked for** — "Google, Microsoft, …" — without
  building an OAuth token-management subsystem first.
- **Honor the invariants.** The outbound send runs only in the worker, post-fsync,
  off the single writer, never in `applyToState` (I1/I2/I4, ADR-0005/0007).
  Model-authored fields are deploy-time data compiled into the process (I5); the
  credential is a *reference* resolved at call time (I6, ADR-0041).
- **Extensibility is the point (ADR-0067).** Adding the kind should be one catalog
  entry + one moddle type + one compiler branch + one worker, and adding a *second
  transport* (native Gmail / Graph API) later should be additive behind a `Client`
  seam, not a rewrite.

## Considered options

**For the transport:**

A. **SMTP only, universal.** One `Client` implementation over `net/smtp`. Google,
   Microsoft 365 and any compliant server are reached via their submission endpoints
   (`smtp.gmail.com:587`, `smtp.office365.com:587`) with an app-password / account
   credential from the vault. Dependency-free, testable, works today.
B. **Native provider APIs.** SMTP plus a Gmail API sender and a Microsoft Graph
   sender (bearer-token HTTP POST). Closer to the providers but needs OAuth token
   acquisition and refresh — a subsystem of its own.
C. **A third-party e-mail SaaS** (SendGrid/SES-style) as the only path. Rejected: it
   is just a REST connector with a vendor lock-in, and doesn't answer "Google,
   Microsoft".

**For the message vs. provider split:** the same fork ADR-0067 settled for REST —
provider in the managed connector store (like clio), message in the model (like REST).

## Decision outcome

Chosen: **a mail connector kind whose provider is a managed connector (option A,
SMTP) and whose message is model-authored, behind a `Client` seam that options B is
additive to.**

- The **mail connector task** is the catalog's fourth kind. A service task bearing an
  `<atlas:mailConnector connector="…" to="…" subject="…" body="…"/>` extension
  compiles to a `TypeConnectorTask` carrying the reserved `MailJobType`
  (`MailJobTypeIndex == 10`). `connector` names a managed provider; `to`/`cc`/`bcc`/
  `from`/`subject`/`body` are literal-or-FEEL values (`RestExpr`, the fx toggle)
  evaluated over the instance's variables at send time.
- The **provider** is a managed connector record of kind `mail`: an SMTP
  `endpoint` (`host:port`), a `sender` that is both the SMTP auth username and the
  default `From`, and a `credentialsRef` whose password is resolved from the vault
  (ADR-0041) at build time. `provider` is stored for forward-compatibility and is
  `smtp` today; a record naming another provider is skipped (its tasks park) rather
  than failing the rebuild.
- The **worker** (`mail.Handler`) resolves the task's connector and message from the
  compiled process, evaluates the FEEL fields, frames a UTF-8 `text/plain` message,
  and sends it through the resolved `Client`. Delivery keeps the ADR-0036/0007 job
  guarantees: at-least-once, with the job key carried as the RFC 5322 `Message-ID` so
  a replayed send after a crash is recognizable (I6). A `Bcc` address is in the SMTP
  envelope but never in a header.

### Consequences

- **Positive:** a notification e-mail is authored end-to-end in the modeler and
  actually sends; Google and Microsoft work today via SMTP with no OAuth; the
  provider host and secret stay with ops, never in a model; the `Client` seam makes a
  native Gmail / Graph provider a purely additive follow-up.
- **Negative / trade-offs accepted:** SMTP submission only (no native provider APIs
  yet), so a customer who mandates OAuth-only submission waits for option B; the body
  is plain text (no HTML/attachments yet); `net/smtp` predates `context`, so a
  per-send timeout is a follow-up; the whole variable scope is *not* the payload here
  (unlike clio/REST) — the message is exactly the authored fields.
- **Follow-ups / risks to watch:** native Gmail API and Microsoft Graph providers
  behind the same `Client`; HTML bodies and attachments; a per-send connection
  timeout; STARTTLS/implicit-TLS policy and certificate pinning options; input
  mappings so a template can be assembled from variables.

## Pros and cons of the options

### Option A — SMTP only
- Good: reaches every named provider now; no OAuth subsystem; dependency-free and
  unit-testable via an injected send seam.
- Bad: no provider-native features (Gmail labels, Graph send-as); OAuth-only shops
  are unserved until option B.

### Option B — Native provider APIs
- Good: first-class provider integration; bearer tokens instead of app passwords.
- Bad: needs OAuth acquisition + refresh + storage before a single mail sends —
  disproportionate to "send a notification".

## Links

- realizes the "email sender" named as the next kind in ADR-0067 (connector catalog)
- follows ADR-0036 (provider in the managed store) and ADR-0067 (message in the model)
- honors I1/I2/I4/I5/I6 and ADR-0005/0007/0041
