// Where the product editor opens.
//
// It used to render under the product list, which is fine with three products and
// unusable with forty: opening a row near the bottom put the form below everything,
// so it was read after a long scroll and with no sight of the product it belonged to.
// It now takes a column beside the list and opens level with its row.
//
// The regression this guards is geometric and silent — nothing throws when a panel
// lands in the wrong place, and no Go test can see a bounding box.
import { test, expect } from "@playwright/test";

const open = async (page, size = { width: 1280, height: 800 }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize(size);
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-cols tbody tr");
};

// The geometry of the open panel, the row it belongs to and the list beside it.
const boxes = async (page) => page.evaluate(() => {
  const r = (sel) => {
    const el = document.querySelector(sel);
    return el ? el.getBoundingClientRect().toJSON() : null;
  };
  return {
    panel: r(".product-editor"),
    row: r(".product-list tr.editing"),
    list: r(".product-list"),
  };
});

test("the editor opens beside the product list, level with the row it was opened from", async ({ page }) => {
  await open(page);

  // The tenth product, deliberately: the first row would pass with the old layout too.
  await page.click('.product-list tbody tr:nth-child(10) button[data-act="edit"]');
  await expect(page.locator(".product-editor .product-form")).toBeVisible();

  const b = await boxes(page);
  // Beside, not below: the panel starts to the right of the list's column.
  expect(b.panel.left).toBeGreaterThanOrEqual(b.list.right - 1);
  // And level with the row, which is the point of the alignment.
  expect(Math.abs(b.panel.top - b.row.top)).toBeLessThanOrEqual(4);
  // The row it belongs to is named rather than implied.
  await expect(page.locator(".product-list tr.editing")).toHaveCount(1);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p10");
  expect(page.__errors).toEqual([]);
});

test("opening a row puts the form where the reader already is", async ({ page }) => {
  await open(page);
  // Where a reader would be: scrolled to the row they mean to edit. Measured against
  // the viewport rather than as a scroll offset, because scrollY is *expected* to
  // change here — opening the panel narrows the list and the rows above rewrap, and
  // the page is scrolled back by exactly what that added so the row stays put. What
  // the reader sees is the question; scrollY is the wrong instrument for it.
  const row = page.locator(".product-list tbody tr:nth-child(10)");
  await row.scrollIntoViewIfNeeded();
  const before = await row.evaluate((el) => el.getBoundingClientRect().top);

  await row.locator('button[data-act="edit"]').click();
  const after = await page.evaluate(() => ({
    row: document.querySelector(".product-list tr.editing").getBoundingClientRect().top,
    panel: document.querySelector(".product-editor").getBoundingClientRect().top,
    height: window.innerHeight,
  }));
  // The row stayed where it was read, and the form is beside it rather than below the
  // scroll the old layout cost.
  expect(Math.abs(after.row - before)).toBeLessThanOrEqual(4);
  expect(after.panel).toBeGreaterThanOrEqual(0);
  expect(after.panel).toBeLessThan(after.height);
});

test("cancelling closes the panel and gives the width back to the list", async ({ page }) => {
  await open(page);
  const wide = await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width);

  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  const narrow = await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width);
  expect(narrow).toBeLessThan(wide);

  await page.click('.product-editor button[data-act="cancel-product"]');
  await expect(page.locator(".product-editor .product-form")).toHaveCount(0);
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
  const back = await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width);
  expect(Math.round(back)).toBe(Math.round(wide));
});

test("a new product opens its form level with the button that asked for it", async ({ page }) => {
  await open(page);
  await page.click('button[data-act="new-product"]');
  const b = await page.evaluate(() => ({
    panel: document.querySelector(".product-editor").getBoundingClientRect().top,
    button: document.querySelector('button[data-act="new-product"]').closest(".row")
      .getBoundingClientRect().top,
  }));
  expect(Math.abs(b.panel - b.button)).toBeLessThanOrEqual(4);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("");
  // No row is claimed: the product has none yet.
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
});

test("on a narrow screen it falls back to the stacked layout", async ({ page }) => {
  await open(page, { width: 800, height: 800 });
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  const b = await boxes(page);
  // Under the list, full width, and no offset applied — one column is one column.
  expect(b.panel.top).toBeGreaterThanOrEqual(b.list.bottom - 1);
  expect(Math.round(b.panel.width)).toBeGreaterThan(Math.round(b.list.width * 0.9));
});

test("sorting the list keeps the panel level with its product", async ({ page }) => {
  await open(page);
  await page.click('.product-list tbody tr:nth-child(10) button[data-act="edit"]');

  // The shared table enhancer sorts the tbody underneath the panel (table.js), which
  // moves the row it is aligned to — the alignment has to be re-measured, not kept.
  await page.click(".product-list thead th:first-child");
  await page.waitForTimeout(120);
  const b = await boxes(page);
  expect(b.row).not.toBeNull();
  expect(Math.abs(b.panel.top - b.row.top)).toBeLessThanOrEqual(4);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p10");
});

test("the panel resists the scroll rather than leaving with it", async ({ page }) => {
  await open(page);
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');

  // Brought to the top of the window first, so what is measured afterwards is the
  // sticking and not the distance it still had to travel.
  await page.locator(".product-editor").scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => document.querySelector(".product-editor").getBoundingClientRect().top);
  await page.evaluate(() => window.scrollBy(0, 120));
  await page.waitForTimeout(80);
  const after = await page.evaluate(() => {
    const r = document.querySelector(".product-editor").getBoundingClientRect();
    return { top: r.top, bottom: r.bottom, height: window.innerHeight };
  });

  // It held its place instead of travelling the full 120px with the page: the form is
  // longer than most screens, and reading to its Save button must not lose the form.
  // (Sticky only within the products section — scrolled past that, it leaves with the
  // section it belongs to, which is the behaviour and not a limit worth defeating.)
  expect(after.top).toBeGreaterThan(before - 100);
  expect(after.top).toBeGreaterThanOrEqual(0);
  // And it is bounded by the window, so what does not fit scrolls inside the panel.
  expect(after.bottom).toBeLessThanOrEqual(after.height + 1);
});
