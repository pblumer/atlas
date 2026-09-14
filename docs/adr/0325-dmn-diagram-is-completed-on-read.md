# ADR-0325: A DMN model's diagram is completed on read, and can be re-laid on request

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

A DMN model, like a BPMN one, has two halves: the **semantic** model (decisions,
input data, requirements, decision tables) and the **diagram interchange**
(`<dmndi:DMNDI>` — the shapes and edges that say *where* each element is drawn).
`dmn-js` needs DMNDI to draw the decision requirements graph.

Most DMN models that reach Atlas carry none:

- `POST /api/v1/dmn-models` accepts any DMN XML, and so does
  `atlas_upload_decision_model` over MCP. An agent writing a decision table writes
  the semantics, not a picture.
- temis's own `git_load_model` / `load_model` round trip works on semantics.
- A model hand-written or exported from another tool frequently has no DMNDI.

Until [ADR-0320](0320-the-decision-editor-is-a-page.md) that did not hurt much: the
only DMN surface was the read-only DRG viewer (`#/modeler/dmn/{ref}`), which
renders from `dmn.ModelGraph` and lays the graph out **in the browser** when the
nodes carry no bounds. Now that a decision is edited in `dmn-js` on a routed page,
the same model opens in an editor that draws nothing.

**What actually happens, measured.** A model with one `inputData`, one `decision`
and the `informationRequirement` between them, with no DMNDI:

- the DRG view renders **one** element — the decision. The input data and the
  requirement arrow are not drawn at all, so the graph the model describes is
  invisible and cannot be rewired;
- the semantic model nonetheless survives a save — `dmn-js` keeps what it cannot
  draw;
- but the save writes back a DMNDI containing **a shape for the decision only**,
  which `dmn-js` invented at (150, 150).

That third point is the one that makes this more than a cosmetic gap. After one
save the model has *a* diagram, so every "does this model have a diagram?" test
now answers yes — including the DRG viewer's own `hasDI` check, which then honours
the one authored bound and puts everything else at the origin. A single visit to
the editor degrades a model that rendered correctly before.

The question: **where does a DMN model's missing diagram come from, and what
happens to a diagram that is only half there?**

## Decision drivers

- **The same answer BPMN already has.** [ADR-0124](0124-server-side-diagram-auto-layout.md)
  settled this for BPMN: generate the diagram interchange in Go, on the server, with
  one generator behind two entry points — `Ensure` on read, `Regenerate` for the
  Auto-layout button. Every argument there applies unchanged here (CGO-free single
  binary, no browser in the read path, determinism, the coverage floor).
- **One layout, not two.** The DRG viewer already lays out a bound-less graph, in
  JavaScript. A second, differently-shaped layout in Go would mean the viewer and
  the editor draw the same model differently. #919's acceptance criteria say "no
  second DMN renderer"; a second *layouter* is the same mistake one level down.
- **Never move what a person placed.** Auto-layout is a service, not an opinion. A
  diagram somebody arranged must survive a read.
- **Design-time only.** This runs when a human opens a model. It touches no engine
  state and no invariant.

## Considered options

1. **Complete the diagram on read, in Go** — generate shapes and edges for what is
   missing, keep what is there; plus an explicit **Auto-layout** that re-lays
   everything.
2. **Lay out in the browser, in `dmn-js`** — a client-side layouter run after
   import.
3. **Refuse to store a DMN model with no DMNDI** — make the upload require a
   diagram.
4. **Leave it** — the DRG viewer works; the editor is for models authored in the
   editor.

## Decision outcome

Chosen option: **1 — complete on read, in Go, with one generator.**

`api/dmnlayout` is the DMN counterpart of `api/layout`: a package with no engine
or server dependency that reads DMN XML with its own minimal structs, places the
requirements graph in layers, and writes `<dmndi:DMNDI>` back.

The placement is the layered one the DRG viewer already uses, so the picture is
the same in both: **input data at the bottom, each decision one layer above the
deepest thing it requires**, nodes spread left to right within a layer in document
order. It is a decision requirements graph, not a process: there is no happy path
to keep straight and no port model to get right, so ADR-0127's pipeline is not
needed here — the layering *is* the layout.

