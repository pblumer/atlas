// End-to-end tests for error and escalation events in the token simulation
// (api/web/token-simulation.js), driven through the real vendored bpmn-js in a headless
// browser. Both are *faults*: they do not complete a path, they hand it to the nearest
// enclosing handler. An error catch always interrupts and an uncaught error parks the
// instance on an incident (ADR-0089); an escalation catch may be non-interrupting and an
// uncaught escalation is benign (ADR-0125). Walking either as a plain end event completes
// the token and raises nothing, which is what these tests guard.
import { test, expect } from "@playwright/test";
import { open, spawn, call, click, stats, waitForStats, hasHere } from "./lib.mjs";

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

// runIntoSubprocessFork walks the shared subprocess model to the point where both inner
// branches are live: one token on the task ahead of the fault end, one parked on the timer.
const runIntoSubprocessFork = async (page) => {
  await spawn(page, "Start");
  await stepTo(page, "InnerStart"); // Start -> Sub: enters the scope
  await stepTo(page, "InnerGw"); // InnerStart -> InnerGw
  await call(page, "step"); // InnerGw forks onto both inner branches
  await page.waitForFunction(
    () =>
      document
        .querySelector('[data-element-id="InnerTaskA"]')
        ?.classList.contains("atlas-sim-here") &&
      document
        .querySelector('[data-element-id="InnerCatch"]')
        ?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
};

test("an error end throws to the boundary on its subprocess, which interrupts it", async ({
  page,
}) => {
  await open(page, "error-end.bpmn");
  await runIntoSubprocessFork(page);
  // The subprocess's held token plus the two inner ones.
  expect(await stats(page)).toMatchObject({ live: 3, completed: 0 });

  // Hand-advance into the error end. It must not complete: the error travels to the boundary,
  // the boundary interrupts the subprocess, and the flow leaves on the recovery path.
  await click(page, "InnerTaskA");
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Recover"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  // Only the recovery token is left: the parked inner branch died with the subprocess.
  expect(await stats(page)).toMatchObject({ live: 1, completed: 0, terminated: 1 });
  expect(await hasHere(page, "InnerCatch")).toBe(false);
  expect(await hasHere(page, "Sub")).toBe(false);
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(false);

  // The recovery path runs to its own end — the process ends there, not at EndOk.
  await call(page, "play");
  await waitForStats(page, () => {
    const s = window.__sim.stats();
    return s.live === 0 && s.completed === 1;
  });
  expect(await hasHere(page, "EndOk")).toBe(false);
});

test("a non-interrupting escalation end runs its handler alongside the subprocess", async ({
  page,
}) => {
  await open(page, "escalation-end.bpmn");
  await runIntoSubprocessFork(page);
  expect(await stats(page)).toMatchObject({ live: 3, completed: 0 });

  // The escalation end raises and ends its own path. The non-interrupting boundary starts the
  // handler branch *beside* the subprocess, which keeps running with its parked branch.
  await click(page, "InnerTaskA");
  await page.waitForFunction(
    () => document.querySelector('[data-element-id="Notify"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );
  expect(await hasHere(page, "InnerCatch")).toBe(true);
  expect(await has(page, "Sub", "atlas-sim-scope")).toBe(true);
  // Sub's held token + the still-parked inner branch + the handler token. Nothing was killed.
  expect(await stats(page)).toMatchObject({ live: 3, completed: 0, terminated: 0 });
});

test("an error nobody catches parks the token instead of completing the process", async ({
  page,
}) => {
  await open(page, "error-uncaught.bpmn");
  await spawn(page, "Start");
  await stepTo(page, "Gw_s");
  await call(page, "step"); // fork onto both branches
  await page.waitForFunction(
    () =>
      document.querySelector('[data-element-id="TaskA"]')?.classList.contains("atlas-sim-here") &&
      document.querySelector('[data-element-id="CatchB"]')?.classList.contains("atlas-sim-here"),
    null,
    { timeout: 8000 },
  );

  await click(page, "TaskA");
  await waitForStats(page, () => window.__sim.stats().incidents === 1, 8000);
  // The token sits on the error end, marked, and nothing completed. The other branch is
  // untouched: an uncaught error tears nothing down.
  expect(await stats(page)).toMatchObject({ live: 2, completed: 0, incidents: 1 });
  expect(await hasHere(page, "ErrEnd")).toBe(true);
  expect(await has(page, "ErrEnd", "atlas-sim-incident")).toBe(true);
  expect(await hasHere(page, "CatchB")).toBe(true);

  // The parked token is not one the run may move. Auto-decide fires the other branch's timer
  // and that branch completes, but the instance never drains: the incident is still standing.
  await call(page, "setAuto", true);
  await call(page, "play");
  await waitForStats(page, () => window.__sim.stats().completed === 1);
  await page.waitForTimeout(400);
  expect(await stats(page)).toMatchObject({ live: 1, completed: 1, incidents: 1 });
  expect(await hasHere(page, "ErrEnd")).toBe(true);
});
