// End-to-end coverage for where Operations opens the comparison of two process
// versions (ADR-draft-compare-process-versions): the ⋯ menu of a process row in the
// Instances list, and the live view of one deployed version.
//
// version-comparison.spec.mjs proves the dialog through a harness, which cannot load
// app.js; these tests boot the real console against a mocked server and click what an
// operator clicks — the gap migration-wiring.spec.mjs exists for, where a row menu
// once closed and did nothing.
import { test, expect } from "@playwright/test";

const listing = (items) => ({ items, total: items.length, totalExact: true, truncated: false });

// Two versions of one process; a second process with a single version, which has
// nothing to compare with.
const PROCESSES = [
  { key: 11, processId: "kredit", name: "Kreditantrag", version: 1 },
  { key: 12, processId: "kredit", name: "Kreditantrag", version: 2 },
  { key: 21, processId: "sauber", name: "Sauber", version: 1 },
];
const SUMMARY = [
  { processDefKey: 11, processId: "kredit", version: 1, active: 1, completed: 0, latestCompletedAt: 0 },
  { processDefKey: 12, processId: "kredit", version: 2, active: 0, completed: 0, latestCompletedAt: 0 },
  { processDefKey: 21, processId: "sauber", version: 1, active: 0, completed: 0, latestCompletedAt: 0 },
];
const COMPARISON = {
  processId: "kredit", sameModel: false, difference: "x",
  from: { key: 11, version: 1 }, to: { key: 12, version: 2 },
  counts: { added: 0, removed: 0, changed: 1 },
  changes: [{ kind: "changed", id: "Task_check", type: "userTask", name: "Prüfen", parent: "kredit",
    details: [{ field: "name", kind: "changed", before: "Prüfen", after: "Genau prüfen" }] }],
};

const XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_vc" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="kredit" name="Kreditantrag" isExecutable="true">
    <bpmn:startEvent id="Start_1"><bpmn:outgoing>f0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="End_1"><bpmn:incoming>f0</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="Start_1" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="kredit">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"><dc:Bounds x="320" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f0_di" bpmnElement="f0"><di:waypoint x="196" y="138"/><di:waypoint x="320" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

const boot = async (page, hash) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  const reads = [];
  page.__reads = reads;
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (req.method() !== "GET") return route.fulfill({ json: {} });
    reads.push(path + url.search);
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (/\/processes\/\d+\/comparison$/.test(path)) return route.fulfill({ json: COMPARISON });
    const versionsOf = path.match(/\/processes\/(\d+)\/versions$/);
    if (versionsOf) {
      const pid = PROCESSES.find((p) => String(p.key) === versionsOf[1]).processId;
      const versions = PROCESSES.filter((p) => p.processId === pid)
        .map((p) => ({ key: p.key, version: p.version, active: true, running: 0, finished: 0, incidents: 0 }))
        .sort((a, b) => b.version - a.version);
      return route.fulfill({ json: { processId: pid, incidentCountsExact: true, versions } });
    }
    if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
    if (path === "/api/v1/instances/summary") return route.fulfill({ json: SUMMARY });
    if (path === "/api/v1/incidents/summary") return route.fulfill({ json: { total: 0, groups: [] } });
    if (/\/processes\/\d+\/xml$/.test(path)) return route.fulfill({ body: XML, contentType: "application/xml" });
    if (/\/processes\/\d+\/runtime$/.test(path)) {
      return route.fulfill({ json: { instances: 0, tokens: 0, finished: 0, elements: [], incidents: [] } });
    }
    if (path === "/api/v1/instances" || path === "/api/v1/tasks") return route.fulfill({ json: listing([]) });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html?lang=de");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null, { timeout: 15000 });
  await page.evaluate((h) => { location.hash = h; }, hash);
};

const row = (page, name) => page.locator("#rows tr", { has: page.locator("td", { hasText: name }) }).first();

test.describe("instances overview", () => {
  test("the row menu opens the comparison of the newest version with the one before", async ({ page }) => {
    await boot(page, "#/operations");
    const r = row(page, "Kreditantrag");
    await expect(r).toBeVisible();
    await r.locator(".dropdown-toggle").click();
    await r.locator(".dropdown-menu button", { hasText: "Versionen vergleichen" }).click();

    const modal = page.locator(".vc-modal");
    await expect(modal.locator("h2")).toHaveText("Versionen vergleichen: Kreditantrag");
    await expect(modal.locator("#vc-from")).toHaveValue("11");
    await expect(modal.locator("#vc-to")).toHaveValue("12");
    await expect(modal.locator(".vc-item.vc-changed")).toContainText("Genau prüfen");
    expect(page.__reads).toContain("/api/v1/processes/12/comparison?with=11");
    expect(page.__errors).toEqual([]);
  });

  test("a process with one version offers no comparison", async ({ page }) => {
    await boot(page, "#/operations");
    const r = row(page, "Sauber");
    await expect(r).toBeVisible();
    await r.locator(".dropdown-toggle").click();
    await expect(r.locator(".dropdown-menu button", { hasText: "Versionen vergleichen" })).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("live view of one version", () => {
  test("compares the version on screen with the one before it", async ({ page }) => {
    await boot(page, "#/operations/p/12");
    const btn = page.locator("#compare-versions");
    await expect(btn).toBeVisible();
    await btn.click();
    const modal = page.locator(".vc-modal");
    await expect(modal.locator("#vc-from")).toHaveValue("11");
    await expect(modal.locator("#vc-to")).toHaveValue("12");
    await expect(modal.locator(".vc-item")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });

  test("a process with a single version offers no comparison", async ({ page }) => {
    await boot(page, "#/operations/p/21");
    await expect(page.locator("#migrate-version")).toBeHidden();
    await expect(page.locator("#compare-versions")).toBeHidden();
    expect(page.__errors).toEqual([]);
  });
});
