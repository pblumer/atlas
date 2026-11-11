// The decision a business rule task calls, one click from the task (api/web/editor.js
// drawDecisionBadges, api/web/decision-graph.js openDecisionPeek, api/web/decision-logic.js;
// ADR-draft-decision-peek).
//
// A form had its mark on the canvas since ADR-0465; a business rule task's decision was
// reachable only from the properties panel, and only by leaving the diagram for the
// decision editor. The mark opens the decision over the diagram instead: its
// requirements graph with the called decision marked, and the logic of whichever node
// the author picks — read from the engine's own views, not parsed in the browser.
//
// These drive the REAL app (index.html → app.js) against a mocked /api/v1, so the
// badge, the window and the door out of it are exercised as an author meets them.
import { test, expect } from "@playwright/test";

const XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_preis" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="preis" name="Preis" isExecutable="true">
    <bpmn:startEvent id="Start_1" name="Anfrage"><bpmn:outgoing>f0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:businessRuleTask id="Task_price" name="Preis bestimmen">
      <bpmn:extensionElements><zeebe:calledDecision decisionId="Price" resultVariable="price"/></bpmn:extensionElements>
      <bpmn:incoming>f0</bpmn:incoming><bpmn:outgoing>f1</bpmn:outgoing>
    </bpmn:businessRuleTask>
    <bpmn:businessRuleTask id="Task_empty" name="Noch offen"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:businessRuleTask>
    <bpmn:businessRuleTask id="Task_lost" name="Verloren">
      <bpmn:extensionElements><zeebe:calledDecision decisionId="Gone" resultVariable="x"/></bpmn:extensionElements>
      <bpmn:incoming>f2</bpmn:incoming><bpmn:outgoing>f3</bpmn:outgoing>
    </bpmn:businessRuleTask>
    <bpmn:businessRuleTask id="Task_pinned" name="Fest gebunden">
      <bpmn:extensionElements><zeebe:calledDecision decisionId="Price" resultVariable="p7" atlas:version="7"/></bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:businessRuleTask id="Task_solo" name="Nur deployt">
      <bpmn:extensionElements><zeebe:calledDecision decisionId="Solo" resultVariable="solo"/></bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="End_1" name="Fertig"><bpmn:incoming>f3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="Start_1" targetRef="Task_price"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Task_price" targetRef="Task_empty"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Task_empty" targetRef="Task_lost"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Task_lost" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="preis">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_price_di" bpmnElement="Task_price"><dc:Bounds x="260" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_empty_di" bpmnElement="Task_empty"><dc:Bounds x="420" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_lost_di" bpmnElement="Task_lost"><dc:Bounds x="580" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_pinned_di" bpmnElement="Task_pinned"><dc:Bounds x="260" y="240" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_solo_di" bpmnElement="Task_solo"><dc:Bounds x="420" y="240" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"><dc:Bounds x="740" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f0_di" bpmnElement="f0"><di:waypoint x="196" y="138"/><di:waypoint x="260" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="360" y="138"/><di:waypoint x="420" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="520" y="138"/><di:waypoint x="580" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f3_di" bpmnElement="f3"><di:waypoint x="680" y="138"/><di:waypoint x="740" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

// The model behind "Price": input data Age feeds a literal "Base", a decision table
// "Band", and the context "Price" that requires both — the shapes the server's logic
// read hands over as temis views.
const GRAPH = {
  resolved: true, valid: true, modelName: "Pricing",
  nodes: [
    { id: "in_age", type: "inputData", name: "Age", dataType: "number", x: 40, y: 300, width: 140, height: 50 },
    { id: "Base", type: "decision", name: "Base", dataType: "number", x: 40, y: 160, width: 160, height: 70 },
    { id: "Band", type: "decision", name: "Band", hasTable: true, x: 260, y: 160, width: 160, height: 70 },
    { id: "Price", type: "decision", name: "Price", x: 150, y: 20, width: 160, height: 70 },
  ],
  edges: [
    { type: "informationRequirement", source: "in_age", target: "Base" },
    { type: "informationRequirement", source: "in_age", target: "Band" },
    { type: "informationRequirement", source: "Base", target: "Price" },
    { type: "informationRequirement", source: "Band", target: "Price" },
  ],
};

