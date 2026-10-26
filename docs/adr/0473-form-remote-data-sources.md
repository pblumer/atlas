# ADR-0473: A form looks remote data up through an ephemeral worker query

- **Status:** Accepted (amended 2026-10-23: a directory field is a form-js select whose
  options source is the remote data source, a fourth beside Static, Input data and
  Expression, edited under the select's own Options source; the vendored form-js bundles
  are rebuilt from a lockfile with Atlas's modules for it; a directory field saved as a
  textfield is still read, and the editor converts one on request)
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** whether Microsoft Graph answers the people search this record
  builds — `startswith` over several user properties joined by `or`, optionally
  `and`-ed with `department eq '…'` and `accountEnabled eq true` — as a default
  (strongly consistent) query in every tenant. Graph's own property tables mark each of
  those operators "default" for `displayName`, `givenName`, `surname`, `mail`,
  `userPrincipalName`, `department`, `jobTitle` and `accountEnabled`, and say nothing
  about a limit on how many such clauses one `$filter` may join. The Worker Type is
  tested against a recorded Graph, not against a live tenant. If a tenant refuses the
  combination, the answer is to send the lookup as an advanced query
  (`ConsistencyLevel: eventual` + `$count=true`), which the client already supports.
- **Question checked:** 2026-10

## Context and problem statement

A process asks a person to choose somebody from the directory — "which colleague is
this request for" — and then acts on that person with the Entra Worker Type
([ADR-0172](0172-entra-id-connector.md)), for instance `get-user-manager` with
`userId="= user"`. The only way to model that today is to run `list-users` first, store
the whole listing in a process variable, and have the form's select read its options
from that variable (`valuesKey`).

That works for a department of forty people and fails for an organisation:

- A tenant of 5 000 to 100 000 accounts is fetched in full before anybody has typed a
  letter. `list-users` *fails* rather than truncates above its `maxUsers` cap, on
  purpose — so the model either raises the cap past the directory size or does not run.
- The listing becomes an event in the log, a variable in the state store, a snapshot in
  every per-step history, and part of every backup — for a list of which exactly one
  entry matters, and which is personal data.
- The form receives the whole list and filters it in the browser.
- The list is stale from the moment it is written: a task can wait for days.

The question this record answers is the one the problem forces: **how can a form run a
secure, read-only, interactive query through an Atlas Worker without turning each
keystroke into a durable BPMN job, and without going around the Worker architecture?**

Three facts bound the answer.

1. **The credential is not in the engine.** Entra is worker-only. ADR-0172 made "the
   engine holds no Entra credential and builds no Graph client" a property of the types,
   because the app registration can read and often change the whole directory.
2. **The Worker protocol has one direction.** A Worker Instance is a client of the
   server: it leases jobs by long-polling `POST /api/v1/jobs/activate` and reports
   results. There is no path from the server to a worker.
   [ADR-0220](0220-checking-a-database-connector.md) declined to invent one "for a
   button", and wrote down that if a worker ever gains a request path, a check belongs
   there.
3. **A job is durable by construction.** Creating one is an event, leasing one is an
   event, completing one is an event, and all of them are replayed on recovery. That is
   right for work a process depends on and wrong for "show me the next 25 Müllers".

## Decision drivers

- **The Worker's credential stays in the Worker** (ADR-0172, ADR-0041/0069).
- **Nothing per keystroke reaches the log, the state store or the processor** (I2, I3,
  I6). A lookup is not a fact about the process; only the person's choice is.
- **The server decides what a form may ask.** The Worker, the tenant, the resource, the
  fields searched and the fields returned come from server-side configuration — never
  from the request.
- **The browser holds bounded results** — at most one page at a time, by default 25.
- **The form stays a form-js document** that renders, degraded but usable, anywhere
  form-js renders (ADR-0028).
- **The submitted value is the business decision**, and completion stays the one
  durable completion path that exists.

## Considered options

1. **Keep preloading** (`list-users` into a variable, `valuesKey` on a select).
2. **A durable job per lookup**: the lookup endpoint creates a job of the Entra type,
   waits for its completion and returns the result variable.
