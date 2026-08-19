# Screenshots

The images the [root `README.md`](../../README.md) uses to show what Atlas looks
like. Every one is a **real capture of the running app** — no mockups, no
retouching — so they can be regenerated whenever the UI moves on.

| File | View | Route |
|------|------|-------|
| `modeler.png` | Modeler with a deployed process open and a user task selected | `#/modeler/d/{processDefKey}` |
| `operations-live.png` | Operations live view: every instance of one version on the diagram | `#/operations/p/{processDefKey}` |
| `instance-replay.png` | Instance replay scrubbed to a mid step, Variables tab open | `#/operations/i/{instanceKey}` |
| `tasks.png` | Tasks inbox with a task selected and its form rendered | `#/tasks` |
| `dmn-decision-table.png` | Embedded DMN editor, decision table | `#/modeler/dmn/{dmnRefId}` → *Bearbeiten* |

## Regenerating them

Captured with Playwright + Chromium against a local server, at a 1440-wide
viewport and `deviceScaleFactor: 2` (so the PNGs are 2880 wide and stay crisp on
a HiDPI screen). The [`e2e/`](../../e2e/) harness already carries the Playwright
dependency, so the simplest route is a throwaway script run from that directory.

1. Build and start a server on a scratch data directory:

   ```bash
   go build -o /tmp/atlas ./cmd/atlas
   /tmp/atlas serve --data-dir /tmp/atlas-shots --addr 127.0.0.1:8080
   ```

2. Give it something to show. The models in [`examples/`](../../examples/) are
   there for exactly this — deploy a few and start instances, e.g.:

   ```bash
   API=http://127.0.0.1:8080/api/v1
   curl -s -X POST "$API/deployments" -H 'Content-Type: application/xml' \
        --data-binary @examples/order-to-cash-live.bpmn      # → {"key": …}
   curl -s -X POST "$API/processes/{key}/instances" \
        -H 'Content-Type: application/json' -d '{"variables":{}}'
   ```

   Forms (`POST /api/v1/forms`, `{"id":…,"schema":…}`) and DMN models
   (`POST /api/v1/dmn-models?name=…` plus `POST /api/v1/dmnrefs`) have to be
   uploaded before the process that binds them, or the task shows no form.

3. Drive the browser from `e2e/`, where `@playwright/test` resolves:

   ```js
   const ctx = await browser.newContext({
     viewport: { width: 1440, height: 620 }, deviceScaleFactor: 2,
   });
   ```

   Three details are worth keeping, because they are what separates a usable
   shot from a cluttered one:

   - **Frame the diagram yourself.** `canvas.zoom('fit-viewport')` fits the model
     edge to edge with *no* padding: in the Modeler the floating palette then
     covers the start event, and in the Operations live view the top row of
     shapes runs straight into the toolbar above the canvas. Leave a margin —
     in the Modeler by re-setting the viewbox with a larger left pad (~170px),
     in the live view by zooming out a notch and re-centering.
   - **In the live view, pan and zoom with real wheel events**, not by writing
     the `.viewport` transform. The token counters and "N tasks" badges are HTML
     overlays that diagram-js positions separately, so a hand-written transform
     moves the shapes and leaves the badges behind, scattered across the canvas.
     `mouse.wheel(dx, dy)` pans and `Control` + `mouse.wheel` zooms through the
     real handlers, and the overlays follow. The Modeler exposes its bpmn-js
     instance as `window.__atlasModeler`; the live view exposes none.
   - **Hide the hover-only context pad** (`.djs-context-pad, .djs-popup
     { display: none }`) when an element is selected — the properties panel
     already shows the selection, and the pad only floats over the model.

Measure the frame rather than eyeballing it: read the content box back from
`viewport.getBBox()` and the container's rect, and assert the diagram sits
inside the padding on all four sides. Note that on the live view the bbox
fluctuates while tokens animate, so converge on it in a loop with a tolerance
instead of solving for it once.

Keep the viewport height close to the content: a wide, short BPMN diagram in a
tall window is mostly empty canvas.
