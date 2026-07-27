# ADR-0062: An embedded DMN editor (dmn-js)

- **Status:** Accepted
- **Date:** 2026-07-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) set a deliberate non-goal:
**Atlas is not a DMN authoring product.** Decisions were authored in temis (its
Modeler / MCP surface); Atlas only *referenced* a model by handle
([ADR-0034](0034-projects-and-artifacts.md) Phase 2), *validated* it,
*read* it (a read-only SVG decision-requirements-graph viewer), and *evaluated*
it — locally via the embedded temis library ([ADR-0039](0039-dmn-io-variable-mappings.md))
or centrally via a connector ([ADR-0050](0050-temis-decision-connector.md)).

In practice this made the single most important DMN flow the worst one. To use a
decision in a business rule task an author had to leave Atlas, model the decision
in a separate tool, export a `.dmn` file, upload it, and only then pick it. The
decision picker's dropdown was **empty** until that whole out-of-band round trip
completed, and nothing guided the author through it. DMN is the discipline Atlas
most wants to be good at, and the authoring gap undercut everything built on top of
it. The non-goal, reasonable when the variable subsystem and picker did not yet
exist, now costs more than it saves.

## Decision

**Embed a real DMN editor in Atlas and let a decision be authored in place, with
its inputs and output adopted into the business rule task automatically.** This
reverses ADR-0014's "no authoring surface" non-goal for the decision-table case.

- **Editor:** the vendored **dmn-js** modeler (bpmn.io — the same family as the
  bpmn-js process modeler Atlas already embeds, ADR-0013). It ships a DRD editor,
  a decision-table editor, and a literal-expression editor as a pre-built UMD
  bundle (`window.DmnJS`), vendored under `api/web/vendor/dmn/` and served from the
  `//go:embed web` tree exactly like bpmn-js — no CDN, no build step, no new
  runtime dependency. Assets load lazily so non-editor pages stay light.
- **Authoring stays dmn-js; storage + evaluation stay temis.** dmn-js produces
  standard DMN XML (DMN 1.3, a namespace temis compiles). Atlas does not gain a DMN
  *engine* or hand-roll table editing — it hosts the editor and stores/evaluates
  what it produces. The FEEL and decision logic are still temis's job.
- **Save path reuses the existing seam.** On save the model XML is stored through
  the DMN upload endpoint (`POST /api/v1/dmn-models`); a new decision creates a
  project reference, an edited one overwrites its model in place (`?handle=`,
  atomic temp-file + rename) so the reference — and any picker selection — stays
  valid. `GET /api/v1/dmn-models/{ref}/xml` serves a model back for editing.
- **Inputs/output are adopted automatically.** Because Atlas already derives a
  decision's inputs from its input-data nodes and its output from the decision
  table (ADR-0039, surfaced via `GET /api/v1/decisions`), a decision authored in
  the editor is re-fetched and applied through the *same* picker code path that a
  hand-picked decision uses: the business rule task's decision id, result variable,
  and input mappings are filled in with no extra step. The seed model is a real
  little DRG (one input-data node → one decision with a table) so the very first
  save already has an input and an output to adopt.

## Consequences

- The empty-dropdown dead end is gone: "＋ Neue Decision" authors a decision and
  selects it; "Bearbeiten" opens the current decision's local model in place.
- The same editor is reachable from the **Project Explorer**, not only the
  business-rule-task picker: a project's DMN artifact offers "Bearbeiten" (and the
  read-only DRG viewer an Edit button) that opens its model in place through the
  identical save seam (`?handle=`). Because editing keeps the handle, an in-editor
  decision rename can leave the reference's display name stale, so the reference
  update endpoint (`PATCH /api/v1/dmnrefs/{id}`) gained an optional `name` — the
  Explorer mirrors the new decision name onto the reference on save.
- Invariants are untouched. This is an authoring/UI concern on the HTTP + web
  surface; nothing changes on the single-writer processor path (I1/I4), decisions
  still compile at deploy (I5), and the event log is still the only source of truth
  (I6). Storing a model file is not engine state, so it never touches the run loop.
- The binary grows by the vendored dmn-js bundle (~1.3 MB of JS + CSS/fonts),
  in the same spirit as the already-vendored bpmn-js modeler.
- Editing overwrites a model in place rather than versioning it; multiple
  references to one handle all see the edit. Model *versioning* is left to temis
  (git-backed models) and is out of scope here.
- Scope is the decision-table case. dmn-js also edits boxed expressions and
  multi-decision DRGs; those work in the editor but are not a design goal of this
  ADR. Central (connector) decisions authored elsewhere are unaffected — this is
  about local models Atlas can resolve and write.
- The model **source** still matters: when a remote temis service serves models
  (`dmn.ServiceResolver`), there is nothing local to write to, so the upload/save
  path is refused (as before) and authoring happens in temis. The embedded editor
  targets the zero-config local model folder (`DirResolver`).
