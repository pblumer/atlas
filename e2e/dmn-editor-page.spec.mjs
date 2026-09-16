// The decision editor is a page of the Modeler, not a window over one
// (ADR-0320), and its Save keeps a draft rather than writing the model every
// reference resolves (ADR-0321). These tests pin what that buys,
// because none of it is visible to the Go suite: a decision has an address, the editor
// wears the chrome its siblings wear, Save and Save to model write different things, a
// model save moves the URL onto the decision it just wrote, and nothing anywhere mounts
// an overlay.
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
function installMock(page, { refs = [], drafts = [], taken = false, deployed = [], trial = null, docs = [] } = {}) {
  const uploads = [];
  const created = [];
  const patched = [];
  const draftSaves = [];
  const draftDeletes = [];
  const deploys = [];
  const tries = [];
  const layouts = [];
  const docPublishes = [];
  const docActions = [];
  let nextKey = 7;
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
    if (path === "/api/v1/decisions/evaluate" && request.method() === "POST") {
      const payload = request.postDataJSON();
      tries.push(payload);
      // The server describes the model out of the same compile that runs it, so the
      // mock answers both halves the same way: decisions always, outputs only when a
      // decision was named.
      const described = {
        ok: true,
        modelName: "Eligibility",
        decisions: [{ id: "Decision_stored", name: "eligibility", inputs: [{ name: "amount", type: "number" }], output: { name: "result", type: "string" } }],
      };
      if (!payload.decisionId) return route.fulfill({ json: described });
      if (trial) return route.fulfill({ json: { ...described, ...trial } });
      const approved = Number(payload.inputs && payload.inputs.amount) >= 100;
      return route.fulfill({
        json: {
          ...described,
          decisionId: payload.decisionId,
          outputs: { eligibility: approved ? "approve" : "reject" },
          trace: {
            tables: [{
              hitPolicy: "U",
              inputs: [{ expression: "amount", value: payload.inputs && payload.inputs.amount }],
              rules: [
                { index: 0, matched: approved, conditions: [{ entry: ">= 100", matched: approved }], outputs: ["approve"] },
                { index: 1, matched: !approved, conditions: [{ entry: "< 100", matched: !approved }], outputs: ["reject"] },
              ],
            }],
          },
        },
      });
    }
    if (/^\/api\/v1\/decisions\/[^/]+\/documentation$/.test(path)) {
      if (request.method() === "GET") return route.fulfill({ json: docs });
      const payload = request.postDataJSON();
      docPublishes.push({ path, body: payload });
      const version = docs.length + 1;
      docs.unshift({
        id: "doc-" + version, decisionId: path.split("/")[4], version,
        title: payload.title, note: payload.note, createdAt: 1789000000, createdBy: "pat",
        pdfUrl: "/api/v1/decision-docs/doc-" + version + "/pdf",
      });
      return route.fulfill({ json: docs[0] });
    }
    if (/^\/api\/v1\/decisions\/[^/]+\/documentation\/prune$/.test(path)) {
      docActions.push({ what: "prune", body: request.postDataJSON() });
      return route.fulfill({ json: { deleted: [], kept: 1 } });
    }
    if (path.startsWith("/api/v1/decision-docs/")) {
      docActions.push({ what: request.method() + " " + path });
      return route.fulfill({ json: {} });
    }
    if (path === "/api/v1/dmn-layout" && request.method() === "POST") {
      layouts.push(request.postData());
      return route.fulfill({ body: RELAID_XML, contentType: "application/xml" });
    }
    if (path === "/api/v1/decision-deployments" && request.method() === "GET") {
      const id = url.searchParams.get("decisionId");
      return route.fulfill({ json: deployed.filter((d) => !id || d.decisionId === id) });
    }
    if (path === "/api/v1/decision-deployments" && request.method() === "POST") {
      const body = request.postData() || "";
      deploys.push({ query: url.search, body });
      // The record is versioned by the decision id in the XML that was posted, the
      // way the server versions it — so the chip the editor then reads back is about
      // the decision the author actually deployed.
      const decisionId = (body.match(/<decision\s+id="([^"]+)"/) || [])[1] || "Decision";
      const version = deployed.filter((d) => d.decisionId === decisionId).length + 1;
      for (const d of deployed) if (d.decisionId === decisionId) d.current = false;
      const row = { key: nextKey++, decisionId, version, current: true };
      deployed.push(row);
      return route.fulfill({ json: { key: row.key, decisions: [row] } });
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
  return { uploads, created, patched, draftSaves, draftDeletes, deploys, deployed, tries, layouts, docPublishes, docActions, docs };
}

// A decision as it looks in a draft: the same shape as the stored model, with a
// decision named differently so a test can tell which of the two was opened.
const DRAFT_XML = STORED_XML.replace('name="eligibility"', 'name="eligibility-wip"');

// What the auto-layout route hands back: the same model with the decision moved, so
// a test can tell that the editor imported the server's answer rather than keeping
// what it had.
const RELAID_XML = STORED_XML.replace('x="160" y="100"', 'x="60" y="60"');

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

  // English, like the rest of the Modeler — and the BPMN editor's grammar: neutral
  // buttons for what you keep, one primary button for the act that leaves the
  // browser. Three verbs, because a decision has three layers
  // (ADR-0321, ADR-0322).
  await expect(page.locator("#dmn-save")).toHaveText("Save");
  await expect(page.locator("#dmn-save-model")).toHaveText("Save to model");
  await expect(page.locator("#dmn-deploy")).toHaveText("Deploy");
  await expect(page.locator("#dmn-save-model")).toHaveClass(/neutral/);
  await expect(page.locator("#dmn-deploy")).not.toHaveClass(/neutral/);
  // Nothing is drafted or deployed yet, so neither marker is offered, nor the way
  // out of a draft.
  await expect(page.locator("#dmn-draft-chip")).toBeHidden();
  await expect(page.locator("#dmn-deployed-chip")).toBeHidden();
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

  // One handler for both prompts, answering in the order they are asked — the same
  // shape worker-delete.spec.mjs uses, and here it is load-bearing rather than tidy.
  // Two `page.once` handlers raced: the mock records the upload *before* it answers
  // 409, and the 409 is what raises the prompt, so waiting on `uploads.length` let
  // this test arm the second handler while the first was still waiting for its
  // dialog. The next prompt then fired both — dismiss, then accept on an already
  // handled dialog — which failed the accept and left the save declined at
  // "Saving to the model…". Waiting on the *answer* is what makes each step ordered.
  const asked = [];
  const answers = ["dismiss", "accept"];
  page.on("dialog", async (d) => {
    asked.push(d.message());
    await (answers.shift() === "accept" ? d.accept() : d.dismiss());
  });

  // Declining leaves everything as it was: no reference, no second copy.
  await page.locator("#dmn-save-model").click();
  await expect.poll(() => asked.length).toBe(1);
  await expect.poll(() => state.uploads.length).toBe(1);
  expect(state.created).toEqual([]);
  await expect(page.locator("#dmn-ref-chip")).toBeHidden();

  // Accepting sends the replacement as a deliberate act, which is the only way the
  // model under that handle is overwritten (ADR-0222).
  await page.locator("#dmn-save-model").click();
  await expect.poll(() => asked.length).toBe(2);
  await expect(page.locator("#dmn-status")).toHaveText("Saved to the model");
  expect(asked[1]).toContain("decision.dmn");
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

test("Deploy ships the decision to the engine, and says so on the bar", async ({ page }) => {
  const state = installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  await editorReady(page);

  await page.locator("#dmn-deploy").click();
  await expect(page.locator("#dmn-status")).toHaveText("Deployed v1 · key 7");

  // What was sent: the decision on screen, filed under the application it was
  // opened for. And nothing else was written — Deploy is not Save and not Save to
  // model, which is the separation the three buttons exist for.
  expect(state.deploys).toHaveLength(1);
  expect(state.deploys[0].query).toBe("?projectId=app-1");
  expect(state.deploys[0].body).toContain("<decision");
  expect(state.uploads).toEqual([]);
  expect(state.draftSaves).toEqual([]);
  expect(state.created).toEqual([]);

  // The version and the key are on the bar, the way a diagram opened from a
  // deployment carries its key.
  await expect(page.locator("#dmn-deployed-chip")).toHaveText("Deployed v1 · key 7");

  // The trap this buys is named where it happens: the decision runs, but nothing
  // can call it by name until it is in the model.
  await expect(page.locator("#toast")).toContainText("not in the model yet");

  // Deploying again is a new version under a new key, and the bar follows.
  await page.locator("#dmn-deploy").click();
  await expect(page.locator("#dmn-deployed-chip")).toHaveText("Deployed v2 · key 8");
});

test("a decision that is in the model deploys with its reference and handle, and is not warned about", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-deploy").click();
  await expect(page.locator("#dmn-status")).toHaveText("Deployed v1 · key 7");

  // Provenance the record keeps: which application, which reference, which model
  // handle this was authored as.
  expect(state.deploys[0].query).toBe("?projectId=app-1&artifactId=ref-1&modelRef=eligibility");
  // It is in the model, so there is nothing to warn about — the toast is the plain
  // confirmation.
  await expect(page.locator("#toast")).toContainText("deployed as version 1");
  await expect(page.locator("#toast")).not.toContainText("not in the model yet");
});

