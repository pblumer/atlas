// e2e for the Operations "Mockups" view's content (api/web/mockups.js, ADR-0216): one
// card per mockup, drawn by its kind's renderer. The SharePoint card
// (ADR-0484) shows each list as a table of its items, each library
// as its files with their properties, and the journal beneath, where a refused operation
// is the row an author came to find; a kind with no renderer still gets a plain card.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/mockups-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const fixture = {
  reports: [{
   kind: "sharepoint", source: "engine", target: "intranet", at: 1, summary: "1 site",
   data: {
    worker: "intranet",
    storedBytes: 2048,
    sites: [{
      address: "contoso.sharepoint.com/sites/verwaltung",
      url: "https://contoso.sharepoint.com/sites/verwaltung",
      lists: [
        {
          title: "Antraege", library: false, columns: ["Betrag", "Kunde", "Status", "Title"], held: 250,
          items: [
            { id: "1", Title: "Zugang <Buchhaltung>", Kunde: "Muster AG", Betrag: 12500, Status: "Neu" },
            { id: "2", Title: "Laptop", Kunde: "17", Status: "Erledigt" },
          ],
        },
        {
          title: "Fallakten", library: true, columns: ["AntragNr"], held: 2,
          files: [
            { path: "Faelle/1", folder: true, size: 0 },
            { path: "Faelle/1/brief.txt", folder: false, size: 2048, fields: { AntragNr: 1 } },
          ],
        },
      ],
    }],
    journal: [
      { seq: 1, at: "2026-10-23T09:00:00Z", site: "x", operation: "create-item", target: "Antraege" },
      { seq: 2, at: "2026-10-23T09:00:01Z", site: "x", operation: "get-item", target: "Antraege #9", error: "sharepoint: get-item: there is no item 9 in \"Antraege\" (mockup)" },
    ],
   },
  }],
};

test("a mockup's lists, libraries and journal are shown, and a refusal stands out", async ({ page }) => {
  await page.evaluate((f) => window.render(f), fixture);
  const card = page.locator('[data-worker="intranet"]');
  await expect(card.locator("h2")).toHaveText("intranet");
  await expect(card.locator(".admock-head > .muted")).toContainText("1 site");
  await expect(card.locator(".admock-head > .muted")).toContainText("2.0 KiB");
  await expect(card.locator(".admock-head > .muted")).toContainText("1 refused");
  await expect(card.locator("h3")).toHaveText("https://contoso.sharepoint.com/sites/verwaltung");

  // A list: id first, then its columns; text as text (escaped), a number as JSON, a
  // missing value as a dash — so "17" the text and 17 the number can be told apart.
  const items = card.locator('[data-k="list:Antraege"] table');
  await expect(items.locator("thead th")).toHaveText(["id", "Betrag", "Kunde", "Status", "Title"]);
  await expect(items.locator("tbody tr").first().locator("td")).toHaveText(["1", "12500", "Muster AG", "Neu", "Zugang <Buchhaltung>"]);
  await expect(items.locator("tbody tr").first().locator("td").nth(1).locator("code")).toHaveText("12500");
  await expect(items.locator("tbody tr").nth(1).locator("td").nth(2).locator("code")).toHaveCount(0);
  await expect(items.locator("tbody tr").nth(1).locator("td").nth(1)).toHaveText("—");
  await expect(card.locator('[data-k="list:Antraege"]')).toContainText("Showing the first 2 of 250");

  // A library: folders marked, sizes readable, properties beside the file.
  const files = card.locator('[data-k="list:Fallakten"] tbody tr');
  await expect(files.nth(0).locator("td").first()).toHaveText("Faelle/1/");
  await expect(files.nth(1)).toContainText("2.0 KiB");
  await expect(files.nth(1)).toContainText("AntragNr = 1");

  // The journal opens by itself when something was refused, and the refusal is red.
  const ops = card.locator('[data-k="ops"]');
  await expect(ops).toHaveAttribute("open", "");
  await expect(ops.locator("tr.sqlmock-failed")).toHaveCount(1);
  await expect(ops.locator("tr.sqlmock-failed .sqlmock-detail")).toContainText("there is no item 9");

  await expect(card.locator("[data-spmock-clear]")).toHaveAttribute("data-spmock-clear", "intranet");
});

test("an empty site and no mockup at all each say what to do", async ({ page }) => {
  await page.evaluate(() => window.render({ reports: [{ kind: "sharepoint", target: "leer", data: { worker: "leer", storedBytes: 0, sites: [], journal: [] } }] }));
  await expect(page.locator('[data-worker="leer"]')).toContainText("the first time a task writes to it");
  await expect(page.locator('[data-k="ops"]')).not.toHaveAttribute("open", "");

  await page.evaluate(() => window.render({ reports: [] }));
  await expect(page.locator("#list")).toContainText("No mockup is running");
  await expect(page.locator("#list")).toContainText("Mockup");
});

