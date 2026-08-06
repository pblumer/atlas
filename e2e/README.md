# Browser end-to-end tests

These tests drive the web UI in a real headless browser (Playwright + Chromium). They
exist because parts of the UI — notably the Design-view **token simulation**
(`api/web/token-simulation.js`) — are plain browser JS running on the vendored bpmn-js, and
their behaviour can only be verified with a DOM and real event/animation timing. The Go
suite can't reach them.

The harness loads the repo's **real** production assets: `server.mjs` serves `../api/web` at
the root, so `harness.html` pulls in the same `bpmn-modeler.js` bundle and
`token-simulation.js` the app ships. Only `harness.html` and `model.bpmn` are test-only —
they live here rather than in `api/web` on purpose, because `api/web` is `go:embed`'d into
the binary and must not carry test fixtures.

## Run locally

```bash
cd e2e
npm ci                                   # first time
npx playwright install chromium          # first time (downloads the browser)
npm test
```

`npm test` starts the static server itself (via the Playwright `webServer` config) and tears
it down afterwards. Use `npx playwright test --headed` to watch it, or
`npx playwright show-trace test-results/**/trace.zip` after a failure.

## What's covered

`token-simulation.spec.mjs` — message semantics across two pools (ADR-0101):

- a thrown message **delivers to a waiting catch**, so both pools complete;
- a message with **nothing waiting is not buffered** (the later catch still parks);
- a parked catch **still fires manually** (the ⚡ / `step()` path).

## CI

The `e2e` job in `.github/workflows/ci.yml` runs this suite on every push and PR, in
parallel with the Go `check` job.
