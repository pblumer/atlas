// End-to-end coverage for when the completion popup of a FEEL field closes on its own
// (api/web/code-editor.js, which backs every FEEL field and the script editors). The
// popup belongs to the word at the caret: picking an item, typing a word out in full,
// moving the caret away or the page writing a value in each leave nothing to offer, and
// none of them may need Escape to clear it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/feel-completion-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.locator("#expr").click();
});

const popup = (page) => page.locator(".code-editor .feel-pop");
const option = (page, label) => popup(page).locator(".feel-opt").filter({ has: page.locator(".feel-opt-label", { hasText: new RegExp(`^${label.replace(/\./g, "\\.")}$`) }) });

test("a picked variable closes the popup instead of offering itself again", async ({ page }) => {
  await page.keyboard.type("if Vorn");
  await expect(option(page, "Vorname")).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("if Vorname");
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a picked literal closes the popup, and the next Enter breaks the line", async ({ page }) => {
  await page.keyboard.type("else fal");
  await expect(option(page, "false")).toBeVisible();
  await page.keyboard.press("Tab");
  await expect(page.locator("#expr")).toHaveValue("else false");
  await expect(popup(page)).toBeHidden();
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("else false\n");
  expect(page.__errors).toEqual([]);
});

test("a word typed out in full leaves nothing to complete", async ({ page }) => {
  await page.keyboard.type("else fals");
  await expect(option(page, "false")).toBeVisible();
  await page.keyboard.type("e");
  await expect(popup(page)).toBeHidden();
  // Enter is a plain Enter again, not a pick that changes nothing.
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("else false\n");
  expect(page.__errors).toEqual([]);
});

test("Enter on a word typed out in full breaks the line while longer offers remain", async ({ page }) => {
  await page.keyboard.type("Vorname");
  // VornameKind still completes it, so the list stays, with the exact word first.
  await expect(option(page, "VornameKind")).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("Vorname\n");
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a function typed out in full is still offered, for its parentheses", async ({ page }) => {
  await page.keyboard.type("substring");
  await expect(option(page, "substring")).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("substring()");
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a member whose pick declares it is still offered when typed out in full", async ({ page }) => {
  await page.keyboard.type("server.TENANT_ID");
  await expect(option(page, "server.TENANT_ID")).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(page.locator("#expr")).toHaveValue("server.TENANT_ID");
  await expect(popup(page)).toBeHidden();
  expect(await page.evaluate(() => window.__accepted)).toEqual(["server.TENANT_ID"]);
  expect(page.__errors).toEqual([]);
});

test("moving the caret away closes the popup", async ({ page }) => {
  await page.keyboard.type("x + Vorn");
  await expect(option(page, "Vorname")).toBeVisible();
  await page.keyboard.press("ArrowLeft");
  await expect(popup(page)).toBeHidden();

  await page.keyboard.press("End");
  await page.keyboard.type("a");
  await expect(option(page, "Vorname")).toBeVisible();
  await page.locator("#expr").click({ position: { x: 4, y: 10 } });
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a closing bracket typed over its pair closes the popup", async ({ page }) => {
  await page.keyboard.type("substring(Vorn");
  await expect(page.locator("#expr")).toHaveValue("substring(Vorn)");
  await expect(option(page, "Vorname")).toBeVisible();
  await page.keyboard.type(")");
  await expect(page.locator("#expr")).toHaveValue("substring(Vorn)");
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a value the page writes in opens no popup, but is highlighted", async ({ page }) => {
  // The FEEL assistant puts its proposal into the editor this way.
  await page.evaluate(() => window.__setValue('if Vorname != null then "x" else fal'));
  await expect(popup(page)).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.__highlightText())).toContain("else fal");
  expect(page.__errors).toEqual([]);
});

test("Ctrl+Space still lists a word typed out in full", async ({ page }) => {
  await page.keyboard.type("false");
  await expect(popup(page)).toBeHidden();
  await page.keyboard.press("Control+Space");
  await expect(option(page, "false")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(popup(page)).toBeHidden();
  expect(page.__errors).toEqual([]);
});
