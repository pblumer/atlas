// A field whose options come from a deployed decision
// (ADR-draft-form-options-from-a-decision; vendor/form-js/src/atlas/decision-source.js,
// viewer.jsx, editor.jsx; formviewer.js decisionOptions). In the form editor the
// decision is a fifth Options source ▸ Type on a select, radio group, checklist and tag
// list; the panel picks the decision and shows what it answers now, and Save sends the
// block instead of options. Rendered, the field is form-js's own, given the decision's
// list — loading until it is there, saying so when the decision cannot answer, and
// keeping a prefilled value only while the list offers it.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1500, height: 1000 } });

const ANSWERS = {
  Kostenstellen: { status: 200, body: { decisionId: "Kostenstellen", skipped: 1, options: [
    { label: "Einkauf", value: "CC100" }, { label: "Verkauf", value: "CC200" }] } },
  "Länder": { status: 200, body: { decisionId: "Länder", skipped: 0, options: [
    { label: "Schweiz", value: "CH" }, { label: "Deutschland", value: "DE" }, { label: "Österreich", value: "AT" }] } },
  Rabatt: { status: 422, body: { code: "takes_inputs", error: "the decision \"Rabatt\" takes inputs (Menge); a form's options come from a decision without inputs" } },
};

// answerDecisions serves the options route from ANSWERS and counts what was asked.
async function answerDecisions(page, answers = ANSWERS) {
  page.__asked = [];
  await page.route("**/api/v1/form-data/decisions/*/options", async (route) => {
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split("/").slice(-2)[0]);
    page.__asked.push(id);
    const a = answers[id] || { status: 404, body: { code: "not_deployed", error: `no decision "${id}" is deployed on this server` } };
    await route.fulfill({ status: a.status, contentType: "application/json", body: JSON.stringify(a.body) });
  });
}

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