const LOGIC = {
  resolved: true, valid: true, modelName: "Pricing",
  elements: [
    { id: "Base", element: "decision", name: "Base", kind: "literal", view: { decisionId: "Base", name: "Base", text: "Age + 10", typeRef: "number" } },
    { id: "Band", element: "decision", name: "Band", kind: "table", view: {
      decisionId: "Band", name: "Band", hitPolicy: "F",
      inputs: [{ label: "Age", expression: "Age", typeRef: "number" }],
      outputs: [{ name: "Band", typeRef: "string" }],
      rules: [
        { inputEntries: ["< 18"], outputEntries: ['"young"'] },
        { inputEntries: ["-"], outputEntries: ['"adult"'] },
      ],
    } },
    { id: "Price", element: "decision", name: "Price", kind: "context", view: {
      decisionId: "Price", name: "Price", simple: true,
      entries: [{ name: "factor", text: 'if Band = "young" then 0.5 else 1', index: 0 }],
      result: "Base * factor",
    } },
  ],
};

// The stored model — what the decision editor opens — has moved on since v2 was
// deployed: Base adds 20 where the deployed version adds 10.
const LOGIC_STORED = {
  ...LOGIC,
  elements: LOGIC.elements.map((e) => (e.id === "Base" ? { ...e, view: { ...e.view, text: "Age + 20" } } : e)),
};

// BOUND is what the server answers for the task's binding: Price evaluates the
// deployed version 2 (latest), while version 7 is not deployed and Gone has nothing.
function bound(q) {
  if (q.get("processKey")) {
    return { decisionId: "Price", perspective: "definition", processKey: Number(q.get("processKey")), binding: "latest",
      source: "decisionDeployment", key: 41, deployedVersion: 1, deployedAt: 1767225600, graph: GRAPH, logic: LOGIC };
  }
  if (q.get("binding") === "version") {
    return { decisionId: "Price", perspective: "draft", binding: "version", version: Number(q.get("version")),
      source: "none", reason: "versionNotDeployed", versions: [1, 2] };
  }
  return { decisionId: "Price", perspective: "draft", binding: "latest",
    source: "decisionDeployment", key: 42, deployedVersion: 2, deployedAt: 1767225600, graph: GRAPH, logic: LOGIC };
}

// Solo exists only as a decision deployment: no stored model, no reference — until
// the peek creates them.
const SOLO_GRAPH = { resolved: true, valid: true, modelName: "Solo-Modell",
  nodes: [{ id: "Solo", type: "decision", name: "Solo", x: 40, y: 40, width: 160, height: 70 }], edges: [] };
const SOLO_LOGIC = { resolved: true, valid: true, modelName: "Solo-Modell",
  elements: [{ id: "Solo", element: "decision", name: "Solo", kind: "literal", view: { decisionId: "Solo", name: "Solo", text: '"allein"' } }] };
const SOLO_XML = '<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Solo-Modell"><decision id="Solo" name="Solo"><literalExpression><text>"allein"</text></literalExpression></decision></definitions>';