test("the bar says which version a decision is deployed at the moment it opens", async ({ page }) => {
  installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
    // Two versions of the same decision: only the current one is what a process
    // deployed now would bind to, so only it belongs on the bar.
    deployed: [
      { key: 4, decisionId: "Decision_stored", version: 1, current: false },
      { key: 9, decisionId: "Decision_stored", version: 2, current: true },
    ],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await expect(page.locator("#dmn-deployed-chip")).toHaveText("Deployed v2 · key 9");
  await expect(page.locator("#dmn-deployed-chip")).toHaveAttribute("title", /version 2 under definition key 9/);
});

test("a decision nothing has deployed yet says nothing, rather than saying zero", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await expect(page.locator("#dmn-ref-chip")).toHaveText("eligibility.dmn");
  await expect(page.locator("#dmn-deployed-chip")).toBeHidden();
});

test("Test runs the decision on screen and shows which rule fired", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-test").click();
  await expect(page.locator("#dmn-test-panel")).toBeVisible();

  // The form is built from what the server said the model wants, not from anything
  // the browser re-derived out of the XML.
  const amount = page.locator('#dmn-test-inputs input[data-in="amount"]');
  await expect(amount).toBeVisible();
  expect(state.tries[0].decisionId).toBeUndefined();
  expect(state.tries[0].xml).toContain("<decision");

  await amount.fill("250");
  await page.locator("#dmn-test-run").click();

  // The answer, and the rule matrix that says why — the same matrix Operations draws.
  await expect(page.locator("#dmn-test-result .res-val")).toHaveText("approve");
  await expect(page.locator("#dmn-test-result .mtable-head")).toContainText("Rule 1 fired");
  await expect(page.locator("#dmn-test-result .mrule.is-hit")).toHaveCount(1);

  // What was sent: the model on screen, the decision named, and the value typed —
  // coerced to a number, because that is what the decision declares.
  const run = state.tries[state.tries.length - 1];
  expect(run.decisionId).toBe("Decision_stored");
  expect(run.inputs).toEqual({ amount: 250 });

  // And nothing was saved or deployed by asking.
  expect(state.uploads).toEqual([]);
  expect(state.draftSaves).toEqual([]);
  expect(state.deploys).toEqual([]);

  // A different value takes a different rule, which is the whole point.
  await amount.fill("12");
  await page.locator("#dmn-test-run").click();
  await expect(page.locator("#dmn-test-result .res-val")).toHaveText("reject");
  await expect(page.locator("#dmn-test-result .mtable-head")).toContainText("Rule 2 fired");
});

