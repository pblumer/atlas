// e2e for the shop's reports that name people (api/web/shop.js renderPeopleReport;
// ADR-0515 §3).
//
// The tabs appear only for what the server says this reader may open: My team for a
// line manager, Organisation for a customer responsible. Each tab reads its report,
// fills the period the server chose, says that it names people and that the reading
// is recorded, and offers both CSV downloads on the same period.
import { test, expect } from "@playwright/test";

function installShop(reports) {
  window.__unmatched = [];
  window.__requested = [];
  const item = { id: "vpn", homeCatalog: "cat_s", state: "active", texts: { de: "VPN-Zugang", en: "VPN access" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" };
  const report = (scope) => ({
    scope, catalog: scope === "organisation" ? "cat_s" : "", from: "2026-08-03", to: "2026-10-31",
    people: [
      { id: "usr_ada", name: "Ada Muster", ordered: 2, open: 0, overdue: 1, awaitingApproval: 0,
        holdings: [{ itemId: "vpn", name: "VPN access", since: 1 }] },
      { id: "usr_bob", name: "Bob Beispiel", ordered: 0, open: 0, overdue: 0, awaitingApproval: 0, holdings: [] },
    ],
    positions: [
      { orderId: "o1", itemId: "vpn", name: "VPN access", recipientName: "Ada Muster", serviceLevel: "Standard",
        deliveryDays: 3, leadDays: 4.25, verdict: "overdue", orderedAt: 1761900000000000000, status: "running" },
      { orderId: "o0", itemId: "vpn", name: "VPN access", recipientName: "Ada Muster", serviceLevel: "Standard",
        deliveryDays: 3, leadDays: 1, verdict: "met", orderedAt: 1761800000000000000, status: "done" },
    ],
    products: [], totals: { ordered: 2, met: 1, overdue: 1, metShare: 0.5 },
  });
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_boss", username: "boss", displayName: "Boss", roles: [] } },
    "/api/v1/shop/catalog": { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" }, items: ["vpn"] },
    "/api/v1/catalogs/cat_s/releases": [{ id: "rel_s", catalogId: "cat_s", items: [item], waves: [["vpn"]], withoutApproval: ["vpn"] }],
    "/api/v1/orders": [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_boss", name: "Boss" }],
    "/api/v1/shop/reports": reports,
    "/api/v1/shop/team-report": report("team"),
    "/api/v1/shop/organisation-report": report("organisation"),
  };
  window.fetch = (input) => {
    const full = String(input).replace(/^https?:\/\/[^/]+/, "");
    const path = full.split("?")[0];
    window.__requested.push(full);
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) {
      return Promise.resolve(new Response(JSON.stringify(ROUTES[path]), {
        status: 200, headers: { "content-type": "application/json" },
      }));
    }
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

async function open(page, reports) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installShop, reports);
  await page.goto("/shop.html");
  await expect(page.getByRole("button", { name: "Catalogue", exact: true })).toBeVisible();
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
  expect(await page.evaluate(() => window.__unmatched)).toEqual([]);
});

test("a reader with no reports sees no report tab", async ({ page }) => {
  await open(page, { team: 0, organisations: [] });
  await expect(page.locator(".nav .navlink")).toHaveCount(3);
});

test("a line manager's team tab names the people and their positions", async ({ page }) => {
  await open(page, { team: 2, organisations: [] });
  await expect(page.getByRole("button", { name: "Organisation", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "My team", exact: true }).click();
  await expect(page.getByText("This report names people. Every reading is recorded.")).toBeVisible();
  const kpis = page.locator(".rp-kpi");
  await expect(kpis).toHaveCount(5);
  await expect(kpis.nth(0)).toContainText("2");
  await expect(kpis.nth(3)).toContainText("50");
  const people = page.locator(".rp-table").nth(0).locator("tbody tr");
  await expect(people).toHaveCount(2);
  await expect(people.nth(0)).toContainText("Ada Muster");
  await expect(people.nth(0)).toContainText("VPN access");
  await expect(people.nth(1)).toContainText("—");
  const positions = page.locator(".rp-table").nth(1).locator("tbody tr");
  await expect(positions).toHaveCount(2);
  await expect(positions.nth(0)).toContainText("overdue");
  await expect(positions.nth(0)).toContainText("4.3");
  await expect(page.locator('.rp-period input[type="date"]').nth(0)).toHaveValue("2026-08-03");

  await page.locator('.rp-period input[type="date"]').nth(0).fill("2026-01-01");
  await page.locator('.rp-period input[type="date"]').nth(0).dispatchEvent("change");
  await page.getByRole("button", { name: "Show", exact: true }).click();
  await expect.poll(() => page.evaluate(() => window.__requested.at(-1)))
    .toContain("/api/v1/shop/team-report?from=2026-01-01&to=2026-10-31");
  await expect(page.locator("a.rp-csv").nth(1)).toHaveAttribute("href", /kind=positions/);
  await expect(page.locator("a.rp-csv").nth(0)).toHaveAttribute("href", /format=csv/);
});

test("a customer responsible's organisation tab reads the catalogue's report", async ({ page }) => {
  await open(page, { team: 0, organisations: [{ catalog: "cat_s", name: "Katalog" }] });
  await expect(page.getByRole("button", { name: "My team", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Organisation", exact: true }).click();
  await expect(page.locator(".rp-table").nth(0).locator("tbody tr")).toHaveCount(2);
  expect(await page.evaluate(() => window.__requested.filter((p) => p.includes("organisation-report"))[0]))
    .toContain("catalog=cat_s");
});
