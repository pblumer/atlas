// The replay's Data tab as a *list* (ADR-0286): its filter row, and the state trail
// that hangs under a row.
//
// Driven through the real app shell rather than a harness, because the thing under
// test is the seam between the two: `enhanceViewTables()` runs once per route, in
// app.js, and editor.js rebuilds this table afterwards. A harness that mounts
// mountInstanceReplay directly never runs the enhancer at all, so it cannot see
// either half of what these tests hold.
import { test, expect } from "@playwright/test";

const XML = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<bpmn:definitions xmlns:bpmn=\"http://www.omg.org/spec/BPMN/20100524/MODEL\" xmlns:bpmndi=\"http://www.omg.org/spec/BPMN/20100524/DI\" xmlns:dc=\"http://www.omg.org/spec/DD/20100524/DC\" xmlns:di=\"http://www.omg.org/spec/DD/20100524/DI\" id=\"Definitions_do\" targetNamespace=\"http://atlas/bpmn\">\n  <bpmn:process id=\"do\" isExecutable=\"true\">\n    <bpmn:startEvent id=\"Start_1\" name=\"Start\"><bpmn:outgoing>f_0</bpmn:outgoing></bpmn:startEvent>\n    <bpmn:task id=\"Erfassen_1\" name=\"Erfassen\"><bpmn:incoming>f_0</bpmn:incoming><bpmn:outgoing>f_1</bpmn:outgoing></bpmn:task>\n    <bpmn:task id=\"Freigeben_1\" name=\"Freigeben\"><bpmn:incoming>f_1</bpmn:incoming><bpmn:outgoing>f_2</bpmn:outgoing></bpmn:task>\n    <bpmn:endEvent id=\"End_1\" name=\"Ende\"><bpmn:incoming>f_2</bpmn:incoming></bpmn:endEvent>\n    <bpmn:sequenceFlow id=\"f_0\" sourceRef=\"Start_1\" targetRef=\"Erfassen_1\"/>\n    <bpmn:sequenceFlow id=\"f_1\" sourceRef=\"Erfassen_1\" targetRef=\"Freigeben_1\"/>\n    <bpmn:sequenceFlow id=\"f_2\" sourceRef=\"Freigeben_1\" targetRef=\"End_1\"/>\n  </bpmn:process>\n  <bpmndi:BPMNDiagram id=\"D\"><bpmndi:BPMNPlane id=\"P\" bpmnElement=\"do\">\n    <bpmndi:BPMNShape id=\"Start_1_di\" bpmnElement=\"Start_1\"><dc:Bounds x=\"160\" y=\"142\" width=\"36\" height=\"36\"/></bpmndi:BPMNShape>\n    <bpmndi:BPMNShape id=\"Erfassen_1_di\" bpmnElement=\"Erfassen_1\"><dc:Bounds x=\"250\" y=\"120\" width=\"100\" height=\"80\"/></bpmndi:BPMNShape>\n    <bpmndi:BPMNShape id=\"Freigeben_1_di\" bpmnElement=\"Freigeben_1\"><dc:Bounds x=\"410\" y=\"120\" width=\"100\" height=\"80\"/></bpmndi:BPMNShape>\n    <bpmndi:BPMNShape id=\"End_1_di\" bpmnElement=\"End_1\"><dc:Bounds x=\"570\" y=\"142\" width=\"36\" height=\"36\"/></bpmndi:BPMNShape>\n    <bpmndi:BPMNEdge id=\"f_0_di\" bpmnElement=\"f_0\"><di:waypoint x=\"196\" y=\"160\"/><di:waypoint x=\"250\" y=\"160\"/></bpmndi:BPMNEdge>\n    <bpmndi:BPMNEdge id=\"f_1_di\" bpmnElement=\"f_1\"><di:waypoint x=\"350\" y=\"160\"/><di:waypoint x=\"410\" y=\"160\"/></bpmndi:BPMNEdge>\n    <bpmndi:BPMNEdge id=\"f_2_di\" bpmnElement=\"f_2\"><di:waypoint x=\"510\" y=\"160\"/><di:waypoint x=\"570\" y=\"160\"/></bpmndi:BPMNEdge>\n  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>\n</bpmn:definitions>";
const T = 1_722_260_303_000;
const ns = (ms) => ms * 1e6;

const steps = [
  { elementId: "Start_1", elementInstanceKey: "1000", type: "bpmn:startEvent", position: 1, tokenId: "1", variables: [] },
  { elementId: "Erfassen_1", elementInstanceKey: "1001", type: "bpmn:task", position: 2, tokenId: "1", variables: [] },
  { elementId: "Freigeben_1", elementInstanceKey: "1002", type: "bpmn:task", position: 3, tokenId: "1", variables: [] },
].map((s, i) => ({ ...s, at: ns(T + i * 1000) }));
const frames = steps.map((s, i) => ({
  position: s.position, at: s.at,
  tokens: i < steps.length - 1
    ? [{ elementId: s.elementId, tokenId: "1", elementInstanceKey: s.elementInstanceKey, state: "active" }] : [],
}));
const TIMELINE = { processDefKey: "7", processId: "do", version: 1, state: "completed", steps, frames };

