# ADR-DRAFT: A form looks up the asking person's own address book through their linked account

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-26
- **Deciders:** Atlas maintainers
- **Open question:** three provider facts this record rests on and could not check
  against a live account. (1) Whether a Microsoft Graph access token obtained by a
  refresh that asks for `People.Read` alone carries only that scope, or every scope the
  connection was granted. Microsoft's guidance says a refresh may ask for the same
  scopes or a subset, but not what the issued token's `scp` claim then holds. The record asks for the subset either way;
  if the token carries more, a lookup Worker Instance holds a broader token than it
  needs, for its lifetime. (2) Whether `GET /me/people?$search=` finds every entry of a
  person's Outlook contacts or only the people Microsoft ranks as relevant to them; the
  documentation describes the second. (3) Whether personal Microsoft accounts can be
  linked at all: ADR-0475 links one Entra tenant's accounts, and its amendment left a
  multi-tenant or consumer registration as a decision of its own.
- **Question checked:** 2026-10

## Context and problem statement

A process asks a person to choose somebody — the recipient of a letter, the guest of an
event, the customer a request is for — and the somebody can come from one of two
places:

- **the organisation's directory**, the same for everybody: the colleagues in Entra ID;
- **the person's own address book**: their Outlook contacts and the people they write
  to, different for every person, and nobody else's business.

[ADR-0473](0473-form-remote-data-sources.md) built the first. A directory field is a
form-js select whose options come from an ephemeral worker query: the server holds the
query in memory, a Worker Instance leases it, searches with **its Worker's own
credential**, and answers one page. Nothing about a search is durable; only the
person's final choice becomes a variable.

[ADR-0475](0475-identity-aware-worker-execution.md) built delegated credentials. A
person links their Microsoft account; a connection holds the refresh token in the vault;
a Worker whose identity policy is delegated receives a short-lived access token, and
only on two conditions: a Permit recorded durably on the log for that job, that lease
and those parameters, and a request from a Worker Instance that Atlas supervises.

The second source needs both mechanisms at once, and today they do not meet. Measured
against `main` at 03438f6 (by reading the code):

| Question | Today |
|---|---|
| Whose credential does a lookup use? | The Worker's own. `lookup.Query` carries no subject, and the broker (`api/workerquery`) does not know who asked. |
| Where is a delegated token released? | Only at `POST /api/v1/jobs/{key}/authorize`, for a leased job, after an `AuthorizationDecided` event, to a supervised Instance presenting the per-start broker key (`X-Atlas-Broker-Key`). |
| Which providers can a form search? | One: `entra`/`users` (`api/formlookup/provider.go`), with the Entra Worker's app identity. |
| Which Worker Types can be delegated? | SharePoint only (`governableKinds`). |
| As whom does a delegated Worker act? | `delegated-fixed`: one connection's owner. `delegated-per-job`: `actAs: "initiator"`, the person who started the instance. |
| What do consent and approval require? | The person's own session (`Principal.Session`), not an API token or an OAuth grant. |

The two records also pull in opposite directions. ADR-0473's first driver is that
nothing per keystroke reaches the log. ADR-0475's guarantee is that no credential leaves
Atlas without a durably recorded decision. Applied together without a decision of their
own, one of them is broken.

The provider constrains the answer as well. Microsoft Graph's list of a person's Outlook
contacts (`GET /me/contacts`, delegated `Contacts.Read`) cannot search by name: its
`$filter` accepts only `emailAddresses/any(a: a/address eq '…')`, and `startswith` is
refused. A type-ahead over it would mean reading the whole contact folder per keystroke.
Graph's people API (`GET /me/people?$search=…`, delegated `People.Read`) searches by name
and alias with fuzzy matching. It works for work and school accounts and, with
`People.Read`, for personal Microsoft accounts. It pages with `$top` only: `$skip` is
refused together with `$search`.

The question this record answers: **how can a form search the address book of the
person filling it in, through that person's linked account, without a durable record
per keystroke, without the credential going anywhere ADR-0475 does not let it go, and
how does a form choose between the person's own address book and the organisation's?**

