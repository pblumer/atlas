// End-to-end coverage for the Modeler Variables panel surfacing a linked form's
// input fields (api/web/editor.js, ADR-0028). Driven through the real vendored
// bpmn-js against a mock `api`: a process whose start event links a form gets that
// form's fields listed under a "Form" group in the Variables panel, next to the
// process's own variables under "Process". The rows are drag sources (their name is
// the drag payload) so a variable can be dropped into a property field.
import { test, expect } from "@playwright/test";

// The Modeler's bar carries Save and Deploy; everything else lives behind the "…" menu
// (ADR-draft-modeler-bar-hierarchy). Open it before reaching for one of those controls.
const openBarMenu = async (page) => {
  await page.locator("#bar-more").click();
  await expect(page.locator("#bar-menu")).toBeVisible();
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/start-form-vars-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  // Open the Variables panel; its render also fires the lazy form-schema fetch.
  await openBarMenu(page);
  await page.locator("#vars-toggle").click();
});

test("a linked start form's fields appear under a Form group", async ({ page }) => {
  const list = page.locator("#vars-list");

  // The form schema loads asynchronously; wait for its fields to land.
  await expect(list.locator('.var-row[data-var="filename"]')).toBeVisible();

  // Two groups, in order: the form's fields first, then the process's variables.
  const heads = page.locator(".vars-group-head");
  await expect(heads).toHaveCount(2);
  // The group header now also carries a collapse chevron and a member count, so read
  // the title span. innerText reflects the CSS text-transform:uppercase, so compare
  // case-insensitively.
  const titles = page.locator(".vars-group-head .vg-title");
  expect((await titles.allInnerTexts()).map((s) => s.toLowerCase())).toEqual(["form", "process"]);

  // Every input-bearing field is surfaced; layout-only components and a dynamic
  // list's inner field (bound under its path) are not.
  const names = await list.locator(".var-name").allInnerTexts();
  expect(names).toEqual(expect.arrayContaining(["filename", "delimiter", "header", "rows", "orderId"]));
  expect(names).not.toContain("cell");

  // A form field names its form as the click-to-select origin.
  await expect(list.locator('.var-row[data-var="filename"] .var-meta')).toContainText("form field · CSV Upload");

  expect(page.__errors).toEqual([]);
});

test("variable rows are drag sources carrying the variable name", async ({ page }) => {
  const row = page.locator('#vars-list .var-row[data-var="filename"]');
  await expect(row).toBeVisible();
  await expect(row).toHaveAttribute("draggable", "true");

  // A synthesized dragstart exposes the variable name as the text/plain payload —
  // what the browser's native drop-to-insert writes into a text field.
  const payload = await row.evaluate((el) => {
    const dt = new DataTransfer();
    el.dispatchEvent(new DragEvent("dragstart", { bubbles: true, dataTransfer: dt }));
    return dt.getData("text/plain");
  });
  expect(payload).toBe("filename");
  expect(page.__errors).toEqual([]);
});

test("a form field's component type is the variable's type", async ({ page }) => {
  // A form is the one design-time source that knows the type of what it writes before
  // anything has run: a checkbox writes a boolean whatever it is labelled, a dynamic
  // list binds an array under its path. The panel says so, per field.
  const list = page.locator("#vars-list");
  await expect(list.locator('.var-row[data-var="filename"]')).toBeVisible();
  const typeOf = async (name) =>
    (await list.locator(`.var-row[data-var="${name}"] .vtag`).innerText()).toLowerCase();

  expect(await typeOf("filename")).toBe("string");   // textfield
  expect(await typeOf("delimiter")).toBe("number");  // number
  expect(await typeOf("header")).toBe("boolean");    // checkbox, inside a layout group
  expect(await typeOf("rows")).toBe("array");        // dynamic list, bound under its path
  expect(await typeOf("orderId")).toBe("string");    // the process's own start variable
  expect(page.__errors).toEqual([]);
});
