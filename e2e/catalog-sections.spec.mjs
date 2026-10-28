// The catalogue page in sections (catalog-admin.js pageSection). A first visit finds
// every section folded, each heading still saying what it holds; a section opened
// stays open on the next visit in the same browser, and one folded again stays
// folded. The steps come in the order a catalogue is filled, and on a wide screen the
// explanation sits in a column beside the controls rather than above them.
import { test, expect } from "@playwright/test";

// A browser that never opened the page: the config's open-everything state is not
// this spec's starting point.
test.use({ storageState: { cookies: [], origins: [] } });

async function open(page) {
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => window.__mount());
  await expect(page.locator("details.cat-section").first()).toBeAttached();
}

test("every section is folded on a first visit, in the order a catalogue is filled", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  const keys = await page.locator("details.cat-section").evaluateAll((ds) => ds.map((d) => d.dataset.section));
  expect(keys.slice(0, 4)).toEqual(["products", "content", "relations", "publish"]);
  expect(keys).toContain("transfer");
  for (const d of await page.locator("details.cat-section").all()) {
    await expect(d).not.toHaveAttribute("open", "");
  }
  // Folded, a heading still says what is in it.
  await expect(page.locator('details[data-section="products"] .cat-sec-sum')).toContainText("offered");
  await expect(page.locator(".cat-meta")).toBeHidden();
});

test("an opened section stays open on the next visit, and a folded one folded", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  await page.locator('details[data-section="settings"] > summary').click();
  await expect(page.locator(".cat-meta")).toBeVisible();

  await page.reload();
  await open(page);
  await expect(page.locator('details[data-section="settings"]')).toHaveAttribute("open", "");
  await expect(page.locator('details[data-section="products"]')).not.toHaveAttribute("open", "");

  await page.locator('details[data-section="settings"] > summary').click();
  await page.reload();
  await open(page);
  await expect(page.locator('details[data-section="settings"]')).not.toHaveAttribute("open", "");
});

test("on a wide screen the explanation is beside the controls, stacked above them on a narrow one", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  await page.locator('details[data-section="settings"] > summary').click();
  const main = await page.locator('details[data-section="settings"] .cat-sec-main').boundingBox();
  const about = await page.locator('details[data-section="settings"] .cat-sec-about').boundingBox();
  expect(about.x).toBeGreaterThan(main.x + main.width - 1);
  expect(Math.abs(about.y - main.y)).toBeLessThan(4);

  await page.setViewportSize({ width: 800, height: 900 });
  const main2 = await page.locator('details[data-section="settings"] .cat-sec-main').boundingBox();
  const about2 = await page.locator('details[data-section="settings"] .cat-sec-about').boundingBox();
  expect(about2.y + about2.height).toBeLessThanOrEqual(main2.y + 1);
});
