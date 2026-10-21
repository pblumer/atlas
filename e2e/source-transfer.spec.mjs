// Moving an application's source to another installation, as the Console asks it
// (ADR-0467, amended 2026-10-21).
//
// A download carries the constant values only when that is ticked, and lists what the
// processes need from the target that no download carries. An import names what the
// target still lacks before anybody publishes. Which URL the dialog downloads, what it
// lists and what it leaves out are things no Go test can see; these drive the real
// module through its own harness.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/source-transfer-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");
const dialog = (page) => page.locator(".source-download");

test("a download lists what the target needs and carries the constants when ticked", async ({ page }) => {
  page.evaluate(() => window.__download(window.NEEDS));
  await expect(dialog(page)).toBeVisible();
  for (const [kind, n] of [["server-constant", 2], ["application-constant", 1], ["worker", 1]]) {
    await expect(dialog(page).locator(`.need-group[data-kind="${kind}"] li`)).toHaveCount(n);
  }
  await expect(dialog(page).locator('[data-need="server-constant/LANGUAGE"]')).toContainText("has a fallback");
  await expect(dialog(page).locator('[data-need="worker/Postausgang"]')).toContainText("mail");
  await expect(dialog(page).locator(".source-unread")).toContainText("halb");

  // Ticked by default when the application holds values: TENANT_ID travels.
  const box = dialog(page).locator('[name="withConstants"]');
  await expect(box).toBeChecked();
  await expect(dialog(page).locator('[data-need="application-constant/TENANT_ID"]')).toContainText("travels with the download");
  await box.uncheck();
  await expect(dialog(page).locator('[data-need="application-constant/TENANT_ID"]')).toContainText("set it on the target");
  await box.check();
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/source?constants=true");
  expect(page.__errors).toEqual([]);
});

test("a download without the constants asks for the plain tree", async ({ page }) => {
  page.evaluate(() => window.__download(window.NEEDS));
  await dialog(page).locator('[name="withConstants"]').uncheck();
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/source");
});

test("an application holding no values offers none, and cancelling downloads nothing", async ({ page }) => {
  page.evaluate(() => window.__download({ applicationId: "a1", constants: 0, unread: [], needs: [] }));
  const box = dialog(page).locator('[name="withConstants"]');
  await expect(box).toBeDisabled();
  await expect(dialog(page)).toContainText("holds no constant values here");
  await expect(dialog(page)).toContainText("read nothing from the installation");
  await dialog(page).locator("[data-cancel]").click();
  await expect(result(page)).toHaveText("cancelled");
  await expect(dialog(page)).toHaveCount(0);
});

test("an import names only what the target still lacks", async ({ page }) => {
  const answer = {
    name: "Bedarf",
    needs: [
      { kind: "server-constant", name: "LANGUAGE", type: "string", fallback: true, state: "fallback" },
      { kind: "server-constant", name: "MAIL_DOMAIN", type: "string", state: "missing" },
      { kind: "application-constant", name: "TENANT_ID", type: "string", state: "type-mismatch", detail: "number" },
      { kind: "worker", name: "Postausgang", type: "mail", state: "missing" },
      { kind: "worker", name: "Archiv", type: "s3", state: "configured" },
    ],
    constantsNotSet: ["REGION"],
    needsUnread: [],
  };
  expect(await page.evaluate((a) => window.__report(a), answer)).toBe(true);
  page.evaluate((a) => window.__importNeeds(a), answer);
  const report = page.locator(".import-needs");
  await expect(report).toBeVisible();
  await expect(report.locator("h2")).toHaveText("Before Bedarf runs here");
  // A need the target answers, or a fallback covers, is not listed.
  await expect(report.locator("[data-need]")).toHaveCount(3);
  await expect(report.locator('[data-need="server-constant/MAIL_DOMAIN"]')).toContainText("missing");
  await expect(report.locator('[data-need="application-constant/TENANT_ID"]')).toContainText("held with another type: number");
  await expect(report.locator('[data-need="worker/Archiv"]')).toHaveCount(0);
  await expect(report.locator('.need-group[data-kind="not-set"]')).toContainText("REGION");
  await report.locator("[data-done]").click();
  await expect(result(page)).toHaveText("closed");

  // Nothing missing, nothing unset: nothing to report.
  expect(await page.evaluate(() => window.__report({ needs: [{ kind: "worker", name: "x", state: "configured" }] }))).toBe(false);
  expect(page.__errors).toEqual([]);
});
