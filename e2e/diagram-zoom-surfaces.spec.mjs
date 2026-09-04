// Every diagram surface in Atlas carries the same zoom control
// (ADR-draft-shared-ui-primitives).
//
// Before this, zoom was a property of whichever library drew the diagram rather than
// of the diagram: diagram-js has always zoomed on ctrl+wheel, but nothing on screen
// said so, so on the BPMN canvas, the class canvas and the DMN editor the ability was
// there and undiscoverable. These drive the real modules through their own harnesses
// and assert the control is on each of them and drives the canvas underneath.
import { test, expect } from "@playwright/test";

const zoomIn = (page) => page.locator(".dzoom button[aria-label='Zoom in']");
const level = (page) => page.locator(".dzoom-level");

// factorRose asserts the canvas actually moved, by reading the factor the control
// states rather than by measuring pixels: the canvas is what owns the zoom here, and
// the stated percentage is read back from it.
async function factorRose(page, act) {
  const before = await level(page).textContent();
  await act();
  await expect(level(page)).not.toHaveText(before);
  const after = await level(page).textContent();
  expect(parseInt(after, 10)).toBeGreaterThan(parseInt(before, 10));
}

test.describe("the BPMN canvas", () => {
  test.beforeEach(async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.__errors = errors;
    await page.goto("/editor-bar-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mount());
    await expect(page.locator(".dzoom")).toBeVisible();
  });

  test("carries the shared control, and it drives the canvas", async ({ page }) => {
    // The editor fits on import, so the control opens stating what the canvas chose.
    await expect(level(page)).toHaveText(/%$/);
    await factorRose(page, () => zoomIn(page).click());
    expect(page.__errors).toEqual([]);
  });

  test("the control states what the canvas does behind its back", async ({ page }) => {
    // Fitting through the canvas rather than the control must still be reflected:
    // every view in the editor fits after import, and the replay refits on each step.
    await zoomIn(page).click();
    await zoomIn(page).click();
    const zoomed = await level(page).textContent();
    await page.evaluate(() => window.__atlasModeler.get("canvas").zoom("fit-viewport"));
    await expect(level(page)).not.toHaveText(zoomed);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("the class canvas", () => {
  test("carries the shared control, and it drives the canvas", async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("/infomodel-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mount());
    await expect(page.locator(".dzoom")).toBeVisible();

    await factorRose(page, () => zoomIn(page).click());
    expect(errors).toEqual([]);
  });
});

test.describe("the DMN editor", () => {
  test.beforeEach(async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.__errors = errors;
    await page.goto("/dmn-editor-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => { window.__open(); });
    await expect(page.locator(".dmn-view-tab").first()).toBeVisible({ timeout: 20000 });
  });

  test("the requirements graph zooms; the decision table has no zoom control", async ({ page }) => {
    // dmn-js opens on the requirements graph: a diagram, so the control is on it.
    await expect(page.locator(".dzoom")).toBeVisible();
    await factorRose(page, () => zoomIn(page).click());

    // Switching to the decision table takes the control away rather than leaving a
    // button that does nothing over a table.
    await page.locator(".dmn-view-tab", { hasText: "Stufe" }).click();
    await expect(page.locator(".dzoom")).toBeHidden();

    // And back: the graph is a diagram again.
    await page.locator(".dmn-view-tab").first().click();
    await expect(page.locator(".dzoom")).toBeVisible();
    expect(page.__errors).toEqual([]);
  });
});
