// e2e for the product manager's delivery dashboard (api/web/deliveryreport.js;
// ADR-0515).
//
// The card asks the delivery report for a period, fills the period boxes with the
// one the server chose when none was given, draws the four tiles and a row per
// product, and keeps the CSV link on the same period. It names nobody because the
// route sends nobody — the test holds the card to drawing only what it was sent.
import { test, expect } from "@playwright/test";

const REPORT = {
  from: "2026-08-03", to: "2026-10-31", catalogs: ["cat_ws"],
  products: [
    { itemId: "laptop", name: "Notebook", serviceLevel: "Standard", deliveryDays: 3,
      ordered: 10, delivered: 8, met: 6, missed: 2, overdue: 1, open: 1, awaitingApproval: 0,
      failed: 0, held: 40, metShare: 6 / 9, leadMedianDays: 2.04, leadP90Days: 4.5, approvalWaitMedianDays: 1 },
    { itemId: "bag", name: "Tasche", ordered: 1, delivered: 1, met: 0, missed: 0, overdue: 0,
      open: 0, awaitingApproval: 0, failed: 0, held: 3 },
  ],
  totals: { ordered: 11, held: 43, met: 6, missed: 2, overdue: 1, metShare: 6 / 9 },
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/delivery-report-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("the card draws the tiles and a row per product", async ({ page }) => {
  await page.evaluate((r) => window.__mount(r), REPORT);
  const card = page.locator("[data-delivery]");
  await expect(card.locator(".dr-kpi")).toHaveCount(4);
  await expect(card.locator(".dr-kpi").nth(0)).toContainText("11");
  await expect(card.locator(".dr-kpi").nth(2)).toContainText("67");
  await expect(card.locator(".dr-kpi").nth(2).locator(".dr-v")).toHaveClass(/dr-bad/);
  await expect(card.locator("tbody tr")).toHaveCount(2);
  const laptop = card.locator("tbody tr").nth(0);
  await expect(laptop).toContainText("Notebook");
  await expect(laptop).toContainText("Standard");
  await expect(laptop).toContainText("2.0");
  await expect(laptop).toContainText("4.5");
  // A product with no service level has no share: a dash, not 0 %.
  await expect(card.locator("tbody tr").nth(1)).toContainText("—");
});

test("the server's period fills the boxes, and a chosen one is asked for and exported", async ({ page }) => {
  await page.evaluate((r) => window.__mount(r), REPORT);
  const card = page.locator("[data-delivery]");
  await expect(card.locator('input[name="from"]')).toHaveValue("2026-08-03");
  await expect(card.locator('input[name="to"]')).toHaveValue("2026-10-31");
  expect(await page.evaluate(() => window.__calls)).toEqual(["GET /api/v1/catalog-products/delivery-report"]);

  await card.locator('input[name="from"]').fill("2026-01-01");
  await card.locator('input[name="to"]').fill("2026-03-31");
  await card.locator('button[type="submit"]').click();
  await expect.poll(() => page.evaluate(() => window.__calls.at(-1)))
    .toBe("GET /api/v1/catalog-products/delivery-report?from=2026-01-01&to=2026-03-31");
  await expect(card.locator("[data-delivery-csv]")).toHaveAttribute("href",
    "/api/v1/catalog-products/delivery-report?from=2026-01-01&to=2026-03-31&format=csv");
});

test("a report that cannot be read says so", async ({ page }) => {
  await page.evaluate(() => window.__mount(new Error("503 down")));
  await expect(page.locator("[data-delivery-body]")).toContainText("503 down");
});
