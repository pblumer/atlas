// End-to-end coverage for the task retry budget in the Modeler Implement panel
// (api/web/editor.js, ADR-0135). Driven through the real vendored bpmn-js: every
// job-backed task kind offers one Retries property, reads what the imported model
// carries, and writes it back onto the extension the compiler reads — so what the
// panel shows and what the engine grants the job cannot drift apart.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/retries-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openRetries selects a task and expands its Failure handling group (groups but
// General start collapsed), so the Retries field is on screen.
async function openRetries(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Failure handling" }).click();
}

// taskXML returns the exported XML of one task element.
async function taskXML(page, tag, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:${tag} id="${id}"[\\s\\S]*?</bpmn:${tag}>`).exec(xml)[0];
}

test("a job worker reads its authored retries and writes changes to the task definition", async ({ page }) => {
  await openRetries(page, "Activity_worker");
  await expect(page.locator("#f-st-retries")).toHaveValue("5");

  await page.locator("#f-st-retries").fill("8");
  await page.locator("#f-st-retries").blur();

  const task = await taskXML(page, "serviceTask", "Activity_worker");
  expect(task).toMatch(/<zeebe:taskDefinition[^>]*retries="8"/);
  expect(task).toMatch(/<zeebe:taskDefinition[^>]*type="payment"/);
  expect(page.__errors).toEqual([]);
});

test("clearing the retries drops the attribute, leaving the engine default", async ({ page }) => {
  await openRetries(page, "Activity_worker");
  await page.locator("#f-st-retries").fill("");
  await page.locator("#f-st-retries").blur();

  const task = await taskXML(page, "serviceTask", "Activity_worker");
  expect(task).not.toContain("retries");
  expect(task).toContain('type="payment"');
  expect(page.__errors).toEqual([]);
});

test("a connector task carries its own retries attribute", async ({ page }) => {
  await openRetries(page, "Activity_rest");
  // No budget authored: the field is empty, meaning "the engine's default".
  await expect(page.locator("#f-st-retries")).toHaveValue("");

  await page.locator("#f-st-retries").fill("6");
  await page.locator("#f-st-retries").blur();

  const task = await taskXML(page, "serviceTask", "Activity_rest");
  expect(task).toMatch(/<atlas:restConnector[^>]*retries="6"/);
  expect(task).not.toContain("taskDefinition");
  expect(page.__errors).toEqual([]);
});

test("switching the implementation kind keeps the retry budget", async ({ page }) => {
  // The budget is a property of the task, not of the connector it happens to use.
  await page.evaluate((el) => window.__select(el), "Activity_worker");
  await page.locator(".pgroup-head", { hasText: "Type" }).click(); // the kind picker
  await page.locator(".stkind-row[data-kind='rest']").click();
  await openRetries(page, "Activity_worker");

  await expect(page.locator("#f-st-retries")).toHaveValue("5");
  const task = await taskXML(page, "serviceTask", "Activity_worker");
  expect(task).toMatch(/<atlas:restConnector[^>]*retries="5"/);
  expect(page.__errors).toEqual([]);
});

test("a polyglot script task carries retries on its jobScript", async ({ page }) => {
  await openRetries(page, "Activity_script");
  await expect(page.locator("#f-psretries")).toHaveValue("");

  await page.locator("#f-psretries").fill("4");
  await page.locator("#f-psretries").blur();

  const task = await taskXML(page, "scriptTask", "Activity_script");
  expect(task).toMatch(/<atlas:jobScript[^>]*retries="4"/);
  // The script itself is untouched by the retry edit.
  expect(task).toContain('resultVariable="Greeting"');
  expect(task).toContain('Write-Output "hi"');
  expect(page.__errors).toEqual([]);
});

test("a business rule task reads and writes retries on its called decision", async ({ page }) => {
  await openRetries(page, "Activity_rule");
  await expect(page.locator("#f-brt-retries")).toHaveValue("2");

  await page.locator("#f-brt-retries").fill("7");
  await page.locator("#f-brt-retries").blur();

  const task = await taskXML(page, "businessRuleTask", "Activity_rule");
  expect(task).toMatch(/<zeebe:calledDecision[^>]*retries="7"/);
  expect(task).toContain('decisionId="stufe"');
  expect(page.__errors).toEqual([]);
});
