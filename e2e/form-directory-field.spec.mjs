// The form editor's directory field (api/web/remote-select-editor.js, form-editor.js,
// ADR-0473): a model author turns a textfield into a search over
// a remote data source without writing JSON, the block the panel writes is what Save
// sends, a Worker that does not let forms search it is named as such, and the preview
// searches made-up people — never a directory.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1500, height: 1000 } });

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountDirectory());
  await expect(page.locator(".fjs-editor-container .fjs-form-field-textfield").first()).toBeVisible({ timeout: 25000 });
  await expect(page.locator("#form-add-directory")).toBeVisible({ timeout: 10000 });
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

const panel = (page) => page.locator(".atlas-ds-panel");

async function save(page) {
  await page.locator("#form-save").click();
  await page.waitForFunction(() => window.__saved !== null);
  return page.evaluate(() => window.__saved.schema);
}

const fieldOf = (schema, key) => schema.components.find((c) => c.key === key);

test("adding a directory field writes a data source through the Worker open to forms", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).locator(".atlas-ds-head")).toContainText("Microsoft Entra ID");
  await expect(panel(page).locator("[data-field=worker]")).toHaveValue("ucc_entra");
  const schema = await save(page);
  const added = fieldOf(schema, "person");
  expect(added.type).toBe("textfield");
  expect(added.validate.required).toBe(true);
  expect(added.atlasDataSource).toMatchObject({
    version: 1, workerType: "entra", worker: "ucc_entra", resource: "users",
    searchFields: ["displayName", "mail", "userPrincipalName"], pageSize: 25, minSearchLength: 2,
  });
});

test("a plain text field becomes a directory field, bound to the department it is filtered by", async ({ page }) => {
  await page.locator(".fjs-editor-container .fjs-form-field-textfield").first().click();
  await expect(panel(page)).toBeVisible();
  await panel(page).locator("[data-act=enable]").click();
  // Search in display name and UPN only.
  await panel(page).locator("[data-list=searchFields][value=mail]").uncheck();
  await panel(page).locator("[data-act=f-add]").click();
  await panel(page).locator(".atlas-ds-filter [data-field=f-field]").selectOption("abteilung");
  await panel(page).locator(".atlas-ds-filter [data-field=f-required]").check();
  await panel(page).locator("[data-list=details][value=department]").check();
  await panel(page).locator("[data-field=pageSize]").fill("10");
  await panel(page).locator("[data-field=pageSize]").blur();
  await expect(panel(page).locator(".atlas-ds-problems")).toHaveCount(0);
  const schema = await save(page);
  const ds = fieldOf(schema, "wer").atlasDataSource;
  expect(ds.searchFields).toEqual(["displayName", "userPrincipalName"]);
  expect(ds.filters).toEqual([{ property: "department", field: "abteilung", required: true }]);
  expect(ds.option.details).toEqual(["department"]);
  expect(ds.pageSize).toBe(10);
});

test("a Worker that does not let forms search it is named as such", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await panel(page).locator("[data-field=worker]").selectOption("contoso");
  await expect(panel(page).locator(".atlas-ds-warn")).toContainText("does not let forms search");
  await expect(panel(page).locator("[data-field=worker] option[value=contoso]")).toHaveText(/not open to forms/);
});

test("an unfinished filter is pointed out before Save", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await panel(page).locator("[data-act=f-add]").click();
  await expect(panel(page).locator(".atlas-ds-problems")).toContainText("needs a field or a fixed value");
  await panel(page).locator(".atlas-ds-filter [data-field=f-mode]").selectOption("value");
  await panel(page).locator(".atlas-ds-filter [data-field=f-value]").fill("IT");
  await panel(page).locator(".atlas-ds-filter [data-field=f-value]").blur();
  await expect(panel(page).locator(".atlas-ds-problems")).toHaveCount(0);
  const schema = await save(page);
  expect(fieldOf(schema, "person").atlasDataSource.filters).toEqual([{ property: "department", value: "IT" }]);
});

test("a directory field turns back into a plain text field", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await panel(page).locator("[data-act=disable]").click();
  await expect(panel(page).locator("[data-act=enable]")).toBeVisible();
  const schema = await save(page);
  expect(fieldOf(schema, "person").atlasDataSource).toBeUndefined();
});

test("the Validate tab previews the field with made-up people and asks no directory", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await page.locator('#form-tabs button[data-ftab="validate"]').click();
  const preview = page.locator("#fv-form");
  await expect(preview.locator(".atlas-rs-preview")).toBeVisible({ timeout: 15000 });
  await preview.locator(".atlas-rs-search").fill("An");
  await expect(preview.locator(".atlas-rs-list li[role=option]").first()).toBeVisible();
  const labels = await preview.locator(".atlas-rs-list .atlas-rs-label").allTextContents();
  expect(labels).toContain("Anna Müller");
  const calls = await page.evaluate(() => window.__calls.map((c) => c.url));
  expect(calls.some((u) => u.includes("/form-data/") && u.endsWith("/query"))).toBe(false);
});

test("the Design pane's own preview shows the field with example data too", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  const preview = page.locator(".fjs-pgl-form-container").nth(1);
  await expect(preview.locator(".atlas-rs-preview")).toBeVisible();
  // The editing canvas is left as form-js draws it.
  await expect(page.locator(".fjs-editor-container .atlas-rs")).toHaveCount(0);
});

test("the panel sits above form-js's properties, which keep their column's full width", async ({ page }) => {
  await page.locator("#form-add-directory").click();
  await expect(panel(page)).toBeVisible();
  const g = await page.evaluate(() => {
    const box = (el) => el.getBoundingClientRect();
    const col = document.querySelector(".fjs-pgl-properties-container");
    return {
      col: Math.round(box(col).width),
      panelBottom: Math.round(box(col.querySelector(".atlas-ds-panel")).bottom),
      props: Math.round(box(col.querySelector(".fjs-properties-container")).width),
      propsTop: Math.round(box(col.querySelector(".fjs-properties-container")).top),
    };
  });
  expect(g.props).toBe(g.col);
  expect(g.panelBottom).toBeLessThanOrEqual(g.propsTop);
});
