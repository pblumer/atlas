// End-to-end coverage for cancelling an instance from the Operations instance-replay
// view (api/web/editor.js mountInstanceReplay).
//
// The replay is where an operator reconstructs why an instance has to be stopped — the
// incident, the step it parked on, the variables it carries. The act itself
// (DELETE /api/v1/instances/{key}) lived only on the live view, so reading the case and
// acting on it were two screens. These tests are what says the button is here, that it
// is offered only while there is something to cancel, that it does nothing until the
// operator confirms, and that a refusal from the server leaves the instance alone.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/replay-cancel-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("a running instance offers Cancel instance, a finished one does not", async ({ page }) => {
  await page.evaluate(() => window.__mount("active"));
  await page.waitForSelector("#history-list .ops-hrow");
  await expect(page.locator("#rp-cancel")).toBeVisible();

  // Nothing to cancel on an instance that is already over, and offering it would be an
  // invitation to a call the engine answers with 404.
  await page.evaluate(() => window.__mount("completed"));
  await page.waitForSelector("#history-list .ops-hrow");
  await expect(page.locator("#rp-cancel")).toBeHidden();

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("declining the confirmation leaves the instance running", async ({ page }) => {
  await page.evaluate(() => window.__mount("active"));
  await page.waitForSelector("#history-list .ops-hrow");

  // No dialog handler: Playwright dismisses it, which is the operator saying no.
  await page.locator("#rp-cancel").click();

  expect(await page.evaluate(() => window.__calls.filter((c) => c.startsWith("DELETE")))).toEqual([]);
  await expect(page.locator("#rp-cancel")).toBeEnabled();
  await expect(page.locator("#rp-state")).toHaveText("active");

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("confirming terminates the instance and the replay re-reads its state", async ({ page }) => {
  await page.evaluate(() => window.__mount("active"));
  await page.waitForSelector("#history-list .ops-hrow");

  page.on("dialog", (d) => d.accept());
  await page.locator("#rp-cancel").click();

  // Exactly the call the live view makes, against this instance and once.
  const key = await page.evaluate(() => window.__INSTANCE);
  await expect
    .poll(() => page.evaluate(() => window.__calls.filter((c) => c.startsWith("DELETE"))))
    .toEqual([`DELETE /api/v1/instances/${key}`]);

  // The termination is the end of this instance's history, so the view is re-read rather
  // than left saying "active" beside a diagram that no longer has a token on it.
  await expect(page.locator("#rp-state")).toHaveText("terminated");
  await expect(page.locator("#rp-cancel")).toBeHidden();
  await expect(page.locator("#rp-migrate")).toBeHidden();
  expect(await page.evaluate(() => window.__toasts.some((t) => t.startsWith("ok:")))).toBe(true);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("a refused cancel is reported and the instance is left as it was", async ({ page }) => {
  await page.evaluate(() => { window.__failCancel = true; return window.__mount("active"); });
  await page.waitForSelector("#history-list .ops-hrow");

  page.on("dialog", (d) => d.accept());
  await page.locator("#rp-cancel").click();

  await expect
    .poll(() => page.evaluate(() => window.__toasts.filter((t) => t.startsWith("err:"))))
    .not.toEqual([]);
  // Still running, and the button is usable again — a failed attempt must not leave the
  // one control that could stop the instance disabled.
  await expect(page.locator("#rp-state")).toHaveText("active");
  await expect(page.locator("#rp-cancel")).toBeVisible();
  await expect(page.locator("#rp-cancel")).toBeEnabled();

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("an instance that finishes while it is being replayed takes both actions with it", async ({ page }) => {
  await page.evaluate(() => window.__mount("active"));
  await page.waitForSelector("#history-list .ops-hrow");
  await expect(page.locator("#rp-cancel")).toBeVisible();

  // The replay polls a running instance, so the state it shows can change under the
  // operator — and with it what they may still do to the instance.
  await page.evaluate(() => window.__setState("completed"));
  await expect(page.locator("#rp-cancel")).toBeHidden({ timeout: 10000 });
  await expect(page.locator("#rp-migrate")).toBeHidden();

  expect(page.__errors, "page errors during replay").toEqual([]);
});
