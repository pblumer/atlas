// End-to-end tests for the Design-view token simulation (api/web/token-simulation.js),
// driven through the *real* vendored bpmn-js in a headless browser. They guard the message
// semantics that are easy to regress and impossible to unit-test without a DOM: a thrown
// message must deliver to a waiting catch across pools (ADR-0101), a message with nothing
// waiting must not be buffered, and a parked catch must still fire manually.
//
// The harness (harness.html) builds a modeler exactly like the editor does and exposes
// `window.__sim` / `window.__reg`; we drive the public service API and assert on stats().
import { test, expect } from "@playwright/test";

const stats = (page) => page.evaluate(() => window.__sim.stats());

// waitForStats polls the simulation until `pred(stats)` holds, so we ride out the async
// dot animations without sleeping on wall-clock time.
const waitForStats = (page, pred, timeout = 10000) =>
  page.waitForFunction(pred, null, { timeout, polling: 50 });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/harness.html");
  await page.waitForFunction(() => window.__ready === true || window.__error, null, {
    timeout: 20000,
  });
  const loadError = await page.evaluate(() => window.__error || null);
  expect(loadError, "harness failed to load").toBeNull();
  expect(errors, "page errors during load").toEqual([]);
  await page.evaluate(() => {
    window.__sim.setActive(true);
    window.__sim.setSpeed(4); // shorten animations; the assertions are on state, not timing
  });
});

test("a thrown message fires a waiting catch across pools", async ({ page }) => {
  // Pool B runs first and parks on its message catch.
  await page.evaluate(() => {
    window.__sim.spawnAt(window.__reg.get("StartB"));
    window.__sim.play();
  });
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.waiting === true && s.live === 1;
  });
  expect(await stats(page)).toMatchObject({ waiting: true, live: 1, completed: 0 });

  // Pool A now throws msgX; delivery must fire B's waiting catch, so both pools finish.
  await page.evaluate(() => window.__sim.spawnAt(window.__reg.get("StartA")));
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 2, live: 0, waiting: false });
});

test("a message with nothing waiting is not buffered, and a parked catch still fires manually", async ({
  page,
}) => {
  // Pool A throws into the void (Pool B hasn't started): the message is only shown, not held.
  await page.evaluate(() => {
    window.__sim.spawnAt(window.__reg.get("StartA"));
    window.__sim.play();
  });
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 1 && s.live === 0 && s.waiting === false;
  });

  // Pool B now arrives at the catch — it must WAIT, not complete from the past throw.
  await page.evaluate(() => {
    window.__sim.spawnAt(window.__reg.get("StartB"));
    window.__sim.play();
  });
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.waiting === true && s.live === 1;
  });
  expect(await stats(page)).toMatchObject({ completed: 1, waiting: true, live: 1 });

  // A manual step fires the parked catch (the ⚡ affordance path), releasing the token.
  await page.evaluate(() => window.__sim.step());
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 2, live: 0 });
});
