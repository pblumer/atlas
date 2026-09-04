// End-to-end coverage for the call-activity drill-down in the Tasks app's Process tab
// (api/web/editor.js mountTaskProcess + app.js, ADR-0245). Everywhere else the "+" on a
// call activity navigates; here it must not — the diagram sits beside a form somebody is
// filling in, and Operations, where the full replay lives, is a role the assignee may
// not hold. So the panel descends into the child instance *in place*, the variables
// beneath it follow, and a back control returns to the caller. Drives the REAL app shell
// against a mocked /api/v1.
import { test, expect } from "@playwright/test";

// The Process tab is a 360px canvas inside the detail pane; give it a window that fits
// the pane and the diagram without scrolling.
test.use({ viewport: { width: 1280, height: 900 } });

const CALLER_INSTANCE = 9001;
const CHILD_INSTANCE = 9002;
const CALLER_DEF = 1;
const CHILD_DEF = 2;

const TASKS = [{
  key: 101, processInstanceKey: CALLER_INSTANCE, elementInstanceKey: 9101,
  processDefKey: CALLER_DEF, processId: "antrag", elementId: "review",
  name: "Antrag prüfen", priority: 50,
}];

const CALLER_XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_caller" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="antrag" name="Antragsbearbeitung" isExecutable="true">
    <bpmn:startEvent id="start" name="Start"><bpmn:outgoing>f0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:callActivity id="kyc" name="KYC-Prüfung">
      <bpmn:extensionElements><zeebe:calledElement processId="kyc-check"/></bpmn:extensionElements>
      <bpmn:incoming>f0</bpmn:incoming><bpmn:outgoing>f1</bpmn:outgoing>
    </bpmn:callActivity>
    <bpmn:userTask id="review" name="Antrag prüfen"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>
    <bpmn:callActivity id="archiv" name="Archivieren">
      <bpmn:extensionElements><zeebe:calledElement processId="archiv"/></bpmn:extensionElements>
      <bpmn:incoming>f2</bpmn:incoming><bpmn:outgoing>f3</bpmn:outgoing>
    </bpmn:callActivity>
    <bpmn:endEvent id="end" name="Ende"><bpmn:incoming>f3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="kyc"/>
    <bpmn:sequenceFlow id="f1" sourceRef="kyc" targetRef="review"/>
    <bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="archiv"/>
    <bpmn:sequenceFlow id="f3" sourceRef="archiv" targetRef="end"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="antrag">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="kyc_di" bpmnElement="kyc"><dc:Bounds x="250" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="review_di" bpmnElement="review"><dc:Bounds x="400" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="archiv_di" bpmnElement="archiv"><dc:Bounds x="550" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="end_di" bpmnElement="end"><dc:Bounds x="700" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f0_di" bpmnElement="f0"><di:waypoint x="196" y="138"/><di:waypoint x="250" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="350" y="138"/><di:waypoint x="400" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="500" y="138"/><di:waypoint x="550" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f3_di" bpmnElement="f3"><di:waypoint x="650" y="138"/><di:waypoint x="700" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

const CHILD_XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_child" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="kyc-check" name="KYC-Prüfung" isExecutable="true">
    <bpmn:startEvent id="cstart" name="Start"><bpmn:outgoing>cf0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="pruefen" name="Identität prüfen"><bpmn:incoming>cf0</bpmn:incoming><bpmn:outgoing>cf1</bpmn:outgoing></bpmn:serviceTask>
    <bpmn:endEvent id="cend" name="Ende"><bpmn:incoming>cf1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="cf0" sourceRef="cstart" targetRef="pruefen"/>
    <bpmn:sequenceFlow id="cf1" sourceRef="pruefen" targetRef="cend"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="CD"><bpmndi:BPMNPlane id="CP" bpmnElement="kyc-check">
    <bpmndi:BPMNShape id="cstart_di" bpmnElement="cstart"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="pruefen_di" bpmnElement="pruefen"><dc:Bounds x="250" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="cend_di" bpmnElement="cend"><dc:Bounds x="400" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="cf0_di" bpmnElement="cf0"><di:waypoint x="196" y="138"/><di:waypoint x="250" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="cf1_di" bpmnElement="cf1"><di:waypoint x="350" y="138"/><di:waypoint x="400" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

const ns = (ms) => ms * 1e6;
const T = 1_722_260_303_000;

// The caller has walked start → kyc (which started the child) → review, where the
// task's token sits now. "archiv" has not been reached, so it has no child at all.
const TIMELINES = {
  [CALLER_INSTANCE]: {
    instanceKey: CALLER_INSTANCE, processDefKey: CALLER_DEF, processId: "antrag", version: 1, state: "active",
    steps: [
      { elementId: "start", type: "bpmn:StartEvent", elementInstanceKey: "1", at: ns(T), position: 1 },
      { elementId: "kyc", type: "bpmn:CallActivity", elementInstanceKey: "2", at: ns(T + 10), position: 2, childInstanceKey: String(CHILD_INSTANCE) },
      { elementId: "review", type: "bpmn:UserTask", elementInstanceKey: "3", at: ns(T + 20), position: 3 },
    ],
    frames: [{ position: 3, at: ns(T + 20), tokens: [{ elementId: "review", tokenId: "1", state: "active" }] }],
  },
  [CHILD_INSTANCE]: {
    instanceKey: CHILD_INSTANCE, processDefKey: CHILD_DEF, processId: "kyc-check", version: 1, state: "completed",
    steps: [
      { elementId: "cstart", type: "bpmn:StartEvent", elementInstanceKey: "11", at: ns(T + 12), position: 1 },
      { elementId: "pruefen", type: "bpmn:ServiceTask", elementInstanceKey: "12", at: ns(T + 14), position: 2 },
      { elementId: "cend", type: "bpmn:EndEvent", elementInstanceKey: "13", at: ns(T + 16), position: 3 },
    ],
    frames: [{ position: 3, at: ns(T + 16), tokens: [] }],
  },
};