test("a decision that does not run says so in the panel, not as a broken page", async ({ page }) => {
  installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
    trial: { ok: false, message: "eligibility: cannot compare number to string" },
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-test").click();
  await page.locator("#dmn-test-run").click();

  await expect(page.locator("#dmn-test-err")).toContainText("cannot compare number to string");
  await expect(page.locator("#dmn-test-panel")).toBeVisible();
  // The editor is untouched: a table that does not work yet is the normal state of
  // one being written.
  await expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible();
});

test("Auto-layout re-flows the requirements graph through the server", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-more").click();
  await expect(page.locator("#dmn-menu")).toBeVisible();
  await page.locator("#dmn-autolayout").click();

  await expect(page.locator("#toast")).toContainText("laid out");
  expect(state.layouts).toHaveLength(1);
  expect(state.layouts[0]).toContain("<decision");
  // The model came back through the editor, not around it: nothing was stored.
  expect(state.uploads).toEqual([]);
});

test("Export XML hands the decision over as a file", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-more").click();
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#dmn-export").click(),
  ]);
  // Named after the model handle, so the file lands as the decision people know.
  expect(download.suggestedFilename()).toBe("eligibility.dmn");
});

// The diagram Atlas generates for a model that has none must be one dmn-js draws
// (ADR-0325). This is the contract between the Go
// generator's output and the vendored editor, so the fixture below is *verbatim*
// what `dmn.EnsureDiagram` produces for a model with one input datum feeding one
// decision — regenerate it if the generator's shape changes, which the Go tests in
// dmn/layout_test.go will tell you about first.
const COMPLETED_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="Definitions_nodi" name="Eligibility" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"/>
  <decision id="eligibility" name="eligibility">
    <informationRequirement id="ir1"><requiredInput href="#id_amount"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1" label="amount"><inputExpression id="ie1" typeRef="number"><text>amount</text></inputExpression></input>
      <output id="out1" label="eligibility" name="eligibility" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 100</text></inputEntry><outputEntry id="o1"><text>"approve"</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/">
    <dmndi:DMNDiagram id="DMNDiagram_atlas">
      <dmndi:DMNShape id="DMNShape_id_amount" dmnElementRef="id_amount">
        <dc:Bounds x="60" y="220" width="125" height="45"/>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_eligibility" dmnElementRef="eligibility">
        <dc:Bounds x="60" y="60" width="180" height="80"/>
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="DMNEdge_ir1" dmnElementRef="ir1">
        <di:waypoint x="122.5" y="220"/>
        <di:waypoint x="150" y="140"/>
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

