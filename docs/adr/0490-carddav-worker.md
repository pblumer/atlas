# ADR-0490: A CardDAV Worker Type — a process finds, keeps and labels contacts in any CardDAV address book

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-25
- **Deciders:** atlas maintainers
- **Open question:** how the hosted servers this record names behave on four points no
  test here reaches. The Worker Type is tested against an in-memory CardDAV server and
  was walked against Radicale 3.2.3 and Xandikos 0.4.10, two independent open-source
  servers, on a developer machine — not against iCloud, Nextcloud, mailbox.org, Posteo or
  Infomaniak.
  (1) That iCloud accepts a vCard 3.0 contact `PUT` under a name the client chose with
  `If-None-Match: *`, and answers a second one with 412 rather than overwriting it.
  (2) That iCloud and Fastmail show a contact Atlas added to a group vCard
  (`X-ADDRESSBOOKSERVER-MEMBER`) as a member of that group in Apple Contacts.
  (3) That Nextcloud's Contacts app shows a label Atlas wrote into `CATEGORIES` as a
  group, and that mailbox.org, Posteo and Infomaniak keep it. (4) That every one of them
  answers `PROPFIND`, `addressbook-multiget` and `If-Match` as RFC 4918, RFC 6352 and
  RFC 7232 say. Somebody with an account at each should run `TestAgainstARealServer`
  (`connector/carddav/live_test.go`, with `ATLAS_TEST_CARDDAV_URL`,
  `ATLAS_TEST_CARDDAV_BUNDLE` and, for group vCards, `ATLAS_TEST_CARDDAV_GROUP`), look at
  the contact in the provider's own app while it runs, and record here what they saw.
- **Question checked:** 2026-10

## Context and problem statement

A mail account comes with an address book far more often than with a Google Workspace.
IMAP itself carries no contacts: the provider bundles them beside the mailbox and serves
them through their own protocol, and outside Google and Microsoft that protocol is
usually CardDAV (RFC 6352) — iCloud, Fastmail, Nextcloud and ownCloud, mailbox.org,
Posteo, Infomaniak, Synology, SOGo and mailcow, Zimbra, Open-Xchange, Radicale, Baïkal.

The Google Contacts Worker Type ([ADR-0476](0476-google-contacts-worker.md)) answers the
business need — recognise a person, keep the address book current, act on a group — for
one provider. A team whose customer list lives in a Nextcloud or iCloud address book
cannot use it, and the generic paths do not reach CardDAV at all:

- The REST Worker Type authors `GET`, `POST`, `PUT`, `PATCH`, `DELETE` and `HEAD`
  (`compiler/parse.go`'s `restMethods`). CardDAV finds an address book with `PROPFIND`
  and reads its contacts with `REPORT`, so neither discovery nor a read of more than one
  known contact is possible.
- Where it does reach — a `PUT` of one vCard whose URL is known — every model would
  build and parse vCard text in FEEL: line folding, escaping, `TYPE=pref`, structured
  `N` and `ADR` values, and, on an update, every property it does not understand kept
  byte for byte.

The question: **how does a process read and write a CardDAV address book without a
credential in the model, without every model re-implementing vCard, and without
claiming that CardDAV and Google Contacts are the same thing when they are not?**

## Decision drivers

- **The five gates of [ADR-0299](0299-worker-type-admission-criteria.md)** are answered
  here.
- **The credential stays on the worker.** It reads a person's or a team's whole address
  book ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)).
- **A process must not be misled about whether a person is known**, the driver ADR-0476
  already names, and the find it decided on is reused.
- **Nothing a person keeps in a contact is lost by a write.** Photos, Apple's labels,
  social profiles and every vendor's `X-` properties travel in the same vCard as the
  fields a process changes. An update that dropped them would be destructive in a way
  nobody would notice until the photo was gone.
- **No equivalence is claimed that has not been shown.** ADR-0480 keeps technical
  capability claims advisory and forbids inferring substitutability from similar verbs.
  Two Worker Types with the same operation names are not thereby interchangeable.

## Considered options

1. **A CardDAV Worker Type with Google Contacts' operations and flat contact**,
   worker-only, supervised by default, its operation table in a dependency-free catalog.
