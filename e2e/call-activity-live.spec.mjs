// End-to-end coverage for the call-activity drill-down in the Operations *live* view
// (api/web/editor.js mountLive, ADR-0076): double-clicking the "+" BPMN draws on a
// call activity follows the call. With one instance in view that means the child
// instance this caller actually started — the running process, on its own live view.
// Under "All instances" there is no single child to mean, so it opens the called
// process's own live view instead. Driven through the real vendored bpmn-js against a
// mock `api`.
import { test, expect } from "@playwright/test";

// The live view puts a toolbar, a variables panel and a legend around the canvas, so
// the diagram gets more room here than the default viewport gives it.
test.use({ viewport: { width: 1280, height: 800 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/call-activity-live-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// markerCenter is the screen position of the "+" bpmn-js drew on a shape — located by
// the `data-marker` attribute its own renderer writes, so the gesture's hit test and
// the drawing cannot drift apart silently.
async function markerCenter(page, elementId) {
  const box = await page
    .locator(`[data-element-id="${elementId}"] path[data-marker="sub-process"]`)
    .boundingBox();
  if (!box) throw new Error(`no sub-process marker on ${elementId}`);
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

test("with one instance in view, the + opens the child instance it started", async ({ page }) => {
  await page.evaluate(() => window.__mountInstance());
  await expect(page.locator('[data-element-id="CallActivity_1"]')).toBeVisible();

  // The cue names the callee before the pointer is anywhere near the 14px marker.
  const shape = await page.locator('[data-element-id="CallActivity_1"] .djs-hit').first().boundingBox();
  await page.mouse.move(shape.x + shape.width * 0.3, shape.y + shape.height * 0.3);
  await expect(page.locator(".ca-drill-tip")).toContainText("identitaet-lebenszyklus");

  const at = await markerCenter(page, "CallActivity_1");
  await page.mouse.dblclick(at.x, at.y);

  const want = await page.evaluate(() => `#/operations/p/${window.__CHILD_DEF}/i/${window.__CHILD}`);
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(want);
  expect(page.__errors, "page errors during the live view").toEqual([]);
});

test("under All instances, the + opens the called process's live view", async ({ page }) => {
  await page.evaluate(() => window.__mountAll());
  await expect(page.locator('[data-element-id="CallActivity_1"]')).toBeVisible();

  const at = await markerCenter(page, "CallActivity_1");
  await page.mouse.dblclick(at.x, at.y);

  // No one child to drill into, so it is the callee's newest deployed version.
  const want = await page.evaluate(() => `#/operations/p/${window.__CHILD_DEF}`);
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(want);
  expect(page.__errors, "page errors during the live view").toEqual([]);
});

test("double-clicking anywhere else on the call activity navigates nowhere", async ({ page }) => {
  await page.evaluate(() => window.__mountInstance());
  await expect(page.locator('[data-element-id="CallActivity_1"]')).toBeVisible();

  const shape = await page.locator('[data-element-id="CallActivity_1"] .djs-hit').first().boundingBox();
  await page.mouse.dblclick(shape.x + shape.width * 0.25, shape.y + shape.height * 0.25);

  // Give a navigation that should not happen time to happen.
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => location.hash)).toBe("");
  expect(page.__errors, "page errors during the live view").toEqual([]);
});