// A kind nobody has written a card for is still shown — plainly, its summary and its
// data as served — rather than dropped (ADR-0216).
test("a kind without a renderer gets a plain card", async ({ page }) => {
  await page.evaluate(() => window.render({ reports: [{ kind: "jira", target: "acme", summary: "2 issues", data: { issues: ["ACME-1 <b>x</b>"] } }] }));
  const card = page.locator('[data-kind="jira"]');
  await expect(card.locator("h2")).toHaveText("acme");
  await expect(card).toContainText("2 issues");
  await expect(card.locator("pre")).toContainText("ACME-1 <b>x</b>");
  await expect(card.locator("[data-spmock-clear]")).toHaveCount(0);
});

// The mock REST API card (ADR-draft-console-managed-openapi-mock). It is where the
// journal lives at all: the served mock refuses /__mock/calls because a request body
// comes from a real instance, so this card is the only place the calls are readable.
// The column an author comes for is an operation at zero calls — the one they thought
// they were calling and were not.
const openAPIFixture = {
  reports: [{
    kind: "openapi", source: "petstore", target: "Pet Store 1.2", at: 1,
    summary: "Pet Store 1.2 — 3 operations, 2 calls",
    data: {
      title: "Pet Store", version: "1.2", basePath: "/v1",
      operations: [
        { method: "GET", path: "/pets/{petId}", id: "getPet", calls: 2 },
        { method: "POST", path: "/pets", id: "createPet", calls: 0 },
        { method: "DELETE", path: "/pets/{petId}", calls: 0 },
      ],
      calls: [
        { seq: 1, method: "GET", path: "/pets/7", operation: "getPet", status: 200, requestId: "2251799813685254" },
        { seq: 2, method: "GET", path: "/pets/<script>", operation: "getPet", status: 404, body: { reason: "none" } },
      ],
    },
  }],
};

test("a mock REST API shows its operations with call counts, and its journal", async ({ page }) => {
  await page.evaluate((f) => window.render(f), openAPIFixture);
  const card = page.locator('[data-kind="openapi"]');
  await expect(card.locator("h2")).toHaveText("petstore");
  await expect(card.locator(".tag")).toHaveText("OpenAPI");
  await expect(card.locator(".admock-head > .muted")).toContainText("Pet Store 1.2");
  await expect(card.locator(".admock-head > .muted")).toContainText("3 operations");
  // Two of the three were never called, which is the finding the card exists for.
  await expect(card.locator(".admock-head > .muted")).toContainText("2 never called");

  const ops = card.locator('[data-k="openapi-ops"]');
  await expect(ops).toHaveAttribute("open", "");
  await expect(ops.locator("tbody tr")).toHaveCount(3);
  await expect(ops.locator("tbody tr").first().locator("td")).toHaveText(["GET getPet", "/pets/{petId}", "2"]);
  // An uncalled operation reads as a dash rather than a 0, and is muted.
  await expect(ops.locator("tbody tr").nth(1).locator("td").nth(2)).toHaveText("—");
  await expect(ops.locator("tbody tr.muted")).toHaveCount(2);

  const calls = card.locator('[data-k="openapi-calls"]');
  await expect(calls).toHaveAttribute("open", "");
  await expect(calls.locator("tbody tr")).toHaveCount(2);
  // The job key travels as X-Request-ID, which is what ties a call back to an instance.
  await expect(calls.locator("tbody tr").first()).toContainText("job 2251799813685254");
  // A 4xx stands out the way a refused SharePoint operation does, and the path is
  // escaped rather than interpreted — it came off the wire from whoever called.
  await expect(calls.locator("tr.sqlmock-failed")).toHaveCount(1);
  await expect(calls.locator("tr.sqlmock-failed code").first()).toHaveText("/pets/<script>");
  await expect(calls.locator("tr.sqlmock-failed .sqlmock-detail")).toContainText('"reason":"none"');
});

test("a mock REST API nothing has called yet says how to reach it", async ({ page }) => {
  await page.evaluate(() => window.render({
    reports: [{ kind: "openapi", source: "orders", data: { title: "Orders", operations: [{ method: "GET", path: "/orders" }], calls: [] } }],
  }));
  const card = page.locator('[data-kind="openapi"]');
  await expect(card).toContainText("Nothing called it yet");
  await expect(card.locator('[data-k="openapi-calls"]')).not.toHaveAttribute("open", "");
  // No clear button: unlike a SharePoint site there is no state to empty — a mock API
  // has no memory, which is ADR-0217's own statement of what it does not do.
  await expect(card.locator("[data-spmock-clear]")).toHaveCount(0);
});

test("the empty view points at the mock REST APIs too", async ({ page }) => {
  await page.evaluate(() => window.render({ reports: [] }));
  await expect(page.locator("#list")).toContainText("mock REST API");
  await expect(page.locator("#list")).toContainText("OpenAPI document");
});
