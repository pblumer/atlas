// The per-column filter row is part of a list, not a mode you have to find.
//
// It used to be collapsed until the funnel in the header was clicked, so the first
// thing a user saw on every list was a table with no way to search it — the search
// was there, one click away, and invisible. Nothing in the source says that: the
// row is in the DOM either way and only app.css decides whether it is displayed, so
// this asserts on what the browser renders.
//
// The funnel still collapses the row for anyone who wants the vertical space back,
// and a list that carries a key remembers that choice the way it already remembers
// its sort column.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/table-filters-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

const keyed = "#card-keyed table";
const plain = "#card-plain table";

test("every list shows its filter boxes without a click", async ({ page }) => {
  await open(page);

  for (const sel of [keyed, plain]) {
    await expect(page.locator(`${sel} tr.dt-filter-row`)).toBeVisible();
    // One box per filterable column — the trailing actions column has no header text
    // and so gets none.
    await expect(page.locator(`${sel} input.dt-filter`)).toHaveCount(3);
    await expect(page.locator(`${sel} input.dt-filter`).first()).toBeVisible();
  }

  expect(page.__errors).toEqual([]);
});

test("typing in a column's box narrows the list to the rows that match", async ({ page }) => {
  await open(page);

  const rows = page.locator(`${keyed} tbody tr:not([data-dt-empty]):visible`);
  await expect(rows).toHaveCount(3);

  await page.locator(`${keyed} input.dt-filter`).first().fill("test");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("Testprozess Atlas");

  // A numeric column filters on its own value, independently of the first.
  await page.locator(`${keyed} input.dt-filter`).first().fill("");
  await page.locator(`${keyed} input.dt-filter`).nth(1).fill("8");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("Bürgschaftswesen");

  expect(page.__errors).toEqual([]);
});

test("the funnel collapses the row, and a keyed list remembers that on the next visit", async ({ page }) => {
  await open(page);

  await page.locator(`${keyed} .dt-filter-toggle`).click();
  await expect(page.locator(`${keyed} tr.dt-filter-row`)).toBeHidden();
  await expect(page.locator(`${keyed} .dt-filter-toggle`)).toHaveAttribute("aria-expanded", "false");

  // Re-render the list the way a navigation does: a fresh table, enhanced again.
  await page.evaluate(() => window.__mount("card-keyed", "e2e-projects"));
  await expect(page.locator(`${keyed} tr.dt-filter-row`)).toBeHidden();

  // Only that list, and only because it has a key to remember by: a list that carries
  // none opens with its filters showing every time.
  await page.evaluate(() => window.__mount("card-plain", null));
  await expect(page.locator(`${plain} tr.dt-filter-row`)).toBeVisible();

  // The funnel puts it back.
  await page.locator(`${keyed} .dt-filter-toggle`).click();
  await expect(page.locator(`${keyed} tr.dt-filter-row`)).toBeVisible();
  await page.evaluate(() => window.__mount("card-keyed", "e2e-projects"));
  await expect(page.locator(`${keyed} tr.dt-filter-row`)).toBeVisible();

  expect(page.__errors).toEqual([]);
});
