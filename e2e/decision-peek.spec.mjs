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

function installMock(page) {
  const state = { stored: XML, draftSaves: [], logicReads: 0 };
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
      return route.fulfill({ json: [
        { id: "Price", name: "Price", model: "Pricing", modelRef: "pricing", inputs: [{ name: "Age" }], output: { name: "Price" } },
        { id: "Band", name: "Band", model: "Pricing", modelRef: "pricing", inputs: [{ name: "Age" }], output: { name: "Band" } },
      ] });
    }
    if (path === "/api/v1/dmnrefs" && method === "GET") return route.fulfill({ json: [{ id: "ref-pricing", name: "Pricing", modelRef: "pricing", projectId: "" }] });
    if (path === "/api/v1/dmnrefs/ref-pricing/graph") return route.fulfill({ json: GRAPH });
    if (path === "/api/v1/dmnrefs/ref-pricing/logic") { state.logicReads++; return route.fulfill({ json: LOGIC }); }
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

async function openDiagram(page) {
  await page.goto("/index.html#/modeler/draft/preis");
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
  expect(state.logicReads).toBe(1); // one read serves every node

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
