# ADR-0478: A mail's documents leave the mailbox — forward, get-attachment, export-attachment, and what inspects them first

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** the provider calls this record relies on are written from the
  providers' documentation and have not been walked at a live tenant: Graph's
  `/attachments/{id}/$value` for file and item attachments and its `forward` action taking
  every recipient inside its `message` parameter; Gmail's media upload
  (`/upload/gmail/v1/users/me/messages/send?uploadType=media`) for a forward, and whether it
  honours a `Bcc` header in the uploaded message as the metadata send does. The tests hold
  the shapes against fakes, not against Microsoft or Google. A second gap is clamd's own
  `StreamMaxLength` (25 MB by default): a document above it is answered with an error, which
  this record treats as "not scanned" and fails — correct, but an installation that raises
  `ATLAS_LIMIT_DOCUMENT` without raising clamd's limit will find every large attachment
  parked, and nothing here can tell it so before the first one.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0438](0438-mailbox-worker.md) gave the mail Worker its mailbox and drew one line on
purpose: a process learns an attachment's name, type and size, and **never its content**.
The reason was sound and still is — what a process receives is in the event log, the
checkpoints, the exporter and every backup, and on a shared installation every operator can
read it.

The request that forces this record is the one that line leaves unanswered: *a supplier
mails an invoice; put the PDF into the object store, or show it to the person who approves
it.* Three variants came up in the discussion that produced it, and each is a real case:

- **Archive it.** The document goes into an S3 bucket ([ADR-0442](0442-s3-object-store-worker.md))
  under a key the process chooses, and a user task later opens it through a presigned link.
- **Read it.** A small document — a CSV a later gateway parses, a one-page PDF a decision
  service reads — becomes a variable.
- **Pass it on.** The whole mail, attachments included, goes to a document store's mail-in
  address, a ticket system or a colleague.

What existed for these was worse than any of them. A REST task can call Graph's attachment
endpoint with client credentials and put `contentBytes` into a variable, which works for
Microsoft 365 alone, puts the document into the log anyway, and stops at about 750 KB once
base64 has grown it; IMAP has no HTTP API and Gmail's grants are not ones the REST Worker
speaks. A script task can do all of it and puts a mailbox credential into a script's
environment, which is what [ADR-0168](0168-connector-work-on-a-worker.md) exists to end.

And all three put a stranger's file in front of a person. Anybody on the internet can write
to an address, so an attachment is untrusted input in the most literal sense, and handing it
on is exactly how mail-borne malware reaches somebody who will double-click it.

The question: **how does a process hand on a mail's documents — into a variable, into an
object store, to another recipient — without the bytes going where ADR-0438 kept them out
of, without a credential leaving its Worker, and without handing on what nobody looked at?**

## Decision drivers

- **The bytes stay out of the engine wherever the case allows.** A document in a variable is
  in the log until the installation's history is gone. The archive and pass-on cases do not
  need that; only the read case does, and there it must be bounded by the variable's own
  budget and chosen visibly.
- **The credential stays with its Worker** (ADR-0168): a model names Workers; access keys and
  mailbox credentials resolve where the job runs and nowhere else.
- **No path around ADR-0205.** A Worker private in the Console must not become usable by
  drawing a task — for the mailbox, and now for the bucket a document is written into.
- **Untrusted input.** A file name and a content type are claims. What a document *is* has to
  be decided from its bytes, and a refusal must be something the process can branch on.
- **Fail closed.** A scanner that cannot answer is not a clean answer.
- **Invariants.** All of it is provider and network I/O in a Worker, after fsync (I2), off the
  processor (I3), never in `applyToState` (I4). Nothing here touches the engine.

## Considered options

For the operations:

1. **No new operation; document the REST and script workarounds.**
2. **`get-attachment` only** — the document into a variable, bounded by the variable budget.
3. **`export-attachment` only** — the document from the mailbox into an object store, inside
   one Worker Instance.
4. **`forward` only** — the provider (or the Worker) sends the mail on, attachments included.
5. **All three, each inspecting every document first** (chosen).

For the inspection:

- **A.** Rely on the provider's own scanning (Defender for Office 365, Gmail).
- **B.** A built-in, deterministic content policy only.
- **C.** A malware scanner the installation runs, over ClamAV's `clamd` protocol.
- **D.** An ICAP client for an enterprise antivirus gateway.

