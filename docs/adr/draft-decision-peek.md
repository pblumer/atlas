# ADR-DRAFT: A business rule task's decision is one click from the task, and its graph can be walked there

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-10
- **Deciders:** Atlas maintainers
- **Open question:** The peek shows the model the decision's reference resolves today — what
  the decision editor opens. A task bound to a pinned version, or to the version deployed with
  its process (ADR-0423), evaluates a deployed model that can differ from it. Whether the peek
  should show the model the binding resolves instead, and how it would say which deployment
  that is for a draft that has never been deployed, is not settled here. The window says which
  binding applies and that the two can differ.
- **Question checked:** 2026-11

## Context and problem statement

A form has a mark on the canvas since ADR-0465: in the Implement view a start event or user
task that names a form carries a small sheet, and a click on it opens the form. A business
rule task had no such mark. The decision it calls was reachable only through the properties
panel — select the task, open "Called decision (DMN)", press "Edit" — and "Edit" leaves the
diagram for the decision editor's page (ADR-0320), saving the diagram on the way.

The request was put plainly: a business rule task should carry a mark like the form's, so the
decision is one click away. And with a refinement: the decision could first be shown over the
diagram, so its requirements graph (DRG) can be explored without leaving.

Reading a decision and changing it are two different jobs. Leaving the diagram is the right
price for the second (ADR-0320's reasons — an address, a draft, a back button — all hold). It is
the wrong price for the first: an author who only wants to know what a task decides, or which
input a decision three levels down reads, should not have to save the diagram, change page and
come back.

A second question came with the first. Showing a decision's logic means reading it. The
decision documentation (ADR-0324) and the process documentation (ADR-0328) read it by parsing
the DMN XML in the browser (`decision-doc.js`), which knows decision tables, literal expressions
and lists. DMN 1.5's other boxed expressions — context, relation, invocation, conditional,
filter, iterator — are unknown to it, and teaching it each one would be a second DMN reader
beside temis, the engine that evaluates the model and already reads all of them.

## Decision drivers

- **One grammar on the canvas.** A linked artifact is marked where the step is drawn, in the
  Implement view, the way the form is (ADR-0465).
- **Looking costs nothing.** Reading a decision must not save or leave the diagram.
- **Editing stays a page.** ADR-0320's decision holds: the decision editor is reached through
  the same door the panel uses, with the way back.
- **DMN 1.5 as the engine reads it.** The logic shown is the engine's own reading of the model,
  for every boxed expression it supports — not a second, partial parse.
- **Stay off the engine's hot path.** A design-time read: nothing in the compiler, the
  processor or the log changes, and the model is compiled off the run loop, as the graph read
  already does (I1, I5).

## Considered options

1. **A mark that is a link to the decision editor**, exactly like the form mark.
2. **A mark that opens a read-only peek over the diagram**, with the editor one button further
   (chosen).
3. **The peek, drawn by the vendored dmn-js** in a read-only mode.
4. For the logic: **parse the XML in the browser** with `decision-doc.js`, or **ask the server
   for temis's typed views** (chosen).

## Decision outcome

Chosen: **option 2, with the logic from temis (option 4, second half)**.

- **The mark.** In the Implement view, a business rule task that calls a decision evaluated by
  this server (it names a `zeebe:calledDecision` and is not bound to an external temis Worker)
  carries a small decision-table mark in its top-right corner, opposite the table bpmn-js draws.
  A task bound to an external temis Worker is not marked: its decision lives elsewhere, and a
  local model that shares its id would be the wrong one to show. The Design view stays the
  drawing it is.
- **The mark is a button, not a link.** A click opens the peek. The form mark is a link because
  its click *is* the navigation; this mark's click is not, and a middle click that saved and
  left the diagram would be the surprise ADR-0465's modified click avoids.
- **The peek** is the decision-graph window of ADR-0405, opened without a case: the model's
  requirements graph with the called decision ringed, and below it the node the author picked —
  what it requires and what requires it, each a button that walks the graph there, and its
  logic. A decision service, which temis's graph does not draw as a node, is shown as the
  decisions it is made of. Escape, the backdrop and navigation close it.
- **The door out** is "Open in the decision editor", which walks the panel's own path
  (`walkDecisionDoor`): the diagram is saved or the author is asked, and the editor opens with
  the way back to this task. The panel's "＋ New decision" and "Edit" now walk the same function.
- **The logic is the engine's.** `GET /api/v1/dmnrefs/{id}/logic` resolves and compiles the
  reference's model off the run loop and returns, for every decision and business knowledge
  model, the kind of its logic and temis's typed view of it (`LiteralExpression`, `LogicView`,
  `BKMFunction`): literal, decision table, context, list, relation, invocation, conditional,
  filter, iterator. One read serves every node. The browser draws the shapes it is given and
  names a kind it has no drawing for rather than guessing at it. The same read is offered over
  MCP as `atlas_dmnref_logic`.

### Consequences

- **Positive:** A step's decision is one click from where the step is drawn, and reading it
  costs neither a save nor a page change. The DRG can be walked from the task, node by node.
- **Positive:** Every DMN 1.5 boxed expression temis evaluates is shown as temis reads it. A
  kind temis learns later reaches the peek through the same view, with no browser parser to
  extend.
- **Negative / trade-offs accepted:** The peek shows the reference's current model, not
  necessarily the version the task's binding evaluates (see the open question). The window
  says so.
- **Negative / trade-offs accepted:** A nested boxed expression (a context entry whose value is
  itself a table, say) is named by its kind, not drawn; the decision editor shows it. temis's
  `LogicView` can walk into it by locator, so drawing it is an extension, not a redesign.
- **Negative / trade-offs accepted:** A decision known to this server only as a deployment, with
  no stored model behind it, cannot be shown; the peek says so in words.
- **Follow-ups / risks to watch:** `decision-doc.js` still parses the XML in the browser for the
  decision and process documents. Moving it onto the same server read would give those
  documents the full DMN 1.5 set too.

## Pros and cons of the options

### Option 1 — a link to the decision editor
- Good: the form mark's exact grammar; no new window.
- Bad: every look is a save and a page change, and the DRG is explored in an editor, where a
  misplaced click is an edit.

### Option 3 — dmn-js read-only
- Good: dmn-js's own drawing and drill-down into every view.
- Bad: the vendored bundle exports the Modeler only (no Viewer); a Modeler made to look
  read-only still edits. Rebuilding the vendor bundle for a Viewer is a larger change than the
  request, and the graph window and its renderer already exist (ADR-0405).

### Option 4 — the browser parses the XML
- Good: no new endpoint.
- Bad: a second DMN reader that knows three of DMN 1.5's boxed expressions, beside the engine
  that knows all of them.

## Links

- extends ADR-0465 (the form mark) to the business rule task
- keeps ADR-0320 (the decision editor is a page): the peek reads, the editor edits
- reuses ADR-0405's window and renderer (`decision-graph.js`), without a case
- shows the binding of ADR-0423; draws decision services per ADR-0398
- `dmn/logic.go`, `api/decisions.go` (`handleDmnRefLogic`), `api/web/decision-logic.js`,
  `api/web/decision-graph.js` (`openDecisionPeek`), `api/web/editor.js`
  (`drawDecisionBadges`, `walkDecisionDoor`), `e2e/decision-peek.spec.mjs`
