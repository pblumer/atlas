// The form editor's directory field (vendor/form-js/src/atlas/editor.jsx, form-editor.js,
// ADR-0473): a select whose options source is a remote data source, edited where any
// select's options are — Options source ▸ Type — without writing JSON. The block the
// panel writes is what Save sends, the source is offered on a select only, a Worker that
// does not let forms search it is named as such, a directory field saved as a textfield is
// converted on request, and the previews search made-up people — never a directory.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1500, height: 1000 } });

async function open(page, opts = {}) {
  await page.evaluate((o) => window.__mountDirectory(o), opts);
  await expect(page.locator(".fjs-editor-container .fjs-form-field-select").first()).toBeVisible({ timeout: 25000 });
}

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

const props = (page) => page.locator(".fjs-properties-panel");
const entry = (page, id) => props(page).locator(`[data-entry-id="${id}"]`);
const typeSelect = (page) => props(page).locator("#bio-properties-panel-valuesSource-select");

async function selectField(page, id) {
  await page.locator(`.fjs-editor-container [data-id="${id}"]`).first().click();
  await expect(props(page)).toHaveAttribute("data-field", id);
}

// openGroup opens a properties group unless it is open already.
async function openGroup(page, id) {
  const group = props(page).locator(`[data-group-id="group-${id}"]`);
  await expect(group).toBeVisible();
  const entries = group.locator(".bio-properties-panel-group-entries");
  if (!(await entries.evaluate((el) => el.classList.contains("open")))) {
    await group.locator(".bio-properties-panel-group-header").click();
  }
  await expect(entries).toHaveClass(/open/);
}

async function chooseRemote(page, id = "Field_wer") {
  await selectField(page, id);
  await openGroup(page, "valuesSource");
  await typeSelect(page).selectOption("remote");
  await expect(entry(page, "atlasRemote-worker")).toBeVisible();
}

async function save(page) {
  await page.locator("#form-save").click();
  await page.waitForFunction(() => window.__saved !== null);
  return page.evaluate(() => window.__saved.schema);
}

const fieldOf = (schema, key) => schema.components.find((c) => c.key === key);

test("the palette's Directory picker adds a select that searches through the Worker open to forms", async ({ page }) => {
  await open(page);
  const preset = page.locator('.fjs-palette-field[data-field-type="directorypicker"]');
  await expect(preset).toBeVisible();
  await expect(preset).toContainText("Verzeichnisauswahl");
  await preset.focus();
  await preset.press("Enter");
  const schema = await save(page);
  const added = schema.components.find((c) => c.atlasDataSource);
  expect(added.type).toBe("select");
  expect(added.label).toBe("Person");
  expect(added.values).toBeUndefined();
  expect(added.atlasDataSource).toMatchObject({
    version: 1, workerType: "entra", worker: "ucc_entra", resource: "users",
    searchFields: ["displayName", "mail", "userPrincipalName"], pageSize: 25, minSearchLength: 2,
  });
});

test("a select gets its options from the remote data source under Options source ▸ Type", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  // Chosen, the source is filled in from what this server offers.
  await expect(entry(page, "atlasRemote-source").locator("select")).toHaveValue("entra/users");
  await expect(entry(page, "atlasRemote-worker").locator("select")).toHaveValue("ucc_entra");
  // A remote select is always searched: form-js's own "Searchable" is not offered.
  await expect(entry(page, "searchable")).toHaveCount(0);
  // Search in display name and UPN only.
  await entry(page, "atlasRemote-searchFields").locator('input[name="atlasRemote-searchFields-mail"]').uncheck();
  const pageSize = entry(page, "atlasRemote-pageSize").locator("input");
  await pageSize.fill("10");
  await pageSize.press("Tab");
  await openGroup(page, "atlasRemoteFilters");
  await props(page).getByRole("button", { name: "Add filter" }).click();
  await entry(page, "atlasRemote-filters-0-field").locator("select").selectOption("abteilung");
  await entry(page, "atlasRemote-filters-0-required").locator("input").check();
  await openGroup(page, "atlasRemoteOption");
  await entry(page, "atlasRemote-optionDetails").locator('input[name="atlasRemote-optionDetails-department"]').check();
  await expect(props(page).locator(".bio-properties-panel-entry.has-error")).toHaveCount(0);

  const schema = await save(page);
  const wer = fieldOf(schema, "wer");
  expect(wer.type).toBe("select");
  expect(wer.values).toBeUndefined();
  const ds = wer.atlasDataSource;
  expect(ds.searchFields).toEqual(["displayName", "userPrincipalName"]);
  expect(ds.filters).toEqual([{ property: "department", field: "abteilung", required: true }]);
  expect(ds.option.details).toEqual(["department"]);
  expect(ds.pageSize).toBe(10);
});

