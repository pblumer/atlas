// End-to-end tests for gateway behaviour in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. Covers the exclusive choice (and auto-decide), the parallel fork/join, and the
// inclusive subset split with a quiescence OR-join (ADR-0096).
import { test, expect } from "@playwright/test";
import { open, spawn, call, click, stats, waitForStats, hasHere } from "./lib.mjs";

test("exclusive gateway pauses for a choice and routes the token down the picked branch", async ({
  page,
}) => {
  await open(page, "xor.bpmn");
  await spawn(page, "Start");
  await call(page, "step"); // Start -> gateway
  await waitForStats(page, () => window.__sim.stats().deciding === true);

  // Pick branch B; the token must travel to TaskB and rest there, not TaskA.
  await click(page, "fb");
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="TaskB"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "TaskB")).toBe(true);
  expect(await hasHere(page, "TaskA")).toBe(false);
});

test("exclusive gateway: auto-decide runs the model to completion without a click", async ({
  page,
}) => {
  await open(page, "xor.bpmn", { auto: true });
  await spawn(page, "Start");
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 1, live: 0, deciding: false });
});

test("parallel gateway forks both branches and the join merges to one completion", async ({
  page,
}) => {
  await open(page, "parallel.bpmn");
  await spawn(page, "Start");
  await call(page, "step"); // Start -> split
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Gw_s"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );

  // Fork: after departing the split, BOTH branches must hold a token at once.
  await call(page, "step");
  await page.waitForFunction(
    () => {
      const here = (id) =>
        document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-here");
      return here("TaskA") && here("TaskB");
    },
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "TaskA")).toBe(true);
  expect(await hasHere(page, "TaskB")).toBe(true);

  // Join merges the two tokens into one, so the process completes exactly once.
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 1, live: 0 });
});

test("inclusive gateway activates a subset and the OR-join quiesces to one completion", async ({
  page,
}) => {
  await open(page, "inclusive.bpmn");
  await spawn(page, "Start");
  await call(page, "step"); // Start -> split
  await waitForStats(page, () => window.__sim.stats().deciding === true);

  // Arm A and C only (skip B), then confirm by clicking the gateway.
  await click(page, "fa");
  await click(page, "fc");
  await click(page, "Gw_s");

  // The OR-join must fire once no token can still reach it (B never activated). If it wrongly
  // waited for all three incoming branches it would deadlock and this would time out.
  await call(page, "play");
  await waitForStats(
    page,
    () => {
      const s = window.__sim.stats();
      return s.completed === 1 && s.live === 0;
    },
    12000,
  );
  expect(await stats(page)).toMatchObject({ completed: 1, live: 0 });
});