## Decision drivers

- **Nothing per keystroke is durable** (ADR-0473; invariants I2 and I6). A search is not
  a fact about the process. Only the choice is.
- **A person's credential leaves Atlas only as a short-lived access token, to a
  supervised Worker Instance** (ADR-0475 §11). External Worker Instances remain refused
  (ADR-0475's open question, ADR-0168).
- **Identity comes from the Worker's configuration**, never from the form, the request
  or a variable (ADR-0475, *Bypass paths*). A form names a Worker; the Worker decides as
  whom it acts.
- **A person searches only their own data, in their own session.** No other person, and
  no application holding a grant, reads anyone's address book through this channel.
- **Consent is the person's.** A connection is used only by the Workers its owner
  allowed (ADR-0475 §3).
- **A submitted value means the same to everybody who reads it later.** A later service
  task may run as another person, or as a service.
- **Failure is visible and owned.** A person without a usable connection is told what to
  do. A search does not silently switch to another source.
- **Mixed audiences.** An organisation and a person on a hosted Atlas use the same
  mechanism. Only the provider account differs.

## Considered options

For **how a lookup is authorized and where the credential is used**:

1. **A durable decision per lookup**: ADR-0475's enforcement point applied to every
   query, with an `AuthorizationDecided` event per keystroke.
2. **A durable lookup grant**: one recorded decision per person, task and Worker when the
   form opens, followed by non-durable lookups under that grant until it expires.
3. **The browser calls the provider** with the person's access token
   (ADR-0473 option 4).
4. **The server calls the provider** with the person's access token: Atlas performs the
   Graph request itself (ADR-0473 option 3, ADR-0475 enforcement option 4).
5. **A delegated worker query**: the query carries the person who asked. The supervised
   Worker Instance that leases it asks for that person's short-lived token, gated by the
   person's own consent and session. Nothing is recorded per lookup.

For **how a form chooses the source**:

- **a.** One field that falls back: the person's own address book when they have a
  connection, otherwise the organisation's directory.
- **b.** One field with several sources, each value tagged with the source it came from.
- **c.** One field, one source. A form that offers both has two fields, or one choice
  that shows one of them.

## Decision outcome

Chosen: **option 5 for the mechanism and option c for the form**, under the rules below.

### Why not the others, at their strongest

**Option 1** is the one that keeps ADR-0475 whole: every use of a person's credential
would be evidence, read from the log after a restart. Its cost is the one ADR-0473
refused for jobs: three durable records per keystroke, and the search term, which is
personal data about a third person, in the WAL and in every backup. It would also record
a decision whose subject, beneficiary and data owner are one and the same person, which
is evidence of nothing an auditor asks for. ADR-0475 records decisions because an action
is taken *for* someone *as* someone, possibly with someone else's approval. A person
reading their own address book is none of those.

**Option 2** is the strongest alternative. It bounds the log to one record per opened
form, and it would let a policy refuse personal lookups to some groups. But a grant is a
durable capability, which ADR-0475 deliberately does not have: an old Permit is never a
capability, and it is bound to a lease epoch the next lease moves past. A grant would
also need a new value type, on the partition of a task that the lookup never changes,
for an authorization the person gives themselves. The control it adds, refusing the
feature to a group, is available without it: a Worker is open to form lookups only when
its editors open it (ADR-0473), and an administrator can leave it closed.

**Option 3** puts no Atlas machinery between the person and their data, and Graph
enforces exactly what the person may see. It also hands a bearer token for the person's
account to the browser, where an extension or a script injected into a form can read it,
and it lets the client decide what is asked. ADR-0473 rejected it for these reasons.
Nothing about a personal address book makes them weaker.

**Option 4** is the least code, with no protocol change and no Worker Type. It puts a
Graph client with a person's token into the engine process, which ADR-0172 and
ADR-0475 (enforcement option 4) both kept out, and it would make Atlas the place where
every person's mailbox-adjacent data is read. That is exactly the concentration the
Worker boundary exists to avoid.

**Option a** is the convenient reading of "use the person's own address book where
there is one": one field that always shows something. It means one field whose meaning
depends on who fills it in. One person's choice comes from the directory,
another's from a private address book, and the process cannot tell which. A person who
has not linked their account sees colleagues instead of their contacts and is not told
why. **Option b** fixes the ambiguity by tagging each value, but then every consumer of
the variable has to branch on the tag. That is two fields' worth of logic hidden inside
one field.

### What a delegated query is

A Worker answers form lookups **as the person who asked** when its identity policy is
delegated. The form does not say so: it names the Worker, as every directory field does,
and the Worker's identity policy decides.

| Worker's identity policy | A form lookup through it runs as |
|---|---|
| *(absent)* or `service` | the Worker's own credential, as ADR-0473 built it |
| `delegated-per-job` | **the person who sent the query**, through that person's own connection, whatever `actAs` says for the Worker's jobs |
| `delegated-fixed` | refused (`invalid_configuration`): it would show one person's address book to everybody with a task |

`actAs: "initiator"` stays what it is for jobs. A lookup has no initiator to act for:
the person who started the instance is not the person searching, and searching the
starter's address book on someone else's behalf is exactly what must not happen.

The server checks, in this order, after ADR-0473's own gates (task right, form field,
Worker exists, enabled, of the provider's type, open to form lookups):

1. **The person's own session.** The caller is a person (`usr_…`) signed in to the
   Console, not an API token, an OAuth grant or a service principal. This is the rule
   ADR-0475 applies to approvals and to consent, for the same reason.
2. **A usable connection of the caller's own**: an active connection of the provider,
   owned by the caller, that allows this Worker and whose granted scopes include the
   provider's lookup scope (`People.Read`). If none does, the answer is
   `connection_required` with what is missing. It does not fall back.
3. **A supervised Worker Instance.** The query is offered only to Instances that present
   the broker key, as delegated jobs are. An external Instance never leases it, and a
   query with no such Instance present is refused at once as `worker_unavailable`.

The broker then holds a query that carries **its requester and connection** beside the
structured search. The Worker Instance that leases it calls a new worker-scope route:

```text
Worker Instance leases query q (lease token t)
  -> POST /api/v1/worker-queries/{q}/credential   {leaseToken: t}
     + X-Atlas-Broker-Key
     server: q still waiting? t its lease? q delegated?
             connection still active, still allows this Worker, still the requester's?
             Worker endpoint still the provider's (Graph)?
             -> identity.AccessToken(connection, scopes: the provider's lookup scopes)
  <- {accessToken, expiresAt}          Cache-Control: no-store
Worker Instance performs the structured query with that token, answers q
```

This is the same check the job route makes, minus the decision: the same broker key,
the same refusal of external Instances, the same endpoint pin, the same connection
state read at release time, and the same per-connection refresh serialization and token
cache. A revoked connection fails the next release. A token already released stays
valid at the provider until it expires (ADR-0475 §13).

The token is requested for the provider's lookup scopes only. Whether Microsoft then
issues a token limited to them is the first open question above.

### What is not recorded, and what is

- **Nothing per lookup is durable.** There is no event, no `AuthorizationDecided`, and no
  search term in a log line, a metric or an error, all as in ADR-0473.
- **The connection's use is visible to its owner** as today: a refresh moves its
  `lastRefreshAt`, which *Connected accounts* shows as "last used". Lookups served from
  the cached token do not move it.
- **The choice is durable**, as the task completion it always was.

The policy decision point is not asked. ADR-0475's policies decide actions taken for
someone; this record's gates are the person's consent (their connection allows the
Worker), the Worker's openness to lookups, and the task right. A policy that must keep
some people from searching their own address book in a form is a follow-up, and so is
the durable grant that would carry its decision (option 2).

### The value a personal source submits

ADR-0473 submits the provider's object id, because a directory id means the same to
everyone who later reads it. A personal source has no such id. A Graph people result or
an Outlook contact id names an entry in one person's mailbox, and a later service task
running as somebody else, or as a service, cannot resolve it.

So a personal provider declares a **portable value property** and submits that: for
people and contacts, an **e-mail address**, one option per address when an entry has
several. Verification at completion (ADR-0473, `"verify": "lookup"`) runs through the
**completing person's own connection**: the address must be in their address book. A
task completed by someone other than the person who searched is checked against the
completer's address book, because the completer is the one submitting it. With
`"verify": "format"`, only the address's shape is checked.

The display name that goes with the address is not submitted. Submitting it would mean
writing values the server verified, beside the one the person chose. That is a change to
completion and is left as a follow-up (*a verified projection*). A process that needs
the name asks for it in a field of its own.

### How a form offers both sources

One field, one source (option c). This is the request behind this record taken
literally: the personal connection is used where the form asks for the person's own
address book, and a central Worker where it asks for the organisation's. A form that lets the person choose writes two
directory fields — *Aus meinen Kontakten* and *Aus dem Firmenverzeichnis* — or a choice
whose value shows one of them (form-js conditional visibility). The process reads two
variables of declared meaning and decides which to use, in FEEL, where the decision can
be seen.

In the Tasks app, a delegated field whose person has no usable connection shows, instead
of a search box, what is missing and where to fix it. That is *Connected accounts* when
linking is set up, or the administrator otherwise (the wording of the Connected accounts
page, ADR-0475 amended). It never shows the other source.

### The first provider

**Microsoft Graph people**: resource `people`, `GET /me/people?$search=…&$top=…`,
delegated `People.Read`. The value is an e-mail address. Searchable means the provider's
own name-and-alias search: no field list, no `startswith`. The readable properties are
`displayName`, the scored e-mail addresses, `companyName` and `jobTitle`. A page holds at
most 25, and there is **no next page**: Graph refuses `$skip` with `$search`, so a person
narrows the term instead of paging.

`GET /me/contacts` is not the first provider, because it cannot search by name. It
remains the right source for a later *contact folder* resource that lists rather than
searches.

The Worker Type that carries the provider is not decided here. It has to:

- talk only to Microsoft Graph;
- hold no credential of its own;
- be delegated-only;
- answer queries and never run jobs.

None of the shipped Worker Types fits. SharePoint is a document Worker; Entra is an
application identity by ADR-0172, and its dynamic Worker names are refused an identity
policy. Its admission is a record of its own under
[ADR-0299](0299-worker-type-admission-criteria.md).

Google follows the same shape once ADR-0475's Google adapter exists. The Google
Contacts Worker Type ([ADR-0476](0476-google-contacts-worker.md)) holds a service
account or one pasted refresh token today, so it would be the organisation's address
book in this record's terms, not the person's. CardDAV
([ADR-0490](0490-carddav-worker.md)) has no delegated form: an app password is not a
connection.

### Who this works for

Organisation accounts work as soon as the provider and its Worker Type exist, with
`People.Read` added to the linking scopes. A connection authorized before that addition
lacks the scope. Gate 2 answers `connection_required`, and its owner signs in once more.

People on a hosted Atlas with a personal Microsoft account (outlook.com, hotmail.com)
are served by the same mechanism and the same provider, since `People.Read` is
available to personal accounts. They cannot link such an account today, though: linking
is one Entra tenant's confidential client (ADR-0475 §4, amended). Opening linking to
personal accounts is the third open question, and the condition for this record to
reach the audience that asked for it.

### Consequences

- **Positive:**
  - A form can offer a person's own contacts beside the organisation's directory. Both go
    through the same field type, the same broker and the same completion check, and
    neither source pretends to be the other.
  - The person's refresh token stays in the vault. Their access token goes only to a
    supervised Worker Instance, only for a query they sent in their own session, and
    only through a connection whose Worker they allowed.
  - No new value type, no event, and no change to the processor, the state store or
    recovery. Invariants I1 to I6 are untouched.
  - The organisation's directory can later be searched the same way, as the person with
    `User.ReadBasic.All`, instead of with the Entra Worker's application identity, so that
    Graph enforces what each person may see. This record does not decide that. It only
    stops ruling it out.
- **Negative / trade-offs accepted:**
  - **A credential use with no durable evidence.** ADR-0475 releases a token only after a
    recorded Permit. This record releases one per lookup with none. The justification is
    narrow: the subject, the beneficiary and the data owner are the same person, acting
    in their own session. It does not carry over to anything that writes, or to anything
    done for someone else.
  - **A compromised supervised Worker Instance** receives the access token of everybody
    who searches through it, for that token's lifetime and scopes. That is ADR-0475 §11's
    exposure, met per keystroke instead of per job.
  - **No paging.** The first provider answers one page of at most 25. A person narrows
    the search; nobody scrolls an address book.
  - **"Relevant people", not "all contacts"**, until the second open question is
    answered.
  - **A new worker-scope route** (`/worker-queries/{id}/credential`) and a new field on
    the leased query (`delegated`). Only supervised Instances receive delegated queries,
    and Atlas starts those from its own binary (`os.Executable`), so no older Worker
    Instance has to be waited for. An external Instance never sees them.
  - **Two fields where a form author might have wanted one.** That is the price of a
    value whose meaning does not depend on who filled it in.
- **Follow-ups / risks to watch:**
  - The Worker Type for Graph people, admitted under ADR-0299.
  - Linking personal Microsoft accounts (ADR-0475, amended: a decision of its own).
  - A verified projection: submitting the display name the server verified, beside the
    address.
  - A contact folder resource (`/me/contacts`), listed rather than searched.
  - Policy over personal lookups, and the durable grant it would need (option 2).
  - Start forms for signed-in people. A personal source does not depend on what a task
    would let the person see, so the task question that confines ADR-0473 to task forms
    may not be the right question here.
  - Returning to the task after linking: the link flow lands on *Connected accounts*
    today.

## Pros and cons of the options

### Option 1 — a durable decision per lookup
- Good: ADR-0475's guarantee unchanged; every credential use is evidence.
- Bad: three durable records and a third person's search term per keystroke; evidence of
  a decision whose subject, beneficiary and data owner are one person.

### Option 2 — a durable lookup grant
- Good: one record per opened form; a policy could refuse personal lookups to a group.
- Bad: a durable capability, which ADR-0475 avoids; a new value type on a partition the
  lookup does not change; control that a closed Worker already gives.

### Option 3 — the browser calls the provider
- Good: no Atlas machinery; the provider enforces visibility.
- Bad: a bearer token for the person's account in the browser; the client decides the
  query.

### Option 4 — the server calls the provider
- Good: least code; no protocol or Worker Type change.
- Bad: a Graph client with people's tokens in the engine process, which ADR-0172 and
  ADR-0475 both refuse.

### Option 5 — a delegated worker query (chosen)
- Good: nothing durable per lookup; the token goes only to a supervised Instance, only
  with the person's consent and session; the mechanism stays ADR-0473's.
- Bad: a credential use without durable evidence, justified only for a person reading
  their own data; a new worker-scope route.

### Option a — one field that falls back
- Good: one field for the author.
- Bad: the value's meaning depends on who filled it in; a person without a connection is
  silently shown other data.

### Option b — one field, values tagged by source
- Good: one field, unambiguous values.
- Bad: every consumer branches on the tag.

### Option c — one field, one source (chosen)
- Good: each variable has one meaning; the process decides between them visibly.
- Bad: two fields where one would look simpler.

## Links

- extends [ADR-0473](0473-form-remote-data-sources.md): a worker query that runs as the
  person who sent it
- extends [ADR-0475](0475-identity-aware-worker-execution.md): delegated credentials for
  a query as well as a job, under the same broker key and the same refusal of external
  Instances; its open question about external Worker Instances is unchanged
- keeps [ADR-0168](0168-connector-work-on-a-worker.md) and
  [ADR-0172](0172-entra-id-connector.md): no provider client with a person's token in the
  engine
- a new Worker Type under [ADR-0299](0299-worker-type-admission-criteria.md)
- relates to [ADR-0476](0476-google-contacts-worker.md) and
  [ADR-0490](0490-carddav-worker.md), the organisation's address books in this record's
  terms
- relates to [ADR-0356](0356-person-picker.md): Atlas's own people, shown to everyone
