# ADR-DRAFT: A person's own Google address book — searched in a form, read and changed in the steps after it

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-24
- **Deciders:** Atlas maintainers
- **Open question:** what Google requires of an OAuth client that private gmail.com accounts
  consent to for the contacts scope, which this record assumes and could not check against
  Google's documentation from where it was written: (1) that a client left in the
  *Testing* publishing status has its refresh tokens expire after seven days, so a family
  installation must publish it *In production*; (2) that an unverified client in
  production shows an "unverified app" warning at consent and is capped at 100 users, which
  a family stays far below, so no verification by Google is needed; (3) that the contacts
  scopes are *sensitive*, not *restricted*, so verification — when an installation outgrows
  the cap — needs no security assessment; (4) whether a child's account under Google Family
  Link may consent to a third-party app at all. Somebody setting up the first family
  installation should record what Google showed them here.
- **Question checked:** 2026-10

## Context and problem statement

A Google Contacts Worker ([ADR-0476](0476-google-contacts-worker.md)) is one address book
under one credential: a service account acting as one Workspace user, or one person's refresh
token that an operator pasted into the vault. A task form searches that one address book
([ADR-0473](0473-form-remote-data-sources.md), second provider), and a service task reads,
changes or deletes contacts in it.

Atlas is also run privately — by one person, or by a family for its household paperwork.
There every person keeps a private gmail.com account, and the address book that matters in
a task is *their own*: the person working on the task picks one of *their* contacts, and a
later step reads it, updates it or deletes it, as the process needs. Three facts shape what
that takes:

1. **gmail.com has no domain-wide delegation.** No credential an operator holds can act as
   every family member. Each person has to grant access themselves.
2. **How people sign in to Atlas differs per installation** — local accounts, Entra, any
   OIDC provider ([ADR-0210](0210-federated-authentication.md)). The Google account a
   person's contacts live in cannot be derived from their login; it has to be linked.
3. **A contact id is meaningless without its address book.** `people/c1234` names a contact
   in the address book that issued it and nothing in anybody else's. A choice made in one
   person's book is only usable later through that same book.

Today the only way to approximate this is one Worker per person, each holding that person's
refresh token, and a task choosing the Worker by FEEL. The operator then holds every
family member's credential, any process that can compute a Worker name can change any of
those address books, and a form cannot choose at all — the Worker of a directory field is a
plain name, by design.

[ADR-0475](0475-identity-aware-worker-execution.md) already decided the general shape:
external identities and connections owned by a person, credentials held only by Atlas and
released per action to a supervised Worker Instance, a decision before every action, and
identity never taken from a variable. It built that for SharePoint and Microsoft accounts;
Google is named there as "a later slice". It covers jobs, not form lookups, and it knows
one per-person execution mode, `actAs: "initiator"` — the person who started the instance,
who is not necessarily the person who picked the contact.

The question: how does Atlas let each person search, read and change **their own** Google
address book in their tasks — with their consent, through every login method, and without
anyone else being able to make Atlas act in that book?

## Decision drivers

- **Consent is the person's.** Their address book is read and written with access they
  granted, revocable by them, never with a credential an operator handled.
- **Nobody else can steer it.** Not a form, not a process variable, not a model, not another
  member of the same installation. Whose book an action uses comes from server-side facts.
- **The choice stays bound to its book.** A contact chosen in a task is read, changed or
  deleted later only through the address book it came from.
- **Losing access is a path, not an incident.** A revoked consent, a person who left the
  household, an account that is gone: a process can catch it and do something sensible.
- **Every login method.** Linking a Google account is independent of how a person signs in.
- **Family scale.** One OAuth client per installation, set up once by whoever runs it; no
  Google verification needed while the installation stays small (open question).
- **The invariants.** No credential in an event, a variable, a job payload or a log line;
  side effects on Worker Instances after fsync; facts in the log, one `applyToState`.

## Considered options

1. **One Worker per person, chosen by FEEL** — what works today.
2. **A service account with domain-wide delegation, acting as the signed-in person.**
3. **ADR-0475 connections for Google, and a Google Contacts Worker that acts as a person**:
   per-person consent, brokered access tokens, the searching person's book in a form, and
   later steps acting as the person who made the choice.
4. **The browser calls the People API** with a token the person's browser obtains, and
   nothing is stored on the server.

## Decision outcome

Chosen option: **3**, in three slices, each landing with its own tests and amendments to
ADR-0475 and ADR-0476.

### Slice 1 — linking a Google account

ADR-0475 §4's Google adapter, made concrete:

- **The OAuth client** is the installation's, configured once by an administrator: a Google
  Cloud project with the People API enabled and an OAuth client of type *Web application*
  whose redirect URI is the installation's callback. Client id and secret are vault
  secrets. The handbook walks through it, including publishing the client *In production*
  (open question 1).
