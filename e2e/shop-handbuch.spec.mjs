// End-to-end coverage for the shop handbook (api/web/shop-handbuch.html) and its
// installer: the page is bilingual like the handbook, and installing the
// administration-services example creates the application, its forms and processes,
// publishes them, and imports the catalogue document with the reader's answers in
// place of its placeholders (ADR-draft-a-catalogue-is-imported-as-one-document).
import { test, expect } from "@playwright/test";

function installMock(page) {
  const calls = [];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const method = req.method();
    const body = req.postData();
    calls.push({ method, path, body });
    if (path.endsWith("/principals")) {
      return route.fulfill({ json: [
        { type: "user", id: "usr-fm", name: "Fabienne Meier" },
        { type: "group", id: "grp-alle", name: "Alle Mitarbeitenden" },
        { type: "group", id: "grp-bau", name: "Fachbereich Bau" },
        { type: "group", id: "grp-geo", name: "Geoportal-Team" },
      ] });
    }
    if (path.endsWith("/applications") && method === "GET") return route.fulfill({ json: [] });
    if (path.endsWith("/applications") && method === "POST") return route.fulfill({ status: 201, json: { id: "app-vd" } });
    if (path.endsWith("/forms") || path.endsWith("/drafts")) return route.fulfill({ status: 201, json: {} });
    if (path.endsWith("/applications/app-vd/publish")) return route.fulfill({ json: { deployed: true, definitions: [] } });
    if (path.endsWith("/catalogs/import")) {
      return route.fulfill({ json: { created: ["catalog:cat-verwaltung-dienste", "catalog:cat-fachbereich-bau"], updated: [], releases: [{}, {}] } });
    }
    return route.fulfill({ json: {} });
  });
  return calls;
}

test("the shop handbook speaks both languages and links the Console's catalogue", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page);
  await page.goto("/shop-handbuch.html");
  await page.click("#lang-de");
  await expect(page.locator("#katalog-aufbauen h2:visible")).toHaveText("Einen Katalog aufbauen");
  await page.click("#lang-en");
  await expect(page.locator("#katalog-aufbauen h2:visible")).toHaveText("Building a catalogue");
  await expect(page.locator('a.applink:visible[href="/#/catalog"]')).toHaveCount(1);
  expect(errors).toEqual([]);
});

test("the installer asks for the audiences and approvers and imports the shop with the answers", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const calls = installMock(page);
  await page.goto("/shop-handbuch.html#beispiel");
  await page.click("#lang-de");
  const q = page.locator("#inst-questions");
  await expect(q.locator("select")).toHaveCount(4);
  await q.locator('select[data-ph="zielgruppe"]').selectOption("grp-alle");
  await q.locator('select[data-ph="zielgruppe-bau"]').selectOption("grp-bau");
  await q.locator('select[data-ph="facility-management"]').selectOption("usr-fm");
  await q.locator('select[data-ph="geoportal-verantwortliche"]').selectOption("grp-geo");
  await page.click("#inst-go");
  await expect(page.locator("#inst-status")).toContainText("Installiert");
  await expect(page.locator("#inst-status")).toContainText("Veröffentlicht: 2");

  const writes = calls.filter((c) => c.method === "POST").map((c) => c.path);
  expect(writes[0]).toBe("/api/v1/applications");
  expect(writes.filter((p) => p === "/api/v1/forms")).toHaveLength(10);
  expect(writes.filter((p) => p === "/api/v1/drafts")).toHaveLength(5);
  // The catalogue goes in last, once the processes it binds are deployed.
  expect(writes.slice(-2)).toEqual(["/api/v1/applications/app-vd/publish", "/api/v1/catalogs/import"]);

  const doc = JSON.parse(calls.find((c) => c.path === "/api/v1/catalogs/import").body);
  expect(doc.publish).toBe(true);
  expect(doc.catalogs.map((c) => c.groups[0])).toEqual(["grp-alle", "grp-bau"]);
  const approval = Object.fromEntries(doc.products.map((p) => [p.id, p.approval]));
  expect(approval["vd-parkplatz"]).toEqual({ kind: "fixed", ref: "usr-fm" });
  expect(approval["vd-geoportal"]).toEqual({ kind: "role", ref: "grp-geo" });
  expect(JSON.stringify(doc)).not.toContain("{{");
  expect(errors).toEqual([]);
});

test("a refused import lists every problem it was refused for", async ({ page }) => {
  installMock(page);
  await page.route("**/api/v1/catalogs/import", (route) => route.fulfill({
    status: 403, json: { problems: [{ subject: "catalog:cat-verwaltung-dienste", problem: "you do not maintain this catalogue" }] },
  }));
  await page.goto("/shop-handbuch.html#beispiel");
  await page.click("#lang-en");
  await expect(page.locator("#inst-questions select")).toHaveCount(4);
  await page.click("#inst-go");
  await expect(page.locator("#inst-status")).toContainText("catalog:cat-verwaltung-dienste: you do not maintain this catalogue");
});
