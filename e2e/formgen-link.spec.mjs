// The "Create a new form" links in the Modeler's Implement panel carry where they were
// pressed from (api/web/editor.js, ADR-0260).
//
// Pressing one on a user task is the author saying what the form is for, and until the
// link carried that, the form editor could not know: it opened blank, the author laid
// the form out by hand and came back to link it. The link now names the process and the
// step, which is what lets the generator open on them.
//
// Three links, three different answers, and the differences are the point — so this
// asserts all three against the same model. It reuses the repair-form harness because
// that model already has the three elements: a user task, a plain start event, and a
// service task that offers a repair form.
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

// open selects an element and expands the named property group.
async function open(page, id, group) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: group }).click();
}

// formLink is the "Create a new form" anchor inside the expanded group.
const formLink = (page, group) =>
  page.locator(".pgroup", { hasText: group }).locator("a", { hasText: "Create a new form" });

test("a user task's link names the process and the task", async ({ page }) => {
  await open(page, "Activity_approve", "Form");
  await expect(formLink(page, "Form")).toHaveAttribute(
    "href", "#/modeler/form/new/for/repairing/Activity_approve");
  expect(page.__errors, "page errors").toEqual([]);
});

test("a start event's link names the process and no step", async ({ page }) => {
  // A start form is for starting the process, not for a step inside it — so the link
  // says the process and stops there, and the generator opens on the start-form case.
  await open(page, "Start_1", "Start form");
  await expect(formLink(page, "Start form")).toHaveAttribute(
    "href", "#/modeler/form/new/for/repairing");
});

test("a repair form's link stays the plain one", async ({ page }) => {
  // A repair form is neither of the two things the generator writes: it is the subset
  // of a parked instance's variables an operator corrects. Opening the generator on
  // this task would frame it as the task's work form, which is exactly the confusion
  // the panel's own wording exists to prevent.
  await open(page, "Activity_charge", "Repair form");
  await expect(formLink(page, "Repair form")).toHaveAttribute("href", "#/modeler/form/new");
});
