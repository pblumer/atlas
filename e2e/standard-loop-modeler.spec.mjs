// End-to-end coverage for the BPMN standard loop in the Modeler Implement panel
// (api/web/editor.js, ADR-0132). Driven through the real vendored bpmn-js: the Loop
// section reads the marker an imported model carries, writing the mode draws (and
// clears) the marker on the shape, and the exported XML is what the engine compiles —
// so the icon and the property can never drift apart.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/standard-loop-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openLoop selects an activity and expands its Loop group (groups but General start
// collapsed), so the section's fields are on screen.
async function openLoop(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Loop" }).click();
}

test("the panel reads the standard loop an imported activity carries", async ({ page }) => {
  await openLoop(page, "Activity_loop");

  await expect(page.locator("#f-mi-mode")).toHaveValue("loop");
  await expect(page.locator("#f-mi-loopcond")).toHaveValue("not(approved)");
  await expect(page.locator("#f-mi-testbefore")).toHaveValue("before");
  await expect(page.locator("#f-mi-loopmax")).toHaveValue("4");
  // The shape shows the marker the property describes.
  await expect(page.locator('[data-element-id="Activity_loop"] [data-marker="loop"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("setting the mode to Loop draws the marker and exports the loop characteristics", async ({ page }) => {
  await openLoop(page, "Activity_plain");
  await expect(page.locator('[data-element-id="Activity_plain"] [data-marker="loop"]')).toHaveCount(0);

  await page.locator("#f-mi-mode").selectOption("loop");
  await page.locator("#f-mi-loopcond").fill("retry");
  await page.locator("#f-mi-loopcond").blur();
  await page.locator("#f-mi-loopmax").fill("7");
  await page.locator("#f-mi-loopmax").blur();

  // Property → icon: bpmn-js draws the loop marker from the characteristics we wrote.
  await expect(page.locator('[data-element-id="Activity_plain"] [data-marker="loop"]')).toHaveCount(1);

  const xml = await page.evaluate(() => window.__xml());
  const task = /<bpmn:serviceTask id="Activity_plain"[\s\S]*?<\/bpmn:serviceTask>/.exec(xml)[0];
  expect(task).toContain("standardLoopCharacteristics");
  expect(task).toContain('loopMaximum="7"');
  expect(task).toMatch(/<bpmn:loopCondition[^>]*>= retry<\/bpmn:loopCondition>/);
  // testBefore defaults to "after each run", so the flag is left off.
  expect(task).not.toContain("testBefore");
  expect(page.__errors).toEqual([]);
});

test("clearing the mode removes the marker and the loop characteristics", async ({ page }) => {
  await openLoop(page, "Activity_loop");
  await page.locator("#f-mi-mode").selectOption("none");

  await expect(page.locator('[data-element-id="Activity_loop"] [data-marker="loop"]')).toHaveCount(0);
  const xml = await page.evaluate(() => window.__xml());
  const task = /<bpmn:serviceTask id="Activity_loop"[\s\S]*?<\/bpmn:serviceTask>/.exec(xml)[0];
  expect(task).not.toContain("LoopCharacteristics");
  expect(task).not.toContain("loopCharacteristics");
  expect(page.__errors).toEqual([]);
});

test("switching from a standard loop to a multi-instance replaces the marker", async ({ page }) => {
  await openLoop(page, "Activity_loop");
  await page.locator("#f-mi-mode").selectOption("sequential");
  await page.locator("#f-mi-inputCollection").fill("items");
  await page.locator("#f-mi-inputCollection").blur();

  // The ↻ marker gives way to the ≡ one — one marker at a time, matching the mode.
  await expect(page.locator('[data-element-id="Activity_loop"] [data-marker="loop"]')).toHaveCount(0);
  await expect(page.locator('[data-element-id="Activity_loop"] [data-marker="sequential"]')).toHaveCount(1);

  const xml = await page.evaluate(() => window.__xml());
  const task = /<bpmn:serviceTask id="Activity_loop"[\s\S]*?<\/bpmn:serviceTask>/.exec(xml)[0];
  expect(task).toContain("multiInstanceLoopCharacteristics");
  expect(task).not.toContain("standardLoopCharacteristics");
  expect(page.__errors).toEqual([]);
});
