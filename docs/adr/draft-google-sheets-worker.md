# ADR-DRAFT: Google Sheets as a Worker Type — a spreadsheet is a process data source

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas maintainers

## Context and problem statement

A large share of the data a business process runs on does not live in a database. It
lives in a spreadsheet that somebody maintains by hand: the list of applicants, the
budget line items, the tracking table the team actually looks at, the register a
regulator asks for once a year. The people who own that data will never open a SQL
client, and asking them to is how a process gets routed around instead of adopted.

Atlas can already reach the systems that have an API and an operator willing to
configure it — mail ([ADR-0079](0079-outbound-mail-connector.md)), SharePoint
([ADR-0141](0141-sharepoint-connector.md)), Jira ([ADR-0201](0201-jira-connector.md)),
Remedy ([ADR-0106](0106-bmc-remedy-connector.md)), a relational database
([ADR-0173](0173-generic-sql-connector.md)). **It cannot reach a Google
spreadsheet at all**, and the reason is not that nobody wrote the package. It is the
credential.

The generic REST Worker Type ([ADR-0067](0067-service-task-connector-catalog.md)) is
the usual answer to "an API we have not wrapped yet", and it does not work here.
Google's APIs are reached with an OAuth2 access token obtained by signing a JWT-bearer
assertion with a service account's RSA private key and exchanging it at Google's token
endpoint. The REST Worker Type's auth surface is a bearer token, HTTP Basic, or an API
key held server-side — none of which a Google API accepts, and none of which a model
author could bridge without putting a private key into a process variable. So the
absence is structural, not merely unwritten work.

The second thing this record has to settle is that **"a spreadsheet" is two APIs**. The
cell-level work — read a range, write a range, append a row, clear a range, add or
remove a tab — is the Sheets API v4, addressed by a spreadsheet id. But a spreadsheet
is also a *file*: creating one in a particular folder, and deleting one, are Drive API
v3 operations. A Worker Type that offered only the Sheets half would answer "create a
sheet" with "create a tab in a spreadsheet you already have", which is not the question
that was asked.

## Decision drivers

- **The existing seam, not a new one.** [ADR-0203](0203-worker-execution-model.md) and
  [ADR-0207](0207-worker-type-packaging.md) say what adding a capability costs:
  one package under `connector/`, one `managedConnectorKind` entry, one reserved job
  type index. Anything more than that is a defect in the change, not in the seam.
- **No credential in a model (I6).** A service account's private key is the most
  dangerous secret in this integration. It belongs in the vault, resolved server-side
  by connector name, and it must never reach a BPMN file, an event, or a variable.
- **Off the hot path (I1/I2/I3/I4).** The call is network I/O: it happens in a job
  worker, after fsync, never on the processor goroutine and never inside
  `applyToState`.
- **Compile, don't interpret (I5).** Which values an operation takes is a deploy-time
  question. A model that authors a range on an operation that has no range must be
  refused at deploy, not have the value silently dropped at call time.
- **The authored surface is what a process does, not what Google offers.** The Sheets
  API has batch update requests for merges, conditional formats, pivot tables and
  charts. None of those is a step a process takes.

## Considered options

1. **A Google Sheets Worker Type with a fixed operation table**, spanning Sheets v4 and
   Drive v3 behind one connector kind and one Google credential.
2. **Extend the REST Worker Type with a Google service-account auth method**, and let
   models author Google's URLs and request bodies themselves.
3. **A script task** (ADR-0047) calling a Google client library from Python.
4. **Sheets v4 only**, and tell an operator to create the spreadsheet by hand.

## Decision outcome

Chosen option: **1 — a Google Sheets Worker Type with a fixed operation table**, spanning
both APIs behind one credential, following the Jira Worker Type's shape (ADR-0201)
line for line: an `Ops` table that the compiler mirrors and a drift test keeps honest,
a single `Do` on the client, and every authored value literal-or-FEEL.

### The operation table

What earns a row is a step a process takes against a spreadsheet — the same test
ADR-0201 applied to Jira, which is why there is no formatting, no chart and no pivot
here.

| Operation | API | What it is for |
|---|---|---|
| `create-spreadsheet` | Drive v3 + Sheets v4 | A new spreadsheet, optionally in a named folder |
| `add-sheet` | Sheets v4 | A new tab in an existing spreadsheet |
| `read-range` | Sheets v4 | Read an A1 range; with `header`, as a list of objects |
| `write-range` | Sheets v4 | Overwrite the cells of an A1 range |
| `append-row` | Sheets v4 | Append rows after the last row of a table |
| `clear-range` | Sheets v4 | Empty an A1 range, keeping the sheet |
| `delete-sheet` | Sheets v4 | Remove one tab |
| `delete-spreadsheet` | Drive v3 | Move the whole file to the owner's trash |

`create-spreadsheet` is the one row that is deliberately two calls: Sheets creates the
file, and Drive moves it into the folder when one is named. A single Drive `files.create`
with the spreadsheet mime type would have been one call, but it cannot seed the tab
titles the same request wants to set, and a process that creates a sheet almost always
creates it with its columns already in place.

`delete-spreadsheet` **trashes rather than purges**. `files.delete` is permanent and
the mistake is unrecoverable; `files.update` with `trashed: true` is what a person
does in the UI and what an owner can undo. A process that deletes the wrong
spreadsheet is a bad afternoon either way, but only one of the two is survivable. A
permanent delete is deliberately not offered.

### The shape of values

