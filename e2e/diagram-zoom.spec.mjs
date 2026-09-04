// End-to-end coverage for the shared diagram zoom (api/web/diagram-zoom.js).
//
// A diagram is not a picture of a diagram: it must be possible to get closer to it.
// The framework-backed canvases (bpmn-js, dmn-js, the class canvas, the Panorama
// viewer) get that from diagram-js. A hand-drawn SVG — the DMN requirements graph
// is the one Atlas has — gets it from here, so that "a diagram can be zoomed" holds
// across the product rather than wherever a framework happened to supply it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/diagram-zoom-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const scale = (page) => page.evaluate(() => window.__scale());

test("the controls are there, named, and reachable by keyboard", async ({ page }) => {
  const controls = page.locator(".dzoom");
  await expect(controls).toHaveCount(1);
  // Named, because a bare + and − say nothing to a screen reader.
  await expect(page.locator(".dzoom button[aria-label='Zoom in']")).toHaveCount(1);
  await expect(page.locator(".dzoom button[aria-label='Zoom out']")).toHaveCount(1);
  await expect(page.locator(".dzoom button[aria-label='Fit the diagram']")).toHaveCount(1);
  // The current factor is stated, not left to be inferred from the picture.
  await expect(page.locator(".dzoom-level")).toHaveText(/%$/);
  expect(page.__errors).toEqual([]);
});

test("zoom in and out change the diagram's rendered size", async ({ page }) => {
  const start = await scale(page);
  await page.locator(".dzoom button[aria-label='Zoom in']").click();
  const bigger = await scale(page);
  expect(bigger).toBeGreaterThan(start);

  await page.locator(".dzoom button[aria-label='Zoom out']").click();
  const backAgain = await scale(page);
  expect(backAgain).toBeLessThan(bigger);
  expect(page.__errors).toEqual([]);
});

test("fit brings the whole diagram inside the frame", async ({ page }) => {
  for (let i = 0; i < 4; i++) {
    await page.locator(".dzoom button[aria-label='Zoom in']").click();
  }
  expect(await scale(page)).toBeGreaterThan(1);

  await page.locator(".dzoom button[aria-label='Fit the diagram']").click();
  const box = await page.locator("#diagram").boundingBox();
  const frame = await page.locator("#canvas").boundingBox();
  expect(box.width).toBeLessThanOrEqual(frame.width + 1);
  expect(box.height).toBeLessThanOrEqual(frame.height + 1);
  expect(page.__errors).toEqual([]);
});

test("ctrl+wheel zooms, a plain wheel does not", async ({ page }) => {
  await page.locator("#canvas").hover();
  const start = await scale(page);

  // A plain wheel is the page's, not the diagram's: scrolling past a diagram must
  // not silently rescale it.
  await page.mouse.wheel(0, -120);
  expect(await scale(page)).toBeCloseTo(start, 5);

  await page.keyboard.down("Control");
  await page.mouse.wheel(0, -120);
  await page.keyboard.up("Control");
  expect(await scale(page)).toBeGreaterThan(start);
  expect(page.__errors).toEqual([]);
});

test("zoom stays within bounds however hard it is pushed", async ({ page }) => {
  for (let i = 0; i < 30; i++) {
    await page.locator(".dzoom button[aria-label='Zoom in']").click();
  }
  const max = await scale(page);
  expect(max).toBeLessThanOrEqual(4);

  for (let i = 0; i < 60; i++) {
    await page.locator(".dzoom button[aria-label='Zoom out']").click();
  }
  expect(await scale(page)).toBeGreaterThanOrEqual(0.2);
  expect(page.__errors).toEqual([]);
});
