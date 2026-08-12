# Conformance catalog

A human-readable catalog of the conformance suite — one entry per scenario, with
its diagram, how it is driven, and the outcome it must produce. It is the readable
face of the [`../tck`](../tck) case format.

**➡ [Browse the scenario pages](scenarios/README.md)** — one page per scenario,
each with its description, diagram, driver, and expected outcome. The pages are
generated from `../scenario.go`, so they never drift from the executable suite.

**▶ Interactive gallery:** a running Atlas server serves a live "try it out" page
at **`/conformance-gallery.html`** (also linked from the web UI's **?** menu). Each
scenario gets a **Deploy & Run** button that deploys the model, starts an instance,
and shows the live path + variables against the expected golden outcome; one button
deploys the whole collection into a project. The page is generated into
[`../../api/web/conformance-gallery.html`](../../api/web/conformance-gallery.html)
by `go test ./conformance -update` (see `../gallery_test.go`).

## Diagrams

Each model's layout is **hand-authored BPMN-DI carried in the fixture itself**
(under [`../models`](../models)). The DI is diagram-only — the compiler ignores it,
so adding or tuning a layout never changes a golden trace. The rendered images live
in [`diagrams/`](diagrams/) and are produced by Atlas's *own* vendored bpmn-js
modeler (`api/web`), so the catalog shows exactly what the engine's modeler shows.

Layout follows the house style (see `AGENTS.md`): one straight horizontal main
axis, each branch in its own lane, orthogonal edges, gateway labels above the
gateway.

### Regenerating the images

```bash
cd conformance/catalog/render
npm ci
npx playwright install chromium     # or set CHROMIUM_PATH to a pre-installed one
npm run render                      # renders every fixture that carries BPMN-DI
```

`render.mjs` serves `api/web` and drives the real Atlas modeler headlessly, cropping
each diagram tightly. The committed PNGs are the source of truth; rerun after editing
a model's layout.

## Scenario pages

The per-scenario Markdown pages live under [`scenarios/`](scenarios/) — one page
per scenario plus an [index](scenarios/README.md). Each page is generated from the
scenario definition and its live run result (description, diagram, driver steps,
and expected path/variables/data objects), so a stale page fails the suite exactly
like a stale golden. Regenerate them alongside the TCK cases:

```bash
go test ./conformance -update
```

## Status

Every positive scenario carries a hand-authored diagram, a generated catalog
page, and an entry in the interactive gallery served at `/conformance-gallery.html`.
Remaining planned work: diagrams for the negative models.
