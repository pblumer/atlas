// e2e for "My services" as the one screen for what somebody holds and what they
// ordered (api/web/shop.js serviceEntries, renderServices), against a fixture.
//
// What was asked for: "My orders" is folded into "My services", in both layouts.
// The cascade keeps its columns, each Marktleistung with its catalogue icon as a
// pie; the tile layout is a list. An entry opened shows the orders and positions
// behind it and the intermediate events — the actions — its product takes. A
// filter says for whom (me, others, everyone), a status, and a search. The
// cascade catalogue carries the tile layout's icons.
//
// It loads the REAL shop.html with only the network replaced.
import { test, expect } from "@playwright/test";

function installShop(layout) {
  window.__asked = [];
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" } };
  const ids = ["mailbox", "mailbox-store", "laptop", "phone"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("mailbox", "Mailbox", "Mailbox", Object.assign({ icon: "mail" }, work)),
      item("mailbox-store", "Speicher", "Storage", { icon: "database" }),
      item("laptop", "Notebook", "Laptop", Object.assign({ icon: "device-laptop" }, work)),
      item("phone", "Telefon", "Phone", Object.assign({ icon: "device-mobile" }, work)),
    ],
    includes: { mailbox: ["mailbox-store"] },
    waves: [ids], withoutApproval: ids,
  };
  const catalogue = { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" },
    items: ids, ...(layout === "tiles" ? { theme: { layout: "tiles" } } : {}) };
  const line = (itemId, status, extra) => Object.assign({ itemId, status, approval: { kind: "none" } }, extra || {});
  const ORDERS = [
    // The reader's mailbox, held, with a lifecycle process that takes actions.
    { id: "ord_mine", recipient: "usr_1", orderer: "usr_1", createdAt: 2e18, releaseId: "rel_s",
      lines: [line("mailbox", "done", { lifecycleProcess: "mbx" }), line("mailbox-store", "done")] },
    // A laptop the reader ordered for a colleague, still on its way.
    { id: "ord_for_bea", recipient: "usr_2", orderer: "usr_1", createdAt: 1.9e18, releaseId: "rel_s",
      lines: [line("laptop", "running")] },
    // A phone of the reader's that was refused.
    { id: "ord_refused", recipient: "usr_1", orderer: "usr_1", createdAt: 1.8e18, releaseId: "rel_s",
      lines: [line("phone", "rejected", { reason: "no budget" })] },
  ];
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": catalogue,
    "/api/v1/shop/catalogs": [catalogue],
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": ORDERS,
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
        labels: { de: "Passwort zurücksetzen", en: "Reset password" }, available: true },
    ] },
  };
  window.fetch = (input, init) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const ask = path.match(/^\/api\/v1\/orders\/ord_mine\/lines\/mailbox\/actions\/([a-z-]+)$/);
    if (ask && init && init.method === "POST") {
      window.__asked.push(ask[1]);
      return Promise.resolve(new Response(JSON.stringify({ action: ask[1] }), {
        status: 200, headers: { "content-type": "application/json" } }));
    }
    const body = Object.prototype.hasOwnProperty.call(ROUTES, path) ? ROUTES[path] : null;
    return Promise.resolve(new Response(body === null ? "not in this fixture" : JSON.stringify(body), {
      status: body === null ? 404 : 200, headers: { "content-type": "application/json" },
    }));
  };
}

async function open(page, layout) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installShop, layout);
  await page.goto("/shop.html");
  await expect(page.getByRole("button", { name: "My services", exact: true })).toBeVisible({ timeout: 10000 });
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("there is no separate orders screen", async ({ page }) => {
  await open(page, "cascade");
  await expect(page.getByRole("button", { name: "My orders" })).toHaveCount(0);
  await expect(page.locator(".nav .navlink")).toHaveText(["Catalogue", "My services"]);
});

test("the cascade catalogue shows each product's icon, as the tiles do", async ({ page }) => {
  await open(page, "cascade");
  const offering = page.locator(".cascade > .col:nth-child(3) .cell", { hasText: "Mailbox" });
  await expect(offering.locator(".cell-icon svg")).toHaveCount(1);
  await offering.locator("button.label").click();
  const part = page.locator(".cascade > .col:nth-child(4) .cell", { hasText: "Storage" });
  await expect(part.locator(".cell-icon svg")).toHaveCount(1);
});

test("the filter says for whom, by status, and finds by order", async ({ page }) => {
  await open(page, "cascade");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  const names = page.locator(".cascade > .col:nth-child(3) .cell button.label");
  // Mine by default: the mailbox held and the phone refused; not the laptop for Bea.
  await expect(names).toHaveText(["Mailbox", "Phone"]);
  await page.getByLabel("For whom").selectOption("others");
  await expect(names).toHaveText(["Laptop"]);
  await expect(page.locator(".cascade .cell", { hasText: "Laptop" })).toContainText("For Bea");
  await page.getByLabel("For whom").selectOption("all");
  await expect(names).toHaveText(["Laptop", "Mailbox", "Phone"]);
  await page.getByLabel("Status").selectOption("ended");
  await expect(names).toHaveText(["Phone"]);
  await page.getByLabel("Status").selectOption("");
  await page.getByLabel("Search service, person or order").fill("ord_for");
  await expect(names).toHaveText(["Laptop"]);
});

test("an entry opened in the cascade shows its events, orders and positions", async ({ page }) => {
  await open(page, "cascade");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await page.locator(".cascade .cell", { hasText: "Mailbox" }).locator("button.label").click();
  const detail = page.locator(".svc-cascade > .col:nth-child(4) .svc-detail");
  await expect(detail.locator(".svc-actions")).toContainText("Possible events");
  await expect(detail.locator(".svc-order-head")).toContainText("ord_mine");
  await expect(detail.locator(".lines > li")).toHaveCount(2);
  page.on("dialog", (d) => d.accept());
  await detail.getByRole("button", { name: "Reset password" }).click();
  await expect.poll(() => page.evaluate(() => window.__asked)).toEqual(["password-reset"]);
  await expect(page.locator(".svc-detail")).toContainText("Requested: Reset password");

  // A refused order says so, and why.
  await page.locator(".cascade .cell", { hasText: "Phone" }).locator("button.label").click();
  await expect(page.locator(".svc-detail .lines")).toContainText("no budget");
  await expect(page.locator(".cascade .cell", { hasText: "Phone" }).locator(".pie-icon"))
    .toHaveAttribute("data-share", "0");
});

test("in the tile layout an entry opens under its row", async ({ page }) => {
  await open(page, "tiles");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  const row = page.locator(".svc-entry", { hasText: "Mailbox" });
  await expect(row.locator(".svc-detail")).toHaveCount(0);
  await row.locator("button.svc-open").click();
  await expect(row.locator("button.svc-open")).toHaveAttribute("aria-expanded", "true");
  await expect(row.locator(".svc-detail .svc-actions")).toContainText("Reset password");
  await expect(row.locator(".svc-detail .svc-order-head")).toContainText("ord_mine");
  await page.getByLabel("For whom").selectOption("all");
  const laptop = page.locator(".svc-entry", { hasText: "Laptop" });
  await expect(laptop.locator(".svc-meta")).toContainText("For Bea");
  await expect(laptop.locator(".pie-icon")).toHaveAttribute("data-share", "0");
});