test("a model the server completed draws its whole requirements graph", async ({ page }) => {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/dmnrefs" && request.method() === "GET") {
      return route.fulfill({ json: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "" }] });
    }
    if (path.startsWith("/api/v1/dmn-models/") && path.endsWith("/xml")) {
      return route.fulfill({ body: COMPLETED_XML, contentType: "application/xml" });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);
  await page.locator("#dmn-views button", { hasText: "Overview (DRG)" }).click();

  // All three, and the arrow between them. Without the generated diagram dmn-js
  // draws the decision alone and silently drops the input data and the requirement,
  // so the graph the model describes cannot be seen or rewired.
  for (const id of ["id_amount", "eligibility", "ir1"]) {
    await expect(page.locator(`.dmn-canvas [data-element-id="${id}"]`)).toBeVisible();
  }
});

test("Documentation publishes a version of the decision and lists its history", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-more").click();
  await page.locator("#dmn-docexport").click();
  await expect(page.locator("#dmn-doc-panel")).toBeVisible();
  await expect(page.locator("#dmn-doc-history")).toContainText("No version published yet");

  await page.locator("#dmn-doc-note").fill("Signed off in March");
  await page.locator("#dmn-doc-publish").click();

  // Filed under the decision the model declares, with the rules and the document.
  await expect.poll(() => state.docPublishes.length).toBe(1);
  const sent = state.docPublishes[0];
  expect(sent.path).toBe("/api/v1/decisions/Decision_stored/documentation");
  expect(sent.body.note).toBe("Signed off in March");
  expect(sent.body.modelRef).toBe("eligibility");
  expect(sent.body.decisions[0].id).toBe("Decision_stored");
  expect(sent.body.pdfBase64.length).toBeGreaterThan(100);

  // And the history now shows it, with its download and a way to share it.
  await expect(page.locator("#dmn-doc-history")).toContainText("v1");
  await expect(page.locator("#dmn-doc-history")).toContainText("Signed off in March");
  await expect(page.locator('#dmn-doc-history [data-share]')).toBeVisible();

  // Nothing else was written by documenting: a document describes the decision,
  // it does not change it.
  expect(state.uploads).toEqual([]);
  expect(state.deploys).toEqual([]);
  expect(state.draftSaves).toEqual([]);
});

