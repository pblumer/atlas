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

  // The mock denies every unrecognised API request, including /api/v1/layout. These
  // two still draw, which is the point: the workshop's models ship hand-authored
  // BPMN-DI, so they render straight from the embedded block — the diagram a reader
  // sees here is the one the Modeler shows, and a signed-out reader sees it too.
  await expect(page.locator('#werkstatt [data-wk-model="bw-bewerbung"] svg')).toBeVisible({ timeout: 20000 });
  await expect(page.locator('#werkstatt [data-wk-model="bw-interview"] svg')).toBeVisible({ timeout: 20000 });
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

// The accounts chapter is the one part of the handbook that documents something a
// reader can lock themselves out with, so it is worth a test that it is there, that
// it names all four roles, and that it says the two things an operator has to know
// before switching the claim mapping on.
test("the accounts chapter names the four roles and the cost of federating them", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await page.goto("/handbuch.html");
  await expect(page.locator('#toc a[href="#konten"]')).toBeVisible();

  const chapter = page.locator("#konten");
  for (const role of ["admin", "modeler", "operator", "user"]) {
    await expect(chapter.locator("table code", { hasText: new RegExp(`^${role}$`) })).toBeVisible();
  }
  // Both languages, because a chapter that exists in one is half a chapter.
  for (const [lang, text] of [
    ["de", "Notfallzugang"],
    ["en", "Break glass"],
  ]) {
    await page.click(`#lang-${lang}`);
    await expect(chapter).toContainText(text);
  }
});

// Panorama and Data are two of the six apps the shell offers, and the handbook
// taught neither for a while — it still said "the four apps" while app.js had six.
// A missing chapter is invisible in a way a wrong sentence is not: nothing fails,
// nobody notices, and the app that goes undocumented is the one nobody discovers.
// These hold the two chapters, in both languages, and hold the count itself.
test("the landscape and data chapters exist, in both languages", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await page.goto("/handbuch.html");

  for (const anchor of ["#panorama", "#infomodell"]) {
    await expect(page.locator(`#toc a[href="${anchor}"]`)).toBeVisible();
    await expect(page.locator(`main section${anchor}`)).toHaveCount(1);
  }

  // Each chapter has to carry the load-bearing fact of its subject in whichever
  // language is on screen. A chapter that exists in one is half a chapter.
  for (const [lang, panorama, infomodel] of [
    ["de", "abgeleitet", "Business Key"],
    ["en", "derived", "business key"],
  ]) {
    await page.click(`#lang-${lang}`);
    await expect(page.locator("#panorama").locator(`[data-l="${lang}"]`, { hasText: panorama }).first())
      .toBeVisible();
    await expect(page.locator("#infomodell").locator(`[data-l="${lang}"]`, { hasText: infomodel }).first())
      .toBeVisible();
  }
});

test("the welcome chapter offers all six apps, Panorama and Data included", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await page.goto("/handbuch.html");

  const cards = page.locator("#willkommen .grid2 .card");
  await expect(cards).toHaveCount(6);
  // The routes matter more than the names: a card that names an app but links
  // somewhere else is the failure a reader meets rather than reads.
  for (const route of ["/#/console", "/#/modeler", "/#/tasks", "/#/operations",
    "/#/panorama/landscape", "/#/data"]) {
    await expect(cards.locator(`a[href="${route}"]`)).toHaveCount(1);
  }
});

// The Playground is the Modeler tab that answers "what does this model do with
// *this* data" — the question the token simulation deliberately does not answer.
// It sits inside the test chapter rather than owning one, so the card that names
// it is the only way a reader finds it, and the anchor it points at has to exist.
test("the test chapter leads to the Playground", async ({ page }) => {
  const calls = [];
  installMock(page, calls);
  await page.goto("/handbuch.html");

  // The page picks its language from navigator.language when nobody has chosen,
  // so a test that does not choose is testing whichever locale the runner has.
  // The card has to lead there in both.
  for (const lang of ["de", "en"]) {
    await page.click(`#lang-${lang}`);
    await expect(page.locator(`#testen [data-l="${lang}"] a[href="#playground"]`).first()).toBeVisible();
  }
  await expect(page.locator("#playground")).toHaveCount(1);
});
