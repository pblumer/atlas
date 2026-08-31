// End-to-end coverage for the Playground tab's browser half (api/web/playground.js,
// ADR-draft-modeler-playground).
//
// The tab is a mode, not a level of detail: it takes over the control strip and a side
// panel, and it drives a server-side sandbox over a dozen endpoints. The Go tests cover
// the sandbox and the API; only a real DOM can show that the tab wires the two together
// — that a waiting task becomes a button, that completing it repaints the diagram, and
// that leaving the editor releases the sandbox instead of leaving it to its TTL.
import { test, expect } from "@playwright/test";

// The editor's toolbar runs the width of the window; a narrow viewport puts the tabs and
// the Playground bar off screen.
test.use({ viewport: { width: 1600, height: 900 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/playground-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator('.etabs button[data-tab="playground"]')).toBeVisible();
});

const calls = (page) => page.evaluate(() => window.__calls);

// openTab switches to the Playground and starts its sandbox.
async function startSandbox(page) {
  await page.locator('.etabs button[data-tab="playground"]').click();
  await expect(page.locator("#pg-bar")).toBeVisible();
  await page.locator("#pg-start").click();
  await expect(page.locator("#pg-case")).toBeVisible();
}

test("the tab is a mode: it takes the bar and the panel, and gives back the canvas", async ({ page }) => {
  // Off the tab, nothing of the Playground is on screen.
  await expect(page.locator("#pg-bar")).toBeHidden();
  await expect(page.locator("#pg-panel")).toBeHidden();
  await expect(page.locator("#props")).toBeVisible();

  await page.locator('.etabs button[data-tab="playground"]').click();
  await expect(page.locator("#pg-bar")).toBeVisible();
  await expect(page.locator("#pg-panel")).toBeVisible();
  // Two side panels would leave the diagram — the thing being watched — a sliver.
  await expect(page.locator("#props")).toBeHidden();

  // The panel's ✕ leaves the mode through the tab that owns it, so the tab bar and the
  // panel cannot disagree about which mode is on.
  await page.locator("#pg-panel-close").click();
  await expect(page.locator("#pg-panel")).toBeHidden();
  await expect(page.locator('.etabs button[data-tab="design"]')).toHaveClass(/active/);
  await expect(page.locator("#props")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("starting a sandbox sends the diagram on screen, not a stored copy", async ({ page }) => {
  await startSandbox(page);
  const open = (await calls(page)).find((c) => /\/playground\/sessions$/.test(c.url));
  expect(open.body.source).toBe("xml");
  expect(open.body.xml).toContain('id="credit"');
  // The whole policy is one stub duration, and it is fixed for the sandbox's life.
  expect(open.body.stubs.default.minMillis).toBe(60000);
  await expect(page.locator("#pg-stats")).toContainText("seed 4711");
  await expect(page.locator("#pg-dur-wrap")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a waiting task becomes a button, and completing it repaints the diagram", async ({ page }) => {
  await startSandbox(page);
  await page.locator("#pg-startvars").fill('{"amount": 12400}');
  await page.locator("#pg-case").click();

  // The person at the keyboard is the worker for a user task.
  const task = page.locator(".pg-task").filter({ hasText: "review" });
  await expect(task).toBeVisible();
  await expect(task).toContainText("user task");
  await expect(page.locator("#pg-hint")).toContainText("waiting for you");

  // The case's variables reached the server as JSON, not as text.
  const started = (await calls(page)).find((c) => /\/cases$/.test(c.url) && c.method === "POST");
  expect(started.body.variables).toEqual({ amount: 12400 });

  // Two elements have been reached, so two carry a count; the waiting one is live.
  await expect(page.locator(".token-badge")).toHaveCount(2);
  await expect(page.locator('.djs-element[data-element-id="review"].atlas-active')).toHaveCount(1);

  await page.locator("#pg-outputs").fill('{"decision":"approved"}');
  await task.locator("button").click();

  await expect(page.locator(".pg-result")).toContainText("completed");
  await expect(page.locator(".pg-result")).toContainText("start → review → score → done");
  await expect(page.locator(".pg-vars")).toContainText("approved");
  // The whole path is drawn now, and nothing is live any more.
  await expect(page.locator(".token-badge")).toHaveCount(4);
  await expect(page.locator(".atlas-active")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("bad JSON is reported rather than sent", async ({ page }) => {
  await startSandbox(page);
  await page.locator("#pg-startvars").fill("{not json");
  await page.locator("#pg-case").click();
  // Nothing was posted, and the panel is still usable.
  const posted = (await calls(page)).filter((c) => /\/cases$/.test(c.url) && c.method === "POST");
  expect(posted).toHaveLength(0);
  await expect(page.locator("#pg-case")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("editing the diagram says the run no longer matches it", async ({ page }) => {
  await startSandbox(page);
  await page.evaluate(() => {
    const modeler = window.__atlasModeler;
    const el = modeler.get("elementRegistry").get("review");
    modeler.get("modeling").updateProperties(el, { name: "Antrag doppelt prüfen" });
  });
  await expect(page.locator("#pg-hint")).toContainText("diagram changed");
  expect(page.__errors).toEqual([]);
});

test("leaving the editor releases the sandbox", async ({ page }) => {
  await startSandbox(page);
  await page.evaluate(() => window.__leave());
  await expect
    .poll(async () => (await calls(page)).some((c) => c.method === "DELETE" && /\/playground\/sessions\//.test(c.url)))
    .toBe(true);
  expect(page.__errors).toEqual([]);
});
