# ADR-0013: Embed the bpmn-js modeler as a vendored asset

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0011 decided Atlas ships a browser BPMN viewer/editor by reusing `bpmn-js`
rather than building a renderer/modeler from scratch, and ADR-0012 decided the
UI is self-contained (no runtime CDN) with a buildless shell. This ADR settles
the concrete mechanics: **which** `bpmn-js` artifact we ship, **how** it gets
into the single binary, and **what obligations** its license imposes.

Two constraints pull against a naive "just `<script src=cdn>`":

- The binary must be self-contained (ADR-0011) — no CDN at runtime.
- The Go build must stay `go build ./...` with no npm prerequisite (ADR-0012) —
  so we cannot bundle `bpmn-js` from source as part of the server build.

## Decision drivers

- Self-contained binary; simple Go build (ADR-0011, ADR-0012).
- Reuse the standard toolkit instead of reimplementing BPMN rendering.
- License compliance — `bpmn-js` is under the bpmn.io license (MIT terms plus a
  watermark requirement).
- Updatability — bumping the vendored version should be a documented, repeatable
  step, not a mystery.

## Considered options

1. **Vendor the pre-built `bpmn-js` UMD dist** (`bpmn-modeler.production.min.js`
   + `dist/assets`) into `api/web/vendor/bpmn/`, embedded via `go:embed`.
2. **Bundle `bpmn-js` (and the properties-panel) from source** with a JS bundler
   as part of the build.
3. **Load `bpmn-js` from a CDN** at runtime.

## Decision outcome

Chosen option: **Option 1 — vendor the pre-built UMD dist.**

- We commit `bpmn-js` `dist/bpmn-modeler.production.min.js` (as
  `api/web/vendor/bpmn/bpmn-modeler.js`) and its `dist/assets` (CSS + the
  self-contained embedded font) into the repo. `go:embed` pulls them into the
  binary. The UMD build exposes a `BpmnJS` global — no bundler, no ES-module
  resolution, so ADR-0012's buildless rule holds.
- **Provenance and updates:** the exact source (npm registry), package version,
  and copy steps are recorded in `api/web/vendor/bpmn/README.md`. Updating is:
  re-fetch the pinned version from the npm registry, copy `dist` over, bump the
  version note.
- **License compliance:** the upstream `LICENSE` is kept verbatim in the vendored
  directory. The bpmn.io **watermark stays visible** in rendered diagrams, as the
  license requires. Note the distinction from ADR-0011's "remove the reference
  suite's branding": that is about *our product's* names and copy — it does **not**
  extend to stripping a third-party dependency's required copyright/license notice
  or watermark, which we must and do preserve.
- The properties panel (`bpmn-js-properties-panel`) is **not** vendored yet: it is
  ES-module-only and would require a bundler, reopening ADR-0012's buildless
  decision. For now the editor ships a small, hand-written Details panel that
  reads/writes element properties through the `bpmn-js` modeling API. A full
  properties panel, if wanted, is a later decision (it would vendor a pre-bundled
  artifact, keeping the Go build clean).

### Consequences

- **Positive:** a real, standard BPMN modeler (canvas, palette, context pad)
  works offline from one binary, with no npm in the Go build. Updates are a
  documented copy step.
- **Negative / trade-offs accepted:** ~1 MB of vendored minified JS + assets live
  in the repo. Vendored code is not rebuilt from source in our CI, so we trust the
  published dist. The rich properties panel is deferred because vendoring it
  cleanly needs a bundling step.
- **Follow-ups / risks to watch:** watch the vendored version for security fixes
  and bump deliberately. If/when we want the full properties panel or custom
  bpmn-js extensions, introduce a **vendored, pre-bundled** artifact step so the
  Go build stays npm-free (new ADR).

## Pros and cons of the options

### Option 1 — Vendor the pre-built UMD dist (chosen)
- Good: self-contained; no bundler; a `BpmnJS` global loads with one `<script>`;
  documented, repeatable updates.
- Bad: ~1 MB in-repo; no from-source rebuild; UMD build can't tree-shake unused
  parts; properties-panel not includable this way.

### Option 2 — Bundle from source
- Good: full control; can include the properties panel and custom extensions;
  smaller tailored output.
- Bad: makes npm + a bundler a build prerequisite, breaking ADR-0012's buildless
  Go build.

### Option 3 — CDN at runtime
- Good: nothing in-repo; trivial to wire.
- Bad: breaks the self-contained binary (ADR-0011); offline/air-gapped installs
  fail; a third party can change or pull the asset.

## Links

- implements ADR-0011 (reuse bpmn-js for the viewer/editor) under the
  self-contained constraint of ADR-0012
