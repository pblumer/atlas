// End-to-end test for the live id-availability check (api/web/idcheck.js).
//
// A draft is stored under its process id and a form under the id a user task binds to,
// so those fields are the artifact's identity: saving onto an id something else already
// holds is refused (ADR-0222). A refusal at Save is too late to be
// the whole answer — the author has typed the id and moved on — so the field checks
// itself as it is typed. These assert what the author actually sees: the field goes red
// and names what holds the id, an id that is free leaves no mark, and the artifact's own
// id is never reported as a collision with itself.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/id-check-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("an id another draft holds turns the field red and names it", async ({ page }) => {
  await page.evaluate(() => window.__type("order-fulfillment"));
  await expect(page.locator("#pid")).toHaveClass(/id-taken/);
  await expect(page.locator(".id-check")).toContainText("Bestellung");
  await expect(page.locator(".id-check")).toBeVisible();
  // Screen readers get the same verdict, not just the colour.
  await expect(page.locator("#pid")).toHaveAttribute("aria-invalid", "true");
  expect(page.__errors).toEqual([]);
});

test("a collision the author may not see is reported without naming it", async ({ page }) => {
  await page.evaluate(() => window.__type("hidden-one"));
  await expect(page.locator("#pid")).toHaveClass(/id-taken/);
  await expect(page.locator(".id-check")).toContainText("another draft");
  expect(page.__errors).toEqual([]);
});

test("a free id clears the mark again", async ({ page }) => {
  await page.evaluate(() => window.__type("order-fulfillment"));
  await expect(page.locator("#pid")).toHaveClass(/id-taken/);
  await page.evaluate(() => window.__type("order-fulfillment-2"));
  await expect(page.locator("#pid")).not.toHaveClass(/id-taken/);
  await expect(page.locator(".id-check")).toBeHidden();
});

test("the draft's own id is never a collision with itself", async ({ page }) => {
  await page.evaluate(() => { window.__probes.length = 0; });
  await page.evaluate(() => window.__type("my-own-draft"));
  await expect(page.locator("#pid")).not.toHaveClass(/id-taken/);
  // Not merely "reported free" — not asked at all, so re-rendering the panel on every
  // selection change does not pepper the server with probes.
  await page.waitForTimeout(500);
  expect(await page.evaluate(() => window.__probes)).toEqual([]);
});

test("typing is debounced into one question, not one per keystroke", async ({ page }) => {
  await page.evaluate(() => { window.__probes.length = 0; });
  await page.evaluate(async () => {
    for (const v of ["o", "or", "ord", "orde", "order-fulfillment"]) window.__type(v);
  });
  await expect(page.locator("#pid")).toHaveClass(/id-taken/);
  await page.waitForTimeout(400);
  expect(await page.evaluate(() => window.__probes)).toEqual(["order-fulfillment"]);
});
