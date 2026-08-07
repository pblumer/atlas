// Shared helpers for the token-simulation e2e specs. They drive the *public* simulation
// service (window.__sim) and route "clicks" through the real bpmn-js eventBus, exactly as
// the editor does — the sim's high-priority element.click handler stops propagation, so no
// other listener runs.
import { expect } from "@playwright/test";

export const stats = (page) => page.evaluate(() => window.__sim.stats());

// waitForStats polls the browser until pred() holds, riding out the async dot animations
// without sleeping on wall-clock time. pred runs in the page and reads window.__sim.stats().
export const waitForStats = (page, pred, timeout = 10000) =>
  page.waitForFunction(pred, null, { timeout, polling: 50 });

// open loads a model into the harness and turns the simulation on.
export async function open(page, model, { speed = 4, auto = false } = {}) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`/harness.html?model=${encodeURIComponent(model)}`);
  await page.waitForFunction(() => window.__ready === true || window.__error, null, {
    timeout: 20000,
  });
  const loadError = await page.evaluate(() => window.__error || null);
  expect(loadError, `harness failed to load ${model}`).toBeNull();
  await page.evaluate(
    ({ speed, auto }) => {
      window.__sim.setActive(true);
      window.__sim.setSpeed(speed);
      if (auto) window.__sim.setAuto(true);
    },
    { speed, auto },
  );
  return errors;
}

export const spawn = (page, id) =>
  page.evaluate((id) => window.__sim.spawnAt(window.__reg.get(id)), id);

// call invokes a public simulation method by name (e.g. "play", "step", "setMiCount").
export const call = (page, method, ...args) =>
  page.evaluate(({ method, args }) => window.__sim[method](...args), { method, args });

// click dispatches a real element.click on the eventBus for the element with this id —
// how the user picks a gateway branch or confirms an inclusive selection.
export const click = (page, id) =>
  page.evaluate((id) => {
    const el = window.__reg.get(id);
    window.__modeler.get("eventBus").fire("element.click", { element: el });
  }, id);

// hasHere reports whether a shape currently holds a resting token (the marker the sim adds
// to the element that a token sits on).
export const hasHere = (page, id) =>
  page.evaluate(
    (id) =>
      !!document
        .querySelector(`[data-element-id="${id}"]`)
        ?.classList.contains("atlas-sim-here"),
    id,
  );

// miBadge returns the text of the (single) visible multi-instance badge, e.g. "‖ 2/3".
export const miBadge = (page) =>
  page.evaluate(() => document.querySelector(".atlas-sim-mi")?.textContent ?? null);
