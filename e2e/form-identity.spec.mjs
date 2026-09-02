// End-to-end test for the form editor's single identity (api/web/form-editor.js).
//
// A form used to have two ids: the one the store filed it under, shown in the toolbar
// chip, and `schema.id`, which is what the Design pane's Form ▸ General ▸ ID field
// edits. Editing the field changed only the document, so the chip and the panel
// disagreed on screen and the rename the author believed they had made never happened —
// while an export of that form carried the typed id, so re-importing it forked a copy.
//
// There is one id now, and it is the schema's: it is reconciled from the stored id on
// open, the chip mirrors it, and the save names the record it is editing so a changed
// id moves the form instead of leaving a duplicate behind
// (ADR-0222).
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountDivergent());
});

test("a form whose schema id drifted opens showing the id it is really stored under", async ({ page }) => {
  await expect(page.locator("#form-id-chip")).toHaveText("form-mtjs4");
  // The screenshot that reported this had the chip saying one id and the properties
  // panel the other. Both now read the id the form is actually stored under.
  await expect(page.locator("#bio-properties-panel-id")).toHaveValue("form-mtjs4");
  await expect(page.locator("#form-id-warn")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("retyping the ID moves the chip with it, and Save asks before renaming", async ({ page }) => {
  const asked = [];
  page.on("dialog", (d) => { asked.push(d.message()); d.accept(); });

  // Type into the panel's ID field the way a person does. The field is driven by
  // form-js's own (preact) properties panel, so the value goes through the native
  // setter and an input event — what preact listens for — rather than element.value,
  // which it would not see. The harness page does not lay the panel out at a size
  // Playwright considers clickable, so this stands in for the keystrokes.
  await page.evaluate((v) => {
    const el = document.getElementById("bio-properties-panel-id");
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
    set.call(el, v);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }, "frm_jira_ticket");

  // The chip follows the field — one id, visible in both places — and marks itself as
  // a rename that has not been saved yet.
  await expect(page.locator("#form-id-chip")).toHaveText("frm_jira_ticket");
  await expect(page.locator("#form-id-chip")).toHaveClass(/id-pending/);

  await page.evaluate(() => window.__save());
  await page.waitForFunction(() => window.__saved !== null, null, { timeout: 10000 });

  // A user task binds to the id, so the rename is a question, not a side effect.
  expect(asked.join(" ")).toContain("frm_jira_ticket");
  expect(asked.join(" ")).toContain("form-mtjs4");

  const saved = await page.evaluate(() => window.__saved);
  expect(saved.id).toBe("frm_jira_ticket");
  expect(saved.schema.id).toBe("frm_jira_ticket");
  // The record it came from: this is a move, not a second form.
  expect(saved.from).toBe("form-mtjs4");
  expect(page.__errors).toEqual([]);
});

test("saving carries the id the form is stored under, in the schema and as its identity", async ({ page }) => {
  await page.evaluate(() => window.__save());
  await page.waitForFunction(() => window.__saved !== null, null, { timeout: 10000 });
  const saved = await page.evaluate(() => window.__saved);
  expect(saved.id).toBe("form-mtjs4");
  // The document agrees with the store by construction — that is what stops an export
  // and a re-import from forking a second form.
  expect(saved.schema.id).toBe("form-mtjs4");
  // "from" names the record being edited, which is what makes a changed id a rename
  // rather than a second form.
  expect(saved.from).toBe("form-mtjs4");
  expect(page.__errors).toEqual([]);
});
