// End-to-end coverage for the handbook's examples chapter (api/web/handbuch.html,
// "Beispiele"). Everything the chapter does beyond prose is browser code with no Go
// test behind it: it fetches the generated catalogue (api/web/examples-catalog.json),
// renders each example's real diagram from it, builds the buttons a card has earned,
// and installs a whole application — application, decisions, forms, drafts, publish —
// over the REST API. The static e2e harness runs no Go server, so these drive the REAL
// page against a mocked /api/v1: what is verified here is the wiring and the call
// sequence, not the server side.
//
// The catalogue itself is guarded on the Go side: `go test ./examples` regenerates it
// from examples/ and fails when the served copy has drifted, when an example has no
// card, or when a card names an example that does not exist.
import { test, expect } from "@playwright/test";

const APP_ID = "app-1";

// installMock answers the endpoints an install walks, in the shapes the page reads,
// and records every call so a test can assert the order. Anything else the page asks
// for — the recipes' layout endpoint above all — gets a benign failure, because a card
// whose model ships its own BPMN-DI must not depend on it.
function installMock(page, calls, over = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname + url.search;
    calls.push(`${req.method()} ${path}`);

    if (path === "/api/v1/applications" && req.method() === "GET") {
      return route.fulfill({ json: over.existingApps || [] });
    }
    if (path === "/api/v1/applications" && req.method() === "POST") {
      return route.fulfill({ json: { id: APP_ID, name: "installed" } });
    }
    if (path === "/api/v1/dmnrefs" && req.method() === "GET") {
      return route.fulfill({ json: over.existingRefs || [] });
    }
    if (path.startsWith("/api/v1/dmn-models")) {
      // The server folds ?handle= to lower case and reports the stored handle back.
      // Answering with the folded value is what the page is expected to carry into the
      // reference it creates.
      const sent = url.searchParams.get("handle") || "";
      return route.fulfill({ json: { modelRef: sent.toLowerCase(), decisions: [sent] } });
    }
    if (path === "/api/v1/dmnrefs" && req.method() === "POST") {
      return route.fulfill({ json: { id: "ref-1" } });
    }
    if (path === "/api/v1/forms") {
      return route.fulfill({ json: { id: "form", savedAt: 1 } });
    }
    if (path.startsWith("/api/v1/drafts")) {
      return route.fulfill({ json: { processId: "draft", savedAt: 1 } });
    }
    if (path.endsWith("/publish")) {
      return route.fulfill(over.publish || {
        json: {
          deployed: true,
          definitions: [
            { key: 41, processId: "proc_check_order", version: 1 },
            { key: 42, processId: "proc_pruefung", version: 1 },
          ],
          release: { version: 1 },
        },
      });
    }
    if (/\/processes\/\d+\/instances$/.test(url.pathname)) {
      return route.fulfill({ json: { definitionKey: 41 } });
    }
    return route.fulfill({ status: 500, json: { error: "not mocked" } });
  });
}

// openChapter loads the chapter and waits until the catalogue has been applied — the
// buttons appear only once it has, so they are the honest signal.
async function openChapter(page) {
  await page.goto("/handbuch.html#beispiele");
  await page.locator("#bsp-pruefe-auftrag .rz-actions button").first().waitFor({ timeout: 20000 });
}

const finish = (page, id) =>
  expect(page.locator(`#${id} .rz-status`)).toHaveClass(/ok|err/, { timeout: 20000 });

test("a card renders its own model, and does not ask the server for a layout to do it", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openChapter(page);

  // The mock denies /api/v1/layout. These still draw, which is the point: the models
  // ship hand-authored BPMN-DI, so a card renders straight from the catalogue and a
  // signed-out reader sees the same diagram the Modeler shows.
  await expect(page.locator("#bsp-pruefung .rz-diagram svg")).toBeVisible({ timeout: 20000 });
  await expect(page.locator("#bsp-reisebuchung .rz-diagram svg")).toBeVisible({ timeout: 20000 });
});

test("the chapter leads each card with the model the card is about", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openChapter(page);

  // Alphabetical order would put the connection test ahead of the model it only tests,
  // and the one-step variant ahead of the trip booking. The catalogue states a lead
  // model per example precisely so the picture matches the prose.
  const leads = await page.evaluate(async () => {
    const cat = await (await fetch("examples-catalog.json")).json();
    const lead = (id) => cat.examples.find((e) => e.id === id).processes[0].file;
    return {
      jira: lead("jira-zugangsantrag"),
      reise: lead("reisebuchung"),
      entra: lead("entra-team-onboarding"),
      otc: lead("order-to-cash"),
    };
  });
  expect(leads).toEqual({
    jira: "jira-zugangsantrag/jira-zugangsantrag.bpmn",
    reise: "reisebuchung/reisebuchung.bpmn",
    entra: "entra-team-onboarding.bpmn",
    otc: "order-to-cash.bpmn",
  });
});

