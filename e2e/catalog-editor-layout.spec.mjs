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
  await page.waitForSelector(".product-cols tbody tr");
};

// The geometry of the open panel, the row it belongs to and the list beside it.
const boxes = async (page) => page.evaluate(() => {
  const r = (sel) => {
    const el = document.querySelector(sel);
    return el ? el.getBoundingClientRect().toJSON() : null;
  };
  return {
    panel: r(".product-side"),
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
    panel: document.querySelector(".product-side").getBoundingClientRect().top,
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
    panel: document.querySelector(".product-side").getBoundingClientRect().top,
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
  await page.locator(".product-side").scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => document.querySelector(".product-side").getBoundingClientRect().top);
  await page.evaluate(() => window.scrollBy(0, 120));
  await page.waitForTimeout(80);
  const after = await page.evaluate(() => {
    const r = document.querySelector(".product-side").getBoundingClientRect();
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

test("the kit opens beside its row too, and takes the panel from the form", async ({ page }) => {
  await open(page);

  // The kit answers a question about one row exactly as the form does — what this
  // product is made of — so it opens in the same place, level with that row.
  await page.click('.product-list tbody tr:nth-child(8) button[data-act="assemble"]');
  await expect(page.locator(".assemble-editor form.assemble")).toBeVisible();

  const b = await boxes(page);
  expect(b.panel.left).toBeGreaterThanOrEqual(b.list.right - 1);
  expect(Math.abs(b.panel.top - b.row.top)).toBeLessThanOrEqual(4);
  await expect(page.locator(".assemble-editor form.assemble")).toHaveAttribute("data-product", "p8");

  // One row, one answer open: the form gives the panel up rather than queueing behind
  // the kit, and the highlight moves with it.
  await page.click('.product-list tbody tr:nth-child(3) button[data-act="edit"]');
  await expect(page.locator(".assemble-editor form.assemble")).toHaveCount(0);
  await expect(page.locator(".product-editor input[name=id]")).toHaveValue("p3");
  await expect(page.locator(".product-list tr.editing")).toHaveCount(1);

  // And back the other way.
  await page.click('.product-list tbody tr:nth-child(3) button[data-act="assemble"]');
  await expect(page.locator(".product-editor .product-form")).toHaveCount(0);
  await expect(page.locator(".assemble-editor form.assemble")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("closing the kit gives the width back to the list", async ({ page }) => {
  await open(page);
  const wide = await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width);
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="assemble"]');
  expect(await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width))
    .toBeLessThan(wide);

  await page.click('.assemble-editor button[data-act="assemble-cancel"]');
  await expect(page.locator(".assemble-editor form.assemble")).toHaveCount(0);
  await expect(page.locator(".product-list tr.editing")).toHaveCount(0);
  const back = await page.evaluate(() => document.querySelector(".product-list").getBoundingClientRect().width);
  expect(Math.round(back)).toBe(Math.round(wide));
});

test("at the width the columns are offered, neither is drawn narrower than it holds", async ({ page }) => {
  // The breakpoint is a measurement, not a taste: the list cannot be drawn under its
  // min-content width and neither can the kit, and below the width where both fit the
  // page stacks instead. A column added to either table, or a fourth button in a row,
  // moves that number — and this is what says so, rather than a reader finding the
  // remove button behind a horizontal scrollbar nobody notices.
  await open(page, { width: 1440, height: 900 });
  await page.click('.product-list tbody tr:nth-child(4) button[data-act="assemble"]');
  const m = await page.evaluate(() => {
    const table = document.querySelector(".product-table");
    const kit = document.querySelector("form.assemble");
    return {
      layout: getComputedStyle(document.querySelector(".product-cols")).display,
      list: table.scrollWidth - table.clientWidth,
      kit: kit.scrollWidth - kit.clientWidth,
    };
  });
  expect(m.layout).toBe("flex"); // the two columns really are in force at this width
  expect(m.list).toBeLessThanOrEqual(1);
  expect(m.kit).toBeLessThanOrEqual(1);
});

test("every list on the catalogue page has its form beside it", async ({ page }) => {
  await open(page);
  // Three pairs on this page: the products and the panel that edits them, the
  // relations and the pair being related, the maintainers and the one being added.
  // The products' panel is empty until a row is opened, so it is not counted here.
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
