// End-to-end coverage for how the business rule task's decision picker is cut up
// (api/web/editor.js, ADR-0050). Driven through the real vendored bpmn-js and the
// real properties panel.
//
// The list is grouped by decision file, this application's files first, and inside a
// file the published interfaces come before the decisions they are made of. What it
// replaces was grouped by nothing but where an entry happened to come from, which put
// a decision service — the one thing a task is meant to call — under "other", below
// every decision inside it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/decision-picker-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openPicker selects the business rule task and expands the group its decision
// picker lives in, then waits for the catalog fetch to have filled it.
async function openPicker(page) {
  await page.evaluate(() => window.__select("Activity_rule"));
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator("#f-decision-pick optgroup").first()).toBeAttached();
}

// groups returns the picker as [{ label, options }], the structure under test.
async function groups(page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("#f-decision-pick optgroup"), (g) => ({
      label: g.label,
      options: Array.from(g.children, (o) => o.textContent),
    })));
}

test("the picker is grouped by decision file, this application's files first", async ({ page }) => {
  await openPicker(page);
  const got = await groups(page);

  expect(got.map((g) => g.label)).toEqual([
    "Kreditfreigabe",
    "Tarif 2027",
    "Alter — other application",
    "freigabe — deployed only",
  ]);
  expect(page.__errors).toEqual([]);
});

test("a file offers its decision service before the decisions it is made of", async ({ page }) => {
  await openPicker(page);
  const [ kredit ] = await groups(page);

  // The service first, whatever order the catalog arrived in: it is the published
  // interface, and the decisions inside it are its workings.
  expect(kredit.options[0]).toBe("Kreditfreigabe — decision service");
  // Each member says which service it belongs to. Calling one works and answers
  // correctly, which is exactly why it is worth saying.
  expect(kredit.options).toContain("Kreditentscheid — inside Kreditfreigabe");
  expect(kredit.options).toContain("Tragbarkeit — inside Kreditfreigabe");
  // Bonität is the service's *input* decision — the caller's boundary, outside the
  // service — so it carries no marker.
  expect(kredit.options).toContain("Bonität");
  expect(page.__errors).toEqual([]);
});

test("picking the decision service fills the task with it", async ({ page }) => {
  await openPicker(page);
  await page.locator("#f-decision-pick").selectOption("Kreditfreigabe");

  const xml = await page.evaluate(() => window.__xml());
  const task = /<bpmn:businessRuleTask id="Activity_rule"[\s\S]*?<\/bpmn:businessRuleTask>/.exec(xml)[0];
  expect(task).toMatch(/<zeebe:calledDecision[^>]*decisionId="Kreditfreigabe"/);
  // Its declared inputs are auto-filled as mappings, the same as for a decision.
  expect(task).toMatch(/<zeebe:input[^>]*target="betrag"/);
  expect(task).toMatch(/<zeebe:input[^>]*target="bonitaet"/);
  expect(page.__errors).toEqual([]);
});
