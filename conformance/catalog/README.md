# Conformance catalog

A human-readable catalog of the conformance suite — one entry per scenario, with
its diagram, how it is driven, and the outcome it must produce. It is the readable
face of the [`../tck`](../tck) case format and the basis for an interactive gallery
(planned).

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

## Status

Rollout is in progress — diagrams are added a batch at a time, flat control-flow
models first, nested ones (subprocess, transaction, call activity) last. The
per-scenario Markdown pages are assembled once the diagrams for a batch land.