// Three objects, deliberately not in alphabetical order, so a sort has something to do.
const DATA_OBJECTS = [
  { name: "order", state: "freigegeben", kind: "json", value: { id: "ORD-1" }, itemType: "Order",
    isCollection: false, at: ns(T + 3000), producedBy: "Freigeben_1",
    history: [
      { at: ns(T + 500), state: "erfasst", kind: "null", value: null, producedBy: "" },
      { at: ns(T + 3000), state: "freigegeben", kind: "json", value: { id: "ORD-1" }, producedBy: "Freigeben_1" },
    ] },
  { name: "beleg", state: "neu", kind: "string", value: "B-1", itemType: "Beleg",
    isCollection: false, at: ns(T + 1000), producedBy: "Erfassen_1",
    history: [{ at: ns(T + 1000), state: "neu", kind: "string", value: "B-1", producedBy: "Erfassen_1" }] },
  { name: "positionen", state: "", kind: "null", value: null, itemType: "Position",
    isCollection: true, at: ns(T + 500), producedBy: "",
    history: [{ at: ns(T + 500), state: "", kind: "null", value: null, producedBy: "" }] },
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  // The catch-all goes first: Playwright gives the *last* matching route priority, so a
  // catch-all registered afterwards would swallow every specific mock below it.
  await page.route("**/api/v1/**", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (r) => r.fulfill({ json: { authEnabled: false, user: null } }));
  await page.route("**/api/v1/instances/*/timeline", (r) => r.fulfill({ json: TIMELINE }));
  await page.route("**/api/v1/instances/*/data-objects", (r) => r.fulfill({ json: DATA_OBJECTS }));
  await page.route("**/api/v1/instances/*/decisions", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/v1/processes/*/xml", (r) => r.fulfill({ body: XML, contentType: "application/xml" }));
  await page.setViewportSize({ width: 1400, height: 900 });

  await page.goto("/index.html#/operations/i/281474976723211");
  // The tab buttons are in the shell's markup from the first paint, but nothing is
  // listening on them until mountInstanceReplay has imported bpmn-js, drawn the
  // diagram and wired its panels. Clicking one before that is a click into the void —
  // the panel stays hidden and the failure reads as a missing row rather than as a
  // race. The history list is written at the end of that mount, so it is the signal
  // that the view is ready to be driven.
  await expect(page.locator("#rp-history .ops-hrow").first()).toBeVisible({ timeout: 20000 });
  await page.locator("#rp-tabs button[data-tab='data']").click();
  await expect(page.locator("#tab-data .do-row").first()).toBeVisible();
});

const table = (page) => page.locator("#tab-data .do-table");

test("the list keeps its filter row when the inspector re-renders", async ({ page }) => {
  // The route's one enhancement pass gave it one...
  await expect(table(page).locator(".dt-filter-row")).toHaveCount(1);

  // ...and selecting an element rebuilds the whole table under it. Without a call of
  // its own the list silently loses both its sorting and its search (ADR-0286 rule 4),
  // which is the defect this covers: the feature is not missing, it is *lost*, and only
  // after the operator has clicked something.
  await page.locator("#rp-history .ops-hrow").first().click();
  await expect(page.locator("#tab-data .do-row").first()).toBeVisible();
  await expect(table(page).locator(".dt-filter-row")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("an open state trail travels with its row when the list is sorted", async ({ page }) => {
  await page.locator('#tab-data .do-row[data-do="order"] .do-toggle').click();
  await expect(page.locator("#tab-data .do-trail")).toBeVisible();

  const rowOrder = () => page.evaluate(() =>
    [...document.querySelectorAll("#tab-data .do-table tbody tr")]
      .map((r) => (r.classList.contains("do-trail") ? "trail" : r.dataset.do || "?")));

  // Both directions, and the second is the one that does the work. A trail sorted as a
  // row of its own goes by the text in its single cell ("State trail · every durable
  // write to …"), which happens to land it last ascending — right where its own row
  // also happens to be. Descending puts it first, nowhere near the row it belongs to,
  // and that is the sort that tells a carried detail from a coincidence.
  const head = page.locator("#tab-data .do-table thead th.dt-sortable").first();
  for (const dir of ["ascending", "descending"]) {
    await head.click();
    const rows = await rowOrder();
    const i = rows.indexOf("trail");
    expect(i, `${dir}: the trail is still in the list`).toBeGreaterThan(0);
    expect(rows[i - 1], `${dir}: the trail sits under its own row`).toBe("order");
  }
  expect(page.__errors).toEqual([]);
});

test("a filter leaves an open trail alone when its row survives, and hides it when it does not", async ({ page }) => {
  await page.locator('#tab-data .do-row[data-do="order"] .do-toggle').click();
  await expect(page.locator("#tab-data .do-trail")).toBeVisible();

  const classFilter = page.locator("#tab-data .do-table thead .dt-filter-row .dt-filter").nth(1);

  // A filter its row matches. The trail's own single cell holds nothing in the Class
  // column, so a trail read as a row of the table's data is hidden here — the reader
  // filters, their row stays, and the trail they had open silently disappears under it.
  await classFilter.fill("Order");
  await expect(page.locator('#tab-data .do-row[data-do="order"]')).toBeVisible();
  await expect(page.locator("#tab-data .do-trail")).toBeVisible();

  // And a filter its row does not match takes the trail with it, rather than leaving it
  // stranded under somebody else's object.
  await classFilter.fill("Beleg");
  await expect(page.locator('#tab-data .do-row[data-do="beleg"]')).toBeVisible();
  await expect(page.locator('#tab-data .do-row[data-do="order"]')).toBeHidden();
  await expect(page.locator("#tab-data .do-trail")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
