# ADR-0498: A wikiwikibangbang Worker Type — a process opens and moves tasks, and creates, reads and extends pages in a wikiwikibangbang wiki

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-26
- **Deciders:** Atlas maintainers
- **Open question:** two points this record could not settle. (1) **As whom a task
  writes.** A Worker is one technical account of the wiki, so every page, task and comment
  a process writes appears as written by that account, and a process may write wherever
  the account may — whoever started the process. Whether a task should act in the name of
  the person behind the process, and how the wiki would accept such a delegation, is
  [ADR-0475](0475-identity-aware-worker-execution.md)'s question for this Worker Type and
  is not answered here. (2) **How long the wiki's API keeps its shape.** wikiwikibangbang
  describes itself as an early prototype, and the Worker Type is built and walked against
  its API as of commit `928fec0` (2026-10-26). Whether the wiki gains a precondition on a
  body commit and an idempotency key on a create — the two pieces that would close the
  gaps named under *Delivery* — is the wiki's decision, not this repository's. Somebody
  who upgrades the wiki should run `TestAgainstARealWiki`
  (`connector/wikiwikibangbang/live_test.go`, with `ATLAS_TEST_WWBB_URL`,
  `ATLAS_TEST_WWBB_TOKEN`, `ATLAS_TEST_WWBB_SPACE`, `ATLAS_TEST_WWBB_TASK_TYPE` and
  `ATLAS_TEST_WWBB_PAGE_TYPE`) and record here what changed.
- **Question checked:** 2026-10

## Context and problem statement

[wikiwikibangbang](https://github.com/dbuchs7/wikiwikibangbang) keeps knowledge and work
in one model: every page and every task is an **item** of a type, with typed properties,
labels, an optional workflow state and an optional block-document body (its ADR-0001). A
task is an item whose type has a workflow; a page is an item whose type has a body.
Atlas already reads that wiki — a service's content is copied from it into the catalogue
([ADR-0491](0491-service-content-from-an-external-source.md)) — but only reads, and on
purpose with a token of scope `read`.

Processes want the other direction. An onboarding process opens a task for the person
who prepares the laptop and waits until it is done; an incident process writes a page
for the post-mortem and appends a line to it at every step; a change process reads the
runbook page it is about to execute and comments on the change task when it is through.
That is the loop the Jira Worker Type ([ADR-0201](0201-jira-connector.md)) covers for
issues — open, read, change, move, comment, find — plus the two things a wiki adds:
**read a page** and **extend a page**.

The generic path reaches the wiki: its API is HTTP and JSON, and its tokens are bearer
tokens, which the REST Worker Type ([ADR-0067](0067-service-task-connector-catalog.md))
can hold server-side. But every model would then re-author the same integration, and get
it wrong in the same places:

- **A body is a list of blocks**, not text or Markdown. A sentence is
  `[{"kind":"paragraph","text":"…"}]`; a model writing anything longer hand-builds that
  list in FEEL.
- **There is no append.** `PUT /api/items/{ref}/body` replaces the whole body, without a
  precondition on the version it was read at. Extending a page is a read, a
  concatenation and a write, and a model that writes the concatenation itself also has
  to carry every block kind it does not understand back unchanged.
- **A parent is an id, not a key** (the wiki's invariant D2), so a model that knows a
  page by its key `OPS-12` first has to read it to learn its id.
- **A create is not repeatable.** A job is delivered at least once
  ([ADR-0007](0007-job-worker-protocol.md)); a create whose answer was lost creates a
  second item unless something finds the first.
- **A transition already made fails.** Moving a task to `done` twice is refused the
  second time with `transition "finish" is not allowed from state "done"`, so a retried
  job parks on an incident for a change that succeeded.

The question: **how does a process open and move tasks and create, read and extend pages
in the wiki, without a credential in the model, without every model re-implementing the
item and block format, and without claiming the wiki and Jira are the same thing?**

## Decision drivers

- **The five gates of [ADR-0299](0299-worker-type-admission-criteria.md)** are answered
  here.
- **The credential stays on the worker** ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)). A token of scope `full` writes
  everything its account may write.
