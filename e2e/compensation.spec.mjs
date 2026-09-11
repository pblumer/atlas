// End-to-end tests for compensation and transactions in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. Compensation is the one part of BPMN that runs *backwards*: a compensation throw
// undoes what already completed, newest first (ADR-0103), and a cancel end rolls its whole
// transaction back and leaves by the cancel boundary rather than the normal exit (ADR-0108).
// Walking either as a plain element completes the path and undoes nothing.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

const has = (page, id, cls) =>
  page.evaluate(
    ({ id, cls }) => document.querySelector(`[data-element-id="${id}"]`)?.classList.contains(cls),
    { id, cls },
  );

// fireIds lists the elements currently offering a "fire this event" affordance.
const fireIds = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll(".atlas-sim-fire")].map(
      (e) => e.closest("[data-container-id]")?.getAttribute("data-container-id") ?? null,
    ),
  );

test("a compensation throw runs the handler of the activity that completed", async ({ page }) => {
  await open(page, "compensation.bpmn");
  await spawn(page, "Start");
  await call(page, "step"); // Start -> Book
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Book"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  // A compensation boundary is inert. Offering it as a fire affordance would let a click kill
  // the host's token and take a sequence flow that does not exist.
  expect(await fireIds(page)).not.toContain("CompB");

  await call(page, "step"); // Book completes: from here it can be compensated
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Comp"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await has(page, "Book", "atlas-sim-compensable")).toBe(true);

  // The throw runs the handler and carries on: UndoBook gets a token of its own.
  await call(page, "step");
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="UndoBook"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  // Book is no longer compensable — an activity is undone once.
  expect(await has(page, "Book", "atlas-sim-compensable")).toBe(false);

  // The handler retires rather than completing the process: the one completion is the token
  // that went on from the throw to End.
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
  expect(await stats(page)).toMatchObject({ live: 0, completed: 1 });
});

test("a cancel end rolls its transaction back and leaves by the cancel boundary", async ({
  page,
}) => {
  await open(page, "transaction.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  // The rollback compensates the work the transaction did: UndoCharge runs inside it while the
  // transaction is marked cancelling.
  await page.waitForFunction(
    () =>
      document
        .querySelector('[data-element-id="UndoCharge"]')
        ?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await has(page, "Tx", "atlas-sim-cancelling")).toBe(true);

  // Once the handler drains the transaction leaves by its cancel boundary, not by EndOk.
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="Refunded"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await has(page, "Tx", "atlas-sim-scope")).toBe(false);
  expect(await hasHere(page, "EndOk")).toBe(false);

  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
  // One completion, on the recovery path. The success path was never taken.
  expect(await stats(page)).toMatchObject({ live: 0, completed: 1 });
});
