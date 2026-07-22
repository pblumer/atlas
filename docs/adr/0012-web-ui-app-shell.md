# ADR-0012: A buildless, self-contained web UI app shell

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0011 committed Atlas to a single binary that serves a web UI. The UI is
growing from a one-page shell into a small **application suite**: a management
console, a BPMN modeler/editor, and placeholders for task, operations, and
insight surfaces, tied together by a common shell (top bar, an app switcher,
per-app navigation). We need to decide how that front end is structured and
built without compromising the two things that make ADR-0011 worth doing:
the binary stays **self-contained** (no runtime CDN, no external fetches) and
the **Go build stays simple** (`go build ./...` produces the whole server).

A second question is naming. The reference design we are orienting on is a
commercial product suite. Atlas is not a clone of it, so the suite's product
names must be replaced with Atlas's own, domain-neutral names.

## Decision drivers

- **Self-contained.** Every asset the UI needs must be embedded via `go:embed`
  (ADR-0011). Nothing may load from a CDN at runtime.
- **Simple Go build.** `go build ./...` must remain the whole story for the
  server. A front-end toolchain (npm, bundlers) must not become a prerequisite
  for building or testing Atlas in CI.
- **Approachable to change.** Contributors editing a view should not need to
  learn a framework or run a dev server; edit a file, rebuild the binary, reload.
- **Own identity.** The product must read as Atlas, not as the suite it is
  oriented on. No third-party product names in our own UI.

## Considered options

1. **A framework SPA (React/Vue) with a bundler**, its build output embedded.
2. **A buildless vanilla-JS app shell**: plain HTML/CSS/ES modules, a tiny
   hash router, no framework and no bundler for our own code; heavy widgets
   (the BPMN modeler) are vendored as pre-built assets (ADR-0013).
3. **Server-rendered HTML** (Go templates) with minimal JS.

## Decision outcome

Chosen option: **Option 2 — a buildless vanilla-JS app shell.**

- The shell (`api/web/`) is plain `index.html` + `app.css` + ES-module JS. A
  minimal hash router (`#/console`, `#/modeler`, `#/modeler/d/{key}`, …) swaps
  views into a content mount. No framework, no bundler, no npm in the Go build.
- The shell provides the common chrome: a top bar with an **app switcher**
  (grid button → left drawer), the Atlas wordmark, per-app secondary navigation,
  and account/help affordances.
- Heavy, solved widgets are **not** written by hand or pulled from a CDN: the
  BPMN modeler is the vendored `bpmn-js` dist, embedded in the binary
  (ADR-0013). That is the one pre-built artifact; our own UI code stays
  buildless.
- **Atlas app names** (replacing the reference suite's product names):
  - **Console** — management/overview (deployments, engine status).
  - **Modeler** — BPMN modeling and the editor.
  - **Tasks** — human task list *(placeholder until the engine supports user tasks)*.
  - **Operations** — running-instance inspection/operator tooling *(placeholder)*.
  - **Insights** — analytics over the exported event stream *(placeholder)*.
  - Only Console and Modeler are functional now; the rest render an honest
    "coming soon" state so the shell is complete but nothing is faked.

### Consequences

- **Positive:** the binary stays self-contained and `go build ./...` stays the
  whole build. No framework churn; a view is just a file. The app shell gives a
  coherent product surface that Atlas features slot into.
- **Negative / trade-offs accepted:** vanilla JS means more manual DOM code than
  a framework would, and no component ecosystem. As the UI grows this may argue
  for a bundler later — at which point the build artifact would still be
  embedded, so the self-contained property holds; only the "buildless" part
  would be revisited in a new ADR.
- **Follow-ups / risks to watch:** keep DOM-building helpers small and shared so
  views stay readable. If a real front-end build becomes necessary, isolate it to
  a vendored artifact step (as ADR-0013 does for bpmn-js) rather than making npm a
  prerequisite of the Go build.

## Pros and cons of the options

### Option 1 — Framework SPA + bundler
- Good: component model, ecosystem, scales to a large UI.
- Bad: introduces npm + a bundler as a build prerequisite; heavier to embed and
  reason about; overkill for the current surface.

### Option 2 — Buildless vanilla shell (chosen)
- Good: no build step for our code; trivially embedded; low barrier to edit;
  self-contained.
- Bad: manual DOM code; no components; may need revisiting at larger scale.

### Option 3 — Server-rendered Go templates
- Good: no client framework; simple.
- Bad: a modeler/editor is inherently a rich client; round-tripping every
  interaction to the server is the wrong shape for it.

## Links

- builds on ADR-0011 (single-binary distribution with an embedded web UI)
- paired with ADR-0013 (embedding the bpmn-js modeler as a vendored asset)
