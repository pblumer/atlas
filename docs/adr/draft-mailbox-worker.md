# ADR-DRAFT: The mail Worker reads its mailbox — an inbound watch, mailbox operations, and who may use them

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** the provider-side setup this record relies on — a Microsoft Graph
  application permission confined to one mailbox by Exchange Online's RBAC for
  Applications, Gmail domain-wide delegation authorizing `gmail.readonly` and
  `gmail.modify`, and whether Graph answers `internetMessageHeaders` on a *list* of
  messages and not only on a single one — is written from the providers'
  documentation. Nobody has walked it at a tenant for this change. The code tolerates the
  header being absent (the verdict reads empty), but an operator following the handbook
  is following steps that have not been checked against a live tenant.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0205](0205-connector-ownership-and-event-delivery.md) was written for a case that
did not exist yet: "if I have an inbound mail connector, nobody but me may use those
events". It built the ownership half (a Worker has an owner, a visibility and members)
and the delivery half (an inbound watch claims its message name) and then said, in as
many words, that the inbound mail Worker itself was "neither built nor on the roadmap".

The request that forces this record is that Worker, asked for on a *shared*
installation — one where a private instance is not the answer — and widened while it
was discussed: not only "a mail arrives and starts a process", but a mailbox a process
can work with: list it, read one message, move it, mark it read, delete it, answer it.
More than one mailbox, each with its own people.

Four facts about Atlas as it stands decide most of the shape:

- **The mail Worker Type sends and nothing else.** `connector/mail` frames a message
  and hands it to SMTP, Gmail or Microsoft Graph. A mail Worker is one configured
  sender: one mailbox identity and one credential.
- **Every other Worker Type that reads a foreign system carries both halves.** Jira,
  Google and Discord are each one Worker Type with service-task operations *and* an
  inbound watch on the same Worker ([ADR-0214](0214-jira-inbound-issue-watch.md),
  [ADR-0234](0234-google-inbound-watch.md), [ADR-0262](0262-discord-inbound-watch.md)).
- **Nothing checks who may *use* a Worker.** ADR-0205 governs who may see and change a
  Worker's configuration and who may add a watch. A deploy is checked against claimed
  message names and against nothing else: any account holding `modeler` may deploy a
  process whose task names any Worker. For sending that is a known, accepted property —
  a shared sender is infrastructure. For an operation that *reads* a mailbox it would
  be a way around everything ADR-0205 built: the mailbox would be private in the
  Console and readable by anyone who can draw a service task.
- **What a process receives, an operator can read.** ADR-0275 narrowed
  `GET /instances/{key}/variables` to project members and task holders. The timeline,
  the variable search, data objects and the instance list are still gated by the
  `operator` role across the whole server, and the OpenSearch exporter
  ([ADR-0114](0114-opensearch-event-exporter.md)) writes every record's value. So
  whatever a mail watch puts into an instance is readable by every operator of a
  shared installation, whoever owns the mailbox.

The question: **how does a mail Worker read its mailbox — as a watch and as operations —
so that the mailbox stays its owner's on a shared installation, and how much of the
mail should a process receive at all?**

## Decision drivers

- **The mailbox is the Worker's identity** ([ADR-0203](0203-worker-execution-model.md)).
  A Worker is "one configured target and identity of that type"; a mailbox is exactly
  that. More than one mailbox is more than one Worker, each with its own owner and
  members — which ADR-0205 and [ADR-0180](0180-groups-as-members.md) already provide.
- **No path around ADR-0205.** A read that is refused in the Console must not be
  possible through a deployed model.
- **Least data.** What a process does not receive cannot leak through the operator
  role, the exporter, checkpoints or backups. The default must be the narrow one.
- **No hidden side effects.** A watch that changes the mailbox (marks a message read,
  moves it) acts on a person's mailbox without anything in a model saying so.
- **Invariants.** The provider calls are network I/O: off the processor goroutine (I3),
  never in `applyToState` (I4), and a publish is durable before it is acted on (I2).
  Nothing here touches the engine.
- **Untrusted input.** Anyone on the internet can write to an address. A `From` header
  is a claim, not an identity.

## Considered options

