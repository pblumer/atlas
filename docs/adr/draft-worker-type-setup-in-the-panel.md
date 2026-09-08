# ADR-DRAFT: A Worker Type carries its own setup, in the panel where it is chosen

- **Status:** Proposed
- **Date:** 2026-09-08
- **Deciders:** Atlas maintainers

## Context and problem statement

A service task's Worker Type catalog (ADR-0067, ADR-0203) describes what a task
*states*: a target, an operation, a result variable. It deliberately says nothing
about what has to exist **outside Atlas** before any of that runs — a Google service
account whose key is in the vault and whose address the spreadsheet is shared with, an
Entra app registration with admin consent, a Jira API token plus the global *Browse
users and groups* permission, a Discord bot invited to the server with *View Channel*.

That knowledge exists, and it is good: the handbook's *Putting a worker into service*
chapter carries a runbook per type, on the page this server serves itself
(`/handbuch.html`). But it is another page, in another tab, that a reader has to know
exists and know has a chapter for exactly this. The observed failure is not a wrong
model — it is a correct model that does nothing, because a checkbox at the provider is
missing and nothing on the screen where the type was chosen said there was one.

Two audiences hit it from opposite ends: an author picking a Worker Type in the
Modeler, and an operator on the Console's *New worker* form having just installed
Atlas. Both are looking at a form whose fields presuppose work they have not been told
about.

## Decision drivers

- Someone who has just started Atlas should be able to send mail or reach Jira without
  first reading a manual end to end.
- Whatever is written must not drift: a new Worker Type must not be able to ship
  without it, and a link into the handbook must not survive the card it points at.
- The panel is 270 px wide. A recipe that pushes the fields off screen for the author
  who configured this type last month is a regression for them.
- No build step (ADR-0012), and no dependency on reaching an external documentation
  site: an air-gapped installation has the same handbook as a public one.

## Considered options

1. **Leave it in the handbook** and rely on the help menu's contextual link.
2. **Put the whole runbook in the panel**, replacing the handbook chapter.
3. **One setup module per Worker Type, rendered where the type is chosen, deep-linking
   into the handbook card that says it at length.**
4. **Serve the setup metadata from the server** (extend `/api/v1/connector-kinds`).

## Decision outcome

Chosen option: **3** — `api/web/workertypedocs.js` holds one entry per Worker Type:
a one-line `needs` (does this need a Worker record and a credential at all?), the
ordered `steps` to get there, the one `trap` this type is actually reported with, and
the `anchor` of its handbook card. Four surfaces render it — the service task's
properties panel, the send task's, the business rule task's temis binding, and the
Console's create form and worker dialog — so an entry is written once and reaches
everyone who has to act on it.

The `needs` line and the handbook link are always visible; the steps are folded into a
`<details>`, because an author who already has the worker wants the fields. The link is
relative (`/handbuch.html#runbook-…`), so it works on an installation with no internet.

Drift is held by `api/workertypedocs_internal_test.go`: every catalog kind has an
entry, every entry names a kind that still exists, every anchor is an id the handbook
actually carries, and every surface still renders the block. The handbook gained a card
for the types that had none (Discord, the AI worker, clio split from temis, SCIM, SOAP,
LDIF, the job worker, user provisioning) and per-product anchors for the three SQL
types.

### Consequences

- **Positive:** The question "what do I have to do before this works" is answered where
  it is asked, for every Worker Type, without leaving the screen. A dangling handbook
  link is now a failing test rather than a reader landing at the top of an 800 KB page.
- **Positive:** "Nothing to configure" is stated rather than implied by an absent block,
  which is what a reader could not tell from silence before.
- **Negative / trade-offs accepted:** The setup text is written twice — tersely in the
  module, at length and bilingually in the handbook. They are held together only by the
  anchor, not by a test that compares prose; a step that changes at a provider has to be
  changed in both. The alternative (rendering the handbook card into the panel) would
  have coupled a 270 px panel to a page written for a full-width reader.
- **Negative:** The panel text is English only, like the rest of the Modeler chrome,
  while the handbook is bilingual.
- **Follow-ups / risks to watch:** Provider UIs are somebody else's and change without
  notice. The entries name paths (*IAM & Admin → Service accounts*), which is what makes
  them useful and what will age; treat a renamed menu as a bug report.

## Pros and cons of the options

### Option 1 — leave it in the handbook
- Good: one place, bilingual, already written and already linked from the help menu.
- Bad: it is the place people do not go. The contextual link resolves per *route*, so
  from the Modeler it offers "Designing processes", never the runbook for the type under
  the cursor.

### Option 2 — the whole runbook in the panel
- Good: nothing to keep in sync.
- Bad: a 270 px panel cannot carry a chapter, and the Console form is not a manual. It
  would also mean deleting the one document an operator can print and hand over.

### Option 4 — serve it from the server
- Good: one source, reachable from the API and MCP too.
- Bad: the catalog it describes lives in the browser (ADR-0067), so this would split one
  description across two languages and two deployment paths for no reader's benefit. The
  Go guard already reaches the JavaScript, which is what the coupling actually needed.

## Links

- relates to ADR-0067 (Worker Type catalog on the service task)
- relates to ADR-0203 (Worker, Worker Type, Worker Instance)
- relates to ADR-0160 (one description of a Worker's fields, shared by both surfaces)
- relates to ADR-0012 (buildless console)
