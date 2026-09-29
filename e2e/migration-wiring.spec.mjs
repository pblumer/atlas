// End-to-end coverage for where the Operations surfaces *open* the batch migration
// (ADR-0162): the ⋯ menu of a process row in the Instances list, and the live view of
// one deployed version.
//
// migration-dialog.spec.mjs proves the dialogs themselves through a harness, and says
// so: it cannot load app.js, so it proves the flows and not app.js's wiring of them.
// That gap is where the row menu went dead — its handlers were bound while the table
// still said "Loading…", so the rows drawn afterwards carried menu items that nothing
// listened to, and "Migrate running instances…" closed the menu and did nothing else.
// These tests boot the real console against a mocked server and click what an operator
// clicks.
import { test, expect } from "@playwright/test";

// listing is how every capped list endpoint answers since ADR-0378.
const listing = (items) => ({ items, total: items.length, totalExact: true, truncated: false });

// Two versions of one process, with instances on both; a second process with a single
// version, which has nowhere to migrate to.
const PROCESSES = [
  { key: 11, processId: "kredit", name: "Kreditantrag", version: 1 },
  { key: 12, processId: "kredit", name: "Kreditantrag", version: 2 },
  { key: 21, processId: "sauber", name: "Sauber", version: 1 },
];
const SUMMARY = [
  { processDefKey: 11, processId: "kredit", version: 1, active: 3, completed: 4, latestCompletedAt: 0 },
  { processDefKey: 12, processId: "kredit", version: 2, active: 1, completed: 0, latestCompletedAt: 0 },
  { processDefKey: 21, processId: "sauber", version: 1, active: 2, completed: 0, latestCompletedAt: 0 },
];

const XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_mig" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="kredit" name="Kreditantrag" isExecutable="true">
    <bpmn:startEvent id="Start_1"><bpmn:outgoing>f0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Task_check" name="Prüfen"><bpmn:incoming>f0</bpmn:incoming><bpmn:outgoing>f1</bpmn:outgoing></bpmn:userTask>
    <bpmn:endEvent id="End_1"><bpmn:incoming>f1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="Start_1" targetRef="Task_check"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Task_check" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="kredit">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_check_di" bpmnElement="Task_check"><dc:Bounds x="260" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"><dc:Bounds x="440" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f0_di" bpmnElement="f0"><di:waypoint x="196" y="138"/><di:waypoint x="260" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="360" y="138"/><di:waypoint x="440" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

// boot starts the real console against the mock. Every write the page makes is kept
// here in the test process, so an assertion never has to reach back into the page.
const boot = async (page, hash, { summaryFails = false } = {}) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  const writes = [];
  page.__writes = writes;
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "POST") {
      const body = JSON.parse(req.postData() || "{}");
      writes.push({ path, body });
      if (/\/processes\/\d+\/migrate-instances$/.test(path)) {
        return route.fulfill({ json: { toProcessDefKey: body.targetProcessDefKey, migrated: 3, remaining: false } });
      }
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
    if (path === "/api/v1/instances/summary") {
      return summaryFails
        ? route.fulfill({ status: 500, json: { error: "summary unavailable" } })
        : route.fulfill({ json: SUMMARY });
    }
    if (path === "/api/v1/incidents/summary") return route.fulfill({ json: { total: 0, groups: [] } });
    if (/\/processes\/\d+\/xml$/.test(path)) {
      return route.fulfill({ body: XML, contentType: "application/xml" });
    }
    if (/\/processes\/\d+\/runtime$/.test(path)) {
      return route.fulfill({ json: { instances: 3, tokens: 3, finished: 0, elements: [], incidents: [] } });
    }
    if (path === "/api/v1/instances" || path === "/api/v1/tasks") return route.fulfill({ json: listing([]) });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null, { timeout: 15000 });
  await page.evaluate((h) => { location.hash = h; }, hash);
};

// row returns the process row whose first cell names this process.
const row = (page, name) => page.locator("#rows tr", { has: page.locator("td", { hasText: name }) }).first();

// clickRowMenu opens a row's ⋯ menu and clicks one of its items.
const clickRowMenu = async (page, name, item) => {
  const r = row(page, name);
  await r.locator(".dropdown-toggle").click();
  await r.locator(".dropdown-menu button", { hasText: item }).click();
};

