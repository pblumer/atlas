// The decision editor is a page of the Modeler, not a window over one
// (ADR-draft-the-decision-editor-is-a-page), and its Save keeps a draft rather than
// writing the model every reference resolves (ADR-draft-decision-drafts). These
// tests pin what that buys, because none of it is visible to the Go suite: a decision
// has an address, the editor wears the chrome its siblings wear, Save and Save to
// model write different things, a model save moves the URL onto the decision it just
// wrote, and nothing anywhere mounts an overlay.
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

// installMock stands in for the server: one application, one stored decision, the
// decision drafts it is holding, and the writes a save makes. It records them so a
// test can assert what the editor sent rather than only what it then showed.
//
// `taken` makes the model upload answer 409 the way the server does when the handle
// a decision would land on is already somebody else's (ADR-0222), unless the request
// says the author chose to replace it.
function installMock(page, { refs = [], drafts = [], taken = false } = {}) {
  const uploads = [];
  const created = [];
  const patched = [];
  const draftSaves = [];
  const draftDeletes = [];
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
    if (path === "/api/v1/dmn-drafts" && request.method() === "GET") {
      return route.fulfill({ json: drafts });
    }
    if (path === "/api/v1/dmn-drafts" && request.method() === "POST") {
      const payload = request.postDataJSON();
      draftSaves.push(payload);
      const id = payload.id || "dd-new";
      return route.fulfill({ json: { ...payload, id, name: "Decision", savedAt: 1 } });
    }
    if (path.startsWith("/api/v1/dmn-drafts/") && path.endsWith("/xml")) {
      return route.fulfill({ body: DRAFT_XML, contentType: "application/xml" });
    }
    if (path.startsWith("/api/v1/dmn-drafts/") && request.method() === "DELETE") {
      draftDeletes.push(path.split("/").pop());
      return route.fulfill({ status: 204, body: "" });
    }
    if (path.endsWith("/xml") && path.startsWith("/api/v1/dmn-models/")) {
      return route.fulfill({ body: STORED_XML, contentType: "application/xml" });
    }
    if (path === "/api/v1/dmn-models" && request.method() === "POST") {
      uploads.push({ query: url.search, body: request.postData() });
      if (taken && url.searchParams.has("from") && url.searchParams.get("overwrite") !== "true") {
        return route.fulfill({
          status: 409,
          json: { error: 'a decision model is already stored as "decision.dmn" — rename this decision, or save it over that model deliberately' },
        });
      }
      return route.fulfill({ json: { modelRef: "decision", modelName: "Decision", decisions: ["Decision"] } });
    }
    return route.fulfill({ json: [] });
  });
  return { uploads, created, patched, draftSaves, draftDeletes };
}

// A decision as it looks in a draft: the same shape as the stored model, with a
// decision named differently so a test can tell which of the two was opened.
const DRAFT_XML = STORED_XML.replace('name="eligibility"', 'name="eligibility-wip"');

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

  // English, like the rest of the Modeler — and the BPMN editor's pairing: a neutral
  // Save that keeps your work, a primary button that ships it.
  await expect(page.locator("#dmn-save")).toHaveText("Save");
  await expect(page.locator("#dmn-save-model")).toHaveText("Save to model");
  // Nothing is drafted yet, so neither the marker nor the way out of one is offered.
  await expect(page.locator("#dmn-draft-chip")).toBeHidden();
  await expect(page.locator("#dmn-discard")).toBeHidden();
  expect(pageErrors).toEqual([]);
});

test("Save keeps a draft and writes nothing anything else resolves", async ({ page }) => {
  const state = installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  await page.locator("#dmn-save").click();
  await expect(page.locator("#dmn-status")).toHaveText("Draft saved");

  // The draft carries the application it was opened for, and no model or reference
  // was written: that is the whole point of pressing Save.
  expect(state.draftSaves).toHaveLength(1);
  expect(state.draftSaves[0].projectId).toBe("app-1");
  expect(state.draftSaves[0].refId).toBe("");
  expect(state.draftSaves[0].xml).toContain("<decision");
  expect(state.uploads).toEqual([]);
  expect(state.created).toEqual([]);

  // A decision that is not in the model has no reference to be addressed by, so the
  // URL now addresses the draft — and the bar says the work is not shared yet.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/d/dd-new");
  await expect(page.locator("#dmn-draft-chip")).toBeVisible();
  await expect(page.locator("#dmn-discard")).toBeVisible();

  // Saving again updates that draft rather than minting a second one.
  await page.locator("#dmn-save").click();
  await expect.poll(() => state.draftSaves.length).toBe(2);
  expect(state.draftSaves[1].id).toBe("dd-new");
});