test.describe("in the form editor", () => {
  test.beforeEach(async ({ page }) => {
    await answerDecisions(page);
    await page.goto("/form-editor-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  const props = (page) => page.locator(".fjs-properties-panel");
  const entry = (page, id) => props(page).locator(`[data-entry-id="${id}"]`);
  const typeSelect = (page) => props(page).locator("#bio-properties-panel-valuesSource-select");

  async function open(page, opts = {}) {
    await page.evaluate((o) => window.__mountDecisions(o), opts);
    await expect(page.locator(".fjs-editor-container .fjs-form-field-select").first()).toBeVisible({ timeout: 25000 });
  }

  async function selectField(page, id) {
    await page.locator(`.fjs-editor-container [data-id="${id}"]`).first().click();
    await expect(props(page)).toHaveAttribute("data-field", id);
  }

  async function openGroup(page, id) {
    const group = props(page).locator(`[data-group-id="group-${id}"]`);
    await expect(group).toBeVisible();
    const entries = group.locator(".bio-properties-panel-group-entries");
    if (!(await entries.evaluate((el) => el.classList.contains("open")))) {
      await group.locator(".bio-properties-panel-group-header").click();
    }
    await expect(entries).toHaveClass(/open/);
  }

  async function save(page) {
    await page.locator("#form-save").click();
    await page.waitForFunction(() => window.__saved !== null);
    return page.evaluate(() => window.__saved.schema);
  }

  const fieldOf = (schema, key) => schema.components.find((c) => c.key === key);

  test("the decision is an options source of all four option fields", async ({ page }) => {
    await open(page);
    for (const id of ["Field_ks", "Field_land", "Field_tags", "Field_more"]) {
      await selectField(page, id);
      await openGroup(page, "valuesSource");
      await expect(typeSelect(page).locator('option[value="decision"]')).toHaveText("Decision (DMN)");
    }
  });

  test("choosing it names the first decision offered, the panel picks another, and Save sends the block", async ({ page }) => {
    await open(page);
    await selectField(page, "Field_land");
    await openGroup(page, "valuesSource");
    await typeSelect(page).selectOption("decision");
    const decision = entry(page, "atlasDecision-decision").locator("select");
    await expect(decision).toHaveValue("Kostenstellen");
    await expect(entry(page, "atlasDecision-decision").locator("option")).toHaveText(["Kostenstellen — Stammdaten", "Länder — Stammdaten"]);
    await expect(entry(page, "atlasDecision-preview")).toContainText("2 options: Einkauf, Verkauf. 1 entry is no option");

    await decision.selectOption("Länder");
    await expect(entry(page, "atlasDecision-preview")).toContainText("3 options: Schweiz, Deutschland, Österreich");
    // The canvas shows the field with the decision's list.
    await expect(page.locator('.fjs-editor-container [data-id="Field_land"]')).toContainText("Österreich");

    const schema = await save(page);
    const land = fieldOf(schema, "land");
    expect(land.type).toBe("radio");
    expect(land.values).toBeUndefined();
    expect(land.valuesKey).toBeUndefined();
    expect(land.atlasDecision).toEqual({ version: 1, decisionId: "Länder" });
  });

  test("a form that already names a decision opens on it and previews what it answers", async ({ page }) => {
    await open(page, { named: true });
    await selectField(page, "Field_ks");
    await openGroup(page, "valuesSource");
    await expect(typeSelect(page)).toHaveValue("decision");
    await expect(entry(page, "atlasDecision-decision").locator("select")).toHaveValue("Kostenstellen");
    await expect(entry(page, "atlasDecision-decision").locator(".bio-properties-panel-error")).toHaveCount(0);
    await expect(entry(page, "atlasDecision-preview")).toContainText("2 options");
  });

  test("a decision the server does not offer is named as such, with the reason it cannot answer", async ({ page }) => {
    // As a form imported from another server would: it names a decision this one has
    // not deployed.
    await open(page, { named: "Rabatt" });
    await selectField(page, "Field_ks");
    await openGroup(page, "valuesSource");
    const decision = entry(page, "atlasDecision-decision");
    await expect(decision.locator("select")).toHaveValue("Rabatt");
    await expect(decision.locator('option[value="Rabatt"]')).toHaveText("Rabatt (not deployed, or takes inputs)");
    await expect(decision.locator(".bio-properties-panel-error")).toContainText("no deployed decision of that name");
    await expect(entry(page, "atlasDecision-preview").locator(".bio-properties-panel-error")).toContainText("takes inputs (Menge)");
    // The canvas says what the runtime would.
    await expect(page.locator('.fjs-editor-container [data-id="Field_ks"]')).toContainText("Die Auswahl konnte nicht geladen werden.");
  });

  test("switching back to static options drops the block", async ({ page }) => {
    await open(page, { named: true });
    await selectField(page, "Field_ks");
    await openGroup(page, "valuesSource");
    await typeSelect(page).selectOption("static");
    const schema = await save(page);
    const ks = fieldOf(schema, "kostenstelle");
    expect(ks.atlasDecision).toBeUndefined();
    expect(Array.isArray(ks.values)).toBe(true);
  });
});

test.describe("rendered", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/decision-options-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  const schema = { type: "default", components: [
    { type: "select", id: "f1", key: "kostenstelle", label: "Kostenstelle", atlasDecision: { version: 1, decisionId: "Kostenstellen" } },
    { type: "radio", id: "f2", key: "land", label: "Land", atlasDecision: { version: 1, decisionId: "Länder" } },
    { type: "checklist", id: "f3", key: "laender", label: "Länder", atlasDecision: { version: 1, decisionId: "Länder" } },
    { type: "taglist", id: "f4", key: "weitere", label: "Weitere", atlasDecision: { version: 1, decisionId: "Kostenstellen" } },
  ] };

  test("each field shows its decision's list and submits the value chosen", async ({ page }) => {
    await answerDecisions(page);
    await page.evaluate((s) => window.__render(s, {}), schema);
    const land = page.locator(".fjs-form-field-radio");
    await expect(land).toContainText("Schweiz");
    await expect(land).toContainText("Österreich");
    await expect(page.locator(".fjs-form-field-checklist")).toContainText("Deutschland");
    await land.getByLabel("Deutschland").check();
    await page.locator(".fjs-form-field-checklist").getByLabel("Österreich").check();
    const { data } = await page.evaluate(() => window.__submit());
    expect(data.land).toBe("DE");
    expect(data.laender).toEqual(["AT"]);
    // Two decisions, four fields: each decision is asked once per form.
    expect(page.__asked.sort()).toEqual(["Kostenstellen", "Länder"]);
  });

  test("a prefilled value stays while the list offers it and goes when it does not", async ({ page }) => {
    await answerDecisions(page);
    await page.evaluate((s) => window.__render(s, { kostenstelle: "CC200", land: "FR", laender: ["CH", "IT"], weitere: ["CC100"] }), schema);
    await expect(page.locator(".fjs-form-field-radio")).toContainText("Schweiz");
    await expect(page.locator(".fjs-form-field-checklist")).toContainText("Schweiz");
    const { data } = await page.evaluate(() => window.__submit());
    expect(data.kostenstelle).toBe("CC200");
    expect(data.land).toBeNull();
    expect(data.laender).toEqual(["CH"]);
    expect(data.weitere).toEqual(["CC100"]);
  });

  test("a decision that cannot answer says the choices could not be loaded, in the form's language", async ({ page }) => {
    await answerDecisions(page);
    const bad = { type: "default", components: [
      { type: "select", id: "f1", key: "rabatt", label: "Rabatt", atlasDecision: { version: 1, decisionId: "Rabatt" } },
    ] };
    await page.evaluate((s) => window.__render(s, {}), bad);
    const status = page.locator(".atlas-decision-field [role=status]");
    await expect(status).toHaveText("Die Auswahl konnte nicht geladen werden.");
    await expect(status).toHaveAttribute("title", /takes inputs/);
    await page.evaluate((s) => window.__render(s, {}, { atlasLocale: "en" }), bad);
    await expect(page.locator(".atlas-decision-field [role=status]")).toHaveText("The choices could not be loaded.");
  });

  test("while the list is on its way the field says it is loading", async ({ page }) => {
    let release;
    const held = new Promise((r) => { release = r; });
    await page.route("**/api/v1/form-data/decisions/*/options", async (route) => {
      await held;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(ANSWERS.Kostenstellen.body) });
    });
    const one = { type: "default", components: [schema.components[0]] };
    await page.evaluate((s) => { window.__render(s, { kostenstelle: "CC100" }); }, one);
    await expect(page.locator(".atlas-decision-field [role=status]")).toHaveText("Auswahl wird geladen …");
    release();
    await expect(page.locator(".fjs-form-field-select")).toContainText("Einkauf");
    const { data } = await page.evaluate(() => window.__submit());
    expect(data.kostenstelle).toBe("CC100");
  });
});
