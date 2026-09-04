// End-to-end coverage for the shared dialog (api/web/dialog.js).
//
// Before it, 22 dialogs across ten files each built the same overlay by hand and each
// re-implemented the same behaviour — role, aria-modal, Escape, the backdrop click,
// the initial focus. Mostly they got it right, which is exactly why the omissions
// were hard to see: `infomodel-import.js` opened its report with the focus still
// behind it, and nothing said so. The parts that are easy to forget belong in one
// place that is tested once.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/dialog-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const dialog = (page) => page.locator(".modal-ov .modal");

test("it is a dialog, and it says so", async ({ page }) => {
  await page.evaluate(() => window.__open());
  await expect(dialog(page)).toHaveAttribute("role", "dialog");
  await expect(dialog(page)).toHaveAttribute("aria-modal", "true");
  await expect(dialog(page)).toHaveAttribute("aria-label", "Rename the thing");
  await expect(page.locator(".modal-head h2")).toHaveText("Rename the thing");
  expect(page.__errors).toEqual([]);
});

test("the focus moves into the dialog, and comes back when it closes", async ({ page }) => {
  await page.locator("#opener").focus();
  await page.evaluate(() => window.__open());

  // Into the dialog, on the first thing a person would type into.
  await expect(page.locator("#f-name")).toBeFocused();

  await page.keyboard.press("Escape");
  await expect(dialog(page)).toHaveCount(0);
  // Back where it came from: a dialog that drops the focus on the body leaves a
  // keyboard user at the top of the page.
  await expect(page.locator("#opener")).toBeFocused();
  expect(page.__errors).toEqual([]);
});

test("a dialog with nothing to type into still takes the focus", async ({ page }) => {
  await page.evaluate(() => {
    const body = document.createElement("div");
    body.textContent = "Just a report.";
    window.__open({ body, actions: [{ label: "Close", kind: "primary", value: null }] });
  });
  // infomodel-import.js's defect: a report with no field opened with the focus
  // behind it. Something in the dialog must hold it.
  const inside = await page.evaluate(() =>
    document.querySelector(".modal-ov .modal").contains(document.activeElement));
  expect(inside).toBe(true);
});

test("tab does not walk out of the dialog", async ({ page }) => {
  await page.evaluate(() => window.__open());
  // Round the loop more times than the dialog has stops, forwards and back.
  for (let i = 0; i < 8; i++) await page.keyboard.press("Tab");
  expect(await page.evaluate(() =>
    document.querySelector(".modal-ov .modal").contains(document.activeElement))).toBe(true);
  for (let i = 0; i < 8; i++) await page.keyboard.press("Shift+Tab");
  expect(await page.evaluate(() =>
    document.querySelector(".modal-ov .modal").contains(document.activeElement))).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("Escape, the backdrop and the close button all close it, and say what closed it", async ({ page }) => {
  await page.evaluate(() => window.__open());
  await page.keyboard.press("Escape");
  await expect(dialog(page)).toHaveCount(0);

  await page.evaluate(() => window.__open());
  await page.locator(".modal-ov").click({ position: { x: 5, y: 5 } });
  await expect(dialog(page)).toHaveCount(0);

  await page.evaluate(() => window.__open());
  await page.locator(".modal-head [aria-label='Close']").click();
  await expect(dialog(page)).toHaveCount(0);

  // All three are a dismissal, which is not the same as pressing the action.
  expect(await page.evaluate(() => window.__closed)).toEqual([null, null, null]);
});

test("an action closes with its value", async ({ page }) => {
  await page.evaluate(() => window.__open());
  await page.locator(".modal-foot button", { hasText: "Rename" }).click();
  await expect(dialog(page)).toHaveCount(0);
  expect(await page.evaluate(() => window.__closed)).toEqual(["renamed"]);
  expect(page.__errors).toEqual([]);
});

test("a click inside the dialog is not a dismissal", async ({ page }) => {
  await page.evaluate(() => window.__open());
  await page.locator(".modal-body p").click();
  await expect(dialog(page)).toHaveCount(1);
  // Nor is a drag that starts inside and ends on the backdrop — selecting text in a
  // field and releasing outside is not "close without saving".
  await page.locator(".modal-body p").hover();
  await page.mouse.down();
  await page.locator(".modal-ov").hover({ position: { x: 5, y: 5 } });
  await page.mouse.up();
  await expect(dialog(page)).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
