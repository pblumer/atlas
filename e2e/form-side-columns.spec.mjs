// End-to-end coverage for the Design pane's resizable side columns (api/web/app.css,
// api/web/form-editor.js enhanceDesignLayout).
//
// The Design tab lets an author widen the Components palette and the Properties column
// with a drag (ADR-0028's editor, our own affordance on top of the vendored form-js
// Playground). Neither panel followed its column. form-js pins the properties panel
// inside its column to a fixed `--properties-panel-width: 250px`, so dragging the column
// wider only grew an empty white strip between the panel and the window's right edge —
// the column got the width, the panel it exists to hold did not — and dragging it
// narrower clipped the panel instead of shrinking it. The palette had the same defect on
// the left: form-js pins it to `--palette-width: 270px` and its tile groups to 236px, so
// a wider column left an empty band between the palette and the canvas (the palette no
// longer sat against its resizer), and a narrower one cut the tiles off.
//
// These tests hold the outcome: each panel is exactly as wide as its column, at the
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

// paletteGeom reports the palette column, the form-js palette inside it, the column's
// resizer, and how the tiles sit in that width. The column keeps form-js's own 1px
// right border, so the room the palette has to fill is the column's client width.
const paletteGeom = (page) => page.evaluate(() => {
  const box = (el) => { const b = el.getBoundingClientRect(); return { x: Math.round(b.x), right: Math.round(b.right), w: Math.round(b.width) }; };
  const col = document.querySelector(".fjs-pgl-palette-container");
  const inner = col.querySelector(".fjs-palette-container");
  const entries = inner.querySelector(".fjs-palette-entries");
  const resizer = col.nextElementSibling;
  const tiles = [...col.querySelectorAll(".fjs-palette-group")[0].querySelectorAll(".fjs-palette-field")];
  const top0 = Math.round(tiles[0].getBoundingClientRect().top);
  const roomRight = box(col).x + col.clientLeft + col.clientWidth;
  return {
    col: box(col), inner: box(inner), resizerClass: resizer.className,
    room: col.clientWidth,
    // What the bug looked like: an empty band between the palette and the canvas.
    gap: roomRight - box(inner).right,
    // And its mirror at a narrow width: tiles cut off at the column's right edge.
    clipped: tiles.filter((t) => Math.round(t.getBoundingClientRect().right) > roomRight).length,
    overflow: inner.scrollWidth - inner.clientWidth,
    // How many tiles the first group lays side by side.
    perRow: tiles.filter((t) => Math.round(t.getBoundingClientRect().top) === top0).length,
    // The width the tile groups lay out in: what a scrollbar or padding leaves of it.
    tileRoom: entries.clientWidth - parseFloat(getComputedStyle(entries).paddingLeft)
      - parseFloat(getComputedStyle(entries).paddingRight),
  };
});

// dragPalette drags the palette's resizer (its next sibling) by dx pixels.
async function dragPalette(page, dx) {
  const resizer = page.locator(".fjs-pgl-palette-container").locator("xpath=following-sibling::*[1]");
  await expect(resizer).toHaveClass(/fv-side-resizer/);
  const r = await resizer.boundingBox();
  await page.mouse.move(r.x + r.width / 2, r.y + 400);
  await page.mouse.down();
  await page.mouse.move(r.x + r.width / 2 + dx, r.y + 400, { steps: 12 });
  await page.mouse.up();
  return resizer;
}

test("the palette fills its column at the default width, three tiles to a row", async ({ page }) => {
  await mount(page);
  let g = await paletteGeom(page);
  expect(g.resizerClass).toMatch(/fv-side-resizer/);
  // form-js's own --palette-width. It used to fall back to 200px, because the column
  // was measured before form-js had rendered the palette into it, which cut the
  // palette's third column of tiles and its search box off.
  expect(g.col.w).toBe(270);
  expect(g.inner.w).toBe(g.room);
  expect(g.gap).toBe(0);
  expect(g.clipped).toBe(0);
  expect(g.overflow).toBe(0);
  expect(g.perRow).toBe(3); // the reference modeler's look

  // Windows and Linux desktops show a classic scrollbar beside the tiles, which takes
  // up to 17px from them; Playwright's headless Chromium hides its scrollbars. Take
  // the same 17px with padding instead: still three to a row.
  const room0 = g.tileRoom;
  await page.addStyleTag({ content: ".fjs-palette-entries { padding-right: 17px; }" });
  g = await paletteGeom(page);
  expect(g.tileRoom).toBe(room0 - 17);
  expect(g.perRow).toBe(3);
  expect(g.clipped).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("dragging the palette wider widens the palette, not an empty band beside it", async ({ page }) => {
  await mount(page);
  const before = await paletteGeom(page);
  await dragPalette(page, 120);

  const after = await paletteGeom(page);
  expect(after.col.w).toBeGreaterThan(before.col.w + 80); // the drag took effect
  expect(after.inner.w).toBe(after.room);                 // and the palette came with it
  expect(after.gap).toBe(0);                              // it still meets the canvas
  expect(after.perRow).toBeGreaterThan(before.perRow);    // and the tiles use the room
  expect(after.overflow).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("a palette width a previous session saved is given to the palette, wide or narrow", async ({ page }) => {
  // 365px is the shape the bug was reported in: a column dragged wide, a 270px palette,
  // and 95px of empty band between it and the canvas.
  await mount(page, { paletteW: 365 });
  let g = await paletteGeom(page);
  expect(g.col.w).toBe(365);
  expect(g.inner.w).toBe(g.room);
  expect(g.gap).toBe(0);

  // The mirror case: narrower than form-js's pinned 270px must reflow, not clip.
  await mount(page, { paletteW: 140 });
  g = await paletteGeom(page);
  expect(g.col.w).toBe(140);
  expect(g.inner.w).toBe(g.room);
  expect(g.gap).toBe(0);
  expect(g.clipped).toBe(0);
  expect(g.overflow).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("double-clicking a side column's resizer restores its default width", async ({ page }) => {
  await mount(page);
  const resizer = await dragPalette(page, 150);
  expect((await paletteGeom(page)).col.w).toBeGreaterThan(400);

  await resizer.dblclick();
  const g = await paletteGeom(page);
  expect(g.col.w).toBe(270);
  expect(g.inner.w).toBe(g.room);
  expect(g.gap).toBe(0);
  // And the reset is what a reload brings back, not the dragged width.
  expect(await page.evaluate(() => localStorage.getItem("atlas.form.design.paletteW"))).toBe("270");

  // The properties column resets the same way, to the width it opened at — not to
  // a width sized by its content, which is what clearing the width used to leave.
  const p0 = (await geom(page)).col.w;
  const props = page.locator(".fjs-pgl-properties-container").locator("xpath=preceding-sibling::*[1]");
  const r = await props.boundingBox();
  await page.mouse.move(r.x + r.width / 2, r.y + 200);
  await page.mouse.down();
  await page.mouse.move(r.x + r.width / 2 - 180, r.y + 200, { steps: 12 });
  await page.mouse.up();
  expect((await geom(page)).col.w).toBeGreaterThan(p0 + 100);
  await props.dblclick();
  const pg = await geom(page);
  expect(pg.col.w).toBe(p0);
  expect(pg.inner.w).toBe(pg.col.w);
  expect(pg.strip).toBe(0);
  expect(page.__errors).toEqual([]);
});
