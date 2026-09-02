# ADR-DRAFT: An artifact's id is its identity — renaming moves it, collisions are refused

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

Two design-time stores key a record by an id the author types:

- A **draft** is filed under the process id read out of its BPMN (ADR-0021,
  `handleSaveDraft` → `processIdentity`).
- A **form** is filed under the id a user task's `formId` binds to (ADR-0028).

Neither save knew *which* record the author was editing. It only knew the id in front
of it, and wrote there. That produced two failures, reported together:

**A rename left a duplicate.** Open the draft `Process_mtjs4`, retype the Process ID to
`order-fulfillment`, Save. The save wrote a new record under the new id and left the old
one untouched: the Modeler home then listed the same diagram twice, and which of the two
a co-editor opened was a coin flip.

**A rename could overwrite somebody else's work.** If a draft (or form) already held the
id being typed, the save landed on top of it. Nothing warned, nothing asked; the
artifact that was there was simply gone. The same held for the first save of a new
diagram whose id happened to match an existing draft.

The form editor had a third, quieter version of the same defect. The store keyed a form
independently of its schema, so the Design pane's **Form ▸ General ▸ ID** field edited
`schema.id` and nothing else. The chip in the toolbar went on showing the id the form
was really stored under, the panel showed the one the author had typed, and the rename
never happened — while an export of that form carried the typed id, so re-importing the
file created a *second* form. One artifact, two ids, disagreeing on screen.

The question: **how does a save know whether a changed id means "rename this artifact"
or "write a different one", and what happens when the id is already in use?**

## Decision drivers

- **No unwanted copies, no unwanted overwrites.** These are the author's two worst
  outcomes and neither may happen silently.
- **The id an author sees must be the id the artifact has.** A field that edits
  something other than the identity it appears to name is a trap.
- **A refusal at Save is too late to be the whole answer.** By then the author has typed
  the id, tabbed away and formed a belief that it took.
- **Non-interactive writers still need a plain upsert.** An import, a source-tree apply
  (ADR-0134) and the MCP authoring tools save the same id repeatedly on purpose; that is
  their documented update path and must keep working.
- **One writer, one turn.** Any check-then-write has to close inside the run loop's turn
  or it is a race that loses a record (ADR-0002).

## Considered options

1. **Give an artifact a synthetic key** (a uuid) and demote the id to an attribute.
2. **Say what you are editing:** the save carries the record it started from, so a
   changed id is a rename; a collision is refused; the id field checks itself live.
3. **Refuse every save onto an existing id** unless an explicit `overwrite=true` is
   passed.
4. **Warn in the UI only** — keep the server's upsert, add a confirmation dialog.

## Decision outcome

Chosen option: **Option 2 — the save names the record it is editing.**

`POST /api/v1/drafts?from=<draft id>` and the form save's `"from"` field name the
artifact the editing session opened, empty for one that has never been saved. Present,
they make the save identity-aware:

- `from` equals the id being saved → a plain update, as before.
- `from` differs → a **rename**: the record is written under the new id and the one it
  came from is deleted, carrying its project and its creator across (ADR-0071), so a
  renamed draft does not fall out of its application into Ungrouped. The response echoes
  `renamedFrom`, which is how the Modeler learns its session now addresses a different
  artifact.
- The target id must be free. If anything already holds it the save is **409**, nothing
  is written, and the message names the id. This covers the first save of a new artifact
  too (`from` is present and empty, so any existing id is somebody else's).

Omitting `from` entirely keeps the original upsert-by-id, which is what every
non-interactive writer wants.

The target is re-checked inside the same run-loop turn that writes, because the read
that authorized the save happened in an earlier turn (ADR-0002). The write order is save
first, then delete the record renamed away from: the reverse loses the diagram if the
write fails, and a failed delete is reported rather than passed off as a clean save.

Two `GET .../{id}/availability` probes back the live check the Console does while an id
is being typed, so a collision colours the field red where it is typed instead of
surfacing as a refused save. They answer only what the 409 already reveals — whether the
id is free — and name what holds it only to a caller who could see that artifact anyway.

In the form editor the schema's id becomes **the** id: it is stamped from the stored one
on open (so the panel stops lying about identity), the toolbar chip mirrors it as it is
typed and marks it dashed while the rename is unsaved, and Save asks before renaming,
because a user task still bound to the old id will find no form.

### Consequences

- **Positive:** A rename is a rename — one artifact, one id, in the store, in the
  schema, in the URL and on screen. No id can be overwritten without the author choosing
  it. The collision shows while the id is being typed.
- **Positive:** The form editor's two ids collapse into one, which also makes
  export → re-import round-trip to the same form instead of forking a copy.
- **Negative / trade-offs accepted:** Save has a mode. A caller that sends `from` gets
  refusals a caller that omits it does not, which is a seam to explain — the price of
  not breaking the importers and agents that legitimately upsert.
- **Negative:** A rename reopens the diagram (the route, the live session and the
  breadcrumbs are all keyed by the old id), which costs the undo history.
- **Negative:** Two flows now *can* hit a collision where they used to write straight
  through: importing a `.bpmn` or `.form` file whose id already exists (usually a
  corrected export of that same artifact), and saving a deployed definition back as a
  draft, where the process ids match by construction. Both are offered as an explicit,
  named overwrite rather than a refusal — replacing what is there is what the author
  meant — but neither happens silently any more, which is the point.
- **Follow-ups / risks to watch:** Forms stored before this change may still carry a
  `schema.id` that disagrees with their store id; opening one in the editor reconciles it
  silently, and a save then renames nothing because the stored id won. A user task bound
  to a form that is later renamed is not rewritten — the confirmation says so, and
  re-linking is the author's.

## Pros and cons of the options

### Option 1 — synthetic key, id as an attribute
- Good: renames become trivially safe; two artifacts may share a display id.
- Bad: the id is not decoration — `zeebe:formDefinition formId` and a call activity's
  process id are *references*, resolved by id at deploy and at runtime. Allowing
  duplicates makes those references ambiguous, which is a far worse failure than the one
  being fixed. It also migrates every stored record and every URL.

### Option 2 — the save names the record it is editing
- Good: the ambiguity is removed where it exists — the client is the only party that
  knows whether an id changed *within one editing session*.
- Good: opt-in, so importers and agents are untouched.
- Bad: two behaviours behind one endpoint.

### Option 3 — refuse every collision unless `overwrite=true`
- Good: one rule, no mode.
- Bad: breaks the documented upsert of `atlas_save_form` and the source-tree apply, and
  would make an agent's ordinary re-save fail until it learned a new flag.

### Option 4 — warn in the UI only
- Good: no API change.
- Bad: leaves the overwrite reachable by every other caller and cannot prevent the
  duplicate at all — the server would still write a second record on a rename. A rule
  the store does not hold is not a rule.

## Links

- relates to [ADR-0021](0021-diagram-drafts.md) — drafts keyed by process id
- relates to [ADR-0028](0028-forms-and-the-tasks-app.md) — forms and `formId`
- relates to [ADR-0071](0071-sharing-scopes.md) — the scope a renamed record carries
- relates to [ADR-0140](0140-live-collaborative-modeling-sessions.md) — sessions are keyed by draft id, so a rename reopens