test("installing an example walks the application in the order a process developer would", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openChapter(page);

  await page.locator("#bsp-pruefung .bsp-install").click();
  await finish(page, "bsp-pruefung");
  await expect(page.locator("#bsp-pruefung .rz-status")).toHaveClass(/ok/);
  await expect(page.locator("#bsp-pruefung .rz-status")).toContainText("Release 1");

  // The bracket first, its content second, publish once everything is in. The decision
  // handle is folded — an unfolded one is stored lower-cased and the reference then
  // resolves against nothing, which the server refuses at publish with "no temis model
  // matches this reference".
  expect(calls.filter((c) => !c.includes("/layout"))).toEqual([
    "GET /api/v1/applications",
    "POST /api/v1/applications",
    "GET /api/v1/dmnrefs",
    "POST /api/v1/dmn-models?handle=notenschluessel",
    "POST /api/v1/dmnrefs",
    "POST /api/v1/forms",
    `POST /api/v1/drafts?projectId=${APP_ID}`,
    `POST /api/v1/applications/${APP_ID}/publish`,
  ]);
});

test("the reference names the handle the upload reports back, not the decision's name", async ({ page }) => {
  const calls = [];
  let ref;
  installMock(page, calls);
  page.on("request", (r) => {
    if (r.url().endsWith("/api/v1/dmnrefs") && r.method() === "POST") ref = r.postDataJSON();
  });
  await openChapter(page);

  await page.locator("#bsp-pruefung .bsp-install").click();
  await finish(page, "bsp-pruefung");

  // The name a process calls the decision by keeps its casing; the model reference is
  // the stored handle. Conflating the two is what made every decision-carrying example
  // fail to publish.
  expect(ref).toEqual({ name: "Notenschluessel", modelRef: "notenschluessel", projectId: APP_ID });
});

test("installing twice reuses the application and leaves no second reference behind", async ({ page }) => {
  const calls = [];
  installMock(page, calls, {
    existingApps: [{ id: "already-here", name: "Beispiel: Prüfung" }],
    existingRefs: [{ id: "ref-0", name: "Notenschluessel", projectId: "already-here" }],
  });
  await openChapter(page);

  await page.locator("#bsp-pruefung .bsp-install").click();
  await finish(page, "bsp-pruefung");
  await expect(page.locator("#bsp-pruefung .rz-status")).toHaveClass(/ok/);

  expect(calls).not.toContain("POST /api/v1/applications");
  expect(calls).not.toContain("POST /api/v1/dmnrefs");
  expect(calls).toContain("POST /api/v1/drafts?projectId=already-here");
});

test("a card with a demo start installs and starts; one without only installs", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openChapter(page);

  await page.locator("#bsp-pruefe-auftrag .bsp-run").click();
  await finish(page, "bsp-pruefe-auftrag");
  await expect(page.locator("#bsp-pruefe-auftrag .rz-status")).toHaveClass(/ok/);
  expect(calls).toContain("POST /api/v1/processes/41/instances");

  // Only the examples that reach an end event with no configured worker get a start
  // button: starting the others would park a token or raise an incident in the reader's
  // instance, which teaches nothing.
  await expect(page.locator("#bsp-jira-zugangsantrag .bsp-run")).toHaveCount(0);
  await expect(page.locator("#bsp-jira-zugangsantrag .bsp-install")).toHaveCount(1);
});

test("the two examples the server owns or cannot deploy offer no install button", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openChapter(page);

  // The user-administration processes are deployed into the protected system project at
  // server start, and their forms are protected system forms — an install could only
  // answer 403. The AD object model is a study with no process at all. Both still open
  // in the Modeler, which is the useful half.
  await expect(page.locator("#bsp-benutzerverwaltung .bsp-install")).toHaveCount(0);
  await expect(page.locator("#bsp-benutzerverwaltung .bsp-open")).toHaveCount(1);
  await expect(page.locator("#bsp-ad-objektmodell .bsp-install")).toHaveCount(0);
  await expect(page.locator("#bsp-ad-objektmodell .bsp-open")).toHaveCount(0);
});

test("a refused publish shows the reason rather than claiming success", async ({ page }) => {
  const calls = [];
  installMock(page, calls, {
    publish: { json: { deployed: false, reason: "1 DMN reference(s) unresolved or invalid" } },
  });
  await openChapter(page);

  await page.locator("#bsp-pruefung .bsp-install").click();
  await finish(page, "bsp-pruefung");
  await expect(page.locator("#bsp-pruefung .rz-status")).toHaveClass(/err/);
  await expect(page.locator("#bsp-pruefung .rz-status")).toContainText("unresolved");
});
