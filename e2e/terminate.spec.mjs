// End-to-end tests for the terminate end event in the token simulation
// (api/web/token-simulation.js). A terminate ends its whole enclosing scope (ADR-0116): every
// other token in it — a catch still waiting for its message included — is torn down, so a
// subprocess completes and its outer token continues instead of the walk-through stalling.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

const has = (page, id, cls) =>
  page.evaluate(
    ({ id, cls }) => document.querySelector(`[data-element-id="${id}"]`)?.classList.contains(cls),
    { id, cls },
  );

test("a terminate inside a subprocess ends the waiting catch beside it and completes the subprocess", async ({
  page,
}) => {
  const errors = await open(page, "terminate-sub.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  // The outer token reaches End: Term ended Sub, taking Catch's waiting token with it.
  await waitForStats(page, () => window.__sim.stats().completed === 1);
  expect(await hasHere(page, "Catch")).toBe(false);
  expect(await hasHere(page, "Sub")).toBe(false);
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(false);

  // The terminate ends its own scope only: the branch outside Sub still waits for its message.
  expect(await hasHere(page, "OuterCatch")).toBe(true);
  expect(await stats(page)).toMatchObject({ completed: 1, live: 1, waiting: true });
  expect(errors).toEqual([]);
});

test("a terminate at the process level ends every other token", async ({ page }) => {
  const errors = await open(page, "terminate.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0;
  });
  expect(await hasHere(page, "Catch")).toBe(false);
  expect(await stats(page)).toMatchObject({ completed: 1, live: 0, waiting: false });
  expect(errors).toEqual([]);
});