2. **`PROPFIND` and `REPORT` on the REST Worker Type**, and models author CardDAV.
3. **One "contacts" Worker Type with Google, CardDAV and Microsoft Graph as backends**,
   the way the mail Worker Type carries SMTP, Gmail and Graph.
4. **Contacts operations on the mail Worker**, because the mailbox and the address book
   usually share one login.

## Decision outcome

Chosen option: **1 — a CardDAV Worker Type**, kind `carddav`, reserved job type
`io.atlas.carddav` at index 37, extension element `<atlas:carddavConnector>`.

### The five gates

| Gate | Answer |
|---|---|
| 1 Operations are process steps | Yes — nine named operations, below, each a task with a verb on it. |
| 2 The generic path is blocked or lossy | **Blocked** for discovery and every read of more than one contact: the REST Worker cannot author `PROPFIND` or `REPORT`. **Lossy** where it reaches: a vCard update has to keep every property it does not change, and no FEEL expression a model author writes does that reliably. |
| 3 The work fits a job | Yes. Each operation is one to a few dozen HTTP requests, bounded: a find reads at most 25 000 contacts in batches of 50, and an answer is read up to the installation's payload budget (`limits.Payload`, 8 MiB by default) — a batch whose photos take it above that is asked for again in halves, and a single card above it is refused by name. Every operation is safe to run twice, `create-contact` included (below). |
| 4 One credential shape | Yes — `{username, password}`, the password usually an app-specific one, with one non-secret setting beside it (`groups`). HTTP Basic authentication only. |
| 5 Arrives complete | Registry row, Repository package `atlas.carddav`, setup entry and handbook card, in this change. |

### The operation table

| Operation | What it is for | CardDAV |
|---|---|---|
| `find-contacts` | Is this e-mail address, phone number or customer number known? | `PROPFIND` of the address book, `addressbook-multiget` of its cards, compared on the worker |
| `get-contact` | Read the contact a process stored the id of | `GET` |
| `search-contacts` | A person's free text | the same read, matched as a substring on the worker |
| `list-contacts` | Every contact, or every member of one group | the same read |
| `create-contact` | A new customer, with its groups | `PUT` with `If-None-Match: *` |
| `update-contact` | A changed address, a new customer number | `GET`, then `PUT` with `If-Match` |
| `delete-contact` | An erasure request | `DELETE` |
| `add-to-group` / `remove-from-group` | Make somebody a "Kunde", or stop | the contact's `CATEGORIES`, or the group's own vCard (below) |

There is no `search-directory`: CardDAV has no organization directory. Photos, address
book administration, sync tokens and creating groups are not offered, for ADR-0476's
reasons.

### The contact is Google Contacts' contact

A contact is the same flat object ADR-0476 decided on, every key present: `id`,
`displayName`, `givenName`, `familyName`, `email`/`emails`, `phone`/`phones`,
`organization`, `jobTitle`, `department`, `address`/`addresses`, `birthday`, `notes`,
`fields` and `groups`. The write rules are the same: null changes nothing, `""` clears, a
singular key changes the primary entry and keeps the others, a plural key replaces the
list, `address` replaces the primary address whole, `fields` sets the fields it names,
and groups are set on create and changed afterwards through the group operations.

The keys map onto vCard (RFC 6350, and vCard 3.0 of RFC 2426, which Apple's clients
write):

| Key | vCard |
|---|---|
| `id` | the contact's resource name in the address book — the last segment of its URL, e.g. `5F3A….vcf` |
| `displayName` | `FN` |
| `givenName`, `familyName` | `N`'s second and first components; the other three are kept |
| `email`, `phone` | the `EMAIL` / `TEL` marked preferred (`TYPE=pref` in 3.0, the lowest `PREF` in 4.0), else the first |
| `organization`, `department` | `ORG`'s first and second components |
| `jobTitle` | `TITLE` |
| `address` | the preferred `ADR`; its components as `poBox`, `extendedAddress`, `streetAddress`, `city`, `region`, `postalCode`, `country` |
| `birthday` | `BDAY`; a birthday without a year reads `--05-17`, written as `--0517` in 4.0 and in Apple's form (`X-APPLE-OMIT-YEAR=1604`) in 3.0, which has no yearless date |
| `notes` | `NOTE` |
| `fields` | the card's own `X-` properties, by name without `X-`: `"fields": {"Kundennummer": "4711"}` is `X-Kundennummer:4711` |
| `groups` | `CATEGORIES`, or the group vCards the contact is a member of |
| entry `type` | `TYPE`, lower-cased, `pref` and `internet` dropped, and `cell` read as `mobile` (and written back as `CELL`), so one value means the same in both Worker Types |

