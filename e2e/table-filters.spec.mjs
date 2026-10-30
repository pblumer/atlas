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

// A box with something in it carries a small cross at its trailing edge that empties
// it. The cross sits at the field's end, not after the typed text: it stays where the
// hand learned to find it however long the term is, and it is not where a click goes to
// put the caret behind the text. An empty box shows none — a cross that does nothing,
// repeated in every column, would only be noise — so a visible cross also says that
// this column is filtering.
test("a box with a term carries a cross that empties it and gives the rows back", async ({ page }) => {
  await open(page);

  const box = page.locator(`${keyed} input.dt-filter`).first();
  const clear = page.locator(`${keyed} .dt-filter-clear`).first();
  const rows = page.locator(`${keyed} tbody tr:not([data-dt-empty]):visible`);

  await expect(clear).toBeHidden();
  await box.fill("test");
  await expect(rows).toHaveCount(1);
  await expect(clear).toBeVisible();
  // Only the column that has a term shows its cross.
  await expect(page.locator(`${keyed} .dt-filter-clear:visible`)).toHaveCount(1);

  // At the box's trailing edge, inside it.
  const b = await box.boundingBox();
  const c = await clear.boundingBox();
  expect(c.x + c.width).toBeLessThanOrEqual(b.x + b.width);
  expect(b.x + b.width - (c.x + c.width)).toBeLessThan(8);
  expect(c.y).toBeGreaterThanOrEqual(b.y);
  expect(c.y + c.height).toBeLessThanOrEqual(b.y + b.height);

  await clear.click();
  await expect(box).toHaveValue("");
  await expect(rows).toHaveCount(3);
  await expect(clear).toBeHidden();
  // The caret stays in the box, ready for the next term.
  await expect(box).toBeFocused();
  // Clearing the last term clears the funnel's mark too.
  await expect(page.locator(`${keyed} .dt-filter-toggle`)).not.toHaveClass(/dt-has-filters/);

  expect(page.__errors).toEqual([]);
});

// The quickest way to empty a box is not to reach for it at all: Escape does it from the
// keyboard. It only empties — an empty box lets Escape through, so a dialog around the
// list still closes on it, one press later.
test("Escape empties a box with a term, and lets an empty box's Escape through", async ({ page }) => {
  await open(page);

  const box = page.locator(`${keyed} input.dt-filter`).first();
  const rows = page.locator(`${keyed} tbody tr:not([data-dt-empty]):visible`);
  await page.evaluate(() => {
    window.__escapes = 0;
    document.addEventListener("keydown", (e) => { if (e.key === "Escape") window.__escapes++; });
  });

  await box.fill("test");
  await expect(rows).toHaveCount(1);
  await box.press("Escape");
  await expect(box).toHaveValue("");
  await expect(rows).toHaveCount(3);
  expect(await page.evaluate(() => window.__escapes)).toBe(0);

  await box.press("Escape");
  expect(await page.evaluate(() => window.__escapes)).toBe(1);

  expect(page.__errors).toEqual([]);
});
