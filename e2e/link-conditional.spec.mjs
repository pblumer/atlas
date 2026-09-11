// End-to-end tests for link and conditional events in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. A link event is BPMN's off-page connector: a goto that stands in for a sequence
// flow the author chose not to draw (ADR-0132), so a token must cross it rather than stop at
// it. A conditional event is the catch triggered by data rather than by a message, a timer or
// a throw (ADR-0137); the simulation evaluates no FEEL, so what it owes is saying so.
import { test, expect } from "@playwright/test";
import { open, spawn, call, stats, waitForStats, hasHere } from "./lib.mjs";

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

// fireOn returns the title of the fire affordance offered on an element, or null.
const fireOn = (page, id) =>
  page.evaluate(
    (id) =>
      [...document.querySelectorAll(".atlas-sim-fire")]
        .filter((e) => e.closest("[data-container-id]")?.getAttribute("data-container-id") === id)
        .map((e) => e.title)[0] ?? null,
    id,
  );

test("a link throw jumps to its catch and the flow carries on there", async ({ page }) => {
  await open(page, "link-events.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "TaskA"); // Start -> TaskA
  await stepTo(page, "Jump"); // TaskA -> Jump
  // A link catch is not an event anyone waits for: it must not park or offer a fire glyph.
  expect(await fireOn(page, "Jump")).toBeNull();
  expect(await fireOn(page, "Land")).toBeNull();

  // The jump crosses to the catch, which then carries on by its own outgoing flow.
  await stepTo(page, "Land");
  expect(await hasHere(page, "Jump")).toBe(false);
  // Nothing has completed — the throw is a goto, not an end.
  expect(await stats(page)).toMatchObject({ live: 1, completed: 0 });
  await stepTo(page, "TaskB");

  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
});

test("a link throw with no matching catch strands its token instead of completing", async ({
  page,
}) => {
  await open(page, "link-dangling.bpmn");
  await spawn(page, "Start");
  await call(page, "play");
  await waitForStats(page, () => window.__sim.stats().stuck === 1, 8000);
  // The token stays on the throw, marked. The far half of the diagram was never reached, so
  // there is nothing to report as completed.
  expect(await stats(page)).toMatchObject({ live: 1, completed: 0, stuck: 1 });
  expect(await hasHere(page, "Jump")).toBe(true);
  expect(await has(page, "Jump", "atlas-sim-stuck")).toBe(true);
  expect(await hasHere(page, "TaskB")).toBe(false);

  // It stays stranded: the run has no way to move a token the diagram gave nowhere to go.
  await call(page, "step");
  await page.waitForTimeout(400);
  expect(await stats(page)).toMatchObject({ live: 1, completed: 0, stuck: 1 });
});

test("a conditional event says it waits on a condition, not on an event arriving", async ({
  page,
}) => {
  await open(page, "conditional-events.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "CondCatch"); // Start -> CondCatch, which parks
  expect(await stats(page)).toMatchObject({ live: 1, waiting: true });
  expect(await fireOn(page, "CondCatch")).toBe("The condition now holds — release the waiting token");

  // Its boundary is named by what it catches, not as a nameless "event boundary event".
  await stepTo(page, "Work"); // the condition holds: CondCatch -> Work
  expect(await fireOn(page, "CondB")).toBe("Fire this non-interrupting conditional boundary event");

  // The non-interrupting boundary runs its branch beside the activity, which keeps its token.
  await page.evaluate(() => window.__sim._fireBoundary(window.__reg.get("CondB")));
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Alert"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "Work")).toBe(true);
  expect(await stats(page)).toMatchObject({ live: 2, terminated: 0 });
});