const VARIABLES = {
  [CALLER_INSTANCE]: { antragId: "A-4711", betrag: 2500 },
  [CHILD_INSTANCE]: { kycScore: 87, geprueftVon: "clearing" },
};

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: TASKS });
    let m = path.match(/\/instances\/(\d+)\/timeline$/);
    if (m) return route.fulfill({ json: TIMELINES[m[1]] || {} });
    m = path.match(/\/instances\/(\d+)\/variables$/);
    if (m) return route.fulfill({ json: VARIABLES[m[1]] || {} });
    m = path.match(/\/processes\/(\d+)\/xml$/);
    if (m) return route.fulfill({ body: Number(m[1]) === CHILD_DEF ? CHILD_XML : CALLER_XML, contentType: "application/xml" });
    return route.fulfill({ json: [] });
  });
}

// openProcessTab boots the inbox, selects the task and switches to its Process tab,
// then waits for the diagram to render.
async function openProcessTab(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await page.locator('.tasks-item[data-key="101"]').click();
  await page.locator('#task-dtabs button[data-dtab="process"]').click();
  await expect(page.locator('#tp-canvas [data-element-id="kyc"]')).toBeVisible({ timeout: 15000 });
}

// markerCenter is the screen position of the "+" bpmn-js drew on a shape.
async function markerCenter(page, elementId) {
  const box = await page
    .locator(`#tp-canvas [data-element-id="${elementId}"] path[data-marker="sub-process"]`)
    .boundingBox();
  if (!box) throw new Error(`no sub-process marker on ${elementId}`);
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
});

test("the + descends into the child instance without leaving the task", async ({ page }) => {
  await openProcessTab(page);
  await expect(page.locator("#tp-vars-body")).toContainText("antragId");

  const at = await markerCenter(page, "kyc");
  await page.mouse.dblclick(at.x, at.y);

  // The child's diagram is now in the panel — and the task is still on screen.
  await expect(page.locator('#tp-canvas [data-element-id="pruefen"]')).toBeVisible();
  await expect(page.locator('#tp-canvas [data-element-id="review"]')).toHaveCount(0);
  await expect(page.locator(".tasks-detail-head h1")).toContainText("Antrag prüfen");
  expect(await page.evaluate(() => location.hash)).toBe("#/tasks");

  // Where you are, and the way back.
  await expect(page.locator(".tp-drill-here")).toContainText("KYC-Prüfung");
  await expect(page.locator(".tp-drill-back")).toContainText("Antragsbearbeitung");

  // The variables below the diagram followed the descent.
  await expect(page.locator("#tp-vars-body")).toContainText("kycScore");
  await expect(page.locator("#tp-vars-body")).not.toContainText("antragId");
  expect(page.__errors).toEqual([]);
});

test("back returns to the caller, with the task's own element marked again", async ({ page }) => {
  await openProcessTab(page);
  const at = await markerCenter(page, "kyc");
  await page.mouse.dblclick(at.x, at.y);
  await expect(page.locator(".tp-drill-back")).toBeVisible();

  await page.locator(".tp-drill-back").click();

  await expect(page.locator('#tp-canvas [data-element-id="review"]')).toBeVisible();
  await expect(page.locator(".tp-drill-back")).toHaveCount(0); // top level: no chrome
  // "You are here" is the task's own element, restored with the level.
  await expect(page.locator('#tp-canvas [data-element-id="review"]')).toHaveClass(/atlas-selected/);
  await expect(page.locator("#tp-vars-body")).toContainText("antragId");
  expect(page.__errors).toEqual([]);
});

test("a call activity that has not run says so instead of doing nothing", async ({ page }) => {
  await openProcessTab(page);
  // "archiv" comes after the task's own element, so no token has reached it and it
  // has no child instance to descend into.
  const at = await markerCenter(page, "archiv");
  await page.mouse.dblclick(at.x, at.y);

  await expect(page.locator(".tp-drill-note")).toContainText("not started a child instance");
  await expect(page.locator('#tp-canvas [data-element-id="review"]')).toBeVisible(); // still the caller
  expect(page.__errors).toEqual([]);
});

test("double-clicking the shape away from the + does not descend", async ({ page }) => {
  await openProcessTab(page);
  const shape = await page.locator('#tp-canvas [data-element-id="kyc"] .djs-hit').first().boundingBox();
  await page.mouse.dblclick(shape.x + shape.width * 0.25, shape.y + shape.height * 0.25);

  await page.waitForTimeout(300);
  await expect(page.locator('#tp-canvas [data-element-id="review"]')).toBeVisible();
  await expect(page.locator(".tp-drill-here")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
