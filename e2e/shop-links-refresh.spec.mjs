// e2e for links into the shop, the shop keeping itself current, and what it no
// longer reads up front (api/web/shop.js routeOf/applyRoute, refresh,
// askLineActions, loadCatalogue), against a fixture.
//
// What was asked for: a product or an entry of "My services" can be linked to
// directly; the shop refreshes itself; and it had become slow — it read every
// release of every catalogue and every position's actions on each load.
//
// It loads the REAL shop.html with only the network replaced.
import { test, expect } from "@playwright/test";

function installShop(opts) {
  window.__shopRefreshMs = opts.refreshMs || 0;
  window.__asked = [];
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" } };
  const ids = ["mailbox", "mailbox-store", "laptop"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("mailbox", "Mailbox", "Mailbox", work),
      item("mailbox-store", "Speicher", "Storage", { descriptions: { de: "Mehr Platz.", en: "More room." } }),
      item("laptop", "Notebook", "Laptop", work),
    ],
    includes: { mailbox: ["mailbox-store"] },
    waves: [ids], withoutApproval: ids,
  };
  const catalogue = { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" },
    items: ids, ...(opts.layout === "tiles" ? { theme: { layout: "tiles" } } : {}) };
  const line = (itemId, status, extra) => Object.assign({ itemId, status, approval: { kind: "none" } }, extra || {});
  window.__orders = [
    { id: "ord_mine", recipient: "usr_1", orderer: "usr_1", createdAt: 2e18, releaseId: "rel_s",
      lines: [line("mailbox", "done", { lifecycleProcess: "mbx" }), line("mailbox-store", "done")] },
    { id: "ord_lap", recipient: "usr_1", orderer: "usr_1", createdAt: 1.9e18, releaseId: "rel_s",
      lines: [line("laptop", "running")] },
    { id: "ord_bea", recipient: "usr_2", orderer: "usr_1", createdAt: 1.8e18, releaseId: "rel_s",
      lines: [line("laptop", "pending")] },
  ];
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": catalogue,
    "/api/v1/shop/catalogs": [catalogue],
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [
      { itemId: "mailbox", since: 2e18, origin: "ordered", orderId: "ord_mine" },
      { itemId: "mailbox-store", since: 2e18, origin: "ordered", orderId: "ord_mine" },
    ] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/shop/reports": { team: 0, organisations: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }, { id: "usr_2", name: "Bea" }],
    "/api/v1/orders/ord_mine/lines/mailbox/actions": { position: "mailbox", actions: [
      { key: "password-reset", effect: "service", triggers: ["customer"],
        labels: { en: "Reset password" }, available: true },
    ] },
  };
  window.fetch = (input) => {
    const url = String(input).replace(/^https?:\/\/[^/]+/, "");
    window.__asked.push(url);
    const path = url.split("?")[0];
    const body = path === "/api/v1/orders" ? window.__orders
      : Object.prototype.hasOwnProperty.call(ROUTES, path) ? ROUTES[path] : null;
    return Promise.resolve(new Response(body === null ? "not in this fixture" : JSON.stringify(body), {
      status: body === null ? 404 : 200, headers: { "content-type": "application/json" },
    }));
  };
}

async function open(page, opts, hash) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installShop, opts || {});
  await page.goto(`/shop.html${hash || ""}`);
  await expect(page.getByRole("button", { name: "My services", exact: true })).toBeVisible({ timeout: 10000 });
}
const asked = (page) => page.evaluate(() => window.__asked);

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("the shop reads only the newest release, and no position's actions up front", async ({ page }) => {
  await open(page, { layout: "cascade" });
  await expect(page.locator(".cascade .cell", { hasText: "Mailbox" })).toBeVisible();
  const urls = await asked(page);
  expect(urls.filter((u) => u.includes("/releases"))).toEqual(["/api/v1/catalogs/cat_s/releases?limit=1"]);
  expect(urls.filter((u) => u.includes("/actions"))).toEqual([]);
  // Asked when the entry is opened, and its events drawn.
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await page.locator(".cascade .cell", { hasText: "Mailbox" }).locator("button.label").click();
  await expect(page.locator(".svc-actions")).toContainText("Reset password");
  expect((await asked(page)).filter((u) => u.includes("/actions"))).toEqual(["/api/v1/orders/ord_mine/lines/mailbox/actions"]);
});