function installMock(page, { modelTaken = false } = {}) {
  const state = { stored: XML, draftSaves: [], logicReads: 0, boundQueries: [], uploads: [], refsCreated: [],
    refs: [{ id: "ref-pricing", name: "Pricing", modelRef: "pricing", projectId: "" }] };
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/drafts" && method === "GET") return route.fulfill({ json: [{ processId: "preis", name: "Preis", projectId: "" }] });
    if (path === "/api/v1/drafts/preis/xml") return route.fulfill({ body: state.stored, contentType: "application/xml" });
    if (path === "/api/v1/drafts" && method === "POST") {
      state.stored = request.postData();
      state.draftSaves.push(state.stored);
      return route.fulfill({ json: { processId: "preis", name: "Preis" } });
    }
    if (path === "/api/v1/decisions") {
      const solo = state.refs.find((r) => r.id === "ref-solo");
      return route.fulfill({ json: [
        { id: "Price", name: "Price", model: "Pricing", modelRef: "pricing", inputs: [{ name: "Age" }], output: { name: "Price" } },
        { id: "Band", name: "Band", model: "Pricing", modelRef: "pricing", inputs: [{ name: "Age" }], output: { name: "Band" } },
        { id: "Solo", name: "Solo", model: "Solo-Modell", modelRef: solo ? solo.modelRef : "", deployed: true },
      ] });
    }
    if (path === "/api/v1/dmnrefs" && method === "GET") return route.fulfill({ json: state.refs });
    if (path === "/api/v1/dmnrefs" && method === "POST") {
      const body = request.postDataJSON();
      state.refsCreated.push(body);
      const rec = { id: "ref-solo", ...body };
      state.refs.push(rec);
      return route.fulfill({ json: rec });
    }
    if (path === "/api/v1/dmn-models" && method === "POST") {
      state.uploads.push({ query: url.search, body: request.postData() });
      if (modelTaken) return route.fulfill({ status: 409, json: { error: "the model handle solo-modell is taken" } });
      return route.fulfill({ json: { modelRef: "solo-modell" } });
    }
    if (path === "/api/v1/decision-deployments/43/xml") return route.fulfill({ body: SOLO_XML, contentType: "application/xml" });
    if (path === "/api/v1/dmnrefs/ref-solo/graph") return route.fulfill({ json: SOLO_GRAPH });
    if (path === "/api/v1/dmnrefs/ref-solo/logic") return route.fulfill({ json: SOLO_LOGIC });
    if (path === "/api/v1/decisions/Solo/binding") {
      return route.fulfill({ json: { decisionId: "Solo", perspective: "draft", binding: "latest", source: "decisionDeployment",
        key: 43, deployedVersion: 1, deployedAt: 1767225600, applicationId: "app-9", modelName: "Solo-Modell",
        graph: SOLO_GRAPH, logic: SOLO_LOGIC } });
    }
    if (path === "/api/v1/dmnrefs/ref-pricing/graph") return route.fulfill({ json: GRAPH });
    if (path === "/api/v1/dmnrefs/ref-pricing/logic") { state.logicReads++; return route.fulfill({ json: LOGIC_STORED }); }
    if (path === "/api/v1/decisions/Gone/binding") {
      return route.fulfill({ json: { decisionId: "Gone", perspective: "draft", binding: "latest", source: "none", reason: "noModel" } });
    }
    if (path === "/api/v1/decisions/Price/binding") {
      state.boundQueries.push(url.search);
      return route.fulfill({ json: bound(url.searchParams) });
    }
    if (path === "/api/v1/processes" && method === "GET") {
      return route.fulfill({ json: [{ key: 7, processId: "preis", name: "Preis", version: 1, projectId: "" }] });
    }
    if (path === "/api/v1/processes/7/xml") return route.fulfill({ body: XML, contentType: "application/xml" });
    if (path.endsWith("/validate")) return route.fulfill({ json: { version: "test", problems: [] } });
    return route.fulfill({ json: [] });
  });
  return state;
}

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.addInitScript(() => { delete window.EventSource; });
});

async function openDiagram(page, hash = "#/modeler/draft/preis") {
  await page.goto("/index.html" + hash);
  await expect(page.locator('[data-element-id="Task_price"]').first()).toBeVisible({ timeout: 20000 });
  await page.locator('[data-tab="implement"]').click();
}

const badge = (page, id) => page.locator(`.djs-overlays[data-container-id="${id}"] .djs-overlay-decision-badge button.decision-badge`);
const peek = (page) => page.locator(".drg-ov.drg-peek");
const node = (page, id) => peek(page).locator(`.drg-canvas .drg-node[data-node="${id}"]`);

