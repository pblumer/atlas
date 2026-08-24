// End-to-end coverage for the handbook's workshop chapter (api/web/handbuch.html,
// "Werkstatt: eine kleine Applikation bauen"). Two things there are browser code with
// no Go test behind them: the chapter renders its two models as diagrams from the
// data block embedded in the page, and its install button creates a whole
// application — application, decision, forms, drafts, publish, start — over the REST
// API. The static e2e harness runs no Go server, so these drive the REAL page against
// a mocked /api/v1, verifying the wiring and the call sequence, not the server side.
//
// The data block itself is guarded on the Go side: `go test ./examples` regenerates it
// from examples/bewerbermanagement/ and fails when the page's copy has drifted.
import { test, expect } from "@playwright/test";

const APP_ID = "app-1";

// installMock answers the endpoints the install button walks, in the shapes the page
// reads, and records every call so a test can assert the order. Anything else the page
// asks for (the recipes' layout endpoint, say) gets a benign failure — the workshop
// must not depend on it.
function installMock(page, calls, over = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname + url.search;
    calls.push(`${req.method()} ${path}`);

    if (path === "/api/v1/applications" && req.method() === "GET") {
      return route.fulfill({ json: over.existing || [] });
    }
    if (path === "/api/v1/applications" && req.method() === "POST") {
      return route.fulfill({ json: { id: APP_ID, name: "Bewerbermanagement", key: "bewerbermanagement" } });
    }
    if (path.startsWith("/api/v1/dmn-models")) {
      return route.fulfill({ json: { modelRef: "bw-vorpruefung", decisions: ["bw-vorpruefung"] } });
    }
    if (path === "/api/v1/dmnrefs") {
      return route.fulfill({ json: { id: "ref-1", name: "bw-vorpruefung" } });
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
            { key: 41, processId: "bw-bewerbung", version: 1 },
            { key: 42, processId: "bw-interview", version: 1 },
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

// openWorkshop loads the chapter and waits for its diagrams, which render lazily.
async function openWorkshop(page) {
  await page.goto("/handbuch.html#werkstatt");
  await page.locator("#wk-install").waitFor();
}

const install = async (page) => {
  await page.locator("#wk-install").click();
  await expect(page.locator("#wk-status")).toHaveClass(/ok|err/, { timeout: 20000 });
};

test("both workshop models render as diagrams without asking the server for a layout", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openWorkshop(page);

  // The mock fails every /api/v1/layout request (the recipes chapter's renderer does
  // make them). These two still draw, which is the point: the workshop's models ship
  // hand-authored BPMN-DI, so they render straight from the embedded block — the
  // diagram a reader sees here is the one the Modeler shows, and a signed-out reader
  // sees it too.
  await expect(page.locator('#werkstatt [data-wk-model="bw-bewerbung"] svg')).toBeVisible({ timeout: 20000 });
  await expect(page.locator('#werkstatt [data-wk-model="bw-interview"] svg')).toBeVisible({ timeout: 20000 });
  expect(calls.filter((c) => c.includes("/layout")).length).toBeGreaterThan(0);
});

test("the install button walks the application in the order a process developer would", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openWorkshop(page);
  await install(page);

  await expect(page.locator("#wk-status")).toHaveClass(/ok/);
  await expect(page.locator("#wk-status")).toContainText("Release 1");
  await expect(page.locator("#wk-done")).toBeVisible();

  // The bracket first, its content second, publish once everything is in, start last.
  expect(calls.filter((c) => !c.includes("/layout"))).toEqual([
    "GET /api/v1/applications",
    "POST /api/v1/applications",
    "POST /api/v1/dmn-models?handle=bw-vorpruefung",
    "POST /api/v1/dmnrefs",
    "POST /api/v1/forms",
    "POST /api/v1/forms",
    "POST /api/v1/forms",
    `POST /api/v1/drafts?projectId=${APP_ID}`,
    `POST /api/v1/drafts?projectId=${APP_ID}`,
    `POST /api/v1/applications/${APP_ID}/publish`,
    "POST /api/v1/processes/41/instances",
  ]);
});

test("installing twice reuses the application instead of leaving an empty copy behind", async ({ page }) => {
  const calls = [];
  installMock(page, calls, {
    existing: [{ id: "already-here", name: "Bewerbermanagement", key: "bewerbermanagement" }],
  });
  await openWorkshop(page);
  await install(page);

  await expect(page.locator("#wk-status")).toHaveClass(/ok/);
  expect(calls).not.toContain("POST /api/v1/applications");
  expect(calls).toContain("POST /api/v1/drafts?projectId=already-here");
});

test("the chosen data decides the number of interview rounds the reader is told about", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await openWorkshop(page);

  // No degree, no experience: the DMN's catch-all rule rejects, so no interview runs
  // and the reader is pointed at Operations rather than the inbox.
  await page.selectOption("#wk-abschluss", "keiner");
  await page.fill("#wk-jahre", "0");
  await install(page);
  await expect(page.locator("#wk-status")).toHaveClass(/ok/);
  await expect(page.locator("#wk-status")).toContainText(/rejected|abgesagt/);

  const started = calls.filter((c) => c.includes("/instances"));
  expect(started).toHaveLength(1);
});

test("a refused publish is reported and nothing is started", async ({ page }) => {
  const calls = [];
  installMock(page, calls, {
    publish: { json: { deployed: false, reason: 'draft "bw-bewerbung" references decision(s) [bw-vorpruefung] not provided by any DMN reference in this project' } },
  });
  await openWorkshop(page);
  await install(page);

  await expect(page.locator("#wk-status")).toHaveClass(/err/);
  await expect(page.locator("#wk-status")).toContainText("bw-vorpruefung");
  await expect(page.locator("#wk-done")).toBeHidden();
  expect(calls.filter((c) => c.includes("/instances"))).toEqual([]);
});

test("the chapter is reachable from the table of contents", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await page.goto("/handbuch.html");
  const toc = page.locator("#toc");
  await expect(toc.locator('a[href="#rolle"]')).toBeVisible();
  await expect(toc.locator('a[href="#werkstatt"]')).toBeVisible();
});