- **Least privilege at the wiki.** The account is an editor in the spaces a process
  writes to and nothing more; an administrator is not bound by the wiki's restrictions
  (its ADR-0022) and is refused. It is a different account from the read-only one
  ADR-0491 reads the catalogue's content with, whose standing that record checks before
  every read.
- **Nothing a person wrote is lost by a write.** An append carries every block it read
  back verbatim, including kinds this Worker does not know.
- **No equivalence is claimed that has not been shown**
  ([ADR-0480](0480-technical-capability-contract.md),
  [ADR-0485](0485-technical-capability-semantics.md)). Where the wiki and Jira mean the
  same thing, the two Worker Types are spelled alike; that is all.

## Considered options

1. **A wikiwikibangbang Worker Type with eight operations, spelled like Jira's where the
   meaning is the same**, worker-only and supervised by default, its operation table in a
   dependency-free catalog.
2. **Recipes for the REST Worker Type**, and models author the wiki's API.
3. **The wiki as a second backend of the Jira Worker Type, or one "work item" Worker
   Type over both.**
4. **An AI agent with the wiki's MCP tools** (its ADR-0012), driven by an agent round
   ([ADR-0254](0254-agent-rounds-on-a-worker.md)).

## Decision outcome

Chosen option: **1 — a wikiwikibangbang Worker Type**, kind `wikiwikibangbang`, reserved
job type `io.atlas.wikiwikibangbang` at index 38, extension element
`<atlas:wikiwikibangbangConnector>`.

### The five gates

| Gate | Answer |
|---|---|
| 1 Operations are process steps | Yes — eight named operations, below, each a task with a verb on it. Spaces, types, permissions, restrictions, attachments and live editing are not offered. |
| 2 The generic path is blocked or lossy | **Lossy.** The REST Worker reaches the API, but every model would build block lists in FEEL, resolve a parent's key to its id, find before it creates, write a read-concatenate-write for an append, and treat a transition already made as a failure — each a place a token parks on an incident rather than a deploy check. |
| 3 The work fits a job | Yes. Each operation is one to four requests; a query reads at most 1 000 items in pages of 100, and every answer is read up to the installation's payload budget (`limits.Payload`, 8 MiB by default). What is and is not safe to run twice is stated per operation under *Delivery*. |
| 4 One credential shape | Yes — `{"token": "…"}`, a wiki API token, beside the Worker's endpoint, the wiki's base URL. |
| 5 Arrives complete | Registry row, Repository package `atlas.wikiwikibangbang`, setup entry and handbook card, in this change. |

### The operation table

| Operation | What it is for | The wiki's API | Jira's counterpart |
|---|---|---|---|
| `create-item` | Open a task, or create a page | `POST /api/items`; with `matchField` first `GET /api/items?where=` | `create-issue` |
| `get-item` | Read an item — its state, properties, labels and body | `GET /api/items/{ref}` | `get-issue` |
| `read-page` | Read a page as Markdown, to mail it or hand it to an agent | `GET /api/items/{ref}/export/md` | — |
| `update-item` | Change the title, properties and labels — hand a task to somebody, set a due date | `PUT …/title`, `PUT …/properties/{field}`, `PUT …/labels` | `update-issue`, `assign-issue` |
| `transition-item` | Move a task through its workflow, by the transition's name | `POST …/transitions` | `transition-issue` |
| `add-comment` | Say something on an item | `POST …/comments` | `add-comment` |
| `append-to-page` | Extend a page at its end | `GET …`, then `PUT …/body` | — |
| `query-items` | Find the items that match — the open tasks of a person, the pages with a label | `GET /api/items?query=` (its ADR-0136) | `search` |