## Decision outcome

Chosen: **option 5, with inspection B always and C when the Worker names a scanner.**

### The three operations

| Operation | Takes | Answers | Where the bytes go |
|---|---|---|---|
| `forward` | `messageId`, `to` (required), `cc`, `bcc`, `body`/`bodyHtml` (a note above it) | — | to the recipients; never into the process |
| `get-attachment` | `messageId`, `attachmentId`, `allowedTypes`, `resultVariable` (required) | `content` (base64), `name`, `contentType` (detected), `declaredType`, `size`, `inspection` | into one variable, under its budget |
| `export-attachment` | `messageId`, `attachmentId`, `allowedTypes`, `store`, `bucket`, `key`, optional `resultVariable` | `bucket`, `key`, `etag`, `versionId`, `size`, `name`, `contentType`, `declaredType`, `inspection` | from the mailbox into the S3 Worker `store`, inside one Worker Instance |

They are the reason none of the three alone was enough: the archive case is export, the
small read is get, the mail-in address is forward, and each of the others is the wrong tool
for the other two. They also differ in how much of ADR-0438's line they move, and say so in
the model: `get-attachment` is the one that puts a document into a variable, and it is a
separate operation an author has to pick rather than a flag on `get`.

**Addressing an attachment.** The envelope's `attachments` list gains an `id` per entry:
Graph's attachment id, Gmail's part id, an IMAP body section. Gmail's own `attachmentId` is
not used because Gmail mints a new one on every read of the same message; the part id is the
address that lasts, and the current `attachmentId` is read from it at the moment of the
fetch. An IMAP section is checked to be digits and dots before it is spliced into a FETCH.