Two entry points, as in BPMN:

- **`Ensure`** runs when the editor fetches a model
  (`GET /api/v1/dmn-models/{ref}/xml`). It never moves a shape that is already
  there.
- **`Regenerate`** backs `POST /api/v1/dmn-layout` and the editor's **Auto-layout**
  action: the author asking for the whole graph to be re-flowed.

### A partial diagram is regenerated, not completed

`Ensure` has three cases, and the third is the one this record exists for:

| the model's DMNDI | what happens | why |
|---|---|---|
| complete — every node has a shape | unchanged | somebody placed these |
| absent | generated whole | nothing to preserve |
| **partial** — some nodes have shapes, some do not | **regenerated whole** | no DMN tool emits a partial diagram on purpose |

Completing a partial diagram *around* the shapes already in it is the tempting
middle road, and it is wrong here. A partial DMNDI is not a half-finished human
arrangement — no editor produces one deliberately. It is the residue of exactly
the defect above: a tool drew what it could and wrote back only that. The
coordinates in it were invented by the tool that could not draw the rest, so there
is nothing in them worth preserving, and preserving them means placing the missing
nodes in whatever space is left rather than laying out the graph the model
actually describes.

The cost is stated plainly: if a person ever *did* place half a diagram by hand and
left the rest, their half is re-laid on the next read. Against that, the case this
fixes is one every uploaded model walks into.

### The viewer's fallback goes

`dmn.Graph` now fills in generated bounds for a model whose DMNDI does not cover
its nodes, so the DRG viewer always receives a graph it can draw. Its
JavaScript layered fallback is therefore removed rather than left as unreachable
code — one layout, in one place, drawn the same in the editor and the viewer.

### Consequences

- **Positive:** Every DMN model in Atlas renders, in both surfaces. A decision
  uploaded by an agent can be opened, seen and rewired in the editor.
- **Positive:** The read-degrades-the-model defect is gone: opening and saving a
  DMN model no longer leaves it worse than it was found.
- **Positive:** One generator, one picture. The viewer and the editor agree.
- **Negative / trade-offs accepted:** A hand-made partial diagram is re-laid. See
  above for why that case is not worth protecting.
- **Negative:** Atlas now owns a second layout generator (a small one). ADR-0124
  accepted that cost for BPMN with a much larger algorithm; a DRG layerer is a
  fraction of it, and the alternative was a *third* one in JavaScript.
- **Follow-ups / risks to watch:** The generator places nodes, and lets `dmn-js`
  route the requirement edges from the shapes. If a dense DRG ever needs real edge
  routing, that is where it goes.

## Pros and cons of the options

### Option 1 — complete on read, in Go *(chosen)*
- Good: the BPMN answer, reused; no browser in the read path.
- Good: deterministic, testable, and covered by the repository floor.
- Bad: a second generator to own, and a rule about partial diagrams to explain.

### Option 2 — lay out in the browser
- Good: no Go code; `dmn-js` already holds the model.
- Bad: it fixes only the editor. The viewer, a documentation export and anything
  else reading the model still sees a model with no diagram.
- Bad: it makes the stored model and the drawn model disagree, so the layout is
  recomputed on every open and never becomes the thing the author saved.

### Option 3 — refuse a model with no DMNDI
- Good: the problem cannot arise.
- Bad: it breaks `atlas_upload_decision_model` and every generator that writes
  semantics, which is most of them. A decision's meaning is its logic, not its
  picture; refusing on the picture is refusing the wrong thing.

### Option 4 — leave it
- Good: nothing to build.
- Bad: it leaves an editor that cannot show the graph it is meant to edit, and a
  save that quietly makes the model worse.

## Links

- extends [ADR-0124](0124-server-side-diagram-auto-layout.md) — the same decision for BPMN, whose reasoning this reuses
- relates to [ADR-0127](0127-layered-layout-pipeline-and-invariants.md) — the BPMN pipeline a DRG does not need
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor that made this visible
- relates to [ADR-0062](0062-embedded-dmn-editor.md) — dmn-js as the editor, unchanged
- relates to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) — the DRG viewer this keeps in step
