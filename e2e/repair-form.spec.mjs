// End-to-end coverage for the repair form on an incident (api/web/incidents.js
// repairFormFlow, ADR-0169).
//
// The point of the record is that the person repairing a stuck process at an awkward
// hour should not have to know which of forty variables matters. These tests are what
// says the modeler's answer actually reaches them: the bound form renders with the
// instance's current values in it, and submitting writes *only* what the form binds —
// which is what keeps "who changed this" true in the audit trail.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/repair-form-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("the bound form opens prefilled from the instance's variables", async ({ page }) => {
  await page.locator("#run").click();
  const modal = page.locator(".repair-modal");
  await expect(modal).toBeVisible();

  // It leads with why the token is parked — the operator's own starting point.
  await expect(modal).toContainText("550 5.1.1 recipient rejected");
  // And says which form it is showing, so a stale binding is tellable from a wrong one.
  await expect(modal.locator(".repair-which")).toContainText("Fix the recipient");

  // The fields the modeler named, holding what the instance currently holds.
  const recipient = modal.locator('.repair-form input').first();
  await expect(recipient).toHaveValue("wrong@example.invalid");
  await expect(modal.locator(".repair-form")).toContainText("Recipient");
  await expect(modal.locator(".repair-form")).toContainText("Subject");

  // Nothing has been written by opening it.
  const writes = await page.evaluate(() => window.__calls.filter((c) => c.method === "POST"));
  expect(writes).toEqual([]);

  expect(page.__errors, "page errors").toEqual([]);
});

test("submitting writes only the keys the form binds", async ({ page }) => {
  await page.locator("#run").click();
  const modal = page.locator(".repair-modal");
  await expect(modal).toBeVisible();

  await modal.locator(".repair-form input").first().fill("right@example.com");
  await modal.locator("[data-repair-save]").click();
  await expect(modal).toHaveCount(0);

  const post = await page.evaluate(() =>
    window.__calls.find((c) => c.method === "POST" && c.url.endsWith("/variables")));
  expect(post).toBeTruthy();
  // The corrected value, through the same audited override the JSON editor uses.
  expect(post.url).toBe("/api/v1/instances/5001/variables");
  expect(post.body.variables.recipient).toBe("right@example.com");
  expect(post.body.variables.subject).toBe("Your order");
  // And nothing else. The override sets exactly the keys it is given, so sending back
  // everything the instance holds would rewrite untouched variables under the operator's
  // name and put values they never saw into the audit trail.
  expect(Object.keys(post.body.variables).sort()).toEqual(["recipient", "subject"]);

  // "Save only" leaves the incident open, and says so.
  expect(await page.evaluate(() => window.__result)).toBe("saved");
  const t = await page.evaluate(() => window.__toast);
  expect(t.msg).toContain("still open");

  expect(page.__errors, "page errors").toEqual([]);
});

test("Save & retry writes and then resolves, in one step", async ({ page }) => {
  await page.locator("#run").click();
  const modal = page.locator(".repair-modal");
  await modal.locator(".repair-form input").first().fill("right@example.com");
  await modal.locator("[data-repair-go]").click();
  await expect(modal).toHaveCount(0);

  const urls = await page.evaluate(() => window.__calls.filter((c) => c.method === "POST").map((c) => c.url));
  // The write first, then the resolve — the order matters: resolving first would retry
  // the job against the values that already failed.
  expect(urls[0]).toBe("/api/v1/instances/5001/variables");
  expect(urls.some((u) => u.includes("9001") && u.includes("resolve"))).toBe(true);

  expect(page.__errors, "page errors").toEqual([]);
});

test("cancelling writes nothing", async ({ page }) => {
  await page.locator("#run").click();
  const modal = page.locator(".repair-modal");
  await modal.locator(".repair-form input").first().fill("typed-then-abandoned@example.com");
  await modal.locator("[data-repair-cancel]").click();
  await expect(modal).toHaveCount(0);

  const writes = await page.evaluate(() => window.__calls.filter((c) => c.method === "POST"));
  expect(writes).toEqual([]);
  expect(await page.evaluate(() => window.__result)).toBe(null);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a stale form binding names the missing form and points at the raw editor", async ({ page }) => {
  await page.locator("#run-missing").click();
  // No dialog: there is nothing to render.
  await expect(page.locator(".repair-modal")).toHaveCount(0);

  const t = await page.evaluate(() => window.__toast);
  // A form id that no longer resolves is a stale binding, not a broken incident — and
  // the operator still has a way through, which the message names.
  expect(t.msg).toContain("gone");
  expect(t.msg).toContain("Fix variables");
  expect(await page.evaluate(() => window.__result)).toBe(null);

  expect(page.__errors, "page errors").toEqual([]);
});

test("the Repair action appears only on an incident whose task bound a form", async ({ page }) => {
  // Two rows: the mail task named a repair form, the shipping task did not.
  const rows = page.locator(".inc-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).locator("[data-repair]")).toHaveCount(1);
  await expect(rows.nth(1).locator("[data-repair]")).toHaveCount(0);

  // The raw editor is on both — a form covers the failure its author anticipated, and
  // an incident nobody anticipated still has to be repairable.
  await expect(rows.nth(0).locator("[data-fix-vars]")).toHaveCount(1);
  await expect(rows.nth(1).locator("[data-fix-vars]")).toHaveCount(1);

  // And the action opens the dialog through the shared binding, not just in isolation.
  await rows.nth(0).locator("[data-repair]").click();
  await expect(page.locator(".repair-modal")).toBeVisible();

  expect(page.__errors, "page errors").toEqual([]);
});