1. **A new Worker Type `mailbox`** beside `mail`, carrying watch, operations and send.
2. **A second inbound-only Worker Type** (`mailinbound`), leaving `mail` as it is.
3. **Extend the `mail` Worker Type**: an inbound watch on the mail Worker, mailbox
   operations as an `operation` on the existing mail task (absent means `send`), and a
   deploy-time check that the deployer may use a mailbox a task reads or changes.
4. **3 without the deploy check**, documenting that a mailbox operation reaches any
   mailbox a model names.

## Decision outcome

Chosen option: **3**.

### One Worker Type, several Workers

The capability is mail; ADR-0203 has one Worker Type per capability and names `mail` as
its example. Option 1 would make a second way to send — either every deployed model
moves to it, or two task kinds send mail forever — and it would reuse every provider
client `mail` already has, so it saves no work and doubles the registration, the setup
documentation and the marketplace entry. Option 2 has the same cost for half the
capability.

The strongest argument for a split is real and is answered one level down: reading and
sending are different rights, and a shared `noreply@` sender is not a personal inbox.
That is a reason for *two Workers* — one with a read-only credential, owned by the
mailbox's person, one sender shared as infrastructure — not for two Worker Types. The
setup documentation recommends exactly that wherever the risk warrants it.

### The watch

A mail Worker carries inbound watches like a Jira or Discord Worker. A watch names a
`mailFolder` (default `INBOX`; a Gmail label id, a Graph folder id or well-known name,
an IMAP mailbox name) and publishes each new message as an Atlas message. It is
**read-only**: it never marks, moves or deletes. A process that wants a message filed
says so with a task, where it is visible in the model and in the audit trail.

Each provider has its own sequence, and each gets the mark its sequence can carry
(the distinction ADR-0214 and ADR-0262 drew):

| Provider | Sequence | Mark | Cursor |
|---|---|---|---|
| IMAP | the message UID, strictly increasing within one `UIDVALIDITY` | one per watch and validity epoch | `uidvalidity:uid` |
| Gmail | the history id of a `messageAdded` record | one per watch | the last history id read |
| Microsoft Graph | `receivedDateTime`, which two messages can share | one per message (immutable id) | the newest timestamp plus the ids already delivered at it |

- **IMAP** is a log and is read like one. A changed `UIDVALIDITY` means the server
  renumbered the mailbox; the watch re-primes to the tip and logs the gap rather than
  replaying the folder under a fresh mark.
- **Gmail's history** is a log too. Several messages can share one history record, so
  the sequence is the record id with the message's position appended in the low ten
  bits. A history id older than Gmail keeps (about a week) answers 404; the watch
  re-primes to the tip and logs the gap.
- **Graph** has no log the watch can read without a delta token that cannot be compared,
  and `receivedDateTime` has a resolution of a second. A scalar mark on it would
  silently drop the second of two messages arriving in the same second, so the mark is
  per message, as Jira's and Drive's are. The cursor carries the ids delivered at its
  timestamp, so the boundary second is not re-published — which matters because a
  re-publish is charged against the watch's hourly budget
  ([ADR-0225](0225-inbound-watch-budget.md)) even though the engine discards it. The
  cost is the one ADR-0214 accepted: one durable mark per received message. The budget
  bounds its rate.

Graph calls ask for **immutable ids** (`Prefer: IdType="ImmutableId"`), so the
`messageId` a watch publishes still addresses the message after a task moved it.

### What a message exposes — metadata by default

The event is a curated envelope and nothing else; unlike the Jira and Discord watches,
there is no raw provider object beside it:

`eventType` (`mail.received`), `messageId` (what an operation addresses),
`internetMessageId`, `folder`, `from`, `fromName`, `replyTo`, `to`, `cc`, `subject`,
`receivedAt`, `unread`, `hasAttachments`, `attachments` (name, content type, size —
never content) and `auth` (`spf`, `dkim`, `dmarc`).

The **body** is included only when the watch says `includeBody`, as plain text (an
HTML-only message is reduced to its text), and is cut at 64 KiB with `bodyTruncated`
set. Attachment content is never included: an attachment is how a mailbox carries
documents, and a document in a process variable is in the WAL, the checkpoints, the
exporter and every backup, with no erasure path short of
[ADR-0314](0314-portal-personal-data.md)'s key destruction.

### Who may trigger a process — sender rules

A watch can carry `allowedSenders` (addresses, or `@domain`) and `requireDmarcPass`.
A message failing either is consumed — the cursor moves past it — but not published and
not charged against the budget.

