// End-to-end coverage for the application delete dialog (api/web/deletedialog.js,
// ADR-0468).
//
// The case behind it: deleting the Microsoft 365 example kept fourteen processes and
// their forms under Not assigned, because keeping them was the only thing the delete
// could do. The dialog now offers to delete everything — and since that cancels
// running instances and cannot be undone, it must show what goes, say what outside
// the application stops working, and not run until the name has been typed.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/application-delete-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");
const ok = (page) => page.locator("[data-ok]");

test("the default keeps the contents and asks for no preview", async ({ page }) => {
  await page.locator("#open").click();
  await expect(ok(page)).toHaveText("Delete application");
  await expect(ok(page)).toBeEnabled();
  await ok(page).click();
  await expect(result(page)).toHaveText("keep");
  await expect(page.locator("#loads")).toHaveText("0");
  expect(page.__errors).toEqual([]);
});

test("deleting everything shows what goes and runs only once the name is typed", async ({ page }) => {
  await page.locator("#open").click();
  await page.locator('input[value="purge"]').check();

  const detail = page.locator("#purge-detail");
  await expect(detail).toContainText("1 deployed process (1 version)");
  await expect(detail).toContainText("2 running instances will be cancelled");
  await expect(detail).toContainText("Teams Phone: Telefonzentrale (proc_m365_telefonzentrale)");
  await expect(detail).toContainText("m365-telefonie v1");
  await expect(detail).toContainText("1 form");
  // The reported blocker, now named as a consequence rather than a refusal.
  await expect(detail).toContainText("m365-telefonzentrale");
  await expect(detail).toContainText("ordering it will fail");
  await expect(detail).toContainText("This cannot be undone");

  await expect(ok(page)).toHaveText("Delete everything permanently");
  await expect(ok(page)).toBeDisabled();
  await page.locator("#purge-confirm").fill("M36");
  await expect(ok(page)).toBeDisabled();
  await page.locator("#purge-confirm").fill("M365");
  await expect(ok(page)).toBeEnabled();
  await ok(page).click();
  await expect(result(page)).toHaveText("purge");
  expect(page.__errors).toEqual([]);
});

test("a refusal from the preview keeps the purge disarmed", async ({ page }) => {
  await page.locator("#open-refused").click();
  await page.locator('input[value="purge"]').check();
  await expect(page.locator("#purge-detail")).toContainText("eligibility v2");
  await expect(page.locator("#purge-confirm")).toHaveCount(0);
  await expect(ok(page)).toBeDisabled();

  // Going back to the default is still possible.
  await page.locator('input[value="keep"]').check();
  await expect(ok(page)).toBeEnabled();
});

test("a caller who may not cancel instances is told so and cannot purge", async ({ page }) => {
  await page.locator("#open-modeler").click();
  await page.locator('input[value="purge"]').check();
  await expect(page.locator("#purge-detail")).toContainText("needs the operator role");
  await page.locator("#purge-confirm").fill("Busy");
  await expect(ok(page)).toBeDisabled();
});

test("Escape cancels without deleting", async ({ page }) => {
  await page.locator("#open").click();
  await page.keyboard.press("Escape");
  await expect(result(page)).toHaveText("cancelled");
});