test("the remote data source is offered on a select only", async ({ page }) => {
  await open(page);
  await selectField(page, "Field_wer");
  await openGroup(page, "valuesSource");
  await expect(typeSelect(page).locator('option[value="remote"]')).toHaveText("Remote data source");
  await selectField(page, "Field_dringlichkeit");
  await openGroup(page, "valuesSource");
  // A radio group gets every other source, the decision included
  // (ADR-draft-form-options-from-a-decision), and not the remote one.
  await expect(typeSelect(page).locator("option")).toHaveText(["Static", "Input data", "Expression", "Decision (DMN)"]);
});

test("a Worker that does not let forms search it is named as such", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  const worker = entry(page, "atlasRemote-worker");
  await expect(worker.locator('option[value="contoso"]')).toHaveText(/not open to forms/);
  await worker.locator("select").selectOption("contoso");
  await expect(worker.locator(".bio-properties-panel-description")).toContainText("does not let forms search");
});

test("an unfinished filter is pointed out, and a fixed value completes it", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  await openGroup(page, "atlasRemoteFilters");
  await props(page).getByRole("button", { name: "Add filter" }).click();
  await expect(entry(page, "atlasRemote-filters-0-field").locator(".bio-properties-panel-error"))
    .toContainText("Choose the field");
  await entry(page, "atlasRemote-filters-0-mode").locator("select").selectOption("value");
  const value = entry(page, "atlasRemote-filters-0-value").locator("input");
  await value.fill("IT");
  await value.press("Tab");
  await expect(props(page).locator(".bio-properties-panel-entry.has-error")).toHaveCount(0);
  const schema = await save(page);
  expect(fieldOf(schema, "wer").atlasDataSource.filters).toEqual([{ property: "department", value: "IT" }]);
});

test("back to static options drops the data source", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  await typeSelect(page).selectOption("static");
  await expect(entry(page, "atlasRemote-worker")).toHaveCount(0);
  const schema = await save(page);
  const wer = fieldOf(schema, "wer");
  expect(wer.atlasDataSource).toBeUndefined();
  expect(wer.values.length).toBeGreaterThan(0);
});

test("the Validate tab previews the field with made-up people and asks no directory", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  await page.locator('#form-tabs button[data-ftab="validate"]').click();
  const preview = page.locator("#fv-form");
  await expect(preview.locator(".atlas-remote-select .atlas-rs-preview")).toBeVisible({ timeout: 15000 });
  await preview.locator(".atlas-rs-search").fill("An");
  await expect(preview.locator(".atlas-rs-list li[role=option]").first()).toBeVisible();
  const labels = await preview.locator(".atlas-rs-list .atlas-rs-label").allTextContents();
  expect(labels).toContain("Anna Müller");
  const calls = await page.evaluate(() => window.__calls.map((c) => c.url));
  expect(calls.some((u) => u.includes("/form-data/") && u.endsWith("/query"))).toBe(false);
});

test("the Design pane's preview searches example data, and the canvas shows the field without searching", async ({ page }) => {
  await open(page);
  await chooseRemote(page);
  const preview = page.locator(".fjs-pgl-form-container").nth(1);
  await expect(preview.locator(".atlas-remote-select .atlas-rs-preview")).toBeVisible();
  await expect(page.locator(".fjs-editor-container .atlas-rs")).toHaveCount(0);
  await expect(page.locator(".fjs-editor-container .atlas-remote-plain")).toBeDisabled();
});

test("a directory field saved as a textfield is converted into a select on request", async ({ page }) => {
  await open(page, { legacy: true });
  await selectField(page, "Field_alt");
  await openGroup(page, "atlasRemoteLegacy");
  await entry(page, "atlasRemote-convert").getByRole("button", { name: "Convert to select" }).click();
  await openGroup(page, "valuesSource");
  await expect(typeSelect(page)).toHaveValue("remote");
  const schema = await save(page);
  const alt = fieldOf(schema, "alt");
  expect(alt.type).toBe("select");
  expect(alt.values).toBeUndefined();
  expect(alt.validate).toEqual({ required: true });
  expect(alt.appearance).toBeUndefined();
  expect(alt.atlasDataSource).toEqual({ version: 1, workerType: "entra", worker: "ucc_entra", resource: "users" });
});
