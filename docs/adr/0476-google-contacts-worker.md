# ADR-0476: A Google Contacts Worker Type — a process finds, keeps and labels the people it works with

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** whether the People API behaves at a real account as this record
  assumes on four points, none of which a test here can reach. The Worker Type is tested
  against an in-memory People API built from Google's discovery document
  (`people.googleapis.com/$discovery/rest?version=v1`), not against Google. (1) That a
  `createContact` carrying `memberships` — `contactGroups/myContacts` plus the named
  labels — creates the contact with those labels in one call. (2) That `people.get` and
  `updateContact` answer a deleted contact with HTTP 404 and status `NOT_FOUND`, which is
  what `GOOGLECONTACTS_CONTACT_NOT_FOUND` and an idempotent `delete-contact` rest on.
  (3) That an update sending back the entries it read, metadata included, keeps which
  e-mail address or phone number is the primary one. (4) That a service account without
  a `subject` reads an empty address book rather than failing, which is the reason a
  bundle without one is refused. Somebody with a Workspace test account should run
  `create-contact` with a label, `update-contact` with `email`, `get-contact` on a deleted
  id, and record what they saw here.
- **Question checked:** 2026-10

## Context and problem statement

Most of what a business process knows about a person outside the organisation — a
customer, a supplier, a lawyer, a contact at an authority — is kept, in many smaller
organisations, in exactly one place: the shared Google address book of a sales or
service team. Its labels ("Kunden", "Lieferanten", "VIP") are the customer list, its
custom fields hold the customer number, and the people who maintain it will not maintain
a second copy in a database a process can read.

A process therefore has to reach that address book to do three things it cannot do
without it:

- **Recognise a person.** A mail, a form or a call arrives from an address or a number;
  the process needs to know whether this is a known customer, and which one.
- **Keep the address book current.** A new customer is created there, a changed address
  is written there, and a customer becomes a "Kunde" by being labelled.
- **Act on a group.** "Every contact labelled VIP" is a list a process iterates.

And, inside a Google Workspace, a fourth: find a *colleague* in the organisation's
directory, which the same API serves.

Atlas cannot do any of this today. The REST Worker Type cannot reach the People API at
all, and a script task could only by putting a service account's private key into a
process variable or onto the worker host. The question this record answers is: **how
does a process read and write a Google address book without a credential in the model,
and without inheriting the People API's shape?**

## Decision drivers

- **The five gates of [ADR-0299](0299-worker-type-admission-criteria.md) are answered
  here**, not re-derived.
- **The credential stays on the worker.** It reads a person's whole address book, which
  is personal data about other people; it has no business on the engine's run loop
  ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)).
- **A process must not be misled about whether a person is known.** The cheapest way to
  ask Google — its search — is the one that can answer wrongly, and the wrong answer
  creates a duplicate customer.
- **The model holds the business shape, not Google's.** A person in the People API is a
  record of lists of entries with source metadata; a process wants `kunde.email`.

## Considered options

1. **A Google Contacts Worker Type with a fixed operation table and a flat contact**,
   worker-only, on the shape of the Exchange Worker Type
   ([ADR-0462](0462-exchange-worker.md)).
2. **Google's service-account grant on the REST Worker Type**, and models author the
   People API themselves.
3. **Contacts operations in the Google Sheets Worker Type**, which already holds a
   Google credential.
4. **A form-lookup provider only** ([ADR-0473](0473-form-remote-data-sources.md)), so a
   person picks a contact in a form and no service task touches the address book.

## Decision outcome