3. **The engine calls Graph itself**, from the vault bundle of a Console-managed Entra
   tenant — the shape of [ADR-0260](0260-ai-form-generation.md) (form generation) and
   ADR-0220 (the database check).
4. **The browser calls Graph** with a delegated token of the signed-in person.
5. **An ephemeral worker query beside the job**: a second, non-durable message kind in
   the Worker protocol, leased by the same Worker Instances under the same credential
   scope, held only in the memory of the server process that received the request.

## Decision outcome

Chosen option: **5 — an ephemeral worker query beside the job.**

### Why not the others, at their strongest

**Option 1** is not wrong for a batch: a nightly recertification that walks every
account wants the whole listing and `list-users` stays exactly as it is. For an
interactive choice it does every expensive thing the problem names, and its cap turns
growth of the directory into a failing process.

**Option 2** is the one that looks like reuse, and that is its appeal: one protocol, one
lease, one place where Entra runs. But a job is an event-sourced work item: a lookup
would write an activation, a lease and a completion to the WAL per keystroke, keep the
search term and the result page in the log and in every backup, and give replay three
records to apply for something with nothing to recover. A job also needs an element
instance to belong to, and a search has none. Reusing the job here is not reuse of an
abstraction, it is reuse of a durability guarantee nobody asked for.

**Option 3** is the cheapest by far and has two precedents. It still breaks the one
property ADR-0172 was written to establish — that the engine never builds a client for
this credential — and it would only work for tenants configured in the Console: a
tenant set on an external worker's host, which the engine has never seen, could not be
searched at all. The precedents are narrower than they look: ADR-0220 dials once, on an
operator's click, with the operator's own credential; ADR-0260 uses a credential whose
whole purpose is to be asked. A directory search is triggered by every task user, all
day, with an identity that can disable accounts.

**Option 4** puts no Atlas credential anywhere near the browser and lets Graph enforce
what the person may see, which is attractive. It requires a second app registration
with delegated permissions, a person signed in to Entra (Atlas authenticates its own
users), and it moves the decision about what a form may ask — which fields, which
filter — into code the person controls. It is also Entra's answer, not a data-source
answer: the next provider has no browser token at all.

### The query, and how it differs from a job

| | Job (service task) | Worker query (form lookup) |
|---|---|---|
| Created by | the processor, on activation | an HTTP request from a person's form |
| Durable | yes — events, WAL, replay | no — memory of one server process |
| Lifetime | until completed, failed or migrated | seconds: a deadline, or the request's cancellation |
| Result | variables, written by a completion command | a page of options, returned to the waiting request |
| Retries / incidents | yes | no — the person sees an error and searches again |
| Lost on restart | never | always; there is nothing to lose |
| Identity | Worker Instance under the `worker` scope | the same |
| Routing | job type (+ Worker name in the payload) | the same, and only to Instances reporting that Worker name |
| Fencing | lease token | the same |

Concretely:

- `POST /api/v1/worker-queries/activate` is the query twin of the job pull. A Worker
  Instance long-polls it with its job type and the Worker names it holds credentials for
  (the `connectors` a job poll already reports, ADR-0168). It receives at most a small
  batch of queries addressed to one of those names, each with an id and a lease token.
- `POST /api/v1/worker-queries/{id}/answer` delivers the result, or an error with a
  fixed code, fenced by that token. An answer to a query whose asker has gone — timed
  out, cancelled, or a server that restarted — is refused and dropped.
- Both routes are in the fail-closed `worker` scope (ADR-0194, ADR-0297), so the
  internal token of a supervised worker and an operator-minted worker token reach them
  and nothing else does more than it did.
- The broker holding them lives in `api/workerquery`. It is neither engine nor
  design-time state, so it is not behind the run loop; it is guarded by its own mutex,
  as the server's rate limiters are. Its queue is bounded per job type, every query has
  a deadline, and an Instance counts as present only while it keeps polling — so a
  lookup with nobody to answer it is refused at once as `worker_unavailable` instead of
  waiting out its deadline.

The processor, the WAL, the state store and recovery do not know any of this exists.

### What a form says, and what it cannot

