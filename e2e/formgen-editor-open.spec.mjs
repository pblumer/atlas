// Arriving in the form editor from a step in the Modeler
// (api/web/form-editor.js, ADR-0260).
//
// Pressing "Create a new form" on a user task is the author saying what the form is
// for. What used to happen next was a blank canvas and no memory of that: the editor
// could not know which task had sent them, so they laid the form out by hand and then
// went back to link it. The router now carries the process and the step through, and
// the generator opens on them — which only holds together if the editor really opens
// the dialog, really preselects, and really applies what comes back.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("the generator opens on the step that sent the author here", async ({ page }) => {
  await page.evaluate(() => window.__mountGenerate());

  // No second button to find: the dialog is up, on the process and the step the link
  // named, with the cursor in the one field the author still has to fill.
  await expect(page.locator(".fg-modal")).toBeVisible();
  await expect(page.locator("#fg-process")).toHaveValue("urlaubsantrag");
  await expect(page.locator("#fg-step")).toHaveValue("Task_Melden");
  await expect(page.locator("#fg-brief")).toBeFocused();

  await page.locator("#fg-brief").fill("Wer war abwesend und wie lange.");
  await page.locator("[data-ok]").click();

  // The request carried the step, and the form the editor is holding keeps its own id.
  const sent = await page.evaluate(() => window.__generated);
  expect(sent.processId).toBe("urlaubsantrag");
  expect(sent.elementId).toBe("Task_Melden");
  expect(sent.formId).toMatch(/^form-/);

  // And what came back is on the canvas, unsaved: the Editor tab shows the schema the
  // model wrote, under the id this editor was already holding.
  await page.locator('[data-ftab="editor"]').click();
  await expect(page.locator("#pane-editor")).toContainText('"grund"');
  await expect(page.locator("#pane-editor")).toContainText(sent.formId);
  expect(page.__errors, "page errors").toEqual([]);
});

test("the dialog closes back to a usable editor when it is dismissed", async ({ page }) => {
  await page.evaluate(() => window.__mountGenerate());
  await expect(page.locator(".fg-modal")).toBeVisible();
  await page.keyboard.press("Escape");

  // Nobody is trapped: the blank form is there to lay out by hand, and the button is
  // in the bar for a second try.
  await expect(page.locator(".fg-modal")).toHaveCount(0);
  await expect(page.locator("#form-generate")).toBeVisible();
  await expect(page.evaluate(() => window.__generated)).resolves.toBeNull();

  await page.locator("#form-generate").click();
  await expect(page.locator(".fg-modal")).toBeVisible();
  // Pressed a second time it still knows what this form is for: that does not change
  // while the form is open.
  await expect(page.locator("#fg-step")).toHaveValue("Task_Melden");
});

test("with no AI Worker the editor simply opens, as the link always did", async ({ page }) => {
  await page.evaluate(() => window.__mountGenerate([]));

  await expect(page.locator(".form-editor")).toBeVisible();
  await expect(page.locator(".fg-modal")).toHaveCount(0);
  // Not a hidden button and not a failed dialog: nothing to offer, so nothing offered.
  await expect(page.locator("#form-generate")).toBeHidden();
  expect(page.__errors, "page errors").toEqual([]);
});
