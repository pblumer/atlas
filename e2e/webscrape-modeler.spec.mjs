// End-to-end coverage for the Web Scraping Connector's authoring: ADR-0190's feed
// formats and ADR-0231's per-item fields.
// It drives the real bpmn-js modeler and Atlas properties panel so format/maxItems
// must survive the moddle round trip, and switching to a feed mode must remove the
// HTML-only selector/attribute rather than leave a model the compiler rejects.
import { test, expect } from "@playwright/test";

// taskXml narrows the serialized model to one service task. The harness carries two
// scrape tasks — a feed and a structured HTML one — so an assertion about "no
// selector" has to mean *this* task's, not the document's.
function taskXml(xml, taskId) {
  const start = xml.indexOf(`id="${taskId}"`);
  const end = xml.indexOf("</bpmn:serviceTask>", start);
  return xml.slice(start, end);
}

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

  const task = taskXml(await page.evaluate(() => window.__xml()), "Activity_feed");
  expect(task).toContain('format="rss"');
  expect(task).toContain('maxItems="3"');
  expect(task).toContain('resultVariable="headlines"');
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

  const task = taskXml(await page.evaluate(() => window.__xml()), "Activity_feed");
  expect(task).toContain('format="atom"');
  expect(task).toContain('maxItems="3"');
  expect(task).not.toContain('selector="');
  expect(task).not.toContain('attribute="');
  expect(page.__errors).toEqual([]);
});

// The structured-HTML half. The field rows are a map editor with a third cell, so
// what has to survive is the moddle round trip of three attributes per child element
// — a dropped attribute here is a field that silently reads the wrong thing.
test("scrapeField rows round-trip name, selector and attribute", async ({ page }) => {
  await page.evaluate(() => window.__selectRates());
  await openGroup(page, "Extraction");

  await expect(page.locator("#f-st-selector")).toHaveValue("tr.row");
  await expect(page.locator("#f-st-absoluteLinks")).toHaveValue("true");
  // With fields, each field carries its own attribute, so the task-level one is gone.
  await expect(page.locator("#f-st-attribute")).toHaveCount(0);

  const rows = page.locator('.st-map-row[data-field="fields"]');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).locator(".st-map-name")).toHaveValue("laufzeit");
  await expect(rows.nth(0).locator(".st-map-value")).toHaveValue("td.term");
  await expect(rows.nth(0).locator(".st-map-extra")).toHaveValue("");
  await expect(rows.nth(1).locator(".st-map-name")).toHaveValue("link");
  await expect(rows.nth(1).locator(".st-map-value")).toHaveValue("a");
  await expect(rows.nth(1).locator(".st-map-extra")).toHaveValue("href");

  const task = taskXml(await page.evaluate(() => window.__xml()), "Activity_rates");
  expect(task).toContain('name="laufzeit"');
  expect(task).toContain('selector="td.term"');
  expect(task).toContain('attribute="href"');
  expect(page.__errors).toEqual([]);
});

test("a new field row is written as a scrapeField child", async ({ page }) => {
  await page.evaluate(() => window.__selectRates());
  await openGroup(page, "Extraction");

  const container = page.locator('.st-map[data-field="fields"]');
  await container.locator(".st-map-add").click();
  const row = page.locator('.st-map-row[data-field="fields"]').nth(2);
  await row.locator(".st-map-name").fill("zins");
  await row.locator(".st-map-name").blur();
  await row.locator(".st-map-value").fill("td.rate");
  await row.locator(".st-map-value").blur();

  const task = taskXml(await page.evaluate(() => window.__xml()), "Activity_rates");
  expect(task).toContain('name="zins"');
  expect(task).toContain('selector="td.rate"');
  expect(page.__errors).toEqual([]);
});

test("switching a field scrape to a feed clears the fields", async ({ page }) => {
  await page.evaluate(() => window.__selectRates());
  await openGroup(page, "Extraction");
  await page.locator("#f-st-format").selectOption("rss");

  await expect(page.locator('.st-map[data-field="fields"]')).toHaveCount(0);
  await expect(page.locator("#f-st-plainText")).toBeVisible();

  const task = taskXml(await page.evaluate(() => window.__xml()), "Activity_rates");
  expect(task).toContain('format="rss"');
  expect(task).not.toContain("scrapeField");
  expect(task).not.toContain('absoluteLinks="true"');
  expect(page.__errors).toEqual([]);
});