The form-js version Atlas vendors (1.24) supports custom components only as Preact
components registered into the bundle's own Preact instance, and both vendored bundles
(viewer and playground) inline a Preact of their own and export none of it. A custom
component would mean rebuilding both bundles with extra exports and registering every
component twice. So the field is a **standard form-js `textfield`** — which holds the
submitted value, its label, `required`, its description and its errors — carrying one
declarative, versioned Atlas extension:

```json
{
  "type": "textfield",
  "key": "user",
  "label": "Person",
  "validate": { "required": true },
  "atlasDataSource": {
    "version": 1,
    "workerType": "entra",
    "worker": "ucc_entra",
    "resource": "users",
    "searchFields": ["displayName", "mail", "userPrincipalName"],
    "filters": [{ "property": "department", "field": "abteilung" }],
    "option": { "label": "displayName", "description": "mail", "details": ["department"] },
    "pageSize": 25,
    "minSearchLength": 2,
    "verify": "lookup"
  }
}
```

The form-js editor keeps unknown component properties on export, so the block survives
the editor; form-js ignores it, so anything that renders form-js without Atlas shows a
plain text field into which an id can be typed. In the Tasks app an Atlas widget
attaches to that field: a search box, a bounded result list, a selection — and it writes
the chosen value into the field the way typing would, so validation and submission stay
form-js's own.

Every value in the block is checked when the form is saved, against a **provider
registry** in the server: for `entra`/`users`, which properties may be searched
(`displayName`, `givenName`, `surname`, `mail`, `userPrincipalName`), filtered on with
`eq` (`department`, `jobTitle`), and shown (those plus `id`); that the value is the
object `id`; and that a page holds at most 50. The worker name must be a plain name —
not FEEL — because it is resolved on the server at query time, not evaluated per
instance.

A filter is either **bound** to another field of the same form (`"field"`) — the value
the person picked there — or **fixed** (`"value"`), which the server applies whatever the
request says. A bound filter whose source field is a select with static options only
accepts one of those options. There is no expression language in the block: the only
input at query time is the person's own form state, which is untrusted, and an
expression over it would add an evaluation surface without adding anything the binding
cannot say. Department choices come from the department field's own static options:
Graph has no "distinct department" query, and discovering them would mean reading the
directory this record exists to stop reading.

### The request, and the security boundary

`POST /api/v1/tasks/{key}/form-data/{field}/query` takes
`{"search", "filters": {property: value}, "cursor", "value", "limit"}` and nothing else;
an unknown member is refused. The server, in order:

1. requires an authenticated caller (route role `user`) and applies a per-person token
   bucket;
2. asks the same question task completion asks — `mayWorkTask`: the caller must be the
   assignee, a member of a candidate group, an operator, or the task must be open work;
3. reads the task's form from the form store and finds the field by key; the field must
   carry a valid `atlasDataSource`;
4. resolves the Worker by name from the Worker store: it must exist, be enabled, be of
   the provider's Worker Type, and have **form lookups enabled** — a switch on the
   Worker that its editors set (ADR-0205). Being able to see a form, or to edit one, is
   not permission to search a directory with somebody's credential;
5. validates the request against the field: search length (the field's minimum, a
   hard maximum of 64 characters, no control characters), only bound filter properties,
   each value at most 128 characters, a page size at most the field's;
6. hands the worker a structured query — resource, search fields, term, `eq` filters,
   properties, page size — never a URL and never an OData string.

The Entra Worker builds the Graph request from that structure with its own copy of the
same allowlist: `startswith(p,'t')` joined by `or` over the search fields, each literal
escaped by doubling `'` as OData requires, `and`-ed with the filters and with
`accountEnabled eq true`, then URL-encoded; `$select` is fixed to the allowed
properties; `$top` is the page size. It follows **no** page beyond the one asked for.

