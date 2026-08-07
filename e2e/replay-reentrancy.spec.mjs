// End-to-end regression test for the Operations instance-replay view
// (api/web/editor.js `mountInstanceReplay`), driven through the *real* vendored
// bpmn-js in a headless browser.
//
// The router (app.js `route()`) is re-entrant: `hashchange` can fire it again while
// a previous mount is still awaiting bpmn assets or the instance timeline. Each
// mount queries `#canvas` and sets the shared `current` viewer only *after* those
// awaits, so a superseded mount used to resume late, build a SECOND bpmn-js canvas
// inside the newer mount's `#canvas`, and render its own instance's history/details
// over the live view — two overlaid diagrams and a Details panel whose instance key
// no longer matched the header (the reported "replay is broken" symptom).
//
// mountInstanceReplay now captures a generation token after cleanup() and bails
// after each await once a newer navigation supersedes it. This test forces the
// interleaving (an early mount with a stalled timeline, a later mount that renders
// immediately) and asserts the later instance wins cleanly: one diagram, one
// consistent instance key.
import { test, expect } from "@playwright/test";

const CLIO = "281474976723211"; // the stale mount (slow timeline)
const CSV = "281474978839056"; // the winning mount (renders immediately)

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("a superseded replay mount does not overlay a second diagram", async ({ page }) => {
  // Start the CLIO replay with a stalled timeline, then immediately start the CSV
  // replay with an instant one. CSV renders first; CLIO resumes afterwards and must
  // bail instead of drawing a second diagram into CSV's canvas.
  await page.evaluate(
    ([clio, csv]) => {
      const a = window.__mountReplay(clio, 600); // superseded; timeline stalls 600ms
      const b = window.__mountReplay(csv, 0); // wins; renders right away
      return Promise.allSettled([a, b]);
    },
    [CLIO, CSV],
  );

  // Exactly one diagram is rendered — the bpmn.io watermark is emitted once per
  // rendered canvas, so two would mean the stale mount overlaid a second one.
  await expect(page.locator("#view .bjs-powered-by")).toHaveCount(1);

  // The header and the Details panel agree on the winning instance; the stale
  // instance's key appears nowhere.
  await expect(page.locator("#m-key")).toHaveText(CSV);
  const details = (await page.locator("#tab-details").textContent()) || "";
  expect(details).toContain(CSV);
  expect(details).not.toContain(CLIO);
  // The Details panel reflects the winning instance's own history, not an empty or
  // stale one ("Elements executed" counts that instance's steps).
  expect(details).toContain("Elements executed");
  expect(details).toContain("3");

  expect(page.__errors, "page errors during replay").toEqual([]);
});
