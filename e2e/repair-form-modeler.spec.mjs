// End-to-end coverage for the repair-form binding in the Modeler Implement panel
// (api/web/editor.js, ADR-0169).
//
// The binding is what makes the whole record usable by anyone who does not hand-edit
// XML: the person authoring a task is the one who knows which values its retry depends
// on, and this is where they say so. Driven through the real vendored bpmn-js, asserting
// against the exported XML — because that is what a deploy sends and what the compiler
// then reads.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/repair-form-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openRepair selects a task and expands its Repair form group.
async function openRepair(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Repair form" }).click();
}

// taskXML returns the exported XML of one element.
async function taskXML(page, tag, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:${tag} id="${id}"[\\s\\S]*?</bpmn:${tag}>`).exec(xml)[0];
}

test("a service task reads the repair form its model carries", async ({ page }) => {
  await openRepair(page, "Activity_charge");
  // The picker is populated from the deployed forms, with the bound one selected and
  // named the way its author named it.
  await expect(page.locator("#f-form")).toHaveValue("fix-payment");
  await expect(page.locator("#f-form")).toContainText("Fix the payment");

  // And the panel says what the binding is *for* — a repair form is not a work form,
  // and the difference is the whole point.
  await expect(page.locator(".pgroup", { hasText: "Repair form" })).toContainText("parks behind an incident");

  expect(page.__errors, "page errors").toEqual([]);
});

test("binding a form writes the extension the compiler reads", async ({ page }) => {
  await openRepair(page, "Activity_ship");
  await expect(page.locator("#f-form")).toHaveValue("");

  await page.locator("#f-form").selectOption("fix-address");

  const task = await taskXML(page, "serviceTask", "Activity_ship");
  expect(task).toMatch(/<zeebe:formDefinition[^>]*formId="fix-address"/);
  // The task's own definition is untouched — the binding is added beside it, not
  // instead of it.
  expect(task).toMatch(/<zeebe:taskDefinition[^>]*type="ship-it"/);

  expect(page.__errors, "page errors").toEqual([]);
});

test("unbinding removes the extension rather than leaving an empty formId", async ({ page }) => {
  await openRepair(page, "Activity_charge");
  await page.locator("#f-form").selectOption("");

  const task = await taskXML(page, "serviceTask", "Activity_charge");
  // An empty formId would compile to a binding pointing at nothing, which the incident
  // surface would then offer and fail to open.
  expect(task).not.toMatch(/formDefinition/);
  expect(task).toMatch(/<zeebe:taskDefinition[^>]*type="charge-card"/);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a business rule task offers it too", async ({ page }) => {
  await openRepair(page, "Activity_rule");
  await page.locator("#f-form").selectOption("fix-address");

  const task = await taskXML(page, "businessRuleTask", "Activity_rule");
  expect(task).toMatch(/<zeebe:formDefinition[^>]*formId="fix-address"/);
  expect(task).toMatch(/<zeebe:calledDecision[^>]*decisionId="stufe"/);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a user task's form is its work form, and is not offered as a repair form", async ({ page }) => {
  await page.evaluate((el) => window.__select(el), "Activity_approve");
  // A user task has no Repair form group at all: its zeebe:formDefinition is the form a
  // person fills in to *do* the task. Offering a second binding on the same element
  // under the same extension would be ambiguous in the model as well as on screen.
  // The group heads carry a collapse marker, so they are read as text and compared by
  // name rather than matched as selectors — "Form" and "Repair form" would otherwise
  // both match a loose text filter, which is exactly the distinction under test.
  const groups = await page.evaluate(() =>
    [...document.querySelectorAll(".pgroup-head")].map((h) => h.textContent.replace(/^[^A-Za-z]+/, "").trim()));
  expect(groups).not.toContain("Repair form");
  // It does still have its own Form section — that binding is unaffected.
  expect(groups).toContain("Form");

  expect(page.__errors, "page errors").toEqual([]);
});
