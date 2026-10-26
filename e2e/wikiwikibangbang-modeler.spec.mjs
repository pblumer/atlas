// End-to-end coverage for the wikiwikibangbang Worker Type in the Modeler's Implement
// panel (api/web/editor.js, api/web/wikiwikibangbang-ops.js): each operation shows the
// fields it takes, and the task's extension states only what the chosen operation uses —
// the compiler refuses a value an operation does not use ("does not use space"), and a
// field the panel hides is one it cannot clear. Driven through the real vendored
// bpmn-js, asserting against the exported XML the deploy sends
// (ADR-draft-wikiwikibangbang-worker).
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/wikiwikibangbang-modeler-harness.html");
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

test("choosing wikiwikibangbang starts on Create item and shows what a create takes", async ({ page }) => {
  await select(page, "Activity_plain");
  await page.locator(".stkind-row[data-kind='wikiwikibangbang']").click();
  await page.locator(".pgroup-all[data-all='expand']").click();

  await expect(page.locator("#f-st-operation")).toHaveValue("create-item");
  for (const shown of ["space", "itemType", "title", "parent", "properties", "matchField", "text", "blocks"]) {
    await expect(page.locator(`#f-st-${shown}`)).toBeVisible();
  }
  for (const hidden of ["item", "transition", "comment", "query", "maxResults", "labels"]) {
    await expect(page.locator(`#f-st-${hidden}`)).toHaveCount(0);
  }
  const task = await taskXML(page, "Activity_plain");
  expect(task).toContain("wikiwikibangbangConnector");
  expect(task).toContain('operation="create-item"');
  expect(page.__errors).toEqual([]);
});

test("switching a create to a transition drops what only a create takes", async ({ page }) => {
  await select(page, "Activity_create");
  await page.locator("#f-st-operation").selectOption("transition-item");

  await expect(page.locator("#f-st-item")).toBeVisible();
  await expect(page.locator("#f-st-transition")).toBeVisible();
  await expect(page.locator("#f-st-space")).toHaveCount(0);
  const task = await taskXML(page, "Activity_create");
  expect(task).toContain('operation="transition-item"');
  for (const gone of ["space=", "itemType=", "title=", "properties=", "matchField=", "text="]) {
    expect(task).not.toContain(gone);
  }
  expect(task).toContain('connector="wiki"');
  expect(page.__errors).toEqual([]);
});

test("a query shows its bound and keeps a result variable", async ({ page }) => {
  await select(page, "Activity_create");
  await page.locator("#f-st-operation").selectOption("query-items");

  await expect(page.locator("#f-st-query")).toBeVisible();
  await expect(page.locator("#f-st-maxResults")).toBeVisible();
  await expect(page.locator("#f-st-resultVariable")).toBeVisible();
  await page.locator("#f-st-query").fill("type = task AND state != done");
  await page.locator("#f-st-query").blur();
  const task = await taskXML(page, "Activity_create");
  expect(task).toContain('operation="query-items"');
  expect(task).toContain('query="type = task AND state != done"');
  expect(task).toContain('resultVariable="aufgabe"');
  expect(page.__errors).toEqual([]);
});

test("properties given by a variable hide the inline values and keep the variable", async ({ page }) => {
  await select(page, "Activity_update");
  await expect(page.locator("#f-st-propertiesVariable")).toHaveValue("aenderung");
  await expect(page.locator("#f-st-properties")).toHaveCount(0);
  await expect(page.locator("#f-st-labels")).toBeVisible();
  await page.locator("#f-st-labels").fill("onboarding, it");
  await page.locator("#f-st-labels").blur();

  const task = await taskXML(page, "Activity_update");
  expect(task).toContain('propertiesVariable="aenderung"');
  expect(task).toContain('item="=aufgabe.id"');
  expect(task).toContain('labels="onboarding, it"');
  expect(page.__errors).toEqual([]);
});