Four differences from Google Contacts are deliberate, and each is what vCard can and
cannot hold:

- **`displayName` is writable.** `FN` is stored, not formatted by the server, and a
  company has an `FN` and no name parts. When a write changes the name parts and names
  no `displayName`, `FN` is formatted again from `N`, honorifics included; when there is
  no name at all it falls back to the organization, then the e-mail address.
- **An address has no `countryCode` and no `formattedValue`.** `ADR` has neither; a key
  the vCard cannot hold is refused rather than reported as written.
- **A custom field's name is a vCard property name**: letters, digits and hyphens. A
  name a vendor's own properties use (`X-ABLabel`, `X-APPLE-…`, `X-ADDRESSBOOKSERVER-…`,
  `X-EVOLUTION-…`, `X-MOZILLA-…`, `X-MS-…`, `X-PHONETIC-…`, `X-SOCIALPROFILE`) is
  neither read as a field nor accepted as one, because a field written must read back as
  the same field and must not change what another client meant.
- **Search matches substrings on the worker**, not prefixes on a server-side index, so it
  is exact and current like a find, and its order is the listing's: by family name, then
  given name.

### A write keeps what it does not change

A vCard is kept as the list of its properties, each with the line it was read from. A
write rewrites only the properties a contact key maps to and serialises every other one
from its original line, so a photo, an Apple label (`item1.X-ABLabel`), a vendor's
`X-` property or a property this code has never heard of survives a write unchanged. A
property the write removes takes the Apple label and country code of its own group
(`X-ABLabel`, `X-ABADR`) with it once nothing else is left in the group, and nothing
else. A card that carries `REV` gets it stamped; one that does not, does not get one —
clients synchronise on ETags. A new card is vCard 3.0, the version every server named
here reads.

### Groups: two forms, one per Worker

CardDAV servers keep groups in two forms that do not mix, and DAVx5 asks the same
question for the same reason:

- **`categories`** (the default): a group is a value of the contact's `CATEGORIES`
  (RFC 6350 §6.7.1). Nextcloud, ownCloud and most self-hosted servers use it. A group
  exists by being on a contact, so a group that no contact carries yet is not refused —
  there is nothing to check it against. Labels set on create travel in the same `PUT`
  as the contact, so it exists with them or not at all.
- **`group-vcards`**: a group is a vCard of its own, `X-ADDRESSBOOKSERVER-KIND:group`
  (3.0) or `KIND:group` (4.0), whose `X-ADDRESSBOOKSERVER-MEMBER` / `MEMBER` lines name
  its members by `urn:uuid:` and their `UID`. iCloud and Fastmail use it, because Apple
  Contacts does. A group must exist, and a misspelt name is refused, as in Google
  Contacts. A membership is a write to the group's card under its `ETag`, re-read up to
  three times when somebody changed it in between. Labels set on create are written to
  the group cards after the contact, and a retry that finds the contact already created
  writes them again, which a member already present makes harmless.

