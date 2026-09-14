# ADR-DRAFT: A decision is published as its own document

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0143](0143-process-documentation-export.md) answered, for a BPMN process,
"how do I hand this to somebody who does not have Atlas open": the browser renders
the model it is already drawing into a PDF, the server stores it as an immutable
numbered version, and a revocable link puts one version in front of a reader with
no account.

A decision has the same audience and a sharper need. A decision table *is* the
business rule — the thing a compliance officer signs off, the thing an auditor
asks about, the thing somebody will want to compare against what was agreed in
March. Today it is readable only inside Atlas, and the process document a reader
*is* handed names the business rule task without showing the table behind it.

Two questions have to be answered, and the second is the one that decides the
shape:

1. What is in a decision's document?
2. Does a decision need a **document version line of its own**, when
   [ADR-0319](0319-durable-versioned-decision-deployments.md) already gives it a
   versioned, immutable runtime record carrying its exact XML?

## Decision drivers

- **The picture must be the picture.** ADR-0143's reason for rendering in the
  browser holds unchanged: dmn-js is already drawing the authoritative DRG, and
  anything that re-derives it will drift.
- **Design-time only.** A document describes a model; it is not an engine fact.
- **Do not invent a second mechanism** where ADR-0143's is right. Sharing,
  revocation, retention and the public route are solved.
- **Do not duplicate a version line that already exists**, if it does.
- **On-disk honesty.** A record's field must mean what it is called, and a landed
  store's records must not change meaning under an upgrade.

## Considered options

1. **A decision documentation version, mirroring ADR-0143** — its own store, its
   own per-decision version counter, its own share link, published from the
   decision editor.
2. **A plain PDF download from the editor, with no stored version** — on the
   grounds that the decision deployment record is already the immutable version.
3. **Fold the decision into the process document only** — a business rule task's
   called decision is documented inside the document of the process that calls it.
4. **Generalise ADR-0143's store to any artifact** — one record type with a kind
   discriminator, serving processes and decisions from one directory.

## Decision outcome

Chosen option: **1 — a decision documentation version, mirroring ADR-0143.**

### Why a decision needs a version line of its own

Option 2 is the one worth arguing with, and it is wrong for a reason worth writing
down: **a documentation version and a deployment version answer different
questions.**

- A deployment version answers *what is running*. It exists only once the decision
  is deployed, and it carries XML, not prose.
- A documentation version answers *what did we agree to, and when*. It can be
  published from a draft, before anything runs; it carries a title, a note, and
  the rendering somebody actually read; and it is the thing a signature is
  attached to.

ADR-0143 makes exactly this distinction for processes, which also have a durable
deployment record with their exact XML. Nothing about a decision weakens it. If
anything the sign-off case is stronger: "legal approved this eligibility table in
March" is a sentence people say about business rules more often than about control
flow.

### What is in the document

The document is the decision as a reader needs it, not as a modeller edits it:

- the **decision requirements graph** as the editor draws it, rasterised from
  dmn-js's own SVG;
- then, per decision in the model: its name and id, the prose in its
  `<description>`, the **input data it consumes with their declared types**, its
  **decision table rendered as a table** — hit policy, input columns, output
  column, one row per rule — and its output variable and type;
- a decision whose logic is a literal expression shows that expression instead,
  set as code, because that is what it is.

The decision table is the whole point, so it is set as a real table rather than as
preformatted text. `pdf.js` grows a `table()` primitive for it — wrapped cells,
ruled rows, a header — which is the one thing ADR-0143's writer did not need.

### The record, and the duplication behind it

The durable shape is ADR-0143's, field for field, in a store of its own
(`decision-docs/`): an immutable record per version, the PDF beside it, a
per-`decisionId` counter, a snapshot of what was documented, and an opaque
revocable share token. The routes are the same seven, under
`/api/v1/decisions/{decisionId}/documentation` and `/api/v1/decision-docs/{id}`.

That is **the same code twice**, and it is deliberate:

- Option 4 — one store with a kind discriminator — means either renaming a landed
  record's `processId` field (a migration of live data for a cosmetic gain) or
  letting `processId` hold a decision id (a field that does not mean what it is
  called, forever).
- Extracting the generic half into a shared service is the right end state, and it
  is a refactor of a landed, tested subsystem that this change does not need in
  order to be correct.

So the two packages are written as visible siblings, and the extraction is named
here as the follow-up: **when a third artifact kind wants a document, extract
first and add second.** Until then, a reviewer can diff `api/decisiondoc` against
`api/processdoc` and see that they agree.

### What this deliberately does not do

The process document still names a business rule task without showing the table
behind it. Putting a called decision's table *into* the document of the process
that calls it is probably worth more than this record is, and it is option 3 — not
instead of this one, but after it, and with its own record, because it raises
questions this one does not (which binding's version is shown, what happens when
the decision is not resolvable, how long a document may become).

### Consequences

- **Positive:** A decision can be handed to somebody who will never open Atlas, as
  a numbered version with a revocable link, and the table is legible in it.
- **Positive:** Sharing, revocation, retention and the public route behave exactly
  as they do for a process, because they are the same design.
- **Positive:** The version line is about sign-off, so it can be published from a
  draft — before the decision is deployed, which is when approval usually happens.
- **Negative / trade-offs accepted:** One subsystem's worth of duplicated Go.
  Named above, with the rule for undoing it.
- **Negative:** Two documents now exist for one process that calls a decision, and
  a reader has to be handed both. Option 3 is the fix and it is not in this record.
- **Negative:** The store grows without bound, like ADR-0143's; it gets the same
  prune route and the same "retention is a decision somebody makes" posture.
- **Follow-ups / risks to watch:** A decision document does not say which
  deployment was live when it was published, the way a process document does. It
  could: the decision deployment record is right there. It is left out until
  somebody asks, because a document published from a draft — the common case here
  — would carry an empty field.

## Pros and cons of the options

### Option 1 — a decision documentation version *(chosen)*
- Good: the audience, the sign-off and the revocable link are the same problem
  ADR-0143 already solved well.
- Good: publishable from a draft, which is when a rule is approved.
- Bad: duplicates the generic half of an existing subsystem.

### Option 2 — a plain download, no stored version
- Good: nothing to store, prune, share or revoke.
- Bad: it answers "let me read this now" and not "what did we sign off in March",
  and the second is what a business rule is asked about.
- Bad: the deployment record it leans on does not exist for a decision that has
  not been deployed, which is exactly when approval happens.

### Option 3 — fold it into the process document only
- Good: it puts the table where a reader meets it, which is worth a great deal.
- Bad: a decision shared by four processes is documented four times and owned by
  none, and a decision in no process is not documented at all.
- Bad: it is not exclusive with this record, and it is better taken separately.

### Option 4 — generalise ADR-0143's store
- Good: no duplication.
- Bad: it migrates live records, or keeps a field whose name is a lie, to avoid
  writing a sibling.
- Bad: the right time to generalise is when there are three, not two.

## Links

- extends [ADR-0143](0143-process-documentation-export.md) — the design this repeats for a second artifact
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the runtime version line this is deliberately not
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor this is published from
- relates to [ADR-0147](0147-splitting-the-api-server-object.md) — the per-area service shape both packages follow
- relates to [ADR-0029](0029-public-process-start-links.md) — the revocable public link mechanism
