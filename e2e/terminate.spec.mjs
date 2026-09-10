// End-to-end tests for the terminate end event in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. A terminate end is the one end event that is about the tokens it does *not* own:
// it ends the enclosing flow scope, killing every other token in it (ADR-0116). Treating it
// as a plain end — completing only the arriving token — leaves the other branches walking
// on for ever, which is exactly the regression these two tests guard.
import { test, expect } from "@playwright/test";
import { open, spawn, call, click, stats, waitForStats, hasHere } from "./lib.mjs";

const has = (page, id, cls) =>
  page.evaluate(
    ({ id, cls }) => document.querySelector(`[data-element-id="${id}"]`)?.classList.contains(cls),
    { id, cls },
  );

// stepTo advances one visible move and waits for the token to land on `id`. A step only
// departs a *resting* token, so the next one must not be issued while a dot is still flying.
const stepTo = async (page, id) => {
  await call(page, "step");
  await page.waitForFunction(
    (id) =>
      document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-here"),
    id,
    { timeout: 8000 },
  );
};

test("a terminate end at the root kills every other token, subprocess included", async ({
  page,
}) => {
  await open(page, "terminate.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "Gw_s"); // Start -> Gw_s
  await call(page, "step"); // Gw_s forks onto all three branches

  // All three branches are live: TaskA holds a token, CatchB is parked on its timer, and the
  // subprocess is running with a token of its own on the inner start.
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="TaskA"]')?.classList.contains("atlas-sim-here") &&
      document.querySelector('[data-element-id="CatchB"]')?.classList.contains("atlas-sim-here") &&
      document
        .querySelector('[data-element-id="InnerStart"]')
        ?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(true);
  // TaskA + CatchB + the subprocess's held token + the token inside it.
  expect(await stats(page)).toMatchObject({ live: 4, completed: 0 });

  // Hand-advance TaskA into the terminate end: the instance ends there and then.
  await click(page, "TaskA");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
  // One completion — the token that reached the terminate end. The other three were killed.
  expect(await stats(page)).toMatchObject({ live: 0, completed: 1, terminated: 3, waiting: false });
  expect(await hasHere(page, "CatchB")).toBe(false);
  expect(await hasHere(page, "InnerStart")).toBe(false);
  expect(await hasHere(page, "Sub")).toBe(false);
  // The subprocess must not keep its "running" highlight after the process is gone.
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(false);
});

test("a terminate end inside a subprocess ends that scope only, and the parent continues", async ({
  page,
}) => {
  await open(page, "terminate-sub.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "InnerStart"); // Start -> Sub: enters the scope, spawns the inner start
  await stepTo(page, "InnerGw"); // InnerStart -> InnerGw
  await call(page, "step"); // InnerGw forks onto both inner branches

  await page.waitForFunction(
    () =>
      document
        .querySelector('[data-element-id="InnerTaskA"]')
        ?.classList.contains("atlas-sim-here") &&
      document
        .querySelector('[data-element-id="InnerCatch"]')
        ?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  // The subprocess's held token plus the two inner ones.
  expect(await stats(page)).toMatchObject({ live: 3, completed: 0 });

  // The inner terminate ends the subprocess, not the instance: the parked inner branch dies,
  // and the outer token leaves the subprocess and runs on to End.
  await click(page, "InnerTaskA");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
  expect(await stats(page)).toMatchObject({ live: 0, completed: 1, terminated: 1, waiting: false });
  expect(await hasHere(page, "InnerCatch")).toBe(false);
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(false);
});