Paging is by **Graph's own `@odata.nextLink`**, which Graph's documentation says to use
whole rather than taking the skip token out of it. The server never shows it to the
browser: it seals it into an opaque cursor (AES-GCM under a key that exists only in this
process's memory) together with the task, the field, the person, the normalized query
and an expiry. A cursor that was altered, belongs to another query, task or person, or
has expired is refused as `invalid_cursor`; a restart makes every cursor invalid, which
costs the person a fresh search. The worker follows the link only within its own Graph
endpoint, as every listing already does (ADR-0172).

Nothing about a lookup is stored. The search term is not logged, not put into metrics,
and not part of any error message: a Graph error carries its request path, and that path
contains the term, so the worker answers with a fixed code (`provider_throttled`,
`provider_unavailable`, `provider_refused`, `not_found`) and the server words the
message. Only the person's final selection becomes a variable.

### Submission

Completion stays `POST /api/v1/tasks/{key}/complete`. For a form whose fields carry an
`atlasDataSource`, it now checks each such field's value before the command is
submitted — after the authorization check, and off the run loop:

- **Structurally invalid** — not a string, or not an object id (for Entra, a GUID):
  `400`. An object where an id was expected is refused, so a whole directory object
  cannot be submitted in place of its id.
- **Required and empty:** `400`.
- With `"verify": "lookup"` (the default), the value is looked up once through the same
  worker query, under the field's fixed filters and the bound filters as the submission
  states them:
  - absent, or outside those filters: `422` — **unauthorized or no longer present**;
  - the worker or Graph unavailable, throttled or too slow: `503` — a **transient**
    failure, nothing is completed, the person can press Complete again.
- `"verify": "format"` skips the lookup and keeps only the structural check.

This is a check, not a transaction. The directory can change between the lookup and the
durable completion, and between the completion and the next service task. Atlas does
not pretend otherwise: the next task reads the person by id, and `get-user-manager`
already has a path — `ENTRA_USER_NOT_FOUND` — for an id that stopped existing.

### Errors

Every refusal is the API's error envelope with a machine-readable `code` beside
`error`, and `retryAfter` (seconds) where waiting helps:

| HTTP | `code` | Meaning |
|---|---|---|
| 401 | — | not signed in (the auth boundary) |
| 403 | `forbidden` | not this person's task, or the Worker does not allow form lookups |
| 404 | `not_found` | no open task, or no such field on its form |
| 400 | `invalid_query` / `invalid_cursor` | the request breaks the field's rules |
| 422 | `invalid_configuration` | the form's data source refers to something the server cannot honour |
| 429 | `rate_limited` | this person searched too fast |
| 503 | `worker_unavailable` / `worker_busy` | no Instance serves that Worker now, or its queue is full |
| 503 | `provider_throttled` | Graph answered 429; `retryAfter` carries Graph's own advice |
| 502 | `provider_unavailable` / `provider_refused` | Graph failed, or refused the Worker's credential |
| 504 | `timeout` | no answer before the deadline |

### Consequences

- **Positive:**
  - The example process loses its `list-users` step: no directory in the log, the
    state, the history or a backup; one id is.
  - The browser holds at most one page; Graph filters on the server; a 100 000-account
    tenant costs what a 100-account tenant costs per keystroke.
  - The credential stays where ADR-0172 put it, for Console-managed and host-configured
    tenants alike, supervised or external.
  - The mechanism is the Worker protocol's, so the next provider is a row in the
    provider registry and a query handler in its Worker Type — not a new channel.
- **Negative / trade-offs accepted:**
  - **A new message kind in the Worker protocol.** It is small, but it is a protocol
    surface: an older external worker does not poll it, and its tenants then answer
    `worker_unavailable` until it is upgraded.
  - **One server process.** A query is answered by Instances polling the process that
    received the request. Atlas runs one process today; a replicated deployment
    (ADR-0175) would need the broker to be shared or the Instances to poll every node.
  - **Forms are not versioned** (ADR-0028, ADR-0465): editing a form's data source
    changes it for every open task bound to it, as editing any field does.
  - **No cache.** Every search reaches Graph. A cache keyed by Worker and query would
    be shared across people with different task rights; one keyed per person would
    rarely hit. Neither is worth its staleness yet.
  - **Department choices are configured**, not discovered, and departments are a
    free-text attribute, not a hierarchy. Administrative units and groups are different
    things and are not offered as one.
  - **Graph's order is Graph's.** Without an advanced query Graph does not sort a
    filtered listing, so the first page is the first 25 matches in Graph's order, not
    alphabetically the first 25.
  - **Only task forms.** A lookup needs a task to authorize against. A start form in the
    Console and a repair form render the field as the plain text field it is, with no
    search and no check of the value; a public start link and a public wizard step
    refuse a form with such a field, because nobody there is signed in to search and a
    check would answer anonymous visitors who is in the directory.
- **Follow-ups / risks to watch:**
  - Groups and administrative units as resources, and dynamic department discovery if
    Graph ever offers it.
  - `$search` as an opt-in search mode for tenants that want word matching and accept
    eventual consistency.
  - The same channel serves ADR-0220's connection check for worker-only kinds.

### As built

- `connector/lookup` — the wire shape: `Query`, `Filter`, `Page`, the failure codes and
  the bounds every lookup keeps (50 per page at most, 25 by default, 64 characters per
  term, 128 per value).
- `connector/entra/lookup.go` — `LookupResources` (the allowlist), `CheckLookup`,
  `RunLookup` and `ValidLookupValue`; `GraphError` carries `Retry-After`.
- `api/workerquery` — the broker (`Ask`, `Lease`, `Answer`, presence) and the two worker
  routes; `worker/query.go` — the Worker Instance's query loop beside its job loop,
  `Options.Queries`; the Entra Worker Type registers its lookup there.
- `api/formlookup` — the `atlasDataSource` schema and its checks (`Parse`, `Check`), the
  provider registry, the sealed cursor, `HandleQuery`, `HandleSources` and
  `CheckSubmission`; `api/formlookups.go` is its reach into the server (the task question,
  the Worker store, the broker, the rate limit).
- `configuredWorker.FormLookups` (`formLookups`) on a Worker record, settable only for a
  kind a provider exists for; the Console's Worker dialog shows it for Entra.
- Completion (`handleCompleteTask`) checks directory fields after the task question and
  before the command; the public start link and the public wizard step refuse a form with
  one; the form save refuses an `atlasDataSource` the server cannot honour.
- `api/web/remote-select.js` — the search on a directory field, for the Tasks app and the
  form editor's previews on made-up people; `api/web/vendor/form-js/src/atlas/` — the
  select that mounts it and the properties that edit it (amended 2026-10-23, below; the
  panel of the first build, `remote-select-editor.js`, and its **＋ Directory field** are
  gone).
- `examples/entra-benutzerauswahl` — `Process_mvjoqvisfajk` without `list-users`.

Tested by `connector/entra/lookup_test.go` (filter construction, escaping, allowlist,
paging, value lookups, failure codes), `api/workerquery` (lease, fencing, deadlines,
cancellation, presence, bounds), `worker/query_test.go`, `api/formlookup` (schema,
request validation, cursor, error mapping, submission), `api/formlookup_http_test.go` (the
example end to end against a recorded Graph with a real Worker Instance, the security
doors, a restart before and after the choice with the log position unchanged by
searches), and the Playwright specs `remote-select`, `tasks-remote-select`,
`form-directory-field` and `worker-test`.

### Amendment (2026-10-23): a directory field is a select

The record chose a textfield carrying the block because the vendored bundles inline a
Preact of their own and export none of it, so a component of Atlas's would have meant
rebuilding both. In the form editor that choice showed. The data source was edited in a
panel of Atlas's own laid above form-js's properties and reached by a button beside Save,
and the field read as a text field on the canvas and in the palette. An author asking
where a field's options come from looks under **Options source**, which is where form-js
asks it of every select; the panel was somewhere else.

So a directory field is now a form-js **select**, and the remote data source is a fourth
options source beside Static, Input data and Expression:

```json
{
  "type": "select",
  "key": "user",
  "label": "Person",
  "validate": { "required": true },
  "atlasDataSource": { "version": 1, "workerType": "entra", "worker": "ucc_entra", "resource": "users" }
}
```

The block is unchanged. It stands where a select's `values`, `valuesKey` or
`valuesExpression` would, and a select that names one of those as well is refused at
save: a select takes its options from one source. Every check above applies as written.

form-js describes its options sources in four exported tables — the sources, their
labels, the property each lives in, and the value a newly chosen one starts with — and
everything that deals with them reads those tables: the editor's Type select, the
behaviour that clears the other sources when one is chosen, and the viewer's
`getOptionsSource`. Adding a row to them is what makes the editor treat the remote
source as one of its own. No file of form-js is patched or copied.

The bundles are rebuilt for it, which is the cost the record had avoided, and paid this
way:

- `form-viewer.js` and `form-playground.js` are built from
  `api/web/vendor/form-js/src/` — the two entries, Atlas's modules and a
  `package-lock.json` — by the recipe in its `ATLAS-VENDORED.txt`, and a test holds each
  bundle to the checksum recorded there, as it holds the DMN bundle. The bundles of the
  first build were made from a recipe that named only the entry packages; they cannot be
  rebuilt byte for byte.
- The modules register into the bundle's own Preact when its Form or Playground is
  constructed. Nothing is exported for a page to register, and nothing is registered
  twice.
- What a search is — the request, the paging, the announcements, the keyboard — stays in
  `api/web/remote-select.js`. The page hands the bundle an adapter as the Form's
  `atlasRemote` option; the select mounts it into its own element and tells it every
  value form-js renders. A form rendered by the bundle without an adapter — a start form,
  a repair form — shows a box for the id, as the textfield did.
- The properties of the remote source are entries in the properties panel's own markup.
  The bundle does not import `@bpmn-io/properties-panel`: form-js-editor carries that
  library inside its dist and exports none of it, and a second copy would bring a second
  Preact, whose hooks do not run under the first.
- The palette's **Directory picker** adds a select with the first source this server
  offers and a Worker already open to forms. It is a preset, not a type: what it
  creates and what is saved is a select.

Forms saved before keep working. The server reads a textfield carrying the block as it
did, at save, at query time and at completion; the Tasks app attaches the same search to
it; and the editor shows it with **Convert to select**, which converts it only when its
author asks — nothing converts a stored form behind its author's back.

One thing is lost: form-js rendered without these modules does not know the fourth
source, and shows a remote select as a select with form-js's own placeholder option,
where it showed the textfield as a text field. Every surface Atlas renders a form on
uses the vendored bundle.

A tag list — several entries chosen from the same source — is not part of this
amendment. The submission check takes one id per field; a list of ids is its own change.

## Pros and cons of the options

### Option 1 — preload into a variable
- Good: exists; no new surface.
- Bad: the whole directory in the log, the state and the browser; fails by design above
  its cap; stale while the task waits.

### Option 2 — a durable job per lookup
- Good: one protocol; Entra runs where it always does.
- Bad: three durable records per keystroke, the search term in the WAL and in backups,
  replay of something with nothing to recover, and no element instance for the job to
  belong to.

### Option 3 — the engine calls Graph
- Good: the least code; no protocol change.
- Bad: the engine builds the client ADR-0172 forbids; host-configured tenants cannot be
  searched; every task user triggers a privileged call from the engine process.

### Option 4 — the browser calls Graph with a delegated token
- Good: no Atlas credential involved; Graph enforces the person's own visibility.
- Bad: a second app registration and an Entra sign-in Atlas does not otherwise need;
  the query is decided by the client; nothing generic for the next provider.

### Option 5 — an ephemeral worker query (chosen)
- Good: credential and execution stay in the Worker; nothing durable per lookup;
  server-resolved configuration; generic.
- Bad: a new protocol message kind; bound to one server process; old workers do not
  answer it.

## Links

- extends [ADR-0028](0028-forms-and-the-tasks-app.md) (forms and the Tasks app) with a
  field whose options come from a remote source
- extends [ADR-0007](0007-job-worker-protocol.md) and
  [ADR-0203](0203-worker-execution-model.md) with a non-durable query beside the job
- keeps [ADR-0172](0172-entra-id-connector.md)'s credential boundary; `list-users` is
  unchanged
- answers the request path [ADR-0220](0220-checking-a-database-connector.md) declined to
  invent for a single check
- authorizes with [ADR-0317](0317-task-commands-are-an-object-question.md)'s task
  question and [ADR-0205](0205-connector-ownership-and-event-delivery.md)'s Worker roles
- reaches workers through [ADR-0194](0194-api-tokens.md)/[ADR-0297](0297-confine-internal-worker-token.md)'s
  `worker` scope
- relates to [ADR-0356](0356-person-picker.md): that picker shows Atlas's own directory to
  everyone; this one shows an external directory only inside a task, only through a
  Worker that allows it
