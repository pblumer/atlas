// The boxed list editor (vendor/dmn/src/atlas/, ADR-0511):
// a decision whose logic is a dmn:List opens in its own tab like a table or a literal
// expression, its entries are added, moved and removed there as steps that undo, and
// a decision is turned into a list from the requirements graph's replace menu without
// losing its id or its requirements. What is asserted is the model the editor writes:
// a model save is captured and read back.
//
// The vendored dmn-js bundle is loaded for real (the static server serves ../api/web).
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1400, height: 900 } });

const XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/" id="Definitions_s" name="Stammdaten" namespace="http://atlas/dmn">
  <inputData id="Input_menge" name="Menge"><variable id="Var_menge" name="Menge" typeRef="number"/></inputData>
  <decision id="Decision_ks" name="Kostenstellen">
    <variable id="Var_ks" name="Kostenstellen"/>
    <list id="List_ks">
      <literalExpression id="LE_1"><text>{value: "CC100", label: "Einkauf"}</text></literalExpression>
      <literalExpression id="LE_2"><text>{value: "CC200", label: "Verkauf"}</text></literalExpression>
    </list>
  </decision>
  <decision id="Decision_rabatt" name="Rabatt">
    <variable id="Var_rabatt" name="Rabatt" typeRef="number"/>
    <informationRequirement id="IR_menge"><requiredInput href="#Input_menge"/></informationRequirement>
    <decisionTable id="DT_rabatt"><input id="I_rabatt" label="Menge"><inputExpression id="IE_rabatt" typeRef="number"><text>Menge</text></inputExpression></input><output id="O_rabatt" name="Rabatt" typeRef="number"/></decisionTable>
  </decision>
  <dmndi:DMNDI><dmndi:DMNDiagram id="DD">
    <dmndi:DMNShape id="S_menge" dmnElementRef="Input_menge"><dc:Bounds height="45" width="125" x="430" y="320"/></dmndi:DMNShape>
    <dmndi:DMNShape id="S_ks" dmnElementRef="Decision_ks"><dc:Bounds height="80" width="180" x="100" y="100"/></dmndi:DMNShape>
    <dmndi:DMNShape id="S_rabatt" dmnElementRef="Decision_rabatt"><dc:Bounds height="80" width="180" x="400" y="100"/></dmndi:DMNShape>
    <dmndi:DMNEdge id="E_menge" dmnElementRef="IR_menge"><di:waypoint x="492" y="320"/><di:waypoint x="490" y="180"/></dmndi:DMNEdge>
  </dmndi:DMNDiagram></dmndi:DMNDI>
