// End-to-end tests for embedded-subprocess behaviour in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. An expanded subprocess is *entered* — a token runs its inner flow — and the outer
// token continues only once the subprocess completes (ADR-0078, extended for scopes).
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

const has = (page, id, cls) =>
  page.evaluate(
    ({ id, cls }) => document.querySelector(`[data-element-id="${id}"]`)?.classList.contains(cls),
    { id, cls },
  );

test("an embedded subprocess is entered and run before the outer token continues", async ({
  page,
}) => {
  await open(page, "subprocess.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  // The token descends into the subprocess: the inner task holds a token, the subprocess
  // shows the running-scope marker, and the outer End has not been reached yet.
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="InnerTask"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "End")).toBe(false);
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(true);

  // Once the inner flow reaches InnerEnd the subprocess completes and the outer token runs on
  // to End: exactly one completion — the inner end does not count as its own.
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 1, live: 0 });
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(false);
});

test("stepping into a subprocess drops a token on its inner start, not on the far side", async ({
  page,
}) => {
  await open(page, "subprocess.bpmn");
  await spawn(page, "Start");
  await call(page, "step"); // Start -> Sub: enters the scope and spawns the inner start

  // The token is now inside the subprocess (on its inner start), the subprocess holds the
  // scope's held token, and the outer End is untouched — the token did not walk over Sub.
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="InnerStart"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "Sub")).toBe(true);
  expect(await hasHere(page, "End")).toBe(false);
});
