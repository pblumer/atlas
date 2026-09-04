// End-to-end coverage for the Modeler's decision picker (api/web/editor.js,
// ADR-0050). Several DMN models may be deployed at once (ADR-0072), so a decision
// id is unique only within its model: the picker therefore identifies an option by
// a composite key of modelRef + id, carried through the DOM on data-key.
//
// The composite key has to survive a round trip through an HTML attribute. It did
// not: the separator was a raw NUL, and the HTML parser replaces U+0000 in an
// attribute value with U+FFFD, so the key read back never equalled the key written
// and the lookup silently fell through to "first decision with this id" — which is
// the wrong model whenever two models share an id, the one case the key exists for.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/decision-picker-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__select("Activity_rule"));
  await expandGroup(page, "Called decision (DMN)");
});

// expandGroup opens one collapsible properties group (pgroup.js) if it is closed,
// so the field under test is on screen. Clicking unconditionally would close a
// group that already happens to be open.
async function expandGroup(page, title) {
  const group = page.locator(`.pgroup[data-group="${title}"]`);
  await expect(group).toHaveCount(1);
  if (await group.evaluate((el) => el.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").click();
  }
}

// pickByLabel selects the picker option whose visible label matches, and returns
// the exported XML of the business rule task afterwards.
async function pickByLabel(page, label) {
  const pick = page.locator("#f-decision-pick");
  await expect(pick.locator("option", { hasText: label })).toHaveCount(1);
  await pick.selectOption({ label });
  const xml = await page.evaluate(() => window.__xml());
  return /<bpmn:businessRuleTask id="Activity_rule"[\s\S]*?<\/bpmn:businessRuleTask>/.exec(xml)[0];
}

test("picking a decision adopts the model it was picked from, not the first with that id", async ({ page }) => {
  // Both options read "Einstufung"; only the model tells them apart.
  const task = await pickByLabel(page, "Einstufung · Einkommen");

  expect(task).toContain('decisionId="stufe"');
  // The second model's decision declares "einkommen" and outputs
  // "stufeNachEinkommen". Adopting the first one instead is the defect.
  expect(task).toMatch(/resultVariable="stufeNachEinkommen"/);
  expect(task).toContain('target="einkommen"');
  expect(task).not.toContain('target="alter"');
  expect(page.__errors).toEqual([]);
});

test("picking the first of two same-id decisions still adopts that one", async ({ page }) => {
  const task = await pickByLabel(page, "Einstufung · Alter");

  expect(task).toContain('decisionId="stufe"');
  expect(task).toMatch(/resultVariable="stufeNachAlter"/);
  expect(task).toContain('target="alter"');
  expect(task).not.toContain('target="einkommen"');
  expect(page.__errors).toEqual([]);
});