</definitions>`;

function installMock(page) {
  const uploads = [];
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: "app-1", name: "Stammdaten", myRole: "owner" }] });
    if (path === "/api/v1/dmnrefs") return route.fulfill({ json: [{ id: "ref-1", name: "Stammdaten", modelRef: "stammdaten", projectId: "app-1" }] });
    if (path.startsWith("/api/v1/dmn-models/") && path.endsWith("/xml")) return route.fulfill({ body: XML, contentType: "application/xml" });
    if (path === "/api/v1/decisions/evaluate") return route.fulfill({ json: { ok: true, modelName: "Stammdaten", decisions: [] } });
    if (path === "/api/v1/dmn-models" && request.method() === "POST") {
      uploads.push(request.postData());
      return route.fulfill({ json: { modelRef: "stammdaten", modelName: "Stammdaten", decisions: ["Kostenstellen", "Rabatt"] } });
    }
    return route.fulfill({ json: [] });
  });
  return { uploads };
}

async function open(page) {
  const state = installMock(page);
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible({ timeout: 20000 });
  return state;
}

const tab = (page, name) => page.locator("#dmn-views button", { hasText: name });
const entries = (page) => page.locator(".atlas-list-entry");
const texts = (page) => page.locator(".atlas-list-entry .atlas-list-value").allTextContents();

// savedModel saves into the model and returns the XML that was uploaded.
async function savedModel(page, state) {
  const before = state.uploads.length;
  await page.locator("#dmn-save-model").click();
  await expect.poll(() => state.uploads.length).toBe(before + 1);
  return state.uploads[before];
}

test.afterEach(async ({ page }) => {
  expect(page.__errors || []).toEqual([]);
});

test("a list decision opens in its own tab, as its entries, with a hint that says what a list is", async ({ page }) => {
  await open(page);
  await expect(page.locator("#dmn-views button")).toHaveText(["Übersicht (DRG)", "Kostenstellen", "Rabatt"]);
  await tab(page, "Kostenstellen").click();
  await expect(entries(page)).toHaveCount(2);
  expect(await texts(page)).toEqual(['{value: "CC100", label: "Einkauf"}', '{value: "CC200", label: "Verkauf"}']);
  // Its header is its name: no "Expression type" that would turn it into a decision
  // this Modeler cannot open again.
  await expect(page.locator(".dmn-boxed-expression-header .expression-type-select")).toHaveCount(0);
  await expect(page.locator(".dmn-hint")).toContainText("Die Logik dieser Entscheidung ist eine Liste");
  // The first entry cannot move up, the last cannot move down.
  await expect(entries(page).first().locator(".atlas-list-up")).toBeDisabled();
  await expect(entries(page).last().locator(".atlas-list-down")).toBeDisabled();
});

test("entries are added, moved and removed, a removal undoes, and the model saved holds the list as shown", async ({ page }) => {
  const state = await open(page);
  await tab(page, "Kostenstellen").click();
  await page.getByRole("button", { name: "Eintrag hinzufügen" }).click();
  await expect(entries(page)).toHaveCount(3);
  await entries(page).nth(2).locator("[contenteditable]").first().click();
  await page.keyboard.type('{value: "CC300", label: "Logistik"}');
  await page.getByRole("button", { name: "Eintrag 3 nach oben verschieben" }).click();
  expect(await texts(page)).toEqual([
    '{value: "CC100", label: "Einkauf"}', '{value: "CC300", label: "Logistik"}', '{value: "CC200", label: "Verkauf"}']);

  await page.getByRole("button", { name: "Eintrag 1 entfernen" }).click();
  await expect(entries(page)).toHaveCount(2);
  await page.keyboard.press("Control+z");
  await expect(entries(page)).toHaveCount(3);
  await page.keyboard.press("Control+y");
  await expect(entries(page)).toHaveCount(2);

  const xml = await savedModel(page, state);
  const listed = [...xml.matchAll(/<(?:dmn:)?literalExpression[^>]*>\s*<(?:dmn:)?text>([^<]*)<\/(?:dmn:)?text>/g)].map((m) => m[1]);
  expect(listed.slice(0, 2)).toEqual([
    '{value: "CC300", label: "Logistik"}', '{value: "CC200", label: "Verkauf"}']);
  expect(xml).toMatch(/<(dmn:)?list id="List_ks">/);
});

test("the replace menu turns a decision into a list, keeping its id and its requirements", async ({ page }) => {
  const state = await open(page);
  await page.locator('.djs-element[data-element-id="Decision_rabatt"]').click();
  await page.locator('.djs-context-pad .entry[data-action="replace"]').click();
  const entry = page.locator('.djs-popup .entry[data-id="replace-with-list"]');
  await expect(entry).toHaveText("Liste");
  await entry.click();

  // The decision carries the overlay that opens a list, and its tab opens on one empty
  // entry to type into.
  await expect(page.locator('.djs-overlay .drill-down-overlay .atlas-icon-list')).toHaveCount(2);
  await tab(page, "Rabatt").click();
  await expect(entries(page)).toHaveCount(1);

  const xml = await savedModel(page, state);
  const rabatt = xml.slice(xml.indexOf('id="Decision_rabatt"'));
  const decision = rabatt.slice(0, rabatt.indexOf("decision>"));
  expect(decision).toContain('href="#Input_menge"');
  expect(decision).toMatch(/<(dmn:)?list /);
  expect(decision).not.toMatch(/decisionTable/);
  // A list decision is not offered List again.
  await tab(page, "Übersicht").click();
  await page.locator('.djs-element[data-element-id="Decision_ks"]').click();
  await page.locator('.djs-context-pad .entry[data-action="replace"]').click();
  await expect(page.locator(".djs-popup .entry")).toHaveText(["Leer", "Entscheidungstabelle", "Literal Expression"]);
});

test("a tab opens the decision as it is now after the replace menu changed its kind", async ({ page }) => {
  // dmn-js compares views by id, so turning a table into a literal expression reported
  // no change, and the tab kept opening the table of a decision no longer in the model.
  await open(page);
  await page.locator('.djs-element[data-element-id="Decision_rabatt"]').click();
  await page.locator('.djs-context-pad .entry[data-action="replace"]').click();
  await page.locator('.djs-popup .entry[data-id="replace-with-literal-expression"]').click();
  await tab(page, "Rabatt").click();
  await expect(page.locator(".dmn-literal-expression-container")).toBeVisible();
  await expect(page.locator(".dmn-decision-table-container")).toHaveCount(0);
});