test("a published version can be shared, and deleting one is confirmed first", async ({ page }) => {
  const state = installMock(page, {
    refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }],
    docs: [{ id: "doc-1", decisionId: "Decision_stored", version: 1, createdAt: 1789000000, pdfUrl: "/api/v1/decision-docs/doc-1/pdf" }],
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await page.locator("#dmn-more").click();
  await page.locator("#dmn-docexport").click();
  await expect(page.locator("#dmn-doc-history")).toContainText("v1");

  await page.locator('#dmn-doc-history [data-share]').click();
  await expect.poll(() => state.docActions.map((a) => a.what))
    .toContain("POST /api/v1/decision-docs/doc-1/share");

  // Deleting a published version destroys an artifact somebody handed out, so it
  // asks first — and a declined confirmation deletes nothing.
  page.once("dialog", (d) => d.dismiss());
  await page.locator('#dmn-doc-history [data-delete]').click();
  await page.waitForTimeout(200);
  expect(state.docActions.filter((a) => (a.what || "").startsWith("DELETE"))).toEqual([]);
});

test("a decision whose logic is a literal expression does not cover the editor bar", async ({ page }) => {
  // dmn-js uses `editor` as a state class inside its own components, and Atlas's
  // `.editor` is the full-bleed page shell: without the reset in app.css the
  // literal-expression view is pinned over the whole viewport, and the tabs, Save,
  // Deploy and Test underneath it cannot be clicked. A fixed element is not
  // clipped by the canvas, so nothing else stops it.
  const LITERAL = STORED_XML.replace(
    /<decisionTable[\s\S]*<\/decisionTable>/,
    `<literalExpression id="le1"><text>0.1 * amount</text></literalExpression>`);
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  await page.route("**/api/v1/dmn-models/*/xml", (route) =>
    route.fulfill({ body: LITERAL, contentType: "application/xml" }));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  // Open the decision's own view, which is the literal expression editor.
  await page.locator(".editor-bar .etabs#dmn-views button").nth(1).click();
  await expect(page.locator(".dmn-canvas .cm-editor")).toBeVisible();

  // The bar is still the thing at the bar's coordinates, and still usable.
  const onTop = await page.evaluate(() => {
    const tab = document.querySelector(".editor-bar .etabs#dmn-views button");
    const r = tab.getBoundingClientRect();
    const at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return at ? at.tagName : "none";
  });
  expect(onTop).toBe("BUTTON");
  await page.locator("#dmn-test").click();
  await expect(page.locator("#dmn-test-panel")).toBeVisible();
});

// A business knowledge model — a reusable FEEL function a decision can invoke —
// does not open in the literal-expression view a decision's expression opens in.
// dmn-js gives it the *boxed-expression* view: a different component, in its own
// container, with its own stylesheets.
//
// Those stylesheets were not loaded, and the failure was silent in exactly the way
// no other test catches. The view rendered: the kind marker, the parameter list, the
// expression body and the result variable were all in the DOM, editing worked, saving
// worked, and nothing errored. It was simply raw — no boxes, no borders, bare text at
// the page edge, and the edit buttons that belong to a hovered section sitting
// permanently on top of the content.
const BKM_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_bkm" name="Eligibility" namespace="http://atlas/dmn">
  <businessKnowledgeModel id="BKM_fee" name="fee">
    <variable name="fee" typeRef="number" />
    <encapsulatedLogic kind="FEEL">
      <formalParameter name="amount" typeRef="number" />
      <literalExpression id="LE_fee"><text>amount * 0.1</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_bkm">
      <dmndi:DMNShape id="DMNShape_bkm" dmnElementRef="BKM_fee">
        <dc:Bounds height="80" width="180" x="160" y="100" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

