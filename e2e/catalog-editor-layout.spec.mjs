// Where the product editor opens.
//
// It used to render under the product list, which is fine with three products and
// unusable with forty; then in a column beside the list, which on a wide screen was a
// third of the page and scrolled inside itself. It now opens in a row of its own
// directly under the product it edits, across the table's width, with sections that
// fold. A new product, which has no row yet, opens under the list's buttons, as wide.
//
// The regression this guards is geometric and silent — nothing throws when a panel
// lands in the wrong place, and no Go test can see a bounding box.
import { test, expect } from "@playwright/test";

// 1600px by default: the two columns are offered from 1440 up (app.css measures why),
// so a narrower default would be testing the stacked layout while claiming otherwise.
const open = async (page, size = { width: 1600, height: 800 }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize(size);
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-list tbody tr");
};

test("the editor opens directly under the row it was opened from, across the list", async ({ page }) => {
  await open(page);

  // The tenth product, deliberately: the first row would pass with any layout.
  await page.click('.product-list tbody tr:nth-child(10) button[data-act="edit"]');
  await expect(page.locator(".product-editor .product-form")).toBeVisible();

  const b = await page.evaluate(() => {
    const r = (el) => el.getBoundingClientRect().toJSON();
    const row = document.querySelector(".product-list tr.editing");
    const edit = document.querySelector(".product-list tr.product-edit-row");
    return {
      row: r(row), edit: r(edit), list: r(document.querySelector(".product-list")),
      next: edit.previousElementSibling === row,
      inside: !!edit.querySelector(".product-editor .product-form"),
    };
  });
  // Directly under its row, in a row of its own, and the table's full width.
  expect(b.next).toBe(true);
  expect(b.inside).toBe(true);
  expect(Math.abs(b.edit.top - b.row.bottom)).toBeLessThanOrEqual(2);
  expect(b.edit.width).toBeGreaterThan(b.list.width * 0.9);
  await expect(page.locator(".product-list tr.editing")).toHaveCount(1);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p10");
  expect(page.__errors).toEqual([]);
});

test("opening a row leaves it where the reader clicked it", async ({ page }) => {
  await open(page);
  const row = page.locator(".product-list tbody tr:nth-child(10)");
  await row.scrollIntoViewIfNeeded();
  const before = await row.evaluate((el) => el.getBoundingClientRect().top);
  await row.locator('button[data-act="edit"]').click();
  const after = await page.evaluate(() =>
    document.querySelector(".product-list tr.editing").getBoundingClientRect().top);
  expect(Math.abs(after - before)).toBeLessThanOrEqual(4);
});

test("cancelling closes the panel and removes its row", async ({ page }) => {
  await open(page);
  const rows = await page.locator(".product-list tbody tr").count();
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  await expect(page.locator(".product-list tbody tr")).toHaveCount(rows + 1);

  await page.click('.product-editor button[data-act="cancel-product"]');
  await expect(page.locator(".product-editor .product-form")).toHaveCount(0);
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
  await expect(page.locator(".product-list tr.product-edit-row")).toHaveCount(0);
  await expect(page.locator(".product-list tbody tr")).toHaveCount(rows);
});