`write-range` and `append-row` take a `values` literal-or-FEEL value. Sheets writes a
list of rows, each a list of cells, and a FEEL value reaching this connector is one of
three shapes:

- a **list of lists** — used as the rows verbatim;
- a **flat list of scalars** — one row;
- a **list of contexts** (the shape a process actually holds: `[{name: "…", amount: 3}]`)
  — projected into rows through the task's `columns` attribute, which names the fields
  and their order.

The third case is why `columns` exists. Without it, every model would carry a FEEL
projection (`for r in rows return [r.name, r.amount]`) to say something the task should
be able to state declaratively, and the column order — the thing that has to match the
sheet — would be buried inside an expression rather than visible in the properties
panel. A list of contexts with no `columns` is refused at deploy, not guessed at: the
key order of a context is not a column order, and picking one would make the failure a
silently transposed table rather than an error.

The reverse direction is `read-range`'s `header` flag: with it set, the range's first
row is read as column names and the result variable receives a list of contexts, which
is what a multi-instance subprocess or a gateway can actually use. Without it, the
result is the raw rows.

### The credential

One vault bundle per connector, the same shape the mail Worker Type already uses for
Gmail (ADR-0093): `serviceAccount` (a `clientEmail` and a PEM `privateKey`, with an
optional `subject` to impersonate a Workspace user through domain-wide delegation), or
`refreshToken` for a consumer account that has granted access once. The scopes are
Google's: `spreadsheets` and `drive` (or `drive.file`, which is enough when the
service account creates everything it touches).

The JWT-bearer service-account grant lived in `connector/mail` as the one grant the
shared `connector/oauth2` package deliberately did not carry — "the one such case", as
its own doc comment put it. This record makes it the second, so it **moves into
`connector/oauth2` as `oauth2.ServiceAccount`** and mail delegates to it. Two copies of
a JWT signer is precisely the duplication that package was extracted to end.

### Consequences

- **Positive:** a spreadsheet becomes a first-class process data source and sink,
  reachable with no credential in any model. The Modeler's properties panel names the
  eight operations and the values each takes, so an author is guided rather than left
  composing Google URLs.
- **Positive:** the service-account grant becomes shared machinery, so a token-refresh
  defect is fixed once rather than in two connectors.
- **Negative / trade-offs accepted:** **delivery is at-least-once, and a spreadsheet has
  no idempotency key.** A crash between "Sheets appended the row" and "job completed"
  replays the append and the row appears twice. Google offers nothing to prevent it —
  there is no request id an append is deduplicated on. A process that cannot tolerate a
  duplicate row must write a mark column and read before it appends, and this is a
  property of the target, not a defect this Worker Type can hide. It is stated in the
  handbook next to the operation rather than left to be discovered in production.
- **Negative:** one connector kind spans two Google APIs, so which operations work
  depends on the scopes the operator granted. A `drive.file`-scoped credential can
  create and then reach its own spreadsheets but not one a person shares with it; the
  failure surfaces as a 403 from Google on the operation, not at configuration time.
- **Negative:** a range is an A1 string. Sheets' own errors for a malformed range are
  poor, and Atlas does not parse A1 to improve them, because a partial parser that
  disagrees with Google about what is valid is worse than passing the string through.
- **Follow-ups / risks to watch:** `batchUpdate` for formatting, if a process ever
  needs it; a Drive-native file Worker Type, of which `create-spreadsheet` and
  `delete-spreadsheet` here are a two-operation preview; and the inbound direction —
  a spreadsheet or a folder as an *event source* — which is its own record.

## Pros and cons of the options

### Option 1 — a Google Sheets Worker Type
- Good: the credential is configured once by an operator and never authored; the
  operations are named in the model and checked at deploy.
- Good: it is the seam every other Worker Type uses, so it moves with them out of the
  engine process when ADR-0203's migration reaches it.
- Bad: eight operations across two APIs is more surface than a wrapper around one
  endpoint, and every one of them needs its own test.

### Option 2 — Google auth on the REST Worker Type
- Good: no new package; every Google API becomes reachable, not only Sheets.
- Bad: the model then carries the URL, the request body shape and the value-input mode
  for every call — Google's API surface leaks into the diagram, and the diagram is the
  thing a business reader is supposed to be able to read.
- Bad: it makes the REST Worker Type the place vendor auth accumulates, which is how a
  generic tool becomes a pile of special cases.
- Not rejected as a future step: a service-account auth method on the REST connector is
  still the right way to reach the *rest* of Google, and this record's move of the grant
  into `connector/oauth2` is what would make it cheap.

### Option 3 — a script task
- Good: available today with no Atlas change.
- Bad: the private key would have to reach the script, which means a process variable
  or a file on the worker host — the credential leaves the vault, which I6 forbids.

### Option 4 — Sheets v4 only
- Good: half the surface, one API, one scope.
- Bad: "create a spreadsheet" and "delete a spreadsheet" are the two operations a
  person asks for first, and neither is a Sheets call. Answering them with "a tab" is
  answering a different question.

## Links

- relates to [ADR-0067](0067-service-task-connector-catalog.md) — the connector task seam
- relates to [ADR-0093](0093-native-mail-providers.md) — where the Google service-account grant came from
- relates to [ADR-0201](0201-jira-connector.md) — the operation-table shape this follows
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker Instance
- relates to [ADR-0174](0174-connector-payloads-are-the-input-mapping.md) — literal-or-FEEL authored values