test.describe("instances overview", () => {
  test("the row menu opens the migration dialog", async ({ page }) => {
    await boot(page, "#/operations");
    await expect(row(page, "Kreditantrag")).toBeVisible();

    await clickRowMenu(page, "Kreditantrag", "Migrate running instances");

    const modal = page.locator(".mig-modal");
    await expect(modal).toBeVisible();
    await expect(modal.locator("#migb-title")).toContainText("Kreditantrag");
    // The source is the oldest version still holding instances, the target the newest.
    await expect(modal.locator("#migb-from")).toHaveValue("11");
    await expect(modal.locator("#migb-to")).toHaveValue("12");
    await expect(modal.locator("#migb-from")).toContainText("3 running");
    expect(page.__errors).toEqual([]);
  });

  test("the row menu still works after the list is redrawn", async ({ page }) => {
    await boot(page, "#/operations");
    await expect(row(page, "Kreditantrag")).toBeVisible();
    // Refresh replaces every row, and with them every menu item a handler was bound to.
    await page.locator("#refresh").click();
    await expect(row(page, "Kreditantrag")).toBeVisible();

    await clickRowMenu(page, "Kreditantrag", "Migrate running instances");
    await expect(page.locator(".mig-modal")).toBeVisible();
    await page.locator("[data-migb-cancel]").click();
    await expect(page.locator(".mig-modal")).toHaveCount(0);

    // The destructive neighbour rides the same dispatch, so it was dead too.
    await clickRowMenu(page, "Kreditantrag", "Terminate all running");
    await expect(page.locator(".confirm-modal h2")).toHaveText("Terminate 4 running instances?");
    expect(page.__errors).toEqual([]);
  });
});

test.describe("live view of one version", () => {
  test("migrates every running instance of the version on screen", async ({ page }) => {
    await boot(page, "#/operations/p/11");
    const btn = page.locator("#migrate-version");
    await expect(btn).toBeVisible();

    await btn.click();
    const modal = page.locator(".mig-modal");
    await expect(modal).toBeVisible();
    // The version being looked at is the source — that is what this button is for —
    // and the picker still says which versions hold instances.
    await expect(modal.locator("#migb-from")).toHaveValue("11");
    await expect(modal.locator("#migb-to")).toHaveValue("12");
    await expect(modal.locator("#migb-from")).toContainText("3 running");

    await modal.locator("#migb-reason").fill("v1 rechnete falsch");
    await modal.locator("[data-migb-go]").click();
    await expect(modal).toHaveCount(0);

    const migrations = page.__writes.filter((w) => w.path.endsWith("/migrate-instances"));
    expect(migrations).toEqual([{
      path: "/api/v1/processes/11/migrate-instances",
      body: { targetProcessDefKey: 12, reason: "v1 rechnete falsch" },
    }]);
    expect(page.__errors).toEqual([]);
  });

  test("a newer version on screen is the source too, and the target has to be chosen", async ({ page }) => {
    await boot(page, "#/operations/p/12");
    await page.locator("#migrate-version").click();
    const modal = page.locator(".mig-modal");
    await expect(modal.locator("#migb-from")).toHaveValue("12");
    // Moving instances back to an older version is legitimate, but never a default:
    // the dialog asks for the choice rather than making it.
    await modal.locator("#migb-reason").fill("zurück auf v1");
    await expect(modal.locator("[data-migb-go]")).toBeDisabled();
    await modal.locator("#migb-to").selectOption("11");
    await expect(modal.locator("[data-migb-go]")).toBeEnabled();
    expect(page.__errors).toEqual([]);
  });

  test("counts that could not be read are not reported as none", async ({ page }) => {
    await boot(page, "#/operations/p/11", { summaryFails: true });
    await page.locator("#migrate-version").click();
    const modal = page.locator(".mig-modal");
    await expect(modal.locator("#migb-from")).toHaveValue("11");
    // "none running" on every version would be a claim, and a false one.
    expect(await modal.locator("#migb-from option").allTextContents()).toEqual(["v2", "v1"]);
    expect(page.__errors).toEqual([]);
  });

  test("a process with a single version offers no migration", async ({ page }) => {
    await boot(page, "#/operations/p/21");
    // The view is fully wired once the diagram is on screen; only then is the button's
    // absence an answer rather than a race.
    await expect(page.locator('#canvas g[data-element-id="Task_check"]')).toBeVisible();
    await expect(page.locator("#migrate-version")).toHaveCount(1);
    await expect(page.locator("#migrate-version")).toBeHidden();
    expect(page.__errors).toEqual([]);
  });
});