An item is addressed by `item`: its id or its key, as the wiki accepts both. A process
that keeps a reference keeps the **id** — a key changes when an item moves to another
space, the id never does (the wiki's D2).

There is no `assign-item`. In the wiki the person a task is assigned to is a property of
its type, with whatever name the type gives it — `assignee` in the common case — so
assigning is `update-item` with `properties: {"assignee": "anna"}`.

### The item a model sees

Every operation that answers with an item answers with the same flat object: `id`, `key`,
`type`, `space`, `parent`, `title`, `state`, `properties`, `labels` and `version`.
`get-item` adds `body`, the blocks as the wiki returns them; the writes do not, so a
process that extends a long page at every step does not copy the page into a variable at
every step. `read-page` answers with the page as Markdown text: title, properties and
body, as the wiki exports it (its ADR-0131). `query-items` answers with a list of items
without their bodies, and `add-comment` with the comment: `id`, `author`, `created`,
`text`.

### Writing a body: text or blocks

`create-item` and `append-to-page` take a body in one of two forms, never both:

- **`text`** — plain text. Each run of lines separated by an empty line becomes a
  paragraph; a single line break stays a line break inside its paragraph, which is how
  the wiki keeps a hard break.
- **`blocks`** — the wiki's own block list, for headings, lists, tables or panels: a JSON
  array written into the model, or a FEEL expression that evaluates to one. The worker
  checks that each block is an object with a `kind`; what a kind may hold is the wiki's
  to check.

Markdown is not accepted. The wiki converts Markdown into blocks in its own code (its
ADR-0138), and a second converter here would drift from it; a wiki that accepts Markdown
on its API would make it a third form.

### Properties and labels

`properties` is a JSON object written into the model, each value literal or `=FEEL`, or
`propertiesVariable` names a variable holding the object. A value that is **null changes
nothing** — an absent variable must not clear a due date — and a value that is **`""`
unsets** the property. Every other value is sent as its FEEL kind decides: a number as a
number, a list as a list, everything else as a string. `labels` is a comma-separated
list or a FEEL list, and replaces the item's labels; the wiki normalises them (`Bad
Label` becomes `bad-label`).

`update-item` sets the title, then each property in the order of its name, then the
labels — one request each, because the wiki has no request that changes more than one.
A failure part-way leaves the earlier ones set; the retry sets them again, which changes
nothing.

### Delivery: what is safe to run twice

At-least-once, like every Worker Type. Per operation:

- **Reads** (`get-item`, `read-page`, `query-items`) are safe.
- **`update-item`** sets values; setting a value twice is setting it once.
- **`transition-item`** that the wiki refuses is answered by reading the item and its
  type: if the item already stands in the state the named transition leads to, the task
  succeeds. A process that asks for `finish` gets a finished task, whether this round or
  an earlier one — or a person — moved it.
- **`create-item` with `matchField`** names the property that identifies the item, e.g.
  `source-id`. The worker first looks for an item of that type in that space whose
  property has the value the task sets, and answers with it if there is one. Where the
  wiki's type declares the field unique (its ADR-0074), two rounds racing each other
  cannot both create: the second is refused, and the worker answers with the first's
  item. **Without `matchField` a repeated create can create a second item**, as Jira's
  can; the record accepts that, as ADR-0201 did.
- **`append-to-page` and `add-comment`** check, **when the job is being delivered again**
  (its lease epoch is above one), whether the round before already wrote: the page ends
  with exactly these blocks, or the last comment is this account's with exactly this
  text. If so, the task succeeds without writing. On a first delivery nothing is checked,
  so a process that appends the same line twice on purpose gets it twice. A check on
  content alone would have skipped that second line on every run.
- **An append is a read and a write.** Between the two, a person can change the page,
  and the write replaces that change; the wiki keeps it in the page's history, from where
  a person restores it. While a person edits the page in a live session (its ADR-0010),
  the wiki refuses the write with 409 and the job is retried. The window is the time
  between two requests of one job, milliseconds in practice; it is closed only by a
  precondition on the wiki's body commit, which it does not have.

### The account and the credential

The Worker's **endpoint** is the wiki's base URL, e.g. `https://wiki.example.ch`. It must
be `https`; `http` is accepted for a loopback host only. A redirect is followed only to
the same host over `https`, so the token is presented to nobody else. The token travels in
the `Authorization` header and appears in no error message and no log line.

The bundle is parsed strictly — an unknown field is refused:

```json
{"token": "wwbb_…"}
```

The token belongs to **a technical account of its own**, not to a person and not to the
read-only account ADR-0491 reads the catalogue with: that record stops every read when
its account's token is not of scope `read`. Before its first call, and again every ten
minutes, the worker reads the account's own standing (`GET /api/me`) and **refuses an
account that holds the wiki's system role `admin` or `erasure`**: an administrator is not
bound by restrictions, and nothing this Worker Type does needs either role. The account
holds `editor` in the spaces processes write to. A Worker that only reads may use a token
of scope `read`; the wiki refuses its writes with 403.

### Where it runs

Worker-only and supervised by default, for Google Contacts' reason
([ADR-0476](0476-google-contacts-worker.md)): the engine builds no client and holds no
token for this kind. It renders each Worker's endpoint and bundle into
`ATLAS_WIKIWIKIBANGBANG_<NAME>_URL` and `ATLAS_WIKIWIKIBANGBANG_<NAME>_CREDENTIALS` for the
supervised worker, and an operator running a worker by hand sets the same variables.

### What a process can catch

Two outcomes have a BPMN error code, thrown as a BPMN error when the task's process
catches it ([ADR-0461](0461-script-bpmn-errors.md)) and a failure otherwise:

| Code | When |
|---|---|
| `WIKIWIKIBANGBANG_ITEM_NOT_FOUND` | The item does not exist **or the account may not read it** — the wiki answers both alike (its D10), and so does this code. |
| `WIKIWIKIBANGBANG_VALIDATION_FAILED` | The wiki refused a value: a required property missing, a value of the wrong kind, a transition not allowed from the item's state, a unique value another item holds. The wiki's own sentence is the message. |

Everything else — 403, 409, a server error, an unreachable wiki — fails the job, with
its retries and then an incident.

### What this record does not claim (ADR-0480)

The operations are spelled like Jira's where the two systems mean the same thing, so a
model moving from one to the other changes little, and so that a later canonical
"work item" contract (ADR-0480's T1, ADR-0485) has two implementations to be held
against. They are **not** declared interchangeable, and this change adds no capability
claim to either — built-in Worker Types cannot carry one yet (the plan's T2), and none
would be true. The differences are exactly the properties ADR-0480 §3 says a contract
must settle first:

| | Jira (ADR-0201) | wikiwikibangbang |
|---|---|---|
| Identity | project, issue type, key; the key is the id a process keeps | space, type, an immutable id beside a key that moves with the item |
| Assignee | an `accountId` or a username, a field of every issue | a property of the type, under the type's own name |
| Transition | by id, or a name resolved against the issue's transitions | by name only |
| Search | JQL | the wiki's own query language |
| Text | wiki markup in a string | a list of blocks |
| An item one may not read | 404 or 403, depending on the configuration | 404, indistinguishable from an item that does not exist |
| A repeated create | creates a second issue | creates a second item, unless `matchField` finds the first |
| A repeated transition | refused | succeeds when the item already stands in its target state |

### What the wiki would need for the gaps that remain

None of these is built here, and none is this repository's to build:

1. **A precondition on a body commit** — the version it was read at — would close the
   append window above.
2. **An idempotency key on a create and on a comment** would make both safe to repeat
   without `matchField` and without the redelivery check.
3. **A check that a user property names an account.** Walked against the wiki at
   `928fec0`, a user property accepted `nobody-here` without complaint; a task assigned
   to a misspelt username is assigned to nobody, and neither side reports it.
4. **A change pushed to Atlas** — a webhook or the change feed (its ADR-0121) delivered
   as a message — would let a process *wait* for a task to be done instead of polling
   `get-item`. That is a message-correlation question, as it was for Jira
   ([ADR-0214](0214-jira-inbound-issue-watch.md)), not a Worker Type one.

### As built

- `connector/wikiwikibangbang/catalog` — the operation table and the checks a deploy and
  a run share. It imports nothing of atlas's, so the compiler reads the rows the worker
  runs.
- `connector/wikiwikibangbang` — `config.go`, `Bundle`, `NewClient`, `CheckBundle`, the
  redirect rule (reads to the same host only, writes never) and the registry;
  `client.go`, the requests (bounded answers, the wiki's 404 and 400 told apart, the
  token taken out of every message) and the standing check; `body.go`, text to blocks
  and the tail comparison; `ops.go`, the eight operations; `offload.go`, `Resolve`
  (engine) and `Run` (worker) and the two codes; `live_test.go`, the run against a real
  wiki.
- `compiler/wikiwikibangbang_compile.go` — `<atlas:wikiwikibangbangConnector>` at
  reserved job type `io.atlas.wikiwikibangbang`, index 38.
- `api` — the worker-only kind, supervised by default (`wikiWorkerEnv`), the payload arm,
  which carries whether the job is being delivered again, the release row and the
  Repository package `atlas.wikiwikibangbang`; `worker/wikiwikibangbangconnector.go`
  builds the Workers from the environment.
- The Modeler panel (`api/web/wikiwikibangbang-ops.js`, held to the catalog by
  `TestModelerKnowsEveryWikiOperation`), the Console card and dialog, the vault shape,
  the setup entry and the handbook card `runbook-wikiwikibangbang`.

Tested by `connector/wikiwikibangbang` against an in-memory wiki over HTTP and by the
compiler, `api`, worker and Playwright tests. `TestAgainstARealWiki`, skipped unless a
wiki is named, walked every operation against wikiwikibangbang at commit `928fec0`, run
locally with a task type that has a workflow and a unique `source-id`, and a page type.

### Consequences

- **Positive:** a task in the wiki is a handful of fields in the properties panel. The
  wiki's address and token move in the Console, not in models. A process can open a task
  and wait on it, and write the page that documents what it did.
- **Positive:** the shape follows Jira's, so the work-item contract ADR-0480 anticipates
  has two implementations to be tested against, and the differences are written down.
- **Negative / trade-offs accepted:** a repeated create without `matchField` can
  duplicate an item; an append can replace a change a person made in the milliseconds
  between its read and its write. Both are named, and both wait on the wiki.
- **Negative:** everything the Worker writes is written as its account. The wiki's
  history and comments name the account, not the person behind the process.
- **Follow-ups / risks to watch:** the open question above; waiting on a task by a
  message rather than by polling; attachments, which need a multipart upload.

## Pros and cons of the options

### Option 1 — a wikiwikibangbang Worker Type *(chosen)*
- Good: the block format, the parent's id, find-before-create, the append and an
  already-made transition are handled once, in code that is tested.
- Good: the token never enters the engine, and an account with a system role is refused.
- Bad: one more Worker Type to keep in step with an API that is still young.

### Option 2 — recipes for the REST Worker Type
- Good: nothing to build; the REST Worker already reaches the wiki with a server-held
  bearer token.
- Bad: every model repeats the block format and the read-concatenate-write, and the one
  that forgets to carry back an unknown block kind deletes it.
- Bad: nothing is checked at deploy, and a retried create duplicates.

### Option 3 — a Jira backend, or one "work item" Worker Type
- Good: one operation set for process authors, and the strongest form of the overlap:
  both systems hand a person a piece of work with a workflow.
- Bad: ADR-0480 rejects exactly this shape for directories, and the table above is why it
  would be wrong here too — identity, assignment, transitions, search and text all differ,
  and a model moved between them unseen would act on the wrong semantics.
- Bad: pages have no Jira counterpart, so the shared surface would be the smaller one.

### Option 4 — an agent with the wiki's MCP tools
- Good: the wiki already offers these operations as MCP tools, with its caller's rights.
- Bad: a model chooses which tool to call; a process step that must open exactly one task
  wants a deterministic call, not a decision. An agent remains the right shape for the
  steps that do need judgement, and `read-page` hands it the page.

## Links

- decided by: [ADR-0299](0299-worker-type-admission-criteria.md) (the gates),
  [ADR-0203](0203-worker-execution-model.md) (what a Worker Type is)
- follows: [ADR-0201](0201-jira-connector.md) (the operation shape and the spelling),
  [ADR-0476](0476-google-contacts-worker.md) and [ADR-0490](0490-carddav-worker.md)
  (worker-only, supervised, a catalog without dependencies, two Worker Types aligned
  without a claim)
- relates to [ADR-0491](0491-service-content-from-an-external-source.md) — the read-only
  account the catalogue reads the same wiki with, which stays separate
- relates to [ADR-0480](0480-technical-capability-contract.md) and
  [ADR-0485](0485-technical-capability-semantics.md) — no claim, and why
- relates to [ADR-0475](0475-identity-aware-worker-execution.md) — the open question of
  as whom a task writes
- relates to [ADR-0461](0461-script-bpmn-errors.md) — the two codes a process can catch
- relates to [ADR-0007](0007-job-worker-protocol.md) — the lease epoch the redelivery
  check reads