test("a knowledge model's expression opens styled, and the hint says what it is", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  await page.route("**/api/v1/dmn-models/*/xml", (route) =>
    route.fulfill({ body: BKM_XML, contentType: "application/xml" }));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  // The knowledge model has a tab of its own, beside the requirements graph.
  const tabs = page.locator(".editor-bar .etabs#dmn-views button");
  await expect(tabs).toHaveCount(2);
  await expect(tabs.nth(1)).toHaveText("fee");
  await tabs.nth(1).click();

  const box = page.locator(".dmn-canvas .dmn-boxed-expression-container");
  await expect(box).toBeVisible();
  // What the author came for is all there: the FEEL kind marker, the formal
  // parameters a caller passes, and the result variable a decision binds.
  await expect(box.locator(".function-definition-kind")).toContainText("F");
  await expect(box.locator(".function-definition-parameters")).toContainText("(amount: number)");
  await expect(box.locator(".element-variable")).toContainText("Result");

  // Styled, not merely present. Without dmn-js-boxed-expression.css the sections are
  // borderless — the rule is there but its colour resolves to nothing, so the border
  // shorthand computes away entirely — and the view is three runs of text on the
  // canvas rather than one box.
  const sections = await box.locator(".dmn-boxed-expression-section").count();
  expect(sections).toBeGreaterThan(1);
  const borders = await box.locator(".dmn-boxed-expression-section").evaluateAll((els) =>
    els.map((el) => getComputedStyle(el).borderLeftStyle));
  expect(borders.every((s) => s === "solid")).toBe(true);

  // And without dmn-js-boxed-expression-controls.css the edit buttons never hide:
  // they are meant to be clipped away until the section they belong to is hovered,
  // which is what keeps them off the expression the author is reading.
  // One for the function kind, one for the formal parameters.
  await expect(box.locator(".edit-button")).toHaveCount(2);
  const editButton = box.locator(".edit-button").first();
  expect(await editButton.evaluate((el) => getComputedStyle(el).clipPath)).toBe("inset(50%)");
  await box.locator(".function-definition-kind").hover();
  await expect.poll(() => editButton.evaluate((el) => getComputedStyle(el).clipPath)).toBe("none");

  // The hint under the canvas describes the view that is open. A knowledge model is
  // not a decision table, and saying so is the difference between a layout that
  // explains itself and one that looks broken.
  await expect(page.locator("#dmn-hint")).toContainText("knowledge model");
  await expect(page.locator("#dmn-hint")).not.toContainText("Model the decision table");
  await tabs.nth(0).click();
  await expect(page.locator("#dmn-hint")).toContainText("decision requirements graph");
});

