// End-to-end tests for message flows in the token simulation (api/web/token-simulation.js,
// ADR-draft-token-simulation-follows-message-flows). A descriptive collaboration often sends
// from a plain task and names no message on either end: the drawn message flow is the only
// statement of who talks to whom, and the simulation must deliver along it — start the
// receiving pool, release a waiting catch, reach a black-box pool — or the reader has to
// start every other pool by hand. Where both ends name a message, the names still decide,
// as they do for the engine (ADR-0023): a flow between two different messages must not
// deliver.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

const pinged = (id) =>
  document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-ping");

test("a plain task's message flows start the other pool and answer the catch waiting for it", async ({
  page,
}) => {
  await open(page, "message-flows.bpmn");
  await spawn(page, "StartA");
  await call(page, "play");

  // TaskA's flows go out when its token leaves: the black-box pool is reached (pinged)...
  await page.waitForFunction(pinged, "Part_Ext", { timeout: 10000, polling: 50 });

  // ...Pool B starts from the flow into its message start, and TaskB's flow back releases
  // Pool A's catch, which names no message. Both pools finish with no manual fire.
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 2, live: 0, waiting: false });
});

test("a message flow between two ends that name different messages does not deliver", async ({
  page,
}) => {
  await open(page, "message-flows.bpmn");
  // Pool B's second path parks on a catch for "y".
  await spawn(page, "StartY");
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.waiting === true && s.live === 1;
  });

  // ThrowX sends "x" along a flow drawn to that catch: the dot arrives (a ping) and the
  // catch keeps waiting, because the engine would never correlate "x" with "y".
  await spawn(page, "StartX");
  await page.waitForFunction(pinged, "CatchY", { timeout: 10000, polling: 50 });
  await waitForStats(page, () => window.__sim.stats().completed === 1);
  expect(await hasHere(page, "CatchY")).toBe(true);
  expect(await stats(page)).toMatchObject({ completed: 1, live: 1, waiting: true });
});
