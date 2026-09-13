// The decision editor is a page of the Modeler, not a window over one
// (ADR-0320). These tests pin what that buys, because
// none of it is visible to the Go suite: a decision has an address, the editor wears
// the chrome its siblings wear, a save moves the URL onto the decision it just wrote,
// and nothing anywhere mounts an overlay.
//
// The vendored dmn-js bundle is loaded for real (the static server serves ../api/web),
// so what is asserted here is the editor that ships.
import { test, expect } from "@playwright/test";

const STORED_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_stored" name="Eligibility" namespace="http://atlas/dmn">
  <decision id="Decision_stored" name="eligibility">
    <decisionTable id="DecisionTable_stored" hitPolicy="UNIQUE">
      <input id="Input_stored" label="amount">
        <inputExpression id="InputExpression_stored" typeRef="number"><text>amount</text></inputExpression>
      </input>
      <output id="Output_stored" name="result" typeRef="string" />
      <rule id="Rule_stored">
        <inputEntry id="InputEntry_stored"><text>&gt;= 100</text></inputEntry>
        <outputEntry id="OutputEntry_stored"><text>"approve"</text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_stored">
      <dmndi:DMNShape id="DMNShape_stored" dmnElementRef="Decision_stored">
        <dc:Bounds height="80" width="180" x="160" y="100" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

// installMock stands in for the server: one application, one stored decision, and
// the two writes a save makes. It records them so a test can assert what the editor
// sent rather than only what it then showed.
function installMock(page, { refs = [] } = {}) {
  const uploads = [];
  const created = [];
  const patched = [];
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Order Management", myRole: "owner" }] });
    }
    if (path === "/api/v1/dmnrefs" && request.method() === "GET") {
      return route.fulfill({ json: refs });
    }
    if (path === "/api/v1/dmnrefs" && request.method() === "POST") {
      const payload = request.postDataJSON();
      created.push(payload);
      return route.fulfill({ json: { id: "ref-new", ...payload } });
    }
    if (path.startsWith("/api/v1/dmnrefs/") && request.method() === "PATCH") {
      patched.push({ id: path.split("/").pop(), body: request.postDataJSON() });
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/xml") && path.startsWith("/api/v1/dmn-models/")) {
      return route.fulfill({ body: STORED_XML, contentType: "application/xml" });
    }
    if (path === "/api/v1/dmn-models" && request.method() === "POST") {
      uploads.push({ query: url.search, body: request.postData() });
      return route.fulfill({ json: { modelRef: "decision", modelName: "Decision", decisions: ["Decision"] } });
    }
    return route.fulfill({ json: [] });
  });
  return { uploads, created, patched };
}

// The editor is slow to first paint only because of the vendored bundle; everything
// asserted after it is instant.
const editorReady = (page) => expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible({ timeout: 20000 });

test("a new decision opens at its own address, in the editor chrome its siblings wear", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  // The frame is the BPMN/form editor's, not a modal's: no overlay exists anywhere,
  // and the page is in the Modeler's full-bleed editor mode.
  await expect(page.locator(".dmn-overlay")).toHaveCount(0);
  await expect(page.locator("body.editor-mode")).toHaveCount(1);
  await expect(page.locator(".dmn-editor .editor-bar")).toBeVisible();

  // Back goes to the application the decision is being filed into, by name.
  const back = page.locator("#dmn-back");
  await expect(back).toHaveAttribute("href", "#/modeler/p/app-1");
  await expect(back).toHaveText("← Order Management");

  // The tab strip is the .etabs strip, carrying the DRG overview and the seeded
  // decision's own table.
  await expect(page.locator(".editor-bar .etabs#dmn-views button").first()).toHaveText("Overview (DRG)");
  await expect(page.locator(".editor-bar .etabs#dmn-views button")).toHaveCount(2);

  // English, like the rest of the Modeler.
  await expect(page.locator("#dmn-save")).toHaveText("Save");
  expect(pageErrors).toEqual([]);
});

test("saving a new decision writes the model and the reference, and moves the URL onto it", async ({ page }) => {
  const state = installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  await page.locator("#dmn-save").click();
  await expect(page.locator("#dmn-status")).toHaveText("Saved");

  // What the editor sent: the seeded model under a name taken from the decision, and
  // a reference filing it under the application it was opened for.
  expect(state.uploads).toHaveLength(1);
  expect(state.uploads[0].query).toBe("?name=Decision");
  expect(state.uploads[0].body).toContain("<decision");
  expect(state.created).toEqual([{ name: "Decision", modelRef: "decision", projectId: "app-1" }]);

  // The URL now addresses the stored decision, so a second save updates it and a
  // reload comes back to what was just written.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/e/ref-new");
  await expect(page.locator("#dmn-ref-chip")).toHaveText("decision.dmn");

  // A second save is an update in place: the handle, not a fresh name.
  await page.locator("#dmn-save").click();
  await expect.poll(() => state.uploads.length).toBe(2);
  expect(state.uploads[1].query).toBe("?handle=decision");
  expect(state.created).toHaveLength(1);
});

test("an existing decision is reachable by deep link and loads its stored model", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  // Straight to the URL — no click path, which is the whole point of the address.
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await expect(page.locator("#dmn-ref-chip")).toHaveText("eligibility.dmn");
  // The stored model came back, not the seed: its decision names the tab.
  await expect(page.locator(".editor-bar .etabs#dmn-views button").nth(1)).toHaveText("eligibility");
  await expect(page.locator(".dmn-overlay")).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});

test("a decision reference that no longer exists says so instead of opening an empty editor", async ({ page }) => {
  installMock(page, { refs: [] });
  await page.goto("/index.html#/modeler/dmn/e/ref-gone");
  await expect(page.locator("#view")).toContainText("Decision not found");
  await expect(page.locator(".dmn-overlay")).toHaveCount(0);
});

test("the application's Create new menu navigates to the decision editor", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/modeler/p/app-1");
  await page.getByRole("button", { name: "Create new" }).click();
  await page.locator('button[data-act="newdec"]').click();

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/new/p/app-1");
  await editorReady(page);
});
