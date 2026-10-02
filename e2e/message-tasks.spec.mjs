// End-to-end tests for message delivery between pools when the message rides on a *task*
// rather than an event definition (api/web/token-simulation.js). A message-kind send task
// names its message by messageRef or through an operationRef (ADR-0112), and a receive task
// waits on its own messageRef (ADR-0102) — neither carries a MessageEventDefinition, so a
// simulation that only reads event definitions lets the message vanish and leaves the
// receiver waiting for a manual fire. They also guard a message reaching a catch behind an
// event-based gateway: the waiting token sits on the gateway, and the message decides the
// race (ADR-0110), as ADR-0101 has a delivered message fire a waiting catch.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

test("a send task's message fires a waiting receive task across pools", async ({ page }) => {
  await open(page, "message-tasks.bpmn");
  // Pool B runs first and parks on its receive task.
  await spawn(page, "StartB");
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.waiting === true && s.live === 1;
  });
  expect(await hasHere(page, "RecvB")).toBe(true);

  // Pool A's send task sends "order"; delivery must release B, so both pools finish.
  await spawn(page, "StartA");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.completed === 2 && s.live === 0;
  });
  expect(await stats(page)).toMatchObject({ completed: 2, live: 0, waiting: false });
});

test("a message sent through an operation decides an event-based race for that message", async ({
  page,
}) => {
  await open(page, "message-tasks.bpmn");
  // Pool C reaches its event-based gateway and waits there: message or timer, first wins.
  await spawn(page, "StartC");
  await call(page, "step");
  await waitForStats(page, () => window.__sim.stats().deciding === true);

  // Pool D's send task names "confirm" through its operation. Step it there and send.
  await spawn(page, "StartD");
  await call(page, "step"); // StartD -> SendD (the gateway's choice is left for a click)
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="SendD"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  await call(page, "step"); // SendD sends "confirm"

  // The message wins the race: C's token takes the message branch, not the timer's.
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="CatchCMsg"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "CatchCMsg")).toBe(true);
  expect(await hasHere(page, "CatchCTimer")).toBe(false);
  expect(await hasHere(page, "GwC")).toBe(false);
  expect((await stats(page)).deciding).toBe(false);
});
