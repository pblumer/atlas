// End-to-end tests for timer events in the token simulation (api/web/token-simulation.js),
// driven through the real vendored bpmn-js in a headless browser. The simulation does not
// honour the clock — a person fires a timer, or auto-decide does (ADR-0096) — so what a timer
// owes the person reading it is which timer it is: a five-minute wait, a thirty-day deadline
// and an hourly reminder are otherwise the same hourglass. The behaviour under test is that
// an interrupting deadline cancels its activity while a repeating reminder does not.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, hasHere } from "./lib.mjs";

// affordance returns the glyph and title of the fire affordance on an element, or null.
const affordance = (page, id) =>
  page.evaluate(
    (id) =>
      [...document.querySelectorAll(".atlas-sim-fire")]
        .filter((e) => e.closest("[data-container-id]")?.getAttribute("data-container-id") === id)
        .map((e) => ({ glyph: e.innerHTML, title: e.title }))[0] ?? null,
    id,
  );

const stepTo = async (page, id) => {
  await call(page, "step");
  await page.waitForFunction(
    (id) =>
      document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-here"),
    id,
    { timeout: 8000 },
  );
};

test("a timer says which timer it is, on the catch and on both boundaries", async ({ page }) => {
  await open(page, "timer-events.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "Wait");

  // The parked catch names the wait the author modelled, not just "an event".
  expect(await affordance(page, "Wait")).toMatchObject({
    glyph: "⏳",
    title: "Fire this timer event (after PT5M) — release the waiting token",
  });

  await stepTo(page, "Work");
  // Both boundaries carry the hourglass rather than one shared spark, and each names its own
  // time: the deadline waits thirty days, the reminder repeats hourly.
  expect(await affordance(page, "Deadline")).toMatchObject({
    glyph: "⏳",
    title: "Fire this interrupting timer boundary event (after P30D)",
  });
  expect(await affordance(page, "Reminder")).toMatchObject({
    glyph: "⏳",
    title: "Fire this non-interrupting timer boundary event (every R/PT1H)",
  });
});

test("a repeating reminder leaves its activity running, a deadline cancels it", async ({
  page,
}) => {
  await open(page, "timer-events.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "Wait");
  await stepTo(page, "Work");

  // A non-interrupting timer fires as often as it comes round, each time beside the activity.
  await page.evaluate(() => window.__sim._fireBoundary(window.__reg.get("Reminder")));
  await page.evaluate(() => window.__sim._fireBoundary(window.__reg.get("Reminder")));
  await page.waitForFunction(
    () => window.__sim.stats().live === 3,
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "Work")).toBe(true);
  expect(await stats(page)).toMatchObject({ live: 3, terminated: 0 });

  // The deadline interrupts: the activity's token leaves by the boundary instead.
  await page.evaluate(() => window.__sim._fireBoundary(window.__reg.get("Deadline")));
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Escalate"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "Work")).toBe(false);
});
