// End-to-end tests for the message and signal forms the token simulation did not cover
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. Two things separate a message from a signal, and the simulation has to get both
// right: where the message name is written — a send or receive *task* carries its own
// messageRef and no event definition at all — and how many catches a throw reaches, since a
// signal is a broadcast (ADR-0088) and a message correlates to exactly one.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

// parked counts how many of the given elements still hold a token.
const parked = (page, ids) =>
  page.evaluate(
    (ids) =>
      ids.filter((id) =>
        document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-here"),
      ).length,
    ids,
  );

test("a send task delivers to the receive task waiting for its message", async ({ page }) => {
  await open(page, "message-tasks.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  // The receive branch parks: a receive task waits for its message like any message catch.
  await waitForStats(page, () => window.__sim.stats().waiting === true);
  expect(await hasHere(page, "Receive")).toBe(true);

  // The send task throws that message, which releases it — both branches then finish. A send
  // task that threw nothing left the receive parked for ever.
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 2, live: 0, waiting: false });
});

test("a signal reaches every catch waiting for it, a message exactly one", async ({ page }) => {
  await open(page, "broadcast.bpmn");
  await spawn(page, "Start");
  await call(page, "play");

  // Both branches park on the signal catch, the throwing branch works its way to the throw.
  await page.waitForFunction(
    () =>
      document
        .querySelector('[data-element-id="SigCatchB"]')
        ?.classList.contains("atlas-sim-here") &&
      document.querySelector('[data-element-id="SigCatchC"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );

  // The signal is a broadcast: it releases both, and both move on to their message catch.
  await page.waitForFunction(
    () =>
      document
        .querySelector('[data-element-id="MsgCatchB"]')
        ?.classList.contains("atlas-sim-here") &&
      document.querySelector('[data-element-id="MsgCatchC"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );

  // The message is not: exactly one of the two is released, the other stays waiting. The
  // throwing branch finishes, so two completions and one token still parked.
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 1;
  });
  expect(await parked(page, ["MsgCatchB", "MsgCatchC"])).toBe(1);
  expect(await stats(page)).toMatchObject({ completed: 2, live: 1, waiting: true });
});
