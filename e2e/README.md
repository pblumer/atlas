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

- **`token-simulation.spec.mjs`** — message semantics across two pools (ADR-0101): a thrown
  message **delivers to a waiting catch** (both pools complete), a message with **nothing
  waiting is not buffered** (the later catch still parks), and a parked catch **still fires
  manually** (the ⚡ / `step()` path).
- **`gateways.spec.mjs`** (ADR-0096): the **exclusive** gateway pauses for a choice and routes
  down the picked branch (and **auto-decide** runs it hands-free); the **parallel** gateway
  forks both branches and the join merges to one completion; the **inclusive** gateway
  activates a chosen subset and the **quiescence OR-join** still converges to one completion.
- **`multi-instance.spec.mjs`** (ADR-0097 / ADR-0100): a modelled **loop cardinality** drives
  the instance count and ticks down; a **data-driven** activity falls back to the
  toolbar-configurable default.
- **`backup.spec.mjs`** (ADR-0107): the **Console → Backup** view — the nav entry opens it,
  the `.tar.gz` download link is present, and the restore flow validates an empty selection,
  then reports the file count and restart note on a (mocked) successful upload. Drives the
  real app shell against a mocked `/api/v1` (backup/restore hit the Go API, which the static
  harness doesn't run).

Each spec loads its own model via `harness.html?model=…`; the `.bpmn` fixtures live here.

## CI

The `e2e` job in `.github/workflows/ci.yml` runs this suite on every push and PR, in
parallel with the Go `check` job.
