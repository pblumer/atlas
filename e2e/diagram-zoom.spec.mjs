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

// A canvas that owns its zoom (diagram-js, the Panorama mesh) gets the same control
// driving it instead of the SVG-resizing default, so one set of buttons sits over
// every diagram in Atlas rather than three sets over some of them.
test.describe("over a canvas that owns its zoom", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/diagram-zoom-controller-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  test("the buttons drive the canvas rather than resizing anything", async ({ page }) => {
    await page.locator("#frame .dzoom button[aria-label='Zoom in']").click();
    expect(await page.evaluate(() => window.__z())).toBeGreaterThan(1);

    await page.locator("#frame .dzoom button[aria-label='Fit the diagram']").click();
    expect(await page.evaluate(() => window.__calls.some((c) => c[0] === "fit"))).toBe(true);
    expect(page.__errors).toEqual([]);
  });

  test("the control sits inside the frame and does not wrap it", async ({ page }) => {
    // A canvas pans internally instead of scrolling, so the control belongs in the
    // frame. Wrapping the frame would be a second box around a sized element and is
    // how a canvas loses its height.
    const inside = await page.evaluate(() =>
      !!document.getElementById("frame").querySelector(".dzoom"));
    expect(inside).toBe(true);
    expect(await page.evaluate(() => !!document.querySelector(".dzoom-wrap"))).toBe(false);
  });

  test("the control box does not steal clicks from the diagram under it", async ({ page }) => {
    // app.css already learned this the hard way for .panorama-tools: a floating box
    // over a diagram takes clicks meant for the shapes beneath it. Only the buttons
    // may be targets; the box around them must not be.
    const boxTakesPointer = await page.evaluate(() => {
      const el = document.querySelector("#frame .dzoom");
      return getComputedStyle(el).pointerEvents !== "none";
    });
    expect(boxTakesPointer).toBe(false);
    const buttonTakesPointer = await page.evaluate(() => {
      const el = document.querySelector("#frame .dzoom button");
      return getComputedStyle(el).pointerEvents === "auto";
    });
    expect(buttonTakesPointer).toBe(true);
  });
});

// The Panorama surfaces already had zoom buttons, in a box shared with other tools
// (undo/redo/save on the viewer, "release" on the mesh). Unifying them onto the
// shared control means mounting it into that box rather than floating a second
// control over the same canvas.
test.describe("mounted into an existing toolbox", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/diagram-zoom-controller-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  test("the control goes in the toolbox, beside what was already there", async ({ page }) => {
    const inBox = page.locator("#toolbox .dzoom");
    await expect(inBox).toHaveCount(1);
    // Mounted, so it is not positioned over anything and needs no wrapper.
    expect(await page.evaluate(() =>
      getComputedStyle(document.querySelector("#toolbox .dzoom")).position)).toBe("static");
    // The tool it was mounted beside is untouched.
    await expect(page.locator("#toolbox #other")).toBeVisible();
  });

  test("a mounted control drives its own canvas, not the other one", async ({ page }) => {
    await page.locator("#toolbox .dzoom button[aria-label='Zoom in']").click();
    expect(await page.evaluate(() => window.__mountedZ())).toBeGreaterThan(1);
    expect(await page.evaluate(() => window.__z())).toBe(1);
  });

  test("the stated factor is what the canvas settled on, not what was asked", async ({ page }) => {
    // A canvas clamps to its own range — the Panorama mesh does, and so does
    // diagram-js. Stating the request rather than the result would put a percentage
    // on screen that the diagram does not have.
    const before = await page.locator("#toolbox .dzoom-level").textContent();
    for (let i = 0; i < 10; i++) {
      await page.locator("#toolbox .dzoom button[aria-label='Zoom in']").click();
    }
    const stated = parseInt(await page.locator("#toolbox .dzoom-level").textContent(), 10);
    const actual = Math.round((await page.evaluate(() => window.__mountedZ())) * 100);
    expect(stated).toBe(actual);
    expect(before).not.toBe(String(stated) + "%");
  });
});