**`get-attachment` refuses rather than truncates.** A document whose base64 does not fit a
process variable (`limits.Variable`, less a quotation's worth for the other fields) is
refused with a message pointing at `export-attachment`, at the moment the attachment and the
alternative are both in view — not at the engine's completion check, which would know
neither.

**`export-attachment` composes two Workers without either seeing the other's credential.** The
mail package defines a `DocumentStore` interface; `connector/s3` provides `s3.Documents`,
which resolves the named S3 Worker among the identities *this process* holds and puts the
document through its own `PutDocument`. The S3 Worker's access key never enters the mail
package, the job, or the engine:

- **In process**, the server passes its own S3 registry, read at call time.
- **On a Worker Instance**, the mail kind builds an S3 registry from the variables the s3 kind
  reads (`ATLAS_S3_<NAME>_CREDENTIALS`, `_URL`), for the names in `ATLAS_S3_CONNECTORS` and in
  a list of its own, `ATLAS_MAIL_STORES`. A supervising engine renders exactly the S3 Workers
  its deployed export tasks name — the narrowest set that works, as it does for AD bind
  passwords — and re-renders when a deploy adds one. The list is separate from
  `ATLAS_S3_CONNECTORS` because a worker serving both kinds is handed both renderings, and
  the s3 kind's list must not be replaced by a narrower one. Identities held for export are
  not reported as names this worker serves: holding a key to write into a bucket is not
  leasing S3 tasks.
- An export on a worker that holds no store fails **before** the mailbox is read, naming the
  store, so a document is not pulled across a network to be thrown away.

`store` is a literal Worker name, as `connector` is: the deploy check has to see which
Worker a model writes into, and a name computed at run time is one it could not check.

The object is stored under its **detected** content type, so a presigned link opens what the
bytes are. A document a browser would run as a page (HTML, SVG, XML) is stored with
`Content-Disposition: attachment`, so a link downloads it instead of opening it; everything
else is `inline`. What inspection found is written into the object's metadata
(`atlas-detected-type`, `atlas-scanned`, `atlas-scanner`). The byte path is bounded by a new
budget, `limits.Document` (35 MiB by default — Exchange Online's default message size and
the Gmail API's largest send), because the variable budget is the wrong number for a document
that never becomes one.

**`forward` is the provider's where it can be.** Graph forwards server-side with its own
`forward` action. IMAP and Gmail cannot, so the Worker frames the forward: the model's note,
the original's sender, date, subject and recipients, and then the original's own body entity
— every part exactly as the sender's client encoded it — inside one `multipart/mixed`. That
is chosen over attaching the whole message as `message/rfc822`, which hides the attachments a
level down where a mail-in address that files attachments does not look; delivered this way
they arrive as attachments, which is also what Graph's forward produces. IMAP sends it through
the Worker's SMTP side (`Message.Raw`), after the IMAP session is closed; Gmail sends it
through the media upload. Bcc travels as the envelope, never as a header the recipients see —
except in the message handed to Gmail, which reads and strips it.

### Who may use them

ADR-0438's deploy check classifies the operations: `get-attachment` **reads** (viewer on the
mail Worker), `forward` and `export-attachment` **write from** the mailbox (editor), as `reply`
does. An export is checked twice: the deployer must also be an **editor of the S3 Worker** it
names, and a store nobody holds is refused at deploy for the reason a mailbox nobody holds
is — a model deployed against a name today would write into whichever bucket somebody
configures under it tomorrow.

### The inspection

Every document is inspected before one of the three operations hands it on, and a forward is
inspected whole: one refused attachment stops the forward. Graph forwards server-side and is
still made to read each file attachment first; a reference attachment (a link to a file held
elsewhere) is not content of the message and is not read.

**B — the built-in policy** decides from the bytes and refuses:

- programs under any name — PE, ELF, Mach-O, a Windows shortcut — and scripts (`#!`);
- names Windows, macOS or a mail client run, open as code, or mount on a double-click — the
  extension list in `blockedExtensions`, including macro-enabled Office formats and OneNote —
  with the trailing dots and spaces Windows ignores taken off first;
- names carrying bidirectional control characters, which is how `exe.pdf` is shown as
  `fdp.exe`;
- Office documents with a VBA project, in a compound file (`_VBA_PROJECT`) or an OOXML
  package (`vbaProject.bin`);
- encrypted archives, which nothing can look inside and which is why malware is mailed that
  way; archives carrying a program or script; Java programs; Android apps;
- PDFs with a `/JavaScript` (`/JS`) or `/Launch` action, with `#xx` name escapes decoded;
- a document whose name names a format its content is not — an `invoice.pdf` that is no PDF.

`allowedTypes` narrows a task further, judged against the detected type: `application/pdf,
image/*` means what the content is, whatever the sender called it.

**C — the scanner.** A mail Worker may name `scanner`: `clamd://host[:3310]` or
`clamd:///path/to/clamd.socket`. Every document is then streamed to clamd's `INSTREAM` after
the policy passed it. A finding is a refusal naming the signature; an `ERROR` answer, a
refused connection or a timeout is a **failure** — retried, then an incident — and never a
pass. The scanner belongs to the Worker rather than to a provider, so it wraps whichever
client the provider built (`mail.WithScanner`), passing the provider's mailbox and check
through; the Worker's *Test connection* pings clamd after the provider's own check. It is
configured in the Console beside the IMAP endpoint, validated where it is typed, and handed
to a supervised worker as `ATLAS_MAIL_<NAME>_SCANNER`.

**A refusal is a BPMN error the process can catch.** It ends the task with
`MAIL_ATTACHMENT_REJECTED` when the task's process catches that code — the codes travel with
the resolved job, as [ADR-0461](0461-script-bpmn-errors.md) set out — and is a failure
otherwise, so a refused document is never handed on and never silently dropped. What
inspection found about a document that passed travels with it: `inspection.detectedType`,
`active`, `scanned`, `scanner`.

### What this does not do

- It does not find malware without a scanner. Policy B refuses the *shapes* malware is mailed
  in; an exploit inside a compressed PDF stream passes it. The handbook says so, and says to
  name a scanner wherever a mailbox receives external mail.
- It does not unpack nested archives. That is the scanner's work, and a policy that unpacked
  them would be a decompression bomb's best friend.
- It does not replace the provider's protection. Defender and Gmail scan on receipt; this
  scans on the way out. Both together are the answer.
- It does not stop a person from being shown an inline image or an HTML receipt; it stops a
  browser from running one as a page by storing it to download.

### Consequences

- **Positive:** the three cases have a first-class answer each, for all three providers, with
  no credential in a model or a script and no document in the log unless the model asked for
  exactly that with `get-attachment`.
- **Positive:** an attachment is inspected before it reaches a person, a bucket or another
  inbox, and a refusal is a path in the model rather than an incident by default.
- **Positive:** the mail and S3 Workers compose through an interface; neither package imports
  the other, and the access key stays where ADR-0442 put it.
- **Negative / trade-offs accepted:** `get-attachment` puts a document into the event log, by
  design and by choice. ADR-0438's rule now holds for every operation but that one, and the
  panel says what the choice costs where it is made.
- **Negative:** the PDF policy refuses interactive forms whose calculations are scripts. That
  is paid on purpose; such a form can still be opened from the mailbox itself.
- **Negative:** a forward through Graph reads every file attachment once more than a plain
  server-side forward would, to inspect it.
- **Negative:** a mail Worker Instance that exports holds S3 access keys besides its mailbox
  credential. They are only the ones its deployed models name, and only on the worker that
  runs those tasks.
- **Negative:** a document operation runs under the shared ten-second call budget
  ([ADR-0149](0149-bounded-connector-call-budget.md)) for the whole exchange — reading the
  attachment, scanning it, putting it into the store. A document near `ATLAS_LIMIT_DOCUMENT`
  over a slow link can exceed it; the job then fails visibly, retries and raises an incident
  naming the timeout. Nothing is handed on half. A budget of its own for document transfers is
  the follow-up if that turns out to be how installations meet it.
- **Follow-ups / risks to watch:** an ICAP client (option D) for installations whose antivirus
  is a gateway rather than clamd — the `Scanner` interface is the seam; the open question
  above; a stored scan verdict a later task could re-check before presenting an object long
  after it was exported, when signatures have moved on.

## Pros and cons of the options

### Option 1 — document the workarounds
- Good: no code.
- Bad: Microsoft 365 only for REST, a credential in a script for everything else, and the
  document in the log either way. Nothing inspects anything.

### Option 2 — `get-attachment` only
- Good: one operation, uniform across providers, and the S3 Worker's `put-object` already
  takes base64.
- Bad: every archived document passes through the log and stops at the variable budget —
  the failure ADR-0442 was written to avoid.

### Option 3 — `export-attachment` only
- Good: no bytes in the engine, no size ceiling beyond the document budget.
- Bad: the small read case and the pass-on case have no answer; and it is the one that
  couples two Workers, which deserves a decision, not a default.

### Option 4 — `forward` only
- Good: the provider does the work; nothing is held anywhere.
- Bad: S3 and Drive receive no mail, and the process learns nothing about where the document
  landed — it cannot present it later.

### Option 5 — all three, inspected (chosen)
- Good: each case has its tool; one inspection serves all three.
- Bad: three operations to explain, and an author has to pick the right one — the panel's
  hints are written for exactly that choice.

### Inspection A — the provider's scanning only
- Good: nothing to run.
- Bad: not every mailbox has it (an IMAP server in a basement); it ran at receipt, possibly
  against older signatures; and nothing in Atlas can see that it ran.

### Inspection B — the built-in policy only
- Good: deterministic, testable, nothing installed.
- Bad: finds shapes, not malware.

### Inspection C — clamd (chosen, beside B)
- Good: the open engine every platform packages; a protocol of a few lines with no client
  library; fail-closed is easy to state and to test.
- Bad: one more service an installation has to run and keep its signatures current.

### Inspection D — ICAP
- Good: what enterprise antivirus gateways speak.
- Bad: a larger protocol (RFC 3507) for a case nobody has reported yet; it is additive behind
  the same interface when one does.

## Links

- amends [ADR-0438](0438-mailbox-worker.md) — the line "attachment content is never included"
  now holds for every operation except `get-attachment`
- the object store and the presigned link a user task opens:
  [ADR-0442](0442-s3-object-store-worker.md)
- credentials stay with their Worker: [ADR-0168](0168-connector-work-on-a-worker.md),
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)
- who may use a Worker from a model: [ADR-0205](0205-connector-ownership-and-event-delivery.md)
- a refusal as a caught code: [ADR-0461](0461-script-bpmn-errors.md),
  [ADR-0089](0089-error-events.md)
- what a process receives, an operator can read: [ADR-0275](0275-instance-visibility.md),
  [ADR-0314](0314-portal-personal-data.md)
