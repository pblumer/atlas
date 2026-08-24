// End-to-end tests for multi-instance activities in the token simulation
// (api/web/token-simulation.js): a modelled fixed loop cardinality wins, a data-driven
// activity falls back to the configurable default, and the instance counter ticks down
// before the token leaves (ADR-0097, ADR-0100).
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, miBadge } from "./lib.mjs";

test("a modelled loop cardinality drives the instance count and ticks down", async ({ page }) => {
  await open(page, "multi-instance.bpmn");
  await spawn(page, "StartC");
  await call(page, "step"); // Start -> MICard (cardinality = 3)

  // The badge reads the modelled cardinality: 3 of 3 to run.
  await page.waitForFunction(
    () => /\b3\/3\b/.test(document.querySelector(".atlas-sim-mi")?.textContent || ""),
    null,
    { timeout: 8000 },
  );
  expect(await miBadge(page)).toContain("3/3");

  // One instance completes per move; the token stays on the activity until the last.
  await call(page, "step");
  await page.waitForFunction(
    () => /\b2\/3\b/.test(document.querySelector(".atlas-sim-mi")?.textContent || ""),
    null,
    { timeout: 8000 },
  );
  expect(await miBadge(page)).toContain("2/3");

  // Run the rest out; the activity releases a single token and the pool completes once.
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
});

test("a data-driven multi-instance activity uses the configurable default count", async ({
  page,
}) => {
  await open(page, "multi-instance.bpmn");
  await call(page, "setMiCount", 5); // applies to activities entered from now on
  await spawn(page, "StartD");
  await call(page, "step"); // Start -> MIDefault (no modelled cardinality)

  await page.waitForFunction(
    () => /\b5\/5\b/.test(document.querySelector(".atlas-sim-mi")?.textContent || ""),
    null,
    { timeout: 8000 },
  );
  expect(await miBadge(page)).toContain("5/5");
});

test("a standard loop counts its modelled maximum and releases one token", async ({ page }) => {
  await open(page, "multi-instance.bpmn");
  await spawn(page, "StartL");
  await call(page, "step"); // Start -> LoopMax (standard loop, loopMaximum = 2)

  // The badge reads the modelled maximum with the loop glyph: 2 of 2 runs to go.
  await page.waitForFunction(
    () => /↻\s*2\/2/.test(document.querySelector(".atlas-sim-mi")?.textContent || ""),
    null,
    { timeout: 8000 },
  );

  // One run per move; the token stays on the activity until the last one.
  await call(page, "step");
  await page.waitForFunction(
    () => /↻\s*1\/2/.test(document.querySelector(".atlas-sim-mi")?.textContent || ""),
    null,
    { timeout: 8000 },
  );

  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
});
