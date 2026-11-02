// e2e for the catalogue app's Building blocks page (api/web/buildingblocks.js;
// tshopper phase A, ADR-0515).
//
// The wiki's services searched from the catalogue's side: the page offers the
// sources the server names, shows each page with the products using it, and turns
// an unused page into a draft product bound to that page.
import { test, expect } from "@playwright/test";

const WORLD = {
  sources: [{ id: "wiki", name: "Service-Wiki", kind: "wikiwikibangbang" }],
  catalogs: [{ id: "cat_ws", texts: { de: "Arbeitsplatz" }, languages: ["de", "fr"] }],
  blocks: [
    { id: "it_1", key: "SVC-12", title: "VPN-Zugang", space: "SVC", state: "live", usedBy: ["vpn"], usedElsewhere: 0 },
    { id: "it_2", key: "SVC-30", title: "Microsoft Teams Telefonie", space: "SVC", usedBy: [], usedElsewhere: 2 },
    { id: "it_3", key: "SVC-31", title: "Größere Mailbox", space: "SVC", usedBy: [] },
  ],
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/building-blocks-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("a product id is the page's title, as product ids are written", async ({ page }) => {
  expect(await page.evaluate(() => [
    window.__productIdFor("Microsoft Teams Telefonie", "SVC-30"),
    window.__productIdFor("Größere Mailbox", "SVC-31"),
    window.__productIdFor("???", "SVC-9"),
  ])).toEqual(["microsoft-teams-telefonie", "groessere-mailbox", "svc-9"]);
});

test("the search shows each page with what uses it", async ({ page }) => {
  await page.evaluate((w) => window.__mount(w), WORLD);
  await expect(page.locator('select[name="source"] option')).toHaveText(["Service-Wiki"]);
  await page.fill('input[name="q"]', "Telefonie");
  await page.click('form.bb-search button[type="submit"]');
  const rows = page.locator("[data-blocks] tbody tr");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("vpn");
  await expect(rows.nth(1)).toContainText("2");
  await expect(rows.nth(2)).toContainText("Größere Mailbox");
  expect((await page.evaluate(() => window.__calls)).map((c) => c.url))
    .toContain("/api/v1/catalog-building-blocks?source=wiki&q=Telefonie");
});

test("create product saves a draft and binds the page in the catalogue's first language", async ({ page }) => {
  await page.evaluate((w) => window.__mount(w), WORLD);
  await page.fill('input[name="q"]', "Teams");
  await page.click('form.bb-search button[type="submit"]');
  await page.locator('[data-create="1"]').click();
  await expect.poll(() => page.evaluate(() => window.__calls.filter((c) => c.method === "POST").length)).toBe(2);
  const posts = await page.evaluate(() => window.__calls.filter((c) => c.method === "POST"));
  expect(posts[0]).toEqual({ method: "POST", url: "/api/v1/catalog-products", body: {
    id: "microsoft-teams-telefonie", homeCatalog: "cat_ws", state: "draft",
    texts: { de: "Microsoft Teams Telefonie" }, approval: { kind: "none" } } });
  expect(posts[1]).toEqual({ method: "POST", url: "/api/v1/catalog-products/microsoft-teams-telefonie/content",
    body: { source: "wiki", primary: "de", pages: { de: "it_2" } } });
});

test("with no source configured the page says so", async ({ page }) => {
  await page.evaluate((w) => window.__mount(w), { ...WORLD, sources: [] });
  await expect(page.locator(".empty")).toBeVisible();
  await expect(page.locator("form.bb-search")).toHaveCount(0);
});