test("Save to model writes the model and the reference, clears the draft, and moves the URL onto the decision", async ({ page }) => {
  const state = installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  // Draft first, so the model save has one to clear.
  await page.locator("#dmn-save").click();
  await expect(page.locator("#dmn-status")).toHaveText("Draft saved");

  await page.locator("#dmn-save-model").click();
  await expect(page.locator("#dmn-status")).toHaveText("Saved to the model");

  // What the editor sent: the seeded model under a name taken from the decision,
  // identity-aware (?from=) so a taken handle is refused rather than forked, and a
  // reference filing it under the application it was opened for.
  expect(state.uploads).toHaveLength(1);
  expect(state.uploads[0].query).toBe("?name=Decision&from=");
  expect(state.uploads[0].body).toContain("<decision");
  expect(state.created).toEqual([{ name: "Decision", modelRef: "decision", projectId: "app-1" }]);

  // A draft exists only while it differs from the model, so it is gone.
  expect(state.draftDeletes).toEqual(["dd-new"]);
  await expect(page.locator("#dmn-draft-chip")).toBeHidden();

  // The URL now addresses the stored decision, so the next save updates it and a
  // reload comes back to what was just written.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/e/ref-new");
  await expect(page.locator("#dmn-ref-chip")).toHaveText("decision.dmn");

  // A second model save is an update in place: the handle, not a fresh name.
  await page.locator("#dmn-save-model").click();
  await expect.poll(() => state.uploads.length).toBe(2);
  expect(state.uploads[1].query).toBe("?handle=decision");
  expect(state.created).toHaveLength(1);
});

test("a model handle another decision holds is asked about, not silently forked", async ({ page }) => {
  const state = installMock(page, { taken: true });
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  // Declining leaves everything as it was: no reference, no second copy.
  page.once("dialog", (d) => d.dismiss());
  await page.locator("#dmn-save-model").click();
  await expect.poll(() => state.uploads.length).toBe(1);
  expect(state.created).toEqual([]);
  await expect(page.locator("#dmn-ref-chip")).toBeHidden();

  // Accepting sends the replacement as a deliberate act, which is the only way the
  // model under that handle is overwritten (ADR-0222).
  const asked = [];
  page.once("dialog", (d) => { asked.push(d.message()); d.accept(); });
  await page.locator("#dmn-save-model").click();
  await expect(page.locator("#dmn-status")).toHaveText("Saved to the model");
  expect(asked[0]).toContain("decision.dmn");
  expect(state.uploads[state.uploads.length - 1].query).toBe("?name=Decision&from=&overwrite=true");
  expect(state.created).toHaveLength(1);
});

test("opening a decision that has a draft opens the draft, and Discard goes back to the model", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
    drafts: [{ id: "ref-1", refId: "ref-1", modelRef: "eligibility", projectId: "app-1", name: "eligibility-wip", savedAt: 1 }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  // The author's work, not the model it has not been written to.
  await expect(page.locator(".editor-bar .etabs#dmn-views button").nth(1)).toHaveText("eligibility-wip");
  await expect(page.locator("#dmn-draft-chip")).toBeVisible();
  await expect(page.locator("#dmn-ref-chip")).toHaveText("eligibility.dmn");

  page.once("dialog", (d) => d.accept());
  await page.locator("#dmn-discard").click();
  expect(state.draftDeletes).toEqual(["ref-1"]);
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

test("the application lists a decision that is only a draft, and marks one that has a draft", async ({ page }) => {
  installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
    drafts: [
      { id: "ref-1", refId: "ref-1", modelRef: "eligibility", projectId: "app-1", name: "Eligibility", savedAt: 2 },
      { id: "dd-1", refId: "", projectId: "app-1", name: "Pricing", savedAt: 1 },
    ],
  });
  await page.goto("/index.html#/modeler/p/app-1");

  // The decision that is in the model carries the marker: what a publish ships is
  // the model, not the work sitting on top of it.
  const inModel = page.locator("#pd-rows tr", { hasText: "Eligibility" });
  await expect(inModel.locator(".draft-chip")).toHaveText("Draft");
  await expect(inModel.locator("a").first()).toHaveAttribute("href", "#/modeler/dmn/ref-1");

  // The decision that is *only* a draft is a row of its own — nothing else in this
  // table represents it — and it says it will not travel.
  const loose = page.locator("#pd-rows tr", { hasText: "Pricing" });
  await expect(loose.locator("a").first()).toHaveAttribute("href", "#/modeler/dmn/d/dd-1");
  await expect(loose).toContainText("not in the model yet");
});

test("a draft saved on the way from a business rule task says the task is not wired yet", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1/for/orders/Activity_rule");
  await editorReady(page);

  // Adoption means "this task calls that decision", and a decision that is not in
  // the model is not callable — so Save says so rather than leaving the author to
  // find the task still empty.
  await page.locator("#dmn-save").click();
  await expect(page.locator("#dmn-status")).toHaveText("Draft saved — save to the model to wire the task");
  // And the draft keeps the way back: the route still names the diagram and the task.
  await expect.poll(() => page.evaluate(() => location.hash))
    .toBe("#/modeler/dmn/d/dd-new/for/orders/Activity_rule");
});

test("the application's Create new menu navigates to the decision editor", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/modeler/p/app-1");
  await page.getByRole("button", { name: "Create new" }).click();
  await page.locator('button[data-act="newdec"]').click();

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/new/p/app-1");
  await editorReady(page);
});