The Worker's bundle names the form: `"groups": "categories"` or `"groups":
"group-vcards"`. Group vCards are never contacts: listings, finds and searches leave
them out in both forms.

In the `group-vcards` form, knowing a contact's groups means reading the address book's
group cards, and a CardDAV server cannot be asked for those reliably — `addressbook-query`
with a property filter is required by RFC 6352 but is reported to be partial on iCloud.
So `get-contact`, `update-contact`, the group operations, and `create-contact` when it
names groups read the address book once per job in this form, as a find does.

### Find reads; search reads too

The `find-contacts` of ADR-0476 is taken as it is: it reads the address book (`PROPFIND`
for the members, then `addressbook-multiget` in batches of 50) and compares on the
worker — an e-mail address ignoring case, a phone number by its digits, or, with
`field`, one custom field. It does not use `addressbook-query`, whose text matching
differs between servers in collation and in what it supports; a find that silently
answered "unknown" on one server would create a duplicate customer.

### Delivery: create-contact is safe to repeat

At-least-once, like every Worker Type, but with no exception. CardDAV lets the client
choose a contact's resource name, so the engine derives one from the job: a UUID made
from this installation's node id and the job key, the same on every delivery of that job
and different for every other job on any installation. The `PUT` carries
`If-None-Match: *`; a retry after a create that succeeded but whose job did not complete
is refused with 412, and the worker reads the contact it already created and completes
with it. The duplicate ADR-0476 accepts for Google cannot happen here.

An update writes under the `ETag` it read and re-reads up to three times on 412 — except
under a weak `ETag`, which `If-Match` compares strongly and would refuse every time, so
that write is sent without the precondition. A delete of a contact already gone succeeds;
a group change already made is no change and no write. A contact
that does not exist ends `get-contact`, `update-contact` and the group operations with
`CARDDAV_CONTACT_NOT_FOUND`, thrown as a BPMN error when the process catches the code
([ADR-0461](0461-script-bpmn-errors.md)) and a failure otherwise.

### The address book and the credential

The Worker's endpoint is required and is either the address book's own URL or any URL the
server can be asked from — the server root, a principal, `/.well-known/carddav`
(RFC 6764). The worker discovers the address book once: the endpoint's resource type,
then `current-user-principal` and `addressbook-home-set`, then the address books in that
home. Exactly one is used; more than one is refused with their names and paths, so the
operator sets the endpoint to the one meant. The address books Nextcloud generates for
itself (`z-app-generated--…`, `z-server-generated--…`, its "Recently contacted") are not
counted, so a Nextcloud account with one address book of its own needs only the server's
`…/remote.php/dav`. DNS SRV discovery is not done: the endpoint is a URL.

The endpoint must be `https`; `http` is accepted for a loopback host only, because the
credential is sent with every request. Redirects are followed for every method —
including `PROPFIND`, which Go's client would turn into a `GET` — up to five hops, never
from `https` to `http`. The credential goes with them and to the home another host names
(iCloud's `pNN-contacts.icloud.com`): it is that server's answer to an authenticated
request, so following it trusts nobody the credential did not already reach.

The bundle is parsed strictly — an unknown field is refused:

```json
{"username": "verkauf@firma.ch", "password": "app-specific password", "groups": "categories"}
```

### Where it runs

Worker-only, for ADR-0476's reason, and supervised by default: the engine renders each
Worker's endpoint and bundle into `ATLAS_CARDDAV_<NAME>_URL` and
`ATLAS_CARDDAV_<NAME>_CREDENTIALS` for the supervised worker, and an operator running a
worker by hand sets the same variables.

### What this record does not claim (ADR-0480)

The two contact Worker Types share operation names and the flat contact so that a model
moving from one address book to the other changes little, and so that a later canonical
contract ([ADR-0480](0480-technical-capability-contract.md)'s T1) has two implementations
to be held against. They are **not** declared interchangeable, and this change adds no
capability claim to either. The differences above — a writable `displayName`, the
address keys, the field names, the two group forms, a create that is safe to repeat here
and not there, and a search that matches differently — are exactly the properties
ADR-0480 §3 says a contract must settle first. Contacts of an Exchange mailbox
(Microsoft Graph) are a third implementation and not part of this change.

### As built

- `connector/carddav/catalog` — the operation table, the contact keys, `CheckContact` at
  deploy and `CheckValues` when the task runs, and the forms a contact id and a field
  name take. It imports nothing of atlas's, so the compiler reads the rows the worker
  runs.
- `connector/carddav` — `vcard.go`, the lossless vCard 3.0/4.0 reader and writer;
  `dav.go`, the WebDAV client (`PROPFIND`, `REPORT`, `GET`, `PUT`, `DELETE`, redirects,
  discovery); `contact.go`, the flat contact in both directions; `book.go`, the
  operations over one job's view of the address book (the full read, find, search, the
  group forms, the create and the updates under their `ETag`); `config.go`, `Bundle`,
  `NewClient` and `CheckBundle`; `offload.go`, `Resolve` (engine) and `Run` (worker),
  `CodeContactNotFound`, `CreateKey` and `CreateUID`; `live_test.go`, the run against a
  real server.
- `compiler/carddav_compile.go` — `<atlas:carddavConnector>` at reserved job type
  `io.atlas.carddav`, index 37.
- `api` — the worker-only kind, supervised by default (`cardDAVWorkerEnv`), the payload
  arm, which carries the create key derived from the node id the server now keeps from
  startup (`Server.nodeID`), the release row and the Repository package `atlas.carddav`;
  `worker/carddavconnector.go` builds the address books from the environment.
- The Modeler panel (`api/web/carddav-ops.js`, held to the catalog by
  `TestModelerKnowsEveryCardDAVOperation`), the Console card and dialog, the vault
  shape, the setup entry and the handbook card `runbook-carddav`.

Tested by `connector/carddav` against an in-memory CardDAV server over HTTP — both group
forms, vCard 3.0 and 4.0, discovery through `/.well-known/carddav` and a home on a second
host, the `ETag` retry, the repeated create, the property round trip — and by the
compiler, `api`, worker and Playwright tests. `TestAgainstARealServer`, skipped unless an
address book is named, walked every operation on a developer machine against
Radicale 3.2.3 (from its root and from the address book, with categories, and with an
Apple-style vCard 3.0 group card) and Xandikos 0.4.10 (from its root, with categories,
and with a vCard 4.0 `KIND:group` card); both passed, the repeated create included.

### Consequences

- **Positive:** every CardDAV address book — most mail providers outside Google and
  Microsoft, and every self-hosted groupware — is reachable from a process with the same
  operations and contact as Google Contacts, and no credential in a model.
- **Positive:** `create-contact` is safe to repeat, which ADR-0476 could not offer.
- **Negative / trade-offs accepted:** a find, a search and a listing read the whole
  address book, and in the `group-vcards` form so does reading one contact. For a book of
  a few thousand contacts that is a few dozen requests.
- **Negative:** HTTP Basic only. A server configured for Digest (older Baïkal installs)
  must be switched to Basic.
- **Negative:** custom fields are `X-` properties, which most contact apps do not show. A
  customer number written by a process is found by a process; a person editing the card
  in Apple Contacts does not see it.
- **Negative:** in the `group-vcards` form a deleted contact's member line stays in its
  group cards: removing it would mean reading the address book for every delete. Clients
  ignore a member they do not hold; whether Apple Contacts does is part of the open
  question.
- **Follow-ups / risks to watch:**
  - `addressbook-query` for the group cards where a server is known to support it, if
    the full read in the `group-vcards` form becomes the cost that matters.
  - Contacts of an Exchange mailbox through Microsoft Graph.
  - A form-lookup provider ([ADR-0473](0473-form-remote-data-sources.md)), as for Google
    Contacts.
  - The open question above.

## Pros and cons of the options

### Option 1 — a CardDAV Worker Type
- Good: one Worker Type reaches a dozen providers and every self-hosted server.
- Good: the contact shape and write rules are ADR-0476's, so they are decided once.
- Bad: a vCard reader and writer and a WebDAV client in the tree — there is no
  dependency for either in `go.mod`, and this repository writes its protocols itself
  (IMAP in `connector/mail`).

### Option 2 — `PROPFIND` and `REPORT` on the REST Worker Type
- Good: opens WebDAV and CalDAV too.
- Bad: every model parses multistatus XML and vCard in FEEL, and an update that keeps the
  properties it does not understand is beyond what a model author can be asked to write.

### Option 3 — one contacts Worker Type with backends
- Good: one Worker Type for an author to find.
- Bad: [ADR-0480](0480-technical-capability-contract.md) rejects exactly this shape for
  people-shaped data: one surface over providers whose differences matter — here a
  create that may or may not duplicate — invites a model written against one to be
  pointed at the other. And Google Contacts has shipped as its own kind, at its own job
  type.

### Option 4 — contacts on the mail Worker
- Good: one login, one Worker, as the user sees one account.
- Bad: the endpoint, the protocol and, at Microsoft, the permission differ; the
  operations and the contact have nothing in common with a mailbox's; and ADR-0476 made
  the same call for Google Contacts beside Google Sheets.

## Links

- answers [ADR-0299](0299-worker-type-admission-criteria.md)'s five gates
- follows [ADR-0476](0476-google-contacts-worker.md) (operations, contact, write rules,
  find) and [ADR-0462](0462-exchange-worker.md) (worker-only, catalog package,
  supervision)
- relates to [ADR-0480](0480-technical-capability-contract.md) (no claim, the
  differences listed)
- throws its refusal under [ADR-0461](0461-script-bpmn-errors.md)'s rule
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker
  Instance