// A knowledge model only runs when something invokes it, and DMN says the invoking
// decision declares a knowledge requirement for it — the arrow the DRG draws. temis
// does not enforce that: a decision whose expression calls one by name evaluates
// correctly with no arrow at all (dmn/knowledgemodel_test.go pins the invocation;
// the same harness says the edge is optional to the engine).
//
// So the two ways a model's graph and its logic can disagree both deploy and both
// run, which is precisely why neither can be left to Deploy to report.
//
// WARN_XML holds one of each, beside a correctly wired pair that must stay quiet:
// "fee" is required and called; "unused rate" is neither; "tier" is called by
// "total" without being required.
const WARN_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_warn" name="Fee" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"><variable name="amount" typeRef="number"/></inputData>
  <businessKnowledgeModel id="bkm_fee" name="fee">
    <variable name="fee" typeRef="number"/>
    <encapsulatedLogic id="fd1" kind="FEEL">
      <formalParameter id="fp1" name="base" typeRef="number"/>
      <literalExpression id="le_fee"><text>base * 0.1</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <businessKnowledgeModel id="bkm_orphan" name="unused rate">
    <variable name="unused rate" typeRef="number"/>
    <encapsulatedLogic id="fd2" kind="FEEL">
      <formalParameter id="fp2" name="x" typeRef="number"/>
      <literalExpression id="le_orphan"><text>x * 2</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <businessKnowledgeModel id="bkm_tier" name="tier">
    <variable name="tier" typeRef="number"/>
    <encapsulatedLogic id="fd3" kind="FEEL">
      <formalParameter id="fp3" name="n" typeRef="number"/>
      <literalExpression id="le_tier"><text>n * 2</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <decision id="dec_total" name="total">
    <variable name="total" typeRef="number"/>
    <informationRequirement id="ir1"><requiredInput href="#id_amount"/></informationRequirement>
    <knowledgeRequirement id="kr1"><requiredKnowledge href="#bkm_fee"/></knowledgeRequirement>
    <literalExpression id="le_total"><text>amount + fee(amount) + tier(amount)</text></literalExpression>
  </decision>
  <dmndi:DMNDI><dmndi:DMNDiagram id="DMNDiagram_warn">
    <dmndi:DMNShape id="s1" dmnElementRef="id_amount"><dc:Bounds x="60" y="320" width="160" height="70"/></dmndi:DMNShape>
    <dmndi:DMNShape id="s2" dmnElementRef="bkm_fee"><dc:Bounds x="300" y="320" width="160" height="70"/></dmndi:DMNShape>
    <dmndi:DMNShape id="s3" dmnElementRef="bkm_orphan"><dc:Bounds x="520" y="320" width="160" height="70"/></dmndi:DMNShape>
    <dmndi:DMNShape id="s4" dmnElementRef="bkm_tier"><dc:Bounds x="740" y="320" width="160" height="70"/></dmndi:DMNShape>
    <dmndi:DMNShape id="s5" dmnElementRef="dec_total"><dc:Bounds x="300" y="140" width="160" height="70"/></dmndi:DMNShape>
  </dmndi:DMNDiagram></dmndi:DMNDI>
