// End-to-end coverage for the Design pane's resizable side columns (api/web/app.css,
// api/web/form-editor.js enhanceDesignLayout).
//
// The Design tab lets an author widen the Components palette and the Properties column
// with a drag (ADR-0028's editor, our own affordance on top of the vendored form-js
// Playground). The palette followed its column; the properties panel did not. form-js
// pins the panel inside the column to a fixed `--properties-panel-width: 250px`, so
// dragging the column wider only grew an empty white strip between the panel and the
// window's right edge — the column got the width, the panel it exists to hold did not —
// and dragging it narrower clipped the panel instead of shrinking it.
//
// These tests hold the outcome: the panel is exactly as wide as its column, at the
// default width, after a drag, and for the width a previous session left behind.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1400, height: 900 } });

// mount opens the Design pane with a field selected (so the properties panel has
// something to describe), optionally seeding the widths a previous session saved.
async function mount(page, saved = {}) {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.evaluate((s) => {
    localStorage.clear();
    for (const [k, v] of Object.entries(s)) localStorage.setItem("atlas.form.design." + k, String(v));
  }, saved);
  await page.reload();
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountReal());
  const field = page.locator(".fjs-form-field-textfield").first();
  await expect(field).toBeVisible({ timeout: 25000 });
  await field.click();
  await expect(page.locator(".bio-properties-panel-group-header").first()).toBeVisible();
}

// geom reports the column, the panel inside it, and the room left over on the right.
const geom = (page) => page.evaluate(() => {
  const box = (el) => { const b = el.getBoundingClientRect(); return { x: Math.round(b.x), right: Math.round(b.right), w: Math.round(b.width) }; };
  const root = document.querySelector(".fjs-pgl-root");
  const col = document.querySelector(".fjs-pgl-properties-container");
  const inner = col.querySelector(".fjs-properties-container");
  const panel = col.querySelector(".bio-properties-panel");
  return {
    root: box(root), col: box(col), inner: box(inner), panel: box(panel),
    // What the bug looked like: unused width between the panel and the window edge.
    strip: box(root).right - box(panel).right,
    // And its mirror at a narrow width: panel wider than the column that holds it.
    overflow: col.scrollWidth - Math.round(col.getBoundingClientRect().width),
  };
});

test("the properties panel fills its column at the default width", async ({ page }) => {
  await mount(page);
  const g = await geom(page);
  expect(g.inner.w).toBe(g.col.w);
  expect(g.strip).toBe(0);
  expect(g.overflow).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("dragging the properties column wider widens the panel, not a white strip", async ({ page }) => {
  await mount(page);
  const before = await geom(page);

  // The right column's resizer is its own previous sibling; drag it left to widen.
  const resizer = page.locator(".fjs-pgl-properties-container").locator("xpath=preceding-sibling::*[1]");
  await expect(resizer).toHaveClass(/fv-side-resizer/);
  const r = await resizer.boundingBox();
  await page.mouse.move(r.x + r.width / 2, r.y + 200);
  await page.mouse.down();
  await page.mouse.move(r.x + r.width / 2 - 180, r.y + 200, { steps: 12 });
  await page.mouse.up();

  const after = await geom(page);
  expect(after.col.w).toBeGreaterThan(before.col.w + 100); // the drag took effect
  expect(after.inner.w).toBe(after.col.w);                 // and the panel came with it
  expect(after.strip).toBe(0);
  expect(after.overflow).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("a width a previous session saved is given to the panel, wide or narrow", async ({ page }) => {
  // 510px is the shape the bug was reported in: a column dragged wide, a 250px panel,
  // and 260px of white between them.
  await mount(page, { propsW: 510, paletteW: 290, previewOpen: 0 });
  let g = await geom(page);
  expect(g.col.w).toBe(510);
  expect(g.inner.w).toBe(510);
  expect(g.strip).toBe(0);

  // The mirror case: narrower than form-js's pinned 250px must shrink, not clip.
  await mount(page, { propsW: 140, previewOpen: 0 });
  g = await geom(page);
  expect(g.col.w).toBe(140);
  expect(g.inner.w).toBe(140);
  expect(g.overflow).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("collapsing the properties column leaves no gap at the right edge", async ({ page }) => {
  await mount(page, { propsW: 510 });
  // The chevron on the column's resizer collapses it to a rail.
  await page.locator(".fjs-pgl-properties-container")
    .locator("xpath=preceding-sibling::*[1]").locator(".fv-railbtn").click();
  const g = await geom(page);
  expect(g.col.w).toBe(0);
  expect(g.inner.w).toBeLessThanOrEqual(1); // form-js's own 1px border, clipped by the rail
  // The editor takes the freed width: main now reaches the root's right edge.
  const mainRight = await page.evaluate(() =>
    Math.round(document.querySelector(".fjs-pgl-main").getBoundingClientRect().right));
  expect(g.root.right - mainRight).toBeLessThanOrEqual(6); // only the resizer's own 6px
  expect(page.__errors).toEqual([]);
});