`From` alone is spoofable, so an allow-list without `requireDmarcPass` keeps out the
mistaken and not the malicious, and the Console says so. The verdict is read from the
**topmost** `Authentication-Results` header, which is the one the receiving server
added; one further down may have been written by the sender. That is a trust decision
about the provider's receiving server, stated here so nobody reads `auth.dmarc` as
cryptographic proof Atlas computed.

### Mailbox operations

The mail task gains `operation`. Absent, or `send`, it is exactly the task every
deployed model already has. The others:

| Operation | Takes | Answers |
|---|---|---|
| `list` | `folder`, `maxResults` (≤ 100, default 25), `unreadOnly`, `includeBody` | the newest messages, newest first, as envelopes |
| `get` | `messageId`, `includeBody` | one envelope |
| `move` | `messageId`, `destination` | the message's id afterwards |
| `mark-read`, `mark-unread` | `messageId` | — |
| `delete` | `messageId` | — (to the provider's trash: Graph's Deleted Items, Gmail's trash, the IMAP `\Trash` folder; an IMAP server with no trash folder only gets the `\Deleted` flag, never an `EXPUNGE` that would take other messages with it) |
| `reply` | `messageId`, `body` and/or `bodyHtml` | — (to the original's `Reply-To` or `From`, threaded by `In-Reply-To`/`References`) |

The compiler refuses a value an operation does not use, as it does for Discord, so a
model never carries a field the worker ignores.

### Who may use a mailbox — the deploy check

A deploy is refused when a task performs a mailbox operation on a mail Worker the
deployer cannot reach: **viewer** on the Worker for `list` and `get`, **editor** for
the operations that change the mailbox or write from it. A task naming a mail Worker
that does not exist is refused too, for those operations only: otherwise a model could
be deployed against a name today and read whichever mailbox somebody configures under
that name tomorrow. `send` is not checked; it stays exactly as open as it was.

It runs at every door a definition enters by — a deploy, a project deploy and an
application import — and the same change puts ADR-0205's message-name claim on the
application import, which had deployed without it.

It is **a gate at the doors, not isolation**, in the sense ADR-0205 uses: it decides
whether a definition may be deployed, and is not consulted while a job runs. Sharing a
Worker and then taking the share back does not undeploy what was deployed while it
stood. An administrator passes, because an administrator reaches every Worker.

### What this does not protect, said plainly

- **Instance data on a shared installation.** Whatever a watch or a `get` puts into an
  instance is readable by every `operator` through the timeline and the variable
  search, and by whoever reads the OpenSearch index. The metadata-only default narrows
  what is exposed; it does not close the operator gap. That is ADR-0275's follow-up —
  instance reads by relationship rather than by role — and it stays open.
- **Administrators.** An engine that evaluates correlation keys over a message has to
  see it in clear. Only a private installation protects mail from its operator.
- **Provider-side reach.** A Graph application with `Mail.Read` can read every mailbox
  in the tenant until Exchange Online confines it; a Google service account with
  domain-wide delegation can impersonate any user of the domain. Atlas uses the
  mailbox it is configured with, but the credential in the vault reaches further, so
  the setup documentation asks for the confinement at the provider.

### Consequences

- **Positive:** a person can own a mailbox Worker, share it with a person or a group,
  have its mail start their processes and nobody else's (ADR-0205's claim), and model
  what happens to a message afterwards — with the read path closed at deploy.
- **Positive:** no engine change, no new reserved job type: the operations ride the
  existing mail job type, so a mail Worker offloaded to a Worker Instance serves them
  with the client it already holds.
- **Negative / trade-offs accepted:** a mail Worker that reads needs a broader
  credential than one that sends — `Mail.Read`/`Mail.ReadWrite`, `gmail.readonly`/
  `gmail.modify`, or IMAP access. Gmail asks for the read scope and the modify scope on
  separate tokens, so a delegation that grants only `gmail.readonly` serves a watch and
  `list`/`get` and refuses the rest with Google's own error.
- **Negative:** IMAP reads use the SMTP Worker's sender and password; an IMAP server
  that requires OAuth (`XOAUTH2`) is reached through the Graph or Gmail provider
  instead.
- **Negative:** Graph watches keep one durable mark per message.
- **Follow-ups / risks to watch:** instance reads by relationship (ADR-0275), which is
  what would make "only I can read my mail" true on a shared installation rather than
  true of the configuration; a check at runtime as well as at deploy (ADR-0205's
  option 5); an IMAP `IDLE` or Graph change-notification path if poll latency ever
  matters.

### As built

- **`connector/mail`** carries the mailbox: `mailbox.go` (the `Mailbox` seam, the envelope
  and its fields, the sender rules), `mimeparse.go` (header words, transfer encodings and
  charsets, HTML reduced to text, `Authentication-Results`, the part tree both IMAP and Gmail
  reduce to), and one file per provider — `imapproto.go`/`imapmailbox.go`, `graphmailbox.go`,
  `gmailmailbox.go`. The IMAP client is written here rather than imported: a mailbox Worker
  speaks a small, read-mostly subset, and the parser — every response read under hard
  limits on literal size, token length and nesting — is the whole of the protocol risk. It
  refuses plaintext: port 143 means a mandatory STARTTLS. Reads use `EXAMINE` and
  `BODY.PEEK`, so a watch or a get cannot set `\Seen` even by accident. A move without
  `MOVE` expunges by UID only with `UIDPLUS`, never with a plain `EXPUNGE`.
- **The operations ride the existing job type.** The compiler's `<atlas:mailConnector>`
  gains `operation` and the attributes it takes, with the Discord-style table that refuses a
  value an operation does not use; `MailOp` is empty for a send, so every deployed model
  recompiles to what it was. The handler became an output handler, and the Worker Instance
  path carries the same fields and completes with the same result.
- **The bridge stores a cursor an empty page moved.** A mail watch can read a page of
  nothing but refused senders, or a renumbered folder; leaving the cursor behind would
  re-read that page forever, and a page of refused mail longer than the batch would stop the
  watch for good. Every other source answers no cursor for an empty page, so for them it is
  the no-op it always was. A gap — a renumbered IMAP folder, expired Gmail history — is
  logged as `inbound_watch.gap`.
- **A Gmail watch cannot backfill**, and the create endpoint says so: Gmail's history is a
  window of about a week with no beginning to list from.
- **The deploy check** is `api/mailboxuse.go`, called at the deploy, the project deploy and
  the application import — which also gained ADR-0205's claim check, found missing while
  wiring this one in.
- **What could not be verified here** is the open question above: the provider-side setup,
  and Graph answering `internetMessageHeaders` on a list. The handbook and the setup panel
  say in so many words that those steps come from the providers' documentation.

## Pros and cons of the options

### Option 1 — a new `mailbox` Worker Type
- Good: one coherent configuration for read, send and manage from the start.
- Bad: a second way to send mail, or a migration of every deployed mail task; the
  provider clients are `mail`'s either way.

### Option 2 — an inbound-only Worker Type
- Good: read and send credentials can never be one Worker's.
- Bad: the same duplication for half the capability; and the separation it buys is
  available as two Workers of one type.

### Option 3 — extend `mail`, check use at deploy (chosen)
- Good: the shape Jira, Google and Discord already have; no migration; the read path
  is closed at the doors ADR-0205 already guards.
- Bad: a gate rather than isolation; the operator gap on instance data stays open.

### Option 4 — extend `mail` without the deploy check
- Good: smallest change.
- Bad: any modeler could read any mailbox by drawing a task. Not acceptable on a shared
  installation, which is the case this record exists for.

## Links

- completes [ADR-0205](0205-connector-ownership-and-event-delivery.md), which decided
  the ownership model for exactly this Worker before it existed
- the sending half: [ADR-0079](0079-outbound-mail-connector.md),
  [ADR-0093](0093-native-mail-providers.md)
- the watch mechanism: [ADR-0075](0075-clio-inbound-event-bridge.md),
  [ADR-0214](0214-jira-inbound-issue-watch.md) (per-item marks),
  [ADR-0262](0262-discord-inbound-watch.md) (a log read as a log),
  [ADR-0225](0225-inbound-watch-budget.md) (the budget)
- the read gap it does not close: [ADR-0275](0275-instance-visibility.md)
- erasure of what a process did receive: [ADR-0314](0314-portal-personal-data.md)
- the Worker vocabulary: [ADR-0203](0203-worker-execution-model.md)
