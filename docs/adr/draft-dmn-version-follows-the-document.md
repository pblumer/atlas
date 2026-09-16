# ADR-DRAFT: A DMN document's own namespace decides which DMN version reads and writes it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** whether the vendored dmn-js 1.5 moddle descriptor covers every construct the 1.3 one does — measured only that a 1.5 document parses and round-trips, not that authoring against it is complete
- **Question checked:** 2026-09

## Context and problem statement

Atlas's decision engine is temis, a DMN 1.5 engine. The MCP surface documents its
input as "DMN 1.5 XML". The deploy path accepts a 1.5 model, the runtime evaluates
it, and the read-only DMN view renders it.

The decision **editor** did not open it:

```
Could not open the decision editor
failed to parse document as <dmn:Definitions>
```

Found by an author on a live instance, on a DRD that had just run two releases'
worth of process instances successfully.

The cause is one missing constructor option. `api/web/dmn-editor.js` built the
vendored dmn-js without `dmnVersion`, and the bundle defaults it:

```js
let n = e && e.dmnVersion || "1.3";
```

With `"1.3"` the `dmn` prefix binds to `…/20191111/MODEL/`, so a root element in
`…/20230324/MODEL/` is an element moddle has never heard of. The bundle ships a
`"1.5"` descriptor; it was simply never selected.

The engine has no such limit. The same model, both namespaces, through
`dmn.Registry` on the pinned temis:

```
1.5 (20230324)   compileErr=<nil>  diagsHasErrors=false  outputs=map[praemie:1250]
1.3 (20191111)   compileErr=<nil>  diagsHasErrors=false  outputs=map[praemie:1250]
```

Identical result, identical trace.

**A DMN file names two independent namespaces**, and this is the part that makes the
obvious fix insufficient. MODEL carries the logic; DMNDI carries the picture. dmn-js
binds them together per version:

| `dmnVersion` | `dmn` (MODEL) | `dmndi` (DMNDI) |
|---|---|---|
| `"1.3"` | `…/20191111/MODEL/` | `…/20191111/DMNDI/` |
| `"1.5"` | `…/20230324/MODEL/` | `…/20230324/DMNDI/` |

DC and DI are version-independent — the bundle contains exactly one URI for each.

`dmn/layout.go` wrote DMNDI in the 1.3 namespace unconditionally. So passing
`dmnVersion: "1.5"` alone would have opened the model and silently dropped its
diagram, because the DMNDI block Atlas itself generated for it is in the namespace
the 1.5 moddle does not read. That failure is quieter than the one it replaces: the
model opens, and the layout is simply gone.

**Why it stayed hidden.** `seedDmnXml()` emits DMN 1.3, so every model authored
inside Atlas is 1.3 and round-trips. Only a model authored elsewhere — the temis
Modeler, another tool, an agent over MCP, anything following the current spec —
lands in 1.5 and becomes uneditable. On the installation where this surfaced, a
survey of all eleven stored models found exactly one in 1.5: the one the author
could not open.

The question this record answers is therefore not "which DMN version is Atlas". It
is: **what decides the version — the product, or the document?**

## Decision drivers

- A model that deploys, evaluates and renders must be editable. Anything else is a
  boundary the product does not state and an author discovers by hitting it.
- Silent loss is worse than loud refusal. A dropped diagram leaves no message.
- An author's document is theirs. Opening it must not change what it is.
- The two namespaces must not be decided in two places, or they will disagree.
- What is already stored must keep working, unchanged and unrewritten.

## Considered options

1. The document decides, at both ends
2. Normalise every model to DMN 1.3 on read
3. Refuse DMN 1.5 with a clear message
4. Move the whole product to DMN 1.5

## Decision outcome

Chosen option: **"The document decides, at both ends"**.

The version is read from the document and applied to everything that reads or writes
it:

- `dmnVersionOf(xml)` in `api/web/dmn-editor.js` picks the moddle descriptors. It
  reads the raw text rather than parsing, because the answer is needed in order to
  decide how to parse; parsing first would be circular. It is a *constructor*
  option, so the model XML is now fetched before the modeler is built rather than
  after — the one structural change this needed.
- `dmndiFor(modelNS)` in `dmn/layout.go` picks the DMNDI namespace for a generated
  diagram, from the model's own MODEL namespace. Both the read path
  (`EnsureDiagram`) and the author-triggered Auto-layout (`RegenerateDiagram`) go
  through it, so they cannot come to disagree.

Anything unrecognised stays 1.3 — what every stored model was written under and what
the seed still emits.

A consequence worth stating as a decision rather than a side effect: **a model opened
as 1.5 saves as 1.5.** dmn-js's writer emits whatever the moddle was built with, so
the author's version survives the round trip instead of being quietly downgraded.

**The seed stays at DMN 1.3.** The engine is a 1.5 engine and the current spec is
1.5, so making new models 1.5 is the tempting move. It is not taken here, because
what has been measured is that a 1.5 document *parses* and *round-trips* — not that
the vendored 1.5 descriptor is complete enough to author against. Changing what
every new decision is, on a descriptor whose coverage is unmeasured, would trade a
bug that affected one imported model for one that could affect every new one. That
is the open question above, and the seed moves when it is answered, not before.

### Consequences

- **Positive:** a spec-conformant model authored anywhere opens, keeps its diagram,
  and is saved back as what it was. The DMN version stops being a property of the
  product that nothing states, and becomes a property of the document.
- **Positive:** the version decision exists once per side, as a named function, so
  the next construct that turns out to be version-sensitive has one place to go.
- **Negative / trade-offs accepted:** the modeler is now built after a network
  round trip rather than before it, so a slow model fetch delays the canvas rather
  than showing an empty one. That is the honest ordering — an empty canvas that then
  refuses the document was not better.
- **Negative / trade-offs accepted:** Atlas now writes two DMNDI namespaces
  depending on input, where it previously wrote one. Any future reader of stored DI
  must not assume the 1.3 URI.
- **Follow-ups / risks to watch:** the seed question above. Separately, a model that
  mixes namespaces — 1.5 MODEL with a hand-written 1.3 DMNDI, which is what the
  model that triggered this actually contained — is now regenerated into a matching
  pair on the read path, but only where Ensure regenerates at all. A fully-drawn
  mismatched model comes back untouched, and its diagram stays invisible to the
  editor. Nothing yet detects that case.

## Pros and cons of the options

### Option 1 — The document decides, at both ends
- Good: nothing is rewritten; the author's version survives.
- Good: covers the DMNDI half, which the obvious fix does not.
- Bad: two code paths must agree on the version. Mitigated by stating it once per
  side.

### Option 2 — Normalise every model to DMN 1.3 on read
- Good: fewest moving parts, and matches what the seed already assumes.
- Bad: rewrites the author's document behind their back.
- Bad: makes Atlas a DMN 1.3 product while the engine underneath is 1.5 — moving
  away from the spec rather than toward it.

### Option 3 — Refuse DMN 1.5 with a clear message
- Good: strictly better than the message that existed; cheap.
- Bad: leaves the product unable to edit what it can deploy and run. Worth doing as
  an interim, not as an answer.

### Option 4 — Move the whole product to DMN 1.5
- Good: one version everywhere; matches the engine and the current spec.
- Bad: rests on the unmeasured assumption above, and rewrites every stored model to
  get there. The larger claim needs the larger measurement first.

## Links

- relates to [ADR-0325](0325-dmn-diagram-is-completed-on-read.md) — the generated diagram this
  changes the namespace of
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor this changes the
  construction of
