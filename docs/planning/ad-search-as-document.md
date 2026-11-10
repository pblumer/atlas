# AD search as a document — delivery plan

**Status of this plan:** S0 and S1 done (2026-11-10). D1–D4 were confirmed as proposed;
D5 was answered with option 1. S2 and S3 are open.

**Builds on:** [ADR-0543](../adr/0543-a-file-is-a-document-not-a-variable.md) (documents),
[ADR-0548](../adr/0548-bulk-validation-evaluates-a-decision-per-row-in-one-job.md)
(a business rule task over a file), ADR-0166 and ADR-0534 (the AD Worker Type's search and
its binary attributes), [ADR-0314](../adr/0314-portal-personal-data.md) (personal data).

## Outcome

A process reads every user of a directory — 50,000 is the target size — and checks each
against business rules held in DMN, exactly as an uploaded file is checked today:

```
AD search (result as a document)  →  business rule task over a file  →  summary + result document
```

The second and third steps exist (ADR-0548 Slice 1, #1663). This plan adds the first: the
AD Worker Type's `search` writes its result as a CSV document instead of into a variable,
so a result of any size the document budget allows completes the job with a reference
rather than failing it.

## What exists, and why it does not reach 50,000

Read from the code, not assumed:

- `search` pages through the directory, 500 entries a page (`adSearchPageSize`), so a
  domain controller's administrative size limit does not refuse it.
- It holds every entry in memory (`Conn.Search` returns `[]Entry`) and writes them into the
  result variable as `{found, count, dn, entries: [{dn, attributes}]}`.
- `maxEntries` caps it, 1,000 by default (`defaultAdSearchMaxEntries`); more fails the job
  rather than truncating.
- Binary attributes (`objectGUID`, `objectSid`, …) arrive as base64 (ADR-0534).
- It runs in process or on a Worker Instance; both can already store a document
  (`jobDocuments.StoreDocument`, `workersdk.UploadDocument`).

50,000 users with ten attributes are roughly 15 MB of JSON: fifteen times the variable
budget (1 MiB) and far past `maxEntries`. Raising either would put a directory into the
log, the state and the per-step history — the cost ADR-0543 exists to avoid.

## Decisions

### D1 — Format: CSV (taken)

CSV, because the next step reads CSV (ADR-0548), and because a person opens it in the tool
they already have. JSON Lines would carry multi-valued attributes without an encoding, but
would need a second reader in the business rule task and opens in no spreadsheet.

### D2 — Columns: `dn`, then `select` in its order (taken)

The first column is `dn`, always: it is the one value that names an entry, and what a
later correction needs. Then one column per attribute in `select`, in the order written.
An empty `select` is refused for the document form: the server's default attribute set
differs between directories, and a file whose columns depend on the server cannot be
checked by a rule that names them.

### D3 — Multi-valued attributes: joined with `|`, escaped (taken)

`memberOf`, `proxyAddresses` and similar hold several values. They are written into one
cell, joined by `|`; a `|` or `\` inside a value is escaped as `\|` and `\\`. A rule tests
membership with FEEL on the split value, e.g.
`list contains(split(row.memberOf, "\|"), "CN=Vertrieb,OU=Gruppen,DC=x")`.

The alternative — one column per value, or one row per value — makes the column set or
the row count depend on the data, which a decision table cannot be written against.

**To verify in S1:** whether FEEL's `split` with that pattern behaves as written in temis,
and whether a helper is needed in the Modeler hint.

### D4 — Limits (taken)

- `maxEntries` keeps its meaning (more fails the job). Its default for the document form is
  100,000 instead of 1,000, because the document form exists for large results.
- The document budget (`limits.Document`, 35 MiB) is the hard ceiling. A result that would
  exceed it fails the job with a message naming both numbers, rather than storing a cut
  file that looks complete.
- The search is written page by page as it arrives, not held whole: `Conn.Search` gains a
  callback form, so memory holds one page (500 entries) and the CSV being written.

### D5 — Personal data (taken: option 1)

A directory export names tens of thousands of people. ADR-0314 seals personal data per
*data subject*: a variable declared personal is enciphered under the key of the instance's
subject, and a document it names is sealed with it (`sealDocuments`). That model has one
subject per instance. A file of 50,000 employees belongs to no single subject; sealing it
under one would make an erasure of anyone else not reach the file.

What the code does today, unchanged by this plan: the result document is bound to the
instance and readable by whoever may read that instance (ADR-0543), and it is removed when
history retention removes the instance.

Options, to be decided before S1 ships:

1. **Accept and document it.** The export is as protected as the instance; operators
   restrict who reads such instances (ADR-0275) and keep retention short.
2. **A retention of its own.** The result document of a directory export is removed after a
   set time, independent of the instance, so the file does not outlive its use.
3. **No personal attributes in the file.** The search's `select` is checked against a list
   of attributes the process declares it may export, refused at deploy otherwise.

Recommendation: 1 now, 2 as a follow-up. 3 restricts what can be checked without making
the file less personal (a `dn` names a person already).

**Taken: option 1.** The export is as protected as the instance it belongs to. Option 2 stays
the follow-up if a check runs regularly. Unchanged by this plan: a result variable the process
declares personal seals the document it names under the instance's subject, as any personal
reference does (`encipherJobVars` → `sealDocuments`).

## Slices

### S0 — Decide

- D1–D4 confirmed or changed; D5 answered.
- Exit: this plan's decisions marked taken.

### S1 — Search writes a document (done)

As built:

- **Compiler** (`compiler/connector_compile.go`, `adResultAs`): `resultAs` (`variable` |
  `document`) and `documentName` on `<atlas:adConnector>`. Refused at deploy: the document
  form on any operation but `search`, without a `resultVariable`, without `select`, with `*`,
  `+` or `dn` in `select` (every name is a column, and `dn` is the first), and a
  `documentName` with a path; `documentName` without the document form. `maxEntries`
  defaults to 100,000 in the document form.
- **Reading** (`connector/ad/document.go`): `goConn.SearchPages` follows the paging
  cookie and hands each page over before asking for the next; the mock pages its own
  result; a connection without paging is read whole. Rows are in the directory's order;
  `dn` in the result is still the smallest DN, tracked as the pages arrive.
- **Writing:** one CSV row per entry, attributes matched without regard to case, a missing
  attribute an empty cell, values joined and escaped as D3 says. The cap and the document
  budget are checked as rows are written, so a result past either fails the job instead of
  storing a cut file.
- **Both places it runs:** in process through `jobDocuments` (which now reports the
  configured document limit), on a Worker Instance through `workersdk.UploadDocument`; the
  job payload carries `asDocument` and `documentName`. The completion binds the document.
- **The chain:** `<atlas:rowsFrom source="benutzer.document"/>` — a business rule task
  over a file may name its file at a field inside a variable, so the AD result is read
  directly, without a step that copies the reference.
- **Modeler:** the moddle descriptor knows `resultAs` and `documentName`, so a model keeps
  them on save. The panel fields are S2.
- **Measured** against the mock directory, 50,000 accounts with three attributes
  (`BenchmarkADocumentSearchOf50000Users`): 0.27 s, a 5.8 MB document. The heap in use
  (165 MB) is not a measure of the search: the mock holds the whole directory in memory.
  Against a real directory, memory holds one page of 500 entries plus the document being
  written; that is by construction, not measured.

Planned, for reference:

- Compiler: the AD task's `search` gains `resultAs="document"` and `documentName`
  (default `ad-search.csv`); `select` required in that form; `maxEntries` default 100,000.
  Refused at deploy: `resultAs="document"` on an operation other than `search`, an empty
  `select`, a `documentName` with a path.
- `connector/ad`: a paged search with a callback; a CSV writer for D2/D3; the result
  variable becomes `{found, count, dn, document: {$document, …}}` — `found`, `count` and the
  first `dn` stay, so a model's gateways keep working; `entries` is not there.
- Stored through the job's document access: in process `jobDocuments`, on a Worker Instance
  `workersdk.UploadDocument`. The completion binds it (ADR-0543).
- Tests:
  - the mock directory (`connector/ad/mock.go`) with 50,000 entries, multi-valued and binary
    attributes, an escaped `|`;
  - the ceiling (`limits.Document`) and `maxEntries`;
  - the same job in process and on a Worker Instance.
- Measured: time and peak heap for 50,000 entries against the mock, written up beside
  `benchmarks/results/measurement-95bce7d.md`.

### S2 — Modeler, example, handbook

- AD panel: *Result as: variable / document*, the file name, a hint on multi-valued
  attributes.
- Example model *AD-Benutzer prüfen*: an AD search as a document, then a business rule task
  over the file, with a small decision (account enabled, e-mail set, department known,
  licence group consistent). Runs against the mock directory.
- Handbook (de/en), CHANGELOG.

### S3 — The same for LDAP and Entra (optional)

The LDAP Worker Type's `search` and the Entra Worker Type's user listing produce the same
`{dn, attributes}` shape. The CSV writer is shared; each gains the same two attributes.
Only if a process needs it.

## Ground rules

- No directory data on the run loop: the search, the CSV and the upload happen in the job
  handler, off the loop (I3); the engine sees one completion with a reference (I2, I4).
- Every message a person reads goes through the locale catalogue (de first, then en).
- `CHANGELOG.md` per user-facing slice; ADR-0548's follow-ups name this plan.

## Out of scope

- Correcting the directory from the result (writing back to AD). That is a provisioning
  process with its own approvals, not a validation step.
- Incremental checks (DirSync deltas instead of a full export). The `sync` operation exists;
  checking only what changed is a later step once a full check runs.

## Open questions

- D5, above.
- Which attributes the first real check needs. The example uses a guess; the real `select`
  comes from the rules a quality owner wants to enforce.
- Whether the directory to check is on-premises AD (this plan) or Entra ID (S3 first).
