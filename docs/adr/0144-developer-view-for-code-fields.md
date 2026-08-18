# ADR-0144: A Developer View for code-bearing fields

- **Status:** Accepted
- **Date:** 2026-08-18
- **Deciders:** Atlas maintainers

## Context and problem statement

Several of the Modeler's property fields do not hold *properties*. They hold
**code**:

- a FEEL expression on a script task, a gateway condition, a correlation key, an
  ad-hoc completion condition, or behind any `fx` toggle (ADR-0067);
- a general-purpose job script — PowerShell, Python or JavaScript (ADR-0047);
- a JSON value: an I/O-mapping payload, a start variable's default, a form
  schema, a sample-variables box;
- a Markdown documentation text, which now reaches an assignee, the instance
  replay and the exported process document (ADR-0143);
- eventually an HTML fragment (a mail body, a small form).

Those fields already have a decent *editing surface*: `code-editor.js` gives them
highlighting, completion, live validation, a gutter and error markers. What they
do not have is **room**. A property panel is a column roughly 320 px wide, and
each of these fields is three rows tall inside it. The two things an author needs
next to the code do not fit there at all:

1. **Which variables are in scope, and where do they come from.** The Variables
   panel answers that, but it is a *different* panel — reading it means leaving
   the field. And it answers it for the diagram, not for the element: it cannot
   say "this one is *this* activity's input mapping and this one is process
   scope".
2. **What the language offers.** The completion popup shows a signature for
   something already being typed. It cannot be browsed, has no categories, no
   worked examples, and nothing to read when the question is "what is the FEEL
   function for this?".

The result is that authoring anything non-trivial means writing it somewhere else
and pasting it in. The question this ADR answers: where does a developer-grade
editing surface live, and what does it cost the rest of the Modeler?

## Decision drivers

- **One editing surface, not two.** If the big editor behaves differently from
  the inline one, every author has to learn two sets of habits, and every future
  editor feature has to be built twice.
- **No new persistence, no new save path.** These fields are already wired to
  save through the property panel and to participate in undo/redo. A second
  writer into the same business object is a correctness problem, not a feature.
- **Buildless and self-contained** (ADR-0012, ADR-0013). No bundler, no CDN, no
  vendored editor framework. Monaco or CodeMirror would answer the *editor*
  question outright and violate this outright.
- **Design-time only.** This touches no value type, no event, no `applyToState`,
  no recovery path (I2, I4).
- **Losing typed code must be impossible by accident.** A modal that discards a
  screenful of work on a stray keystroke is worse than no modal.

## Considered options

1. **Grow the property panel.** Make the code fields resizable / full-height in
   place, and put the variables and function reference into collapsible sections
   under them.
2. **A full-screen modal over the same editor** — F2 lifts the focused field into
   a dialog that hosts `code-editor.js` plus side panels, and writes the value
   back to the field.
3. **Vendor a full editor (Monaco / CodeMirror)** with its own language services,
   in a modal or a docked pane.

## Decision outcome

Chosen option: **"A full-screen modal over the same editor"**.

Option 1 does not solve the actual constraint. The panel's width is what makes
the code fields cramped, and widening the panel takes the space from the canvas —
which is the thing the author is modelling in. A reference catalogue and a
scope-grouped variable list simply do not belong in a 320 px column.

Option 3 buys a better editor at the price of the two rules this repository is
built on: it is not buildless, and it is a large third-party surface that would
own our authoring experience. It also would not answer the parts that actually
matter here — *Atlas's* variables in *Atlas's* scopes, and FEEL's builtins with
worked examples — because none of that is language-server-shaped; we would still
write it ourselves, and then maintain the adapter too.

Option 2 gets the room without any of that. Concretely:

- **`dev-view.js`** renders the modal and **delegates the code area to
  `attachCodeEditor`** — the same module, the same language modules, the same
  completion, validation and drag-and-drop as inline. The modal adds panes, not
  an editor.
- **`dev-lang.js`** is the registry of what differs per language: the editor
  module, wrap/gutter/format behaviour, the overview text, the function
  reference (grouped, with signatures and worked examples), and the snippets. It
  also carries two new tokenizers, **Markdown** and **HTML**, and adapts the
  JSON tokenizer to the code-editor's token contract.
- **A field declares itself** with `data-devlang="<id>"` (`markDevField`). One
  capture-phase `F2` handler per document (`installDevShortcut`) finds the
  focused field and opens it. That is why F2 works in every JSON field in the app
  from one line in `json-editor.js`, without those call sites knowing the modal
  exists.
- **The context is resolved at press time**, not at render time: the resolver
  reads the current selection, so the variables shown are the ones in scope now.
- **Variables are grouped by scope** — input mappings, what the element writes,
  process scope, form fields, data objects — which `devVariables` derives from the
  same static analysis as the Variables panel (`collectDiagramVariables`), so the
  two cannot drift.
- **The side panel folds away** to a rail of tab labels, and the choice is
  remembered in `localStorage` — a wide script can have the whole modal, and a
  developer who works without the reference does not re-collapse it every time.
  Picking a tab from the rail expands onto it.
- **Writing back is the only mutation.** Apply sets `field.value` and dispatches
  `input` + `change`, exactly what a keystroke produces. The panel's existing
  save wiring runs unchanged; nothing new touches moddle, modeling or the
  command stack.

Losing work is prevented by making the destructive path two steps: `Esc` with
unsaved changes asks (`Discard` / `Keep editing`), and only the second `Esc`
discards. `Ctrl`/`Cmd`+`Enter` (and `F2` again) applies.

### Consequences

Good:

- Every code field in the app gains the big editor by declaring one attribute.
- The reference content lives in one file, so adding a language is: a tokenizer
  (or an existing module), a reference, snippets, one registry entry.
- Nothing about the engine, the save path, or recovery changes; the feature is
  removable by deleting two files and four call sites.

Bad / accepted:

- The FEEL categories and examples in `dev-lang.js` duplicate *knowledge* that
  `feel.js` also encodes (its builtin list). They are joined by name at render
  time, and a builtin with no help entry still shows under "Other" — the
  degradation is visible but harmless. Signatures and one-line docs are **not**
  duplicated; they are read from `feel.js`.
- The modal is one-at-a-time by construction (a module-level guard). Editing two
  fields side by side is not a use case we support.
- The Markdown and HTML highlighters are line/scan-oriented, not parsers. They
  make structure visible; they will mis-colour pathological input.

### Compliance with the invariants

Design-time UI only. No allocation path, no WAL, no state, no `applyToState`, no
partition. Invariants I1–I6 are untouched.
