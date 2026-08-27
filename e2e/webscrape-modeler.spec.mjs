// End-to-end coverage for ADR-0190's Web Scraping Connector feed authoring.
// It drives the real bpmn-js modeler and Atlas properties panel so format/maxItems
// must survive the moddle round trip, and switching to a feed mode must remove the
// HTML-only selector/attribute rather than leave a model the compiler rejects.
import { test, expect } from "@playwright/test";

async function openGroup(page, title) {
  const group = page.locator(".pgroup", { has: page.locator(".pgroup-title", { hasText: title }) });
  if (await group.count() === 0) return;
  if (await group.evaluate((el) => el.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").click();
  }
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/webscrape-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__selectFeed());
  await openGroup(page, "Page");
  await openGroup(page, "Extraction");
});

test("RSS format and maxItems survive the real bpmn-js round trip", async ({ page }) => {
  await expect(page.locator("#f-st-format")).toHaveValue("rss");
  await expect(page.locator("#f-st-maxItems")).toHaveValue("3");
  await expect(page.locator("#f-st-selector")).toHaveCount(0);
  await expect(page.locator("#f-st-attribute")).toHaveCount(0);

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('format="rss"');
  expect(xml).toContain('maxItems="3"');
  expect(xml).toContain('resultVariable="headlines"');
  expect(page.__errors).toEqual([]);
});

test("switching HTML to a feed clears HTML-only extraction attributes", async ({ page }) => {
  await page.locator("#f-st-format").selectOption("");
  await openGroup(page, "Extraction");
  await expect(page.locator("#f-st-selector")).toBeVisible();
  await expect(page.locator("#f-st-attribute")).toBeVisible();

  await page.locator("#f-st-selector").fill(".headline a");
  await page.locator("#f-st-selector").blur();
  await page.locator("#f-st-attribute").fill("href");
  await page.locator("#f-st-attribute").blur();

  await page.locator("#f-st-format").selectOption("atom");
  await expect(page.locator("#f-st-selector")).toHaveCount(0);
  await expect(page.locator("#f-st-attribute")).toHaveCount(0);

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('format="atom"');
  expect(xml).toContain('maxItems="3"');
  expect(xml).not.toContain('selector="');
  expect(xml).not.toContain('attribute="');
  expect(page.__errors).toEqual([]);
});
