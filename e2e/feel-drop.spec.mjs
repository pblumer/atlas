// End-to-end coverage for dropping a variable into a FEEL editor at the drop point
// (api/web/code-editor.js). A row dragged from the Modeler's Variables panel carries
// the variable name as a text/plain payload; the code editor (which backs every FEEL
// field and the script editors) inserts it where the pointer is, never ahead of the
// locked '=' fx prefix, and re-highlights the field.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/feel-drop-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("a variable dropped at the end of a FEEL field is inserted there", async ({ page }) => {
  // Drop near the right edge of the field, past the text — maps to the end.
  const r = await page.evaluate(() => window.__rect("plain"));
  await page.evaluate(({ x, y }) => window.__drop("plain", "total", x, y),
    { x: r.right - 4, y: r.top + r.height / 2 });

  await expect.poll(() => page.evaluate(() => window.__value("plain"))).toBe("a + total");
  // The backdrop re-highlighted with the inserted variable.
  expect(await page.evaluate(() => window.__highlightText("plain"))).toContain("total");
  expect(page.__errors).toEqual([]);
});

test("a variable dropped at the start of a FEEL field lands there, not appended", async ({ page }) => {
  // Aim at the far left of a plain field — the insertion honours the drop point
  // rather than the field's end (proving it tracks the pointer, not the old caret).
  const r = await page.evaluate(() => window.__rect("plain"));
  await page.evaluate(({ x, y }) => window.__drop("plain", "total", x, y),
    { x: r.left + 1, y: r.top + r.height / 2 });

  await expect.poll(() => page.evaluate(() => window.__value("plain"))).toBe("totala + ");
  expect(page.__errors).toEqual([]);
});

test("a drop never lands ahead of the locked '=' prefix", async ({ page }) => {
  // Aim at the far left of the fx field, over the dimmed '=' — the insertion must be
  // clamped to just after it, never before.
  const r = await page.evaluate(() => window.__rect("locked"));
  await page.evaluate(({ x, y }) => window.__drop("locked", "amount", x, y),
    { x: r.left + 1, y: r.top + r.height / 2 });

  const value = await page.evaluate(() => window.__value("locked"));
  expect(value.startsWith("=")).toBe(true);
  expect(value).toBe("=amount");
  expect(page.__errors).toEqual([]);
});
