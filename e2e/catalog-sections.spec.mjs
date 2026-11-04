// The catalogue page in sections (catalog-admin.js pageSection), each folding like
// every topic of the Console (fold.js). A first visit finds every section folded, each
// heading still saying what it holds; a section opened stays open on the next visit in
// the same browser, and one folded again stays folded. The steps come in the order a catalogue is filled, and on a wide screen the
// explanation sits in a column beside the controls rather than above them.
import { test, expect } from "@playwright/test";

// A browser that never opened the page: the config's open-everything state is not
// this spec's starting point.
test.use({ storageState: { cookies: [], origins: [] } });

async function open(page) {
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => window.__mount());
  await expect(page.locator("section.cat-section [data-fold-toggle]").first()).toBeAttached();
}

const section = (page, key) => page.locator(`section.cat-section[data-section="${key}"]`);
const toggle = (page, key) => section(page, key).locator("[data-fold-toggle]");

test("every section is folded on a first visit, in the order a catalogue is filled", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  const keys = await page.locator("section.cat-section").evaluateAll((ds) => ds.map((d) => d.dataset.section));
  expect(keys.slice(0, 4)).toEqual(["products", "content", "relations", "publish"]);
  expect(keys).toContain("transfer");
  for (const key of keys) {
    await expect(toggle(page, key)).toHaveAttribute("aria-expanded", "false");
    await expect(section(page, key).locator(".cat-sec-body")).toBeHidden();
  }
  // Folded, a heading still says what is in it, and which step it is.
  await expect(section(page, "products").locator(".cat-sec-sum")).toContainText("offered");
  await expect(toggle(page, "products").locator(".cat-sec-step")).toHaveText("1");
  await expect(page.locator(".cat-meta")).toBeHidden();
});

test("an opened section stays open on the next visit, and a folded one folded", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  await toggle(page, "settings").click();
  await expect(page.locator(".cat-meta")).toBeVisible();

  await page.reload();
  await open(page);
  await expect(toggle(page, "settings")).toHaveAttribute("aria-expanded", "true");
  await expect(toggle(page, "products")).toHaveAttribute("aria-expanded", "false");

  await toggle(page, "settings").click();
  await page.reload();
  await open(page);
  await expect(toggle(page, "settings")).toHaveAttribute("aria-expanded", "false");
});

test("on a wide screen the explanation is beside the controls, stacked above them on a narrow one", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page);
  await toggle(page, "settings").click();
  const main = await section(page, "settings").locator(".cat-sec-main").boundingBox();
  const about = await section(page, "settings").locator(".cat-sec-about").boundingBox();
  expect(about.x).toBeGreaterThan(main.x + main.width - 1);
  expect(Math.abs(about.y - main.y)).toBeLessThan(4);

  await page.setViewportSize({ width: 800, height: 900 });
  const main2 = await section(page, "settings").locator(".cat-sec-main").boundingBox();
  const about2 = await section(page, "settings").locator(".cat-sec-about").boundingBox();
  expect(about2.y + about2.height).toBeLessThanOrEqual(main2.y + 1);
});
