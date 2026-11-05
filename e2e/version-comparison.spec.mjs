// End-to-end coverage for the comparison of two process versions
// (api/web/version-comparison.js, ADR-0530).
//
// The server lines the two models up by element id and lists what changed; what these
// tests are about is whether a person can read that: which two versions are compared
// and in which direction, every change with its kind, and for a changed element each
// field before and after — a long script as a block, an absent field told apart from
// an empty one — and no "no changes" where the server did not say so.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/version-comparison-harness.html?lang=de");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("the version on screen is compared with the one before it, and every change is listed", async ({ page }) => {
  await page.locator("#cmp-default").click();
  const modal = page.locator(".vc-modal");
  await expect(modal).toBeVisible();
  await expect(modal.locator("#vc-from")).toHaveValue("101");
  await expect(modal.locator("#vc-to")).toHaveValue("102");
  // The versions are offered newest first, the tag beside the number.
  await expect(modal.locator("#vc-from option")).toHaveText(["v3 · hotfix", "v2", "v1"]);

  await expect(modal.locator(".vc-summary")).toHaveText("1 neu · 1 entfernt · 1 geändert");
  const items = modal.locator(".vc-item");
  await expect(items).toHaveCount(3);
  await expect(items.nth(0)).toHaveClass(/vc-added/);
  await expect(items.nth(0)).toContainText("Neu");
  await expect(items.nth(0)).toContainText("Unterschreiben");
  await expect(items.nth(0)).toContainText("in order");
  await expect(items.nth(2)).toHaveClass(/vc-removed/);
  await expect(items.nth(2)).toContainText("Entfernt");

  // The changed element: each field with its value before and after.
  const rows = items.nth(1).locator(".vc-details tbody tr");
  await expect(rows).toHaveCount(4);
  await expect(rows.nth(0)).toContainText("Prüfen");
  await expect(rows.nth(0)).toContainText("Betrag prüfen");
  // A script spanning lines is a block, kept as written.
  await expect(rows.nth(1).locator("pre.vc-pre")).toHaveText(/result = "gross"\n/);
  // A part added with nothing in it is "(leer)" after and absent before.
  await expect(rows.nth(2).locator("td").nth(1)).toHaveText("—");
  await expect(rows.nth(2).locator("td").nth(2)).toHaveText("(leer)");
  // A removed field is absent after.
  await expect(rows.nth(3).locator("td").nth(1)).toHaveText("=customer");
  await expect(rows.nth(3).locator("td").nth(2)).toHaveText("—");

  expect(await page.evaluate(() => window.__calls)).toEqual(["GET /api/v1/processes/102/comparison?with=101"]);
  expect(page.__errors, "page errors").toEqual([]);
});

test("picking other versions compares those, and the same model says so", async ({ page }) => {
  await page.locator("#cmp-default").click();
  const modal = page.locator(".vc-modal");
  await expect(modal.locator(".vc-item")).toHaveCount(3);

  await modal.locator("#vc-from").selectOption("102");
  await modal.locator("#vc-to").selectOption("103");
  await expect(modal.locator(".vc-summary")).toHaveText(/dasselbe Modell/);
  await expect(modal.locator(".vc-item")).toHaveCount(0);

  // The same version on both sides is not sent to the server.
  await modal.locator("#vc-from").selectOption("103");
  await expect(modal.locator(".vc-summary")).toHaveText(/dieselbe Version/);

  // Swapping turns the direction round: v2 → v1 is what the server answers only as hints.
  await modal.locator("#vc-from").selectOption("102");
  await modal.locator("#vc-to").selectOption("101");
  await expect(modal.locator(".vc-summary")).toContainText("incoming/outgoing");
  await expect(modal.locator(".vc-summary")).not.toContainText("Keine Änderungen");

  await modal.locator("#vc-swap").click();
  await expect(modal.locator("#vc-from")).toHaveValue("101");
  await expect(modal.locator("#vc-to")).toHaveValue("102");
  await expect(modal.locator(".vc-item")).toHaveCount(3);

  // A failed reading is said as a failure, not as an empty comparison.
  await modal.locator("#vc-from").selectOption("103");
  await modal.locator("#vc-to").selectOption("101");
  await expect(modal.locator(".vc-error")).toContainText("fehlgeschlagen");

  // Escape closes it.
  await page.keyboard.press("Escape");
  await expect(modal).toHaveCount(0);
  expect(page.__errors, "page errors").toEqual([]);
});

test("the oldest version is compared with the one after it", async ({ page }) => {
  await page.locator("#cmp-oldest").click();
  const modal = page.locator(".vc-modal");
  await expect(modal.locator("#vc-to")).toHaveValue("101");
  await expect(modal.locator("#vc-from")).not.toHaveValue("101");
  expect(page.__errors, "page errors").toEqual([]);
});

test("a draft's comparison is shown under the title it is given", async ({ page }) => {
  await page.locator("#cmp-draft").click();
  const modal = page.locator(".vc-modal");
  await expect(modal.locator("h2")).toHaveText("Entwurf gegenüber v2");
  await expect(modal.locator(".vc-item")).toHaveCount(3);
  await modal.locator("[data-vc-close]").click();
  await expect(modal).toHaveCount(0);
  expect(await page.evaluate(() => window.__calls)).toEqual([]);
  expect(page.__errors, "page errors").toEqual([]);
});