test("a new product opens under the list's buttons, across the list", async ({ page }) => {
  await open(page);
  await page.click('button[data-act="new-product"]');
  const b = await page.evaluate(() => {
    const r = (el) => el.getBoundingClientRect();
    const buttons = r(document.querySelector('button[data-act="new-product"]').closest(".row"));
    const card = r(document.querySelector(".product-panels .product-editor .card"));
    const list = r(document.querySelector(".product-list"));
    return { gap: card.top - buttons.bottom, width: card.width, list: list.width };
  });
  // Directly under the buttons, and as wide as the form's measure allows rather than a
  // third of the page.
  expect(b.gap).toBeGreaterThanOrEqual(0);
  expect(b.gap).toBeLessThanOrEqual(24);
  expect(b.width).toBeGreaterThanOrEqual(Math.min(1280, b.list) - 2);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("");
  // No row is claimed: the product has none yet.
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
  await expect(page.locator(".product-list tr.product-edit-row")).toHaveCount(0);

  // And editing a row afterwards takes the form out from under the buttons and under
  // the row.
  await page.click('.product-list tbody tr:nth-child(3) button[data-act="edit"]');
  await expect(page.locator(".product-list tr.product-edit-row .product-form")).toHaveCount(1);
  await expect(page.locator(".product-panels .product-form")).toHaveCount(0);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p3");

  // Closing it puts the containers back, so the next new product opens where it should.
  await page.click('.product-editor button[data-act="cancel-product"]');
  await page.click('button[data-act="new-product"]');
  await expect(page.locator(".product-panels .product-editor .product-form")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("on a narrow screen the form is still under its row", async ({ page }) => {
  await open(page, { width: 800, height: 800 });
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  const b = await page.evaluate(() => ({
    row: document.querySelector(".product-list tr.editing").getBoundingClientRect().bottom,
    edit: document.querySelector(".product-list tr.product-edit-row").getBoundingClientRect().top,
  }));
  expect(Math.abs(b.edit - b.row)).toBeLessThanOrEqual(2);
});

test("sorting the list keeps the form under its product", async ({ page }) => {
  await open(page);
  await page.click('.product-list tbody tr:nth-child(10) button[data-act="edit"]');

  // The shared table enhancer sorts the tbody (table.js); the form's row is a detail
  // row and travels with its product.
  await page.click(".product-list thead th:first-child");
  await expect.poll(() => page.evaluate(() => {
    const edit = document.querySelector(".product-list tr.product-edit-row");
    return !!edit && edit.previousElementSibling === document.querySelector(".product-list tr.editing");
  })).toBe(true);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p10");
});

test("sections fold, and a folded section stays folded for the next product", async ({ page }) => {
  await open(page);
  await page.evaluate(() => localStorage.removeItem("atlas.catalog.productSections"));
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  const offer = page.locator('.product-editor details.form-group[data-sec="offer"]');
  await expect(offer).toHaveAttribute("open", "");
  await expect(page.locator('.product-editor input[name="price"]')).toBeVisible();

  await offer.locator("summary").click();
  await expect(offer).not.toHaveAttribute("open", "");
  await expect(page.locator('.product-editor input[name="price"]')).toBeHidden();

  // The next product opens with the same section folded.
  await page.click('.product-list tbody tr:nth-child(5) button[data-act="edit"]');
  await expect(page.locator('.product-editor details.form-group[data-sec="offer"]')).not.toHaveAttribute("open", "");

  // Collapse all folds every section; the button then offers the opposite.
  await page.click('.product-editor button[data-act="fold-sections"]');
  await expect(page.locator(".product-editor details.form-group[open]")).toHaveCount(0);
  await expect(page.locator('.product-editor button[data-act="fold-sections"]')).toHaveText("Expand all");
  await page.click('.product-editor button[data-act="fold-sections"]');
  await expect(page.locator(".product-editor details.form-group:not([open])")).toHaveCount(0);
  await page.evaluate(() => localStorage.removeItem("atlas.catalog.productSections"));
  expect(page.__errors).toEqual([]);
});

test("Save Draft stores the product, Save & Publish also publishes the catalogue", async ({ page }) => {
  await open(page);
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  await expect(page.locator('.product-editor button[data-publish="no"]')).toHaveText("Save Draft");

  await page.evaluate(() => { window.__sent.length = 0; });
  await page.click('.product-editor button[data-publish="no"]');
  await expect.poll(() => page.evaluate(() =>
    window.__sent.some((c) => c.method === "POST" && /\/catalog-products$/.test(c.url)))).toBe(true);
  expect(await page.evaluate(() =>
    window.__sent.some((c) => c.method === "POST" && /\/releases$/.test(c.url)))).toBe(false);

  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-list tbody tr");
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  await page.evaluate(() => { window.__sent.length = 0; });
  await page.click('.product-editor button[data-publish="yes"]');
  await expect.poll(() => page.evaluate(() => {
    const posts = window.__sent.filter((c) => c.method === "POST");
    const save = posts.findIndex((c) => /\/catalog-products$/.test(c.url));
    const pub = posts.findIndex((c) => /\/catalogs\/c1\/releases$/.test(c.url));
    return save >= 0 && pub > save;
  })).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("the row's actions sit at the table's right edge", async ({ page }) => {
  await open(page, { width: 1600, height: 800 });
  // The page is full width here, so a left-aligned action cell would leave its buttons
  // stranded in the middle of the row with the table's edge far to their right.
  const b = await page.evaluate(() => {
    const row = document.querySelector(".product-list tbody tr");
    const cell = row.querySelector("td.row-actions");
    const buttons = [...cell.querySelectorAll("button")];
    const cellBox = cell.getBoundingClientRect();
    return {
      count: buttons.length,
      lastButton: buttons[buttons.length - 1].getBoundingClientRect().right,
      cell: cellBox.right,
      cellHeight: cellBox.height,
      table: row.closest("table").getBoundingClientRect().right,
      rowHeight: row.getBoundingClientRect().height,
    };
  });
  // Measured from the last button rather than the first, so this says "flush right"
  // whatever a row offers — two buttons on a plain product, three where it can also be
  // assembled. Only the cell's own padding stands between the two.
  expect(b.count).toBeGreaterThanOrEqual(2);
  expect(b.cell - b.table).toBeLessThanOrEqual(1);
  expect(b.cell - b.lastButton).toBeLessThan(24);
  // And on one line: buttons that wrap would make one row taller than the rest.
  expect(b.cellHeight).toBeLessThanOrEqual(b.rowHeight);
});

test("the kit opens under its row too, and takes the panel from the form", async ({ page }) => {
  await open(page);

  // The kit answers a question about one row exactly as the form does — what this
  // product is made of — so it opens in the same place, directly under that row.
  await page.click('.product-list tbody tr:nth-child(8) button[data-act="assemble"]');
  await expect(page.locator(".product-list tr.product-edit-row form.assemble")).toBeVisible();
  await expect(page.locator(".assemble-editor form.assemble")).toHaveAttribute("data-product", "p8");

  // One row, one answer open: the form gives the panel up rather than queueing behind
  // the kit, and the highlight moves with it.
  await page.click('.product-list tbody tr:nth-child(3) button[data-act="edit"]');
  await expect(page.locator(".assemble-editor form.assemble")).toHaveCount(0);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p3");
  await expect(page.locator(".product-list tr.editing")).toHaveCount(1);
  await expect(page.locator(".product-list tr.product-edit-row")).toHaveCount(1);

  // And back the other way.
  await page.click('.product-list tbody tr:nth-child(3) button[data-act="assemble"]');
  await expect(page.locator(".product-editor .product-form")).toHaveCount(0);
  await expect(page.locator(".assemble-editor form.assemble")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("closing the kit removes its row", async ({ page }) => {
  await open(page);
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="assemble"]');
  await expect(page.locator(".product-list tr.product-edit-row")).toHaveCount(1);
  await page.click('.assemble-editor button[data-act="assemble-cancel"]');
  await expect(page.locator(".assemble-editor form.assemble")).toHaveCount(0);
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
  await expect(page.locator(".product-list tr.product-edit-row")).toHaveCount(0);
});

test("at 1440px neither the list nor a kit under its row scrolls sideways", async ({ page }) => {
  // A column added to either table, or a fourth button in a row, would push the
  // buttons behind a horizontal scrollbar nobody notices; this is what says so.
  await open(page, { width: 1440, height: 900 });
  await page.click('.product-list tbody tr:nth-child(4) button[data-act="assemble"]');
  const m = await page.evaluate(() => {
    const table = document.querySelector(".product-table");
    const kit = document.querySelector("form.assemble");
    return {
      list: table.scrollWidth - table.clientWidth,
      kit: kit.scrollWidth - kit.clientWidth,
    };
  });
  expect(m.list).toBeLessThanOrEqual(1);
  expect(m.kit).toBeLessThanOrEqual(1);
});

test("every list on the catalogue page has its form beside it", async ({ page }) => {
  await open(page);
  // The pairs on this page below the catalogue's own cards: the relations and the pair
  // being related, the maintainers and the one being added.
  const pairs = await page.evaluate(() => [...document.querySelectorAll(".cat-cols")]
    .map((c) => {
      const main = c.querySelector(".cat-main");
      const side = c.querySelector(".cat-side");
      if (!main || !side) return null;
      const m = main.getBoundingClientRect();
      const s = side.getBoundingClientRect();
      // A closed panel is not a column: the products' one is hidden until a row is
      // opened, which is what gives the list the whole width in the meantime.
      if (!s.width) return null;
      return { beside: s.left >= m.right - 1, level: Math.abs(s.top - m.top) <= 1, width: s.width };
    }).filter(Boolean));

  expect(pairs.length).toBeGreaterThanOrEqual(2); // relations and maintainers, at least
  for (const p of pairs) {
    expect(p.beside).toBe(true);
    expect(p.width).toBeGreaterThanOrEqual(380);
  }
  // The relate form and the share form start level with their lists — they answer the
  // whole list rather than one row, so they sit at its top rather than at an offset.
  expect(pairs.filter((p) => p.level).length).toBeGreaterThanOrEqual(2);
  expect(page.__errors).toEqual([]);
});

test("the catalogues themselves are a list beside the one being created", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.setViewportSize({ width: 1600, height: 800 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountList());
  await page.waitForSelector(".cat-cols .cat-main table");

  const b = await page.evaluate(() => {
    const main = document.querySelector(".cat-cols > .cat-main").getBoundingClientRect();
    const side = document.querySelector(".cat-cols > .cat-side").getBoundingClientRect();
    return { beside: side.left >= main.right - 1, level: Math.abs(side.top - main.top) <= 1 };
  });
  expect(b.beside).toBe(true);
  expect(b.level).toBe(true);
  await expect(page.locator(".cat-side form.cat-new")).toBeVisible();
  expect(errors).toEqual([]);
});

test("below the breakpoint every pair stacks, form under list", async ({ page }) => {
  await open(page, { width: 1100, height: 800 });
  const stacked = await page.evaluate(() => [...document.querySelectorAll(".cat-cols")]
    .map((c) => {
      const main = c.querySelector(".cat-main");
      const side = c.querySelector(".cat-side");
      if (!main || !side || !side.getBoundingClientRect().width) return null;
      return side.getBoundingClientRect().top >= main.getBoundingClientRect().bottom - 1;
    }).filter((x) => x !== null));
  expect(stacked.length).toBeGreaterThanOrEqual(2);
  for (const s of stacked) expect(s).toBe(true);
});

test("the catalogue's own two cards are read side by side, not down the left edge", async ({ page }) => {
  await open(page);
  // The first screenful used to be two 640px cards under one another with the whole
  // right half of a widened page empty — the page was wide and did not read as wide.
  const b = await page.evaluate(() => {
    const [left, right] = [...document.querySelectorAll(".grid2 > section")]
      .map((s) => s.getBoundingClientRect());
    const main = document.querySelector("main") || document.body;
    return left && right
      ? { beside: right.left >= left.right - 1, level: Math.abs(right.top - left.top) <= 1,
        covered: (right.right - left.left) / main.getBoundingClientRect().width }
      : null;
  });
  expect(b).not.toBeNull();
  expect(b.beside).toBe(true);
  expect(b.level).toBe(true);
  // Between them they use the page rather than a column of it.
  expect(b.covered).toBeGreaterThan(0.8);
});

test("a number field is drawn like every other field", async ({ page }) => {
  await open(page);
  // Rank is type="number" and was the one control on the page wearing the browser's
  // own default while the inputs beside it wore the console's.
  const b = await page.evaluate(() => {
    const form = document.querySelector("form.cat-meta");
    const box = (el) => el.getBoundingClientRect();
    return {
      rank: box(form.querySelector('input[name="rank"]')).width,
      text: box(form.querySelector('input[name="languages"]')).width,
      border: getComputedStyle(form.querySelector('input[name="rank"]')).borderTopWidth,
    };
  });
  expect(Math.round(b.rank)).toBe(Math.round(b.text));
  expect(b.border).toBe("1px");
});