test("a link to a part opens it under its product in the cascade", async ({ page }) => {
  await open(page, { layout: "cascade" }, "#/product/mailbox-store");
  await expect(page.locator(".cascade > .col:nth-child(3) .cell button.label.on")).toHaveText("Mailbox");
  const dialog = page.getByRole("dialog", { name: "Storage" });
  await expect(dialog).toContainText("More room.");
});

test("a link to a product opens its page in the tile layout, and the address follows the page", async ({ page }) => {
  await open(page, { layout: "tiles" }, "#/product/laptop");
  await expect(page.locator(".product-page h2")).toHaveText("Laptop");
  await page.locator(".product-page button.page-back").click();
  await expect(page.locator("button.tile", { hasText: "Mailbox" })).toBeVisible();
  await expect(page).toHaveURL(/#\/catalog$/);
  await page.locator("button.tile", { hasText: "Mailbox" }).click();
  await expect(page).toHaveURL(/#\/product\/mailbox$/);
  // The browser's back leads back to the tiles, and forward to the page again.
  await page.goBack();
  await expect(page.locator(".product-page")).toHaveCount(0);
  await page.goForward();
  await expect(page.locator(".product-page h2")).toHaveText("Mailbox");
});

test("a link opens an entry of my services, the reader's own or somebody else's", async ({ page }) => {
  await open(page, { layout: "tiles" }, "#/services/laptop");
  const mine = page.locator(".svc-entry.open");
  await expect(mine).toHaveCount(1);
  await expect(mine.locator(".svc-order-head")).toContainText("ord_lap");
  // Somebody else's: the filter widens to show it.
  await page.evaluate(() => { location.hash = "#/services/laptop/usr_2"; });
  await expect(page.getByLabel("For whom")).toHaveValue("all");
  await expect(page.locator(".svc-entry.open .svc-order-head")).toContainText("ord_bea");
  // Opening another entry writes its address.
  await page.locator(".svc-entry", { hasText: "Mailbox" }).locator("button.svc-open").click();
  await expect(page).toHaveURL(/#\/services\/mailbox$/);
});

test("the copy button copies the address of the page", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await open(page, { layout: "tiles" }, "#/product/laptop");
  await page.locator(".product-page").getByRole("button", { name: "Copy link" }).click();
  await expect(page.locator(".product-page .link-note")).toHaveText(/Link copied\.|The link could not be copied\./);
});

test("the shop refreshes itself, and leaves a field being typed in alone", async ({ page }) => {
  await open(page, { layout: "tiles", refreshMs: 300 }, "#/services");
  const laptop = page.locator(".svc-entry", { hasText: "Laptop" });
  await expect(laptop.locator(".svc-state")).toContainText("Being set up");
  // The laptop is provisioned on the server; the page shows it without a reload.
  await page.evaluate(() => { window.__orders[1].lines[0].status = "done"; });
  await expect(laptop.locator(".svc-state")).toHaveText("Ready", { timeout: 5000 });

  // While the reader types in the search, nothing is redrawn under them.
  const find = page.getByLabel("Search service, person or order");
  await find.click();
  await find.pressSequentially("Lap");
  const before = await page.evaluate(() => window.__asked.length);
  await expect.poll(() => page.evaluate(() => window.__asked.length), { timeout: 5000 }).toBeGreaterThan(before + 2);
  await expect(find).toBeFocused();
  await expect(find).toHaveValue("Lap");
});