Chosen option: **1 — a Google Contacts Worker Type**, worker-only and supervised by
default, with the operation table in a dependency-free package the compiler reads
directly (`connector/googlecontacts/catalog`, Exchange's arrangement).

### The five gates

| Gate | Answer |
|---|---|
| 1 Operations are process steps | Yes — ten named operations, below; each is a task with a verb on it. |
| 2 The generic path is blocked or lossy | **Blocked**, as for Google Sheets: Google's APIs take an OAuth2 token obtained by signing a JWT assertion with a service account's key, which the REST Worker cannot do. And **lossy** besides: every read needs a field mask, every update a read of the contact's etag first and a field list after, and every model would flatten `emailAddresses[].metadata.primary` itself. |
| 3 The work fits a job | Yes. Each operation is one to a few dozen HTTP calls, bounded: a listing by `maxResults`, a find by Google's limit of 25 000 contacts per account. Every operation but `create-contact` is safe to run twice (below). |
| 4 One credential shape | Yes — the Google Sheets bundle, `serviceAccount` or `refreshToken`, with `subject` required for a service account. |
| 5 Arrives complete | Registry row, Repository package `atlas.google-contacts`, setup entry and handbook card, in this change. |

### The operation table

| Operation | What it is for | People API |
|---|---|---|
| `find-contacts` | Is this e-mail address, phone number or customer number known? | `connections.list` (or a label's members), compared on the worker |
| `get-contact` | Read the contact a process stored the id of | `people.get` |
| `search-contacts` | A person's free text | `searchContacts`, after an empty warm-up query |
| `list-contacts` | Every contact, or every contact with one label | `connections.list`, or `contactGroups.get` + `people:batchGet` |
| `create-contact` | A new customer, with its labels | `createContact` |
| `update-contact` | A changed address, a new customer number | `people.get` + `updateContact` under the etag read |
| `delete-contact` | An erasure request | `deleteContact` |
| `add-to-group` / `remove-from-group` | Make somebody a "Kunde", or stop | `contactGroups.members.modify` |
| `search-directory` | A colleague in the Workspace | `searchDirectoryPeople` over profiles and domain contacts |

What is deliberately not offered: photos, "other contacts" (addresses Gmail collected on
its own), creating and renaming labels, and sync tokens. None is a step a process takes;
a label a process needs is created once, by a person, and a misspelt one is refused
rather than created.

### Find reads; search searches

Google's `searchContacts` answers from a cache Google asks clients to warm with an empty
query, matches prefixes, and — Google's own samples wait several seconds after the
warm-up — lags behind changes. Asked "is anna@firma.ch known?", it can answer "no" for a
contact created a minute ago, and the process then creates her a second time.

So the question "is this person known?" is a different operation. `find-contacts` reads
the address book (or one label's members) and compares on the worker: an e-mail address
ignoring case, a phone number by its digits — as stored, or in Google's E.164
`canonicalForm` — or, with `field`, one named custom field. It is exact and current, and
it costs a read of the address book: at most 25 pages of a thousand contacts. A query
with a letter in it, or with fewer than five digits, never matches a phone number by its
digits.

`search-contacts` remains for what it is good at — a person's free text — warms the cache
before every search, and is documented as the wrong tool for the existence question.

### The contact a model sees and writes

A contact is one flat object, every key present: `id`, `displayName`, `givenName`,
`familyName`, `email` (the primary address) and `emails` (all of them, `{value, type}`),
`phone` and `phones`, `organization`, `jobTitle`, `department`, `address` (the primary
one) and `addresses`, `birthday` (`1980-05-17`, or `--05-17` without a year), `notes`,
`fields` (the custom fields by name) and `groups` (the labels a person made, by name;
system groups such as `myContacts` are not labels anybody put on a contact). Nested
objects keep Google's field names (`streetAddress`, `postalCode`, `countryCode`, …), so
Google's documentation of them stays true.

Writing uses the same keys, and the rules are decided once, for create and update alike:

- **null changes nothing.** It is what a FEEL value is when the variable it reads is not
  set, and an unset variable must never wipe a customer's data.
- **"" clears** — the primary address, the notes, one custom field.
- **A singular key changes the primary entry and keeps the others** (`email` is the
  address that changed); the plural key replaces the list.
- **`address` replaces the primary address whole**, keeping its type: a street from the
  new address and a city from the old one is no address.
- **`fields` sets the custom fields it names** and keeps the others.
- **Labels are set on create** (`groups`), in the same call as the fields, so a contact
  exists with them or not at all; on an existing contact they change through
  `add-to-group` and `remove-from-group` only.

An update reads the contact's current version, applies the changes and writes it back
under that version's etag; Google refuses a write against a changed contact with
`FAILED_PRECONDITION`, and the worker reads again, up to three times.

A key that is not one of these is refused — at deploy when the contact is authored inline,
when the task runs when it comes from a variable — rather than dropped: `famillyName`
would otherwise be a write reported as done that changed nothing.

### Delivery

At-least-once. An update writes the same values again, a label set twice is one label,
and a delete of a contact already gone succeeds — a retried delete must not fail because
its first round succeeded. `create-contact` is the exception: Google offers no request id
a create is deduplicated on, so a crash between "Google created the contact" and "the job
completed" creates it twice on the retry. A process that cannot tolerate that finds before
it creates; Google Contacts' own *Merge & fix* view shows a duplicate that slips through.

A contact that does not exist ends `get-contact`, `update-contact` and the label
operations with `GOOGLECONTACTS_CONTACT_NOT_FOUND`, thrown as a BPMN error when the
process catches the code ([ADR-0461](0461-script-bpmn-errors.md)'s rule) and a
failure otherwise.

### The credential

The Google Sheets bundle ([ADR-0235](0235-google-sheets-worker.md)), parsed strictly — an
unknown field is refused — with one difference: a `serviceAccount` bundle **must** name a
`subject`. A service account has no address book of its own; without a subject it would
read an empty one and report every customer as unknown. The subject is the Workspace user
whose address book the Worker uses, reached through domain-wide delegation of
`https://www.googleapis.com/auth/contacts`. `search-directory` also needs
`directory.readonly`, which the bundle names in `scope` — it is not requested by default,
because delegation grants exactly the scopes an administrator listed and asking for one
more fails the token request for every operation.

A `refreshToken` bundle serves a gmail.com account and asks for no scope of its own: the
token carries what its consent granted.

### Where it runs

Worker-only, for Exchange's reason: the engine builds no client and holds no credential
for this kind. The kind is supervised by default; the engine renders each Worker's bundle
into `ATLAS_GOOGLECONTACTS_<NAME>_CREDENTIALS` for the supervised worker, and an operator
running a worker by hand sets the same variables.

### As built

- `connector/googlecontacts/catalog` — the operation table, the contact keys and the
  checks a contact object is held to (`CheckContact` at deploy, `CheckValues` when the
  task runs), and the forms a contact id and a label take.
- `connector/googlecontacts` — `Bundle`, `NewClient` and `CheckBundle`; the People API
  client; `project` and `apply`, the flat contact in both directions; `Resolve` (engine)
  and `Run` (worker); `CodeContactNotFound`.
- `compiler/googlecontacts_compile.go` — `<atlas:googleContactsConnector>` at reserved job
  type `io.atlas.googlecontacts`, index 36.
- `api` — the worker-only kind, supervised by default (`googleContactsWorkerEnv` renders
  `ATLAS_GOOGLECONTACTS_<NAME>_CREDENTIALS` and `_URL`), the payload arm, the release row
  and the Repository package `atlas.google-contacts`; `worker/googlecontactsconnector.go`
  builds the address books from that environment.
- The Modeler panel (`api/web/googlecontacts-ops.js`, held to the catalog by
  `TestModelerKnowsEveryGoogleContactsOperation`), the Console card and dialog, the vault
  shape, the setup entry and the handbook card `runbook-googlecontacts`.

Tested by `connector/googlecontacts` against an in-memory People API over HTTP (every
operation, the etag retry, the label resolution, the error codes, the token grants, that
no error quotes a query), `compiler/googlecontacts_connector_test.go`, the supervision and
payload guards in `api`, `worker/googlecontactsconnector_test.go`, and the Playwright spec
`googlecontacts-modeler`.

### Consequences

- **Positive:** a process recognises, creates, updates and labels the people it works
  with in the address book the team already keeps, with no credential in any model and
  no People API shape in any diagram.
- **Positive:** the existence question has an exact answer, and the inexact one is named
  as such in the panel, the handbook and the package.
- **Negative / trade-offs accepted:** `find-contacts` reads the whole address book per
  call. For a book of a few thousand contacts that is a few requests; for one near
  Google's 25 000 limit it is twenty-five, and a process doing that for every incoming
  mail meets Google's per-minute read quota. Narrowing a find to a label is the remedy
  that exists today.
- **Negative:** a duplicate contact on a retried create, as stated above.
- **Negative:** one Worker is one address book. A team with several shared address books
  configures several Workers, or names the Worker with FEEL.
- **Follow-ups / risks to watch:**
  - A form-lookup provider for this kind ([ADR-0473](0473-form-remote-data-sources.md)),
    so a person picks a customer in a task form — the question "which customer is this
    request for" answered interactively rather than by a service task. The worker query
    channel exists; this kind would add a query handler and a provider row.
  - Whether `find-contacts` should keep a per-Worker index between calls if the quota
    becomes the limit in practice.
  - The open question above.

## Pros and cons of the options

### Option 1 — a Google Contacts Worker Type
- Good: the credential is configured once and never authored; the operations are named,
  checked at deploy and documented where they are chosen.
- Good: the flat contact and the write rules are decided once, not in every model.
- Bad: ten operations, a contact shape and its write rules are a surface to keep in step
  with Google's.

### Option 2 — Google auth on the REST Worker Type
- Good: every Google API becomes reachable at once.
- Bad: every model authors field masks, etags and the primary-entry logic itself, and the
  existence question is answered with whatever request the author happened to write.
- Not rejected as a future step, as ADR-0235 already said: it remains the way to reach
  the rest of Google.

### Option 3 — contacts in the Google Sheets Worker Type
- Good: one Google credential, one Worker.
- Bad: a spreadsheet credential and an address-book credential are different grants with
  different delegations, and one Worker holding both would need both scopes even where an
  operator wanted to give only one.

### Option 4 — a form-lookup provider only
- Good: no personal data in the event log beyond the chosen id.
- Bad: answers only "a person picks a contact"; recognising a sender, creating a customer
  and labelling one remain impossible. It is the follow-up, not the alternative.

## Links

- answers [ADR-0299](0299-worker-type-admission-criteria.md)'s five gates
- follows [ADR-0462](0462-exchange-worker.md) (worker-only, catalog package, supervision)
- shares the credential bundle of [ADR-0235](0235-google-sheets-worker.md) and the grant in
  `connector/oauth2` it moved there
- throws its refusal under [ADR-0461](0461-script-bpmn-errors.md)'s rule
- relates to [ADR-0473](0473-form-remote-data-sources.md) (the follow-up above)
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker Instance