</definitions>`;

test("the editor says when a knowledge model is never invoked, or invoked without being required", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Fee", modelRef: "fee", projectId: "app-1" }] });
  await page.route("**/api/v1/dmn-models/*/xml", (route) =>
    route.fulfill({ body: WARN_XML, contentType: "application/xml" }));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  const strip = page.locator("#dmn-warn");
  await expect(strip).toBeVisible();
  // One <li> per finding. The message is a button (it navigates), and a finding may
  // carry a second one offering its repair, so the rows are counted by the item.
  await expect(strip.locator("li")).toHaveCount(2);
  const rows = strip.locator("li button[data-el]");
  // Both findings name the model they are about, and say what actually follows —
  // one is never evaluated, the other runs and draws a graph that omits it.
  await expect(rows.nth(0)).toContainText("Nothing invokes the knowledge model “unused rate”");
  await expect(rows.nth(1)).toContainText("“total” calls the knowledge model “tier” but does not require it");
  // The correctly wired pair is not mentioned: a warning an author learns to ignore
  // is worse than no warning.
  await expect(strip).not.toContainText("“fee”");

  // One badge per offending shape in the requirements graph: the knowledge model
  // nothing reaches, and the decision whose declaration is incomplete. The knowledge
  // model that is properly required is unmarked, and so is the one being called —
  // the incomplete declaration is the caller's.
  const badged = await page.locator(".dmn-canvas .djs-overlay .unsup-badge").count();
  expect(badged).toBe(2);

  // A finding points at its element. Clicked from a decision's own view — where the
  // graph is not on screen at all — it goes back to the graph first, because pointing
  // at a shape in a view that does not draw it would point at nothing.
  await page.locator(".editor-bar .etabs#dmn-views button", { hasText: "total" }).click();
  await expect(page.locator(".dmn-canvas .dmn-literal-expression-container")).toBeVisible();
  await expect(strip).toBeVisible(); // the findings are about the model, not the view
  await strip.locator("li button[data-el]").first().click();
  await expect(page.locator(".editor-bar .etabs#dmn-views button").first()).toHaveClass(/active/);
  await expect.poll(() => page.evaluate(() =>
    document.querySelectorAll(".dmn-canvas .djs-element.selected").length)).toBeGreaterThan(0);
});

test("the findings follow the model as it is edited, from whichever view is doing the editing", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Fee", modelRef: "fee", projectId: "app-1" }] });
  await page.route("**/api/v1/dmn-models/*/xml", (route) =>
    route.fulfill({ body: WARN_XML, contentType: "application/xml" }));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  const strip = page.locator("#dmn-warn");
  await expect(strip).toContainText("calls the knowledge model “tier” but does not require it");

  // Edit the expression in the decision's own view — a different diagram-js instance
  // from the graph the findings are drawn on. Taking the call out of it changes which
  // finding is true: "tier" stops being called without being required, and starts
  // being a knowledge model nothing invokes at all.
  await page.locator(".editor-bar .etabs#dmn-views button", { hasText: "total" }).click();
  const body = page.locator(".dmn-canvas .cm-content");
  await expect(body).toBeVisible();
  await body.click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("amount + fee(amount)");

  // No save, no blur, no going back to the graph: the strip is about the model, and
  // the model changed.
  await expect(strip).toContainText("Nothing invokes the knowledge model “tier”");
  await expect(strip).not.toContainText("does not require it");
  await expect(strip.locator("li")).toHaveCount(2);
  // Both findings are now the kind with no determinate repair, so neither offers one.
  await expect(strip.locator(".dmn-warn-fix")).toHaveCount(0);
});

test("a decision model with nothing to say says nothing", async ({ page }) => {
  installMock(page, { refs: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);
  // The stored fixture is a plain decision table: no knowledge model, no finding, and
  // a strip that is present in the DOM but never shown.
  await expect(page.locator("#dmn-warn")).toBeHidden();
});

test("the missing requirement can be drawn from the finding, and the drawing is the author's own edit", async ({ page }) => {
  const state = installMock(page, { refs: [{ id: "ref-1", name: "Fee", modelRef: "fee", projectId: "app-1" }] });
  await page.route("**/api/v1/dmn-models/*/xml", (route) =>
    route.fulfill({ body: WARN_XML, contentType: "application/xml" }));
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  const strip = page.locator("#dmn-warn");
  await expect(strip.locator("li")).toHaveCount(2);
  // Only the finding with a determinate repair offers one. Which decision ought to call
  // an uninvoked knowledge model is the author's to decide, so that one has no button.
  const fix = strip.locator(".dmn-warn-fix");
  await expect(fix).toHaveCount(1);
  await expect(fix).toHaveText("Draw the requirement");

  await fix.click();

  // The finding is gone because the model changed, not because the strip was told to
  // hide it: the findings are recomputed from the model after every command.
  await expect(strip.locator("li")).toHaveCount(1);
  await expect(strip).not.toContainText("does not require it");
  await expect(strip).toContainText("Nothing invokes the knowledge model “unused rate”");
  await expect(page.locator(".dmn-canvas .djs-overlay .unsup-badge")).toHaveCount(1);

  // What was drawn is in the model, not only on the canvas — the save carries it.
  await page.locator("#dmn-save").click();
  await expect(page.locator("#dmn-status")).toHaveText("Draft saved");
  expect(state.draftSaves).toHaveLength(1);
  expect(state.draftSaves[0].xml).toMatch(/<knowledgeRequirement[\s\S]*?requiredKnowledge[^>]*#bkm_tier/);

  // And it is as easy to take back as it was to make, which is what makes offering the
  // button safe at all. The new connection is left selected, so its context pad is
  // already open on the one entry it has — the bin — and using it brings the finding
  // back. (Not Ctrl+Z: dmn-js's keyboard is bound to nothing reachable in this editor,
  // so no shortcut works here, for this edit or any other.)
  await expect(page.locator(".dmn-canvas .djs-element.selected")).toHaveCount(1);
  const bin = page.locator('.dmn-canvas .djs-context-pad .entry[data-action="delete"]');
  await expect(bin).toHaveCount(1);
  await bin.click();
  await expect(strip.locator("li")).toHaveCount(2);
  await expect(strip).toContainText("does not require it");
});