test("the Implement view marks a task that calls a decision, and only there", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await expect(badge(page, "Task_price")).toBeVisible();
  await expect(badge(page, "Task_price")).toHaveAttribute("title", /Price/);
  await expect(badge(page, "Task_lost")).toBeVisible();
  // No decision named, no mark.
  await expect(badge(page, "Task_empty")).toHaveCount(0);
  // The Design view stays the drawing it is.
  await page.locator('[data-tab="design"]').click();
  await expect(page.locator("button.decision-badge")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the mark opens the decision over the diagram, and its graph can be walked", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await badge(page, "Task_price").click();

  await expect(peek(page)).toBeVisible();
  await expect(peek(page).locator("#drg-title")).toHaveText("Price");
  await expect(peek(page).locator(".drg-sub")).toContainText("Pricing");
  // What is shown is what the binding resolves to — the server's answer, for the
  // draft's latest binding: deployed version 2.
  expect(state.boundQueries).toEqual(["?binding=latest"]);
  await expect(peek(page).locator(".drg-peek-binding")).toContainText("Version 2");
  await expect(peek(page).locator(".drg-peek-binding")).toContainText(/Entwurf/);
  // Nothing was saved or left to look.
  expect(await page.evaluate(() => location.hash)).toBe("#/modeler/draft/preis");
  expect(state.draftSaves).toHaveLength(0);

  // The called decision is marked and shown first: a context, with its entries.
  await expect(node(page, "Price")).toHaveClass(/is-called/);
  await expect(node(page, "Price")).toHaveClass(/is-selected/);
  const detail = peek(page).locator(".drg-peek-detail");
  await expect(detail.locator("h3")).toContainText("Price");
  await expect(detail).toContainText("factor");
  await expect(detail).toContainText("Base * factor");

  // A requirement is a way into the graph: Band's decision table, rule by rule.
  await detail.locator('.drg-chip[data-node="Band"]').click();
  await expect(node(page, "Band")).toHaveClass(/is-selected/);
  await expect(node(page, "Price")).not.toHaveClass(/is-selected/);
  await expect(node(page, "Price")).toHaveClass(/is-called/);
  await expect(detail.locator("table.mgrid tr.mrule")).toHaveCount(2);
  await expect(detail.locator(".mtable-policy")).toHaveText("F");
  await expect(detail).toContainText('"young"');

  // So is the graph itself, by mouse and by keyboard.
  await node(page, "Base").click();
  await expect(detail.locator("pre.drg-code")).toHaveText("Age + 10");
  await node(page, "in_age").focus();
  await page.keyboard.press("Enter");
  await expect(detail.locator("h3")).toContainText("Age");
  await expect(detail).toContainText("number");
  await expect(detail.locator('.drg-chip[data-node="Band"]')).toBeVisible();
  // The binding's answer carried graph and logic. The stored model is read beside it,
  // once, because the editor button says how it stands against what is shown: here it
  // has moved on, so the hint warns that the editor opens something else.
  const hint = peek(page).locator("[data-peek-edit-hint]");
  await expect(hint).toHaveClass(/is-warn/);
  await expect(hint).toContainText("weicht");
  await expect(hint).toContainText("Version 2");
  await expect(peek(page).locator("[data-peek-edit]")).toHaveAttribute("title", /weicht/);
  expect(state.logicReads).toBe(1);

  // The stored model — what the editor opens — is one switch away, and says it is
  // not what runs. Here it has moved on: Base adds 20 where v2 adds 10.
  await peek(page).locator('[data-peek-view="stored"]').click();
  await expect(peek(page).locator(".drg-peek-binding.is-stored")).toBeVisible();
  await node(page, "Base").click();
  await expect(detail.locator("pre.drg-code")).toHaveText("Age + 20");
  expect(state.logicReads).toBe(1);
  await peek(page).locator('[data-peek-view="bound"]').click();
  await expect(peek(page).locator('[data-peek-view="bound"]')).toHaveAttribute("aria-selected", "true");
  await expect(peek(page).locator(".drg-peek-detail pre.drg-code")).toHaveText("Age + 10");
  expect(state.logicReads).toBe(1); // each view is read once

  // Escape closes it, and the diagram is where it was.
  await page.keyboard.press("Escape");
  await expect(peek(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a decision this server does not hold is said, not drawn", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await page.evaluate(() => window.__atlasModeler.get("canvas").scroll({ dx: -200, dy: 0 }));
  await badge(page, "Task_lost").click();
  await expect(peek(page)).toContainText("Gone");
  await expect(peek(page).locator(".drg-canvas")).toHaveCount(0);
  await expect(peek(page).locator("[data-peek-edit]")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a version that is not deployed is said, with the versions that are", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await badge(page, "Task_pinned").click();
  expect(state.boundQueries).toEqual(["?binding=version&version=7"]);
  await expect(peek(page).locator(".drg-peek-binding")).toContainText("Version 7");
  await expect(peek(page)).toContainText("v1, v2");
  await expect(peek(page).locator(".drg-canvas")).toHaveCount(0);
  await expect(peek(page).locator("[data-peek-edit-hint]")).toContainText("derzeit kein Modell");
  // The stored model is still there to look at.
  await peek(page).locator('[data-peek-view="stored"]').click();
  await expect(node(page, "Price")).toHaveClass(/is-called/);
  expect(page.__errors).toEqual([]);
});

test("a deployed version on screen is resolved as that definition resolves it", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page, "#/modeler/d/7");
  await badge(page, "Task_price").click();
  await expect(peek(page).locator(".drg-peek-binding")).toContainText("Version 1");
  expect(state.boundQueries).toEqual(["?processKey=7&elementId=Task_price"]);
  await expect(peek(page).locator(".drg-peek-binding")).toContainText(/deployte Prozessversion/);
  expect(page.__errors).toEqual([]);
});

test("a decision that exists only as a deployment can be stored, unchanged, and then opened", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await badge(page, "Task_solo").click();
  await expect(peek(page).locator(".drg-peek-binding")).toContainText("Version 1");
  // Nothing for the editor to open, so no editor door — the way to make one instead.
  await expect(peek(page).locator("[data-peek-edit]")).toHaveCount(0);
  await peek(page).locator("[data-peek-create]").click();

  // The deployed source, unchanged, under the deployment's model name, refused rather
  // than overwritten if the name is taken; the reference filed where it was published.
  await expect(peek(page).locator(".drg-peek-notice")).toContainText("Solo-Modell");
  expect(state.uploads).toHaveLength(1);
  expect(state.uploads[0].query).toBe("?name=Solo-Modell&from=");
  expect(state.uploads[0].body).toBe(SOLO_XML);
  expect(state.refsCreated).toEqual([{ name: "Solo-Modell", modelRef: "solo-modell", projectId: "app-9" }]);

  // Opened again, the window finds the new model: the editor door, and a hint that it
  // matches what runs.
  await expect(peek(page).locator("[data-peek-create]")).toHaveCount(0);
  await expect(peek(page).locator("[data-peek-edit-hint]")).toContainText("entsprechen");
  await peek(page).locator("[data-peek-edit]").click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/e/ref-solo/for/preis/Task_solo");
});

test("a stored model is not created over a name that is taken", async ({ page }) => {
  const state = installMock(page, { modelTaken: true });
  await openDiagram(page);
  await badge(page, "Task_solo").click();
  await peek(page).locator("[data-peek-create]").click();
  await expect(peek(page).locator('[role="alert"]')).toContainText("existiert bereits");
  expect(state.refsCreated).toHaveLength(0);
  await expect(peek(page).locator("[data-peek-create]")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("the peek's editor button is the panel's door: the decision editor, with the way back", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await badge(page, "Task_price").click();
  await peek(page).locator("[data-peek-edit]").click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/dmn/e/ref-pricing/for/preis/Task_price");
  await expect(peek(page)).toHaveCount(0);
  // An untouched diagram is not saved on the way out.
  expect(state.draftSaves).toHaveLength(0);
});
