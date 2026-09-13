// The one flow that changed shape when the decision editor stopped being an overlay
// (ADR-draft-the-decision-editor-is-a-page): authoring a decision from a business rule
// task. It used to open a window over the diagram and resolve with what it saved; it
// now leaves for a page and the diagram adopts what was authored on the way back.
//
// The promise ADR-0062 bought — press one button, model the decision, come back to a
// task that is already wired — has to survive that move, and it is the kind of promise
// that breaks silently. Both legs are held here.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/dmn-roundtrip-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// openTask selects a business rule task, opens the Implement tab and expands its
// Called decision group — property groups start collapsed, so the decision fields are
// behind one click.
async function openTask(page, id) {
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
}

test("＋ New decision leaves for the decision editor, carrying the task it was pressed on", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  await openTask(page, "Activity_rule");

  await expect(page.locator("#f-dmn-new")).toHaveText("＋ New decision");
  await page.locator("#f-dmn-new").click();

  // The route names the diagram to come back to and the task to wire, which is what
  // makes the return leg possible at all. No overlay was mounted on the way.
  await expect
    .poll(() => page.evaluate(() => location.hash))
    .toBe("#/modeler/dmn/new/for/orders/Activity_rule");
  await expect(page.locator(".dmn-overlay")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("Edit opens the selected decision's own page", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  // A task that already names a decision: Edit is about the one it names.
  await openTask(page, "Activity_rule_set");
  await expect(page.locator("#f-decisionid")).toHaveValue("eligibility");

  await page.locator("#f-dmn-edit").click();
  // The panel holds a model handle; the editor's address is the reference pointing at
  // it, so the button resolves one from the other.
  await expect
    .poll(() => page.evaluate(() => location.hash))
    .toBe("#/modeler/dmn/e/ref-1/for/orders/Activity_rule_set");
  expect(page.__errors).toEqual([]);
});

test("coming back from the decision editor selects the task and wires it", async ({ page }) => {
  // What app.js hands the editor when the decision editor left an adoption behind.
  await page.evaluate(() => window.__mount({
    adopt: { processId: "orders", elementId: "Activity_rule", name: "eligibility", modelRef: "eligibility" },
  }));
  await page.locator('[data-tab="implement"]').click();

  // The task the decision was authored for is selected, without the author hunting for
  // it — otherwise the adoption would sit waiting on a click nobody knows to make.
  await expect.poll(() => page.evaluate(() => window.__selected())).toBe("Activity_rule");
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();

  // And it is wired: the decision id, the result variable taken from the decision's
  // output, and an input mapping per declared input — exactly what the overlay filled
  // in when it resolved.
  await expect(page.locator("#f-decisionid")).toHaveValue("eligibility");
  await expect(page.locator("#f-resultvar")).toHaveValue("verdict");
  await expect(page.locator(".dmn-input-row .dmn-in-target").first()).toHaveValue("amount");
  expect(page.__errors).toEqual([]);
});

test("an adoption is applied once, not on every later open of the diagram", async ({ page }) => {
  await page.evaluate(() => window.__mount({
    adopt: { processId: "orders", elementId: "Activity_rule", name: "eligibility", modelRef: "eligibility" },
  }));
  await page.locator('[data-tab="implement"]').click();
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator("#f-decisionid")).toHaveValue("eligibility");

  // Reopening the same diagram with no adoption pending must not re-apply the last
  // one: the round trip wires the task once, when the author comes back from it.
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await expect.poll(() => page.evaluate(() => window.__selected())).toBe("");
  expect(page.__errors).toEqual([]);
});