- **The flow** is ADR-0475's: Authorization Code with PKCE (S256) against
  `https://accounts.google.com`, `state` bound to the signed-in Atlas session and
  single-use, a `nonce`, the ID token validated with ADR-0210's verifier (issuer, audience,
  expiry, nonce, non-empty `sub`), `access_type=offline` so Google issues a refresh token.
- **The external identity** is `sub` under `https://accounts.google.com` — never the e-mail
  address, which a person can change. `hd` is absent for a gmail.com account and is not
  required.
- **Scopes are the person's choice, at Google's consent screen.** Linking asks for
  `contacts.readonly`; allowing a Worker that writes (below) asks again, incrementally, for
  `contacts`. The connection stores the scopes Google reported as granted, and a Worker
  whose needs exceed them is refused for that connection (`CONNECTION_SCOPE_INSUFFICIENT`, a
  new built-in guard beside ADR-0475 §7's).
- **The refresh token** lives in the vault under `atlas:connection-credential:`, refreshed
  only by Atlas under ADR-0475's refresh rules.

The person links and unlinks under their own account menu, and chooses there which Google
Contacts Workers may use the connection — ADR-0475's "allowed Workers".

### Slice 2 — a Worker that searches the searching person's book

**A new identity mode, `delegated-per-person`,** for the Google Contacts Worker Type. Such a
Worker holds no credential and no endpoint other than the People API's. It names no person
either: every use says whose book, from a server-side fact, and the connection is that
person's one connection that allows this Worker. A person may link several Google accounts,
but only one of them may allow a given Worker; linking a second is refused for that Worker,
so "whose book" never has two answers.

**A read-only option on the Worker.** `readOnly: true` makes the deploy refuse a writing
operation that names the Worker (create, update, delete, label changes), the Worker Instance
refuse one it is handed anyway, and the Worker need only `contacts.readonly`. A household
that wants processes to change contacts configures a second, writing Worker, and each person
decides separately whether to allow it. That is the granularity of consent: read, or read
and change — chosen per Worker by the person, not per operation by a model.

**Form lookups use the searching person's connection.** A directory field naming a
`delegated-per-person` Worker is searched as the principal the server authenticated for the
request — the one ADR-0473's task question already established, never a value the browser
sends. The server resolves that principal's connection for the Worker, evaluates the
built-in guards and the published policies for the action `contacts.search`, and releases a
short-lived access token to the supervised Worker Instance together with the query. Without
a connection the field answers a new code, `not_connected`, and the field shows a link to
connect one. Each member of a family therefore searches only their own book through the same
form, and nobody can search somebody else's — the field cannot name a person at all.

**A lookup decision is not written to the log.** This departs from ADR-0475 §12, which
records every decision, and it is the trade-off most worth challenging (*Pros and cons*,
option 3). The reasons: ADR-0473 keeps searches out of the log on purpose — a keystroke is no
fact about the process, and a log of searches is a log of what people typed; and a lookup
reads, as the person, a bounded page of the person's own book and shows it to that same
person. Nothing leaves their reach that was not theirs. The token is released only for the
query it travels with, never cached on the Worker Instance, and the submission (below) *is*
recorded.

**Submission is verified in the same book.** ADR-0473's check before completion reads the
chosen contact through the completing person's connection. It succeeds only for a contact
that exists in their book and holds the field's filters.

### Slice 3 — later steps act as the person who chose

**The choice is recorded as a fact.** When a completion passes the check above, the server
appends, in the same batch as the completion, a `ChoiceBound` record keyed under the
instance: the variable, the value, the principal who chose and the connection it was checked
through. It carries no credential. `applyToState` keeps the latest binding per instance and
variable, and replay reproduces it.

**A task names whose book by naming the choice.** A Google Contacts task on a
`delegated-per-person` Worker states `actAs="chooser:kunde"` — "as the person who chose the
current value of `kunde`". At decision time (ADR-0475 §5) the server reads the binding for
`kunde` and the variable's current value. They must agree: a variable a script task, an
output mapping or an operator changed after the choice no longer has a chooser, and the
decision is `CHOICE_NOT_BOUND` — the identity never follows a variable, only a recorded fact
about who chose the value it still holds. The contact id the task uses must be that value, so
a task cannot act as the chooser on another contact either.

`actAs="initiator"` (ADR-0475) stays available for a step that should act as whoever
started the instance, and a task on such a Worker without `actAs` is refused at deploy.

**Losing access is catchable.** A decision that finds no usable connection — revoked,
needing re-authentication, unlinked, the person disabled or deleted, the Worker no longer
allowed, the scope insufficient — fails the job as the BPMN error
`GOOGLECONTACTS_NOT_CONNECTED` when the process has a boundary for it, and as an incident
otherwise, as ADR-0476's `GOOGLECONTACTS_CONTACT_NOT_FOUND` does. A process can then ask
somebody else, or skip the step.

**Writing is a decision like any other.** Updates and deletes run only on a writing Worker
the chooser allowed, under a Permit from the published policies (ADR-0475 §6–7) for this
job, this lease and these parameters. An installation that wants a person to confirm before
a process deletes one of their contacts uses ADR-0475 §9's approvals, with the chooser as
approver.

**Dynamic Worker names.** ADR-0475 refuses an identity policy on Worker Types whose tasks
may name the Worker with FEEL until the resolved name is part of the decision. Google
Contacts tasks may (ADR-0476); for this Worker Type the decision reads the name the engine
resolved into the job, which is what ADR-0475 asks for, and the record's table row is
amended accordingly.

### Consequences

- **Positive:**
  - Every family member searches, reads and — where they allowed it — changes their own
    address book through the same forms and processes, and nobody else can.
  - No member's credential is ever handled by another, including whoever runs Atlas.
  - A choice stays bound to its book across days and restarts, through a fact in the log.
  - The connection model, the broker and the policy engine are ADR-0475's; Google adds an
    adapter and the Worker Type a mode, not a second mechanism.
- **Negative / trade-offs accepted:**
  - **An engine change.** `ChoiceBound` is a new value type with an `applyToState` arm, and
    the completion batch grows by one record for every directory field on a
    `delegated-per-person` Worker.
  - **Searches are decided but not recorded**, a deliberate exception to ADR-0475 §12.
  - **Google's terms are the installation's to meet.** Each installation brings its own
    OAuth client; the warning screen of an unverified app is part of the experience;
    outgrowing the user cap means Google's verification (open question).
  - **Personal data of private contacts enters the process** once a step reads a contact:
    the result variable is in the log, the state and every backup. That is the household's
    decision to make per process; the record makes it possible, not advisable.
  - **One Worker per mode.** A household that wants read-only and writing access configures
    two Workers.
- **Follow-ups / risks to watch:**
  - The same `delegated-per-person` mode for the Google Sheets Worker Type and for Microsoft
    accounts' contacts through Graph.
  - A view under the person's account listing every running instance bound to their
    connection, so revoking is an informed choice.
  - Whether `ChoiceBound` should cover Entra directory fields too, where the chosen id is
    tenant-wide and the binding matters less.

## Pros and cons of the options

### Option 1 — one Worker per person, chosen by FEEL
- Good: works today, without a line of code; each refresh token reaches only one book.
- Bad: the operator obtains and pastes every person's refresh token, so consent is
  delegated to whoever runs Atlas; any process that can compute a Worker name can change any
  member's book; a form cannot choose the Worker at all; nothing binds a chosen contact to
  the book it came from.

### Option 2 — a service account acting as the signed-in person
- Good: no per-person consent flow; the smallest change for a Google Workspace.
- Bad: gmail.com accounts cannot be delegated to, so it does not exist for this audience; in
  a Workspace the one key reads every book in the domain, and the only guard is Atlas's
  mapping from login to Google account, which fact 2 says Atlas does not have.
- Not rejected for Workspaces: it remains ADR-0476's service-account method with a fixed
  subject, and a per-person subject is a separate question for a separate record.

### Option 3 — ADR-0475 connections, a Worker that acts as a person (chosen)
- Good: consent, revocation and audit are the person's and Atlas's, never the operator's;
  identity comes only from facts the server holds; the choice is bound to its book.
- Bad: three slices, an engine change, and Google's client requirements per installation.
- The strongest objection, to the unrecorded lookup decision: ADR-0475 says every decision
  is evidence, and a person's book was read by a Worker Instance holding a released token
  with no trace in the log. The answer accepted here is that a read of one's own data, shown
  only to oneself, is not an action on anybody's behalf but one's own, and that the log's
  promise of keeping keystrokes out (ADR-0473) is worth more for this read than the record
  would be. If an installation needs the trace, a per-search audit entry outside the
  process log is the follow-up — not a record per keystroke in it.

### Option 4 — the browser calls the People API
- Good: no token on the server for the search; Google enforces the person's own visibility.
- Bad: answers only the search. A later step runs when the person is not there, so the
  server needs the person's access anyway; the browser-side token is a second OAuth client
  and a second consent; and ADR-0473 rejected letting the client decide the query for the
  same reasons.

## Links

- builds on [ADR-0475](0475-identity-aware-worker-execution.md) (its Google slice, a new
  identity mode, an exception to §12, an amended row of §11)
- extends [ADR-0476](0476-google-contacts-worker.md) (the Worker Type) and
  [ADR-0473](0473-form-remote-data-sources.md) (lookups as the searching person)
- reuses [ADR-0210](0210-federated-authentication.md)'s ID-token verifier and
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)'s vault
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker
  Instance
