// End-to-end coverage for the CardDAV Worker Type in the Modeler's Implement panel
// (api/web/editor.js, api/web/carddav-ops.js): each operation shows the fields it takes,
// and the task's extension states only what the chosen operation uses — the compiler
// refuses a value an operation does not use ("does not use query"), and a field the panel
// hides is one it cannot clear. Driven through the real vendored bpmn-js, asserting
// against the exported XML the deploy sends (ADR-0490).
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/carddav-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

async function select(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-all[data-all='expand']").click();
}

async function taskXML(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:serviceTask id="${id}"[\\s\\S]*?</bpmn:serviceTask>`).exec(xml)[0];
}

test("choosing CardDAV starts on Find contacts and shows what a find takes", async ({ page }) => {
  await select(page, "Activity_plain");
  await page.locator(".stkind-row[data-kind='carddav']").click();
  await page.locator(".pgroup-all[data-all='expand']").click();

  await expect(page.locator("#f-st-operation")).toHaveValue("find-contacts");
  await expect(page.locator("#f-st-query")).toBeVisible();
  await expect(page.locator("#f-st-field")).toBeVisible();
  await expect(page.locator("#f-st-group")).toBeVisible();
  await expect(page.locator("#f-st-maxResults")).toBeVisible();
  await expect(page.locator("#f-st-contactId")).toHaveCount(0);
  await expect(page.locator("#f-st-contact")).toHaveCount(0);
  const task = await taskXML(page, "Activity_plain");
  expect(task).toContain("carddavConnector");
  expect(task).toContain('operation="find-contacts"');
  expect(page.__errors).toEqual([]);
});

test("switching a find to an update drops what only a find takes", async ({ page }) => {
  await select(page, "Activity_find");
  await page.locator("#f-st-operation").selectOption("update-contact");

  await expect(page.locator("#f-st-contactId")).toBeVisible();
  await expect(page.locator("#f-st-query")).toHaveCount(0);
  const task = await taskXML(page, "Activity_find");
  expect(task).toContain('operation="update-contact"');
  for (const gone of ["query=", "field=", "group=", "maxResults="]) {
    expect(task).not.toContain(gone);
  }
  expect(task).toContain('connector="verkauf"');
  expect(page.__errors).toEqual([]);
});

test("a delete offers no result variable and writes none", async ({ page }) => {
  await select(page, "Activity_find");
  await page.locator("#f-st-operation").selectOption("delete-contact");

  await expect(page.locator("#f-st-resultVariable")).toHaveCount(0);
  const task = await taskXML(page, "Activity_find");
  expect(task).toContain('operation="delete-contact"');
  expect(task).not.toContain("resultVariable");
  expect(page.__errors).toEqual([]);
});

test("a contact given by a variable hides the inline values and keeps the variable", async ({ page }) => {
  await select(page, "Activity_update");
  await expect(page.locator("#f-st-contactVariable")).toHaveValue("aenderung");
  await expect(page.locator("#f-st-contact")).toHaveCount(0);
  await page.locator("#f-st-resultVariable").fill("kunde");
  await page.locator("#f-st-resultVariable").blur();

  const task = await taskXML(page, "Activity_update");
  expect(task).toContain('contactVariable="aenderung"');
  expect(task).toContain('contactId="=treffer[1].id"');
  expect(task).toContain('resultVariable="kunde"');
  expect(page.__errors).toEqual([]);
});
