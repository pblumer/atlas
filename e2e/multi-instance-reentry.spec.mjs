// End-to-end tests for the lifetime of a multi-instance count in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. The countdown badge belongs to the token standing on the activity, not to the
// activity: cancel the activity part-way through and the runs it had left go with it, so the
// next token to arrive runs the full multiplicity the model asks for (ADR-0097 / ADR-0100).
import { test, expect } from "@playwright/test";
import { open, spawn, call, miBadge, hasHere } from "./lib.mjs";

const stepTo = async (page, id) => {
  await call(page, "step");
  await page.waitForFunction(
    (id) =>
      document.querySelector(`[data-element-id="${id}"]`)?.classList.contains("atlas-sim-here"),
    id,
    { timeout: 8000 },
  );
};

test("a cancelled multi-instance activity takes its remaining runs with it", async ({ page }) => {
  await open(page, "multi-instance-reentry.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "Gw");
  await call(page, "step"); // the gateway forks: one branch to Batch, one to the timer
  await stepTo(page, "Batch");
  expect(await miBadge(page)).toBe("‖ 3/3");

  // One run of three is done.
  await call(page, "step");
  await page.waitForFunction(
    () => document.querySelector(".atlas-sim-mi")?.textContent === "‖ 2/3",
    null,
    { timeout: 8000 },
  );

  // The interrupting boundary cancels the activity. No token stands there any more, so no
  // countdown may be drawn there either — an activity that is not running must not look like
  // one that is.
  await page.evaluate(() => window.__sim._fireBoundary(window.__reg.get("Abort")));
  await page.waitForFunction(() => !document.querySelector(".atlas-sim-mi"), null, {
    timeout: 8000,
  });
  expect(await hasHere(page, "Batch")).toBe(false);

  // The held branch now enters the same activity. It is a fresh visit and runs the full three
  // the model asks for, not the two the cancelled visit had left.
  await page.evaluate(() => window.__sim._fireTrigger(window.__reg.get("Hold")));
  await stepTo(page, "Batch");
  expect(await miBadge(page)).toBe("‖ 3/3");
});
