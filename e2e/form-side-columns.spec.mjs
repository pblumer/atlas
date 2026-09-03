// End-to-end coverage for the Design pane's resizable side columns (api/web/app.css,
// api/web/form-editor.js enhanceDesignLayout).
//
// The Design tab lets an author widen the Components palette and the Properties column
// with a drag (ADR-0028's editor, our own affordance on top of the vendored form-js
// Playground). Neither column passed its width on to what it holds. form-js pins the
// properties panel to `--properties-panel-width: 250px` and the palette to
// `--palette-width: 270px`, with the tile grid inside the palette fixed at 236px, so
// dragging a column wider only grew an empty white strip beside its contents — the
// column got the width, the thing it exists to hold did not — and dragging it narrower
// clipped those contents instead of shrinking them.
//
// The palette had the narrow case out of the box: the width it opens with is captured
// from the rendered column, the palette renders a frame later than that capture, and
// the fallback that stood in was 200px against a palette form-js draws at 270 — so the
// third tile of every row was cut off by the column's own edge before anyone touched a
// divider.
//
// These tests hold the outcome for both columns: what a column holds is exactly as wide
// as the column, at the width it opens with, after a drag, and for a width a previous
// session left behind.
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

// --- Components palette ------------------------------------------------------
// palette reports the column, the palette inside it, and how the tiles are laid out.
const palette = (page) => page.evaluate(() => {
  const w = (el) => Math.round(el.getBoundingClientRect().width);
  const col = document.querySelector(".fjs-pgl-palette-container");
  const inner = col.querySelector(".fjs-palette-container");
  const tiles = [...col.querySelectorAll(".fjs-palette-field")];
  const firstRowTop = tiles.length ? Math.round(tiles[0].getBoundingClientRect().top) : 0;
  const colRight = col.getBoundingClientRect().right;
  return {
    col: w(col), inner: w(inner),
    // The white strip: column width the palette never received. 1px is form-js's border.
    strip: w(col) - w(inner),
    // The mirror: content the column is cutting off rather than shrinking.
    clipped: col.scrollWidth - col.clientWidth,
    tilesPerRow: tiles.filter((t) => Math.round(t.getBoundingClientRect().top) === firstRowTop).length,
    tileOverhang: tiles.some((t) => t.getBoundingClientRect().right > colRight + 0.5),
  };
});

test("the palette opens at form-js's own width, with no tile cut off", async ({ page }) => {
  await mount(page);
  const g = await palette(page);
  expect(g.col).toBe(270);        // --palette-width, not a fallback of our own invention
  expect(g.strip).toBeLessThanOrEqual(1);
  expect(g.clipped).toBe(0);
  expect(g.tileOverhang).toBe(false);
  expect(g.tilesPerRow).toBe(3);  // the reference modeler's three 72px tiles per row
  expect(page.__errors).toEqual([]);
});

test("dragging the palette wider fills it with tiles, not white space", async ({ page }) => {
  await mount(page, { paletteW: 420 });
  const g = await palette(page);
  expect(g.col).toBe(420);
  expect(g.strip).toBeLessThanOrEqual(1);
  expect(g.tileOverhang).toBe(false);
  expect(g.tilesPerRow).toBeGreaterThan(3); // the extra width goes to the grid, not a strip
  expect(page.__errors).toEqual([]);
});

test("dragging the palette narrower reflows the tiles instead of cutting them off", async ({ page }) => {
  await mount(page, { paletteW: 130 });
  const g = await palette(page);
  expect(g.col).toBe(130);
  expect(g.clipped).toBe(0);
  expect(g.tileOverhang).toBe(false);
  expect(g.tilesPerRow).toBe(1);
  expect(page.__errors).toEqual([]);
});
