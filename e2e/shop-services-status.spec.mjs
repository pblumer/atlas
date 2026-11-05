// e2e for the lights and the counts in "My services" (api/web/shop.js
// serviceState, building).
//
// What was asked for: "My services" shows, as "My orders" does, whether a thing is
// already there or still being set up — the same coloured dots — and how many of
// each somebody holds, since some products are ordered more than once.
import { test, expect } from "@playwright/test";

function installShop() {
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const identity = { category: "Identitaet", categoryTexts: { de: "Identität", en: "Identity" } };
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" } };
  const ids = ["funktion", "monitor", "android"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("funktion", "Funktionsaccount (F)", "Functional account (F)", Object.assign({ multipleAllowed: true }, identity)),
      item("monitor", "Monitor 24 Zoll", "Monitor 24-inch", Object.assign({ multipleAllowed: true }, work)),
      item("android", "Android Smartphone", "Android smartphone", work),
    ],
    waves: [ids], withoutApproval: ids,
  };
  const held = ["funktion", "funktion", "funktion", "monitor"];
  const line = (itemId, status, positionId) => ({ itemId, status, positionId, approval: { kind: "none" } });
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": { id: "cat_s", rank: 1, languages: ["de", "en"],
      texts: { de: "Katalog", en: "Catalogue" }, items: ids },
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    // A second monitor on its way, and a phone that is waiting.
    "/api/v1/orders": [{ id: "ord_1", recipient: "usr_1", orderer: "usr_1", createdAt: 1, releaseId: "rel_s",
      lines: [line("monitor", "running", "p1"), line("android", "pending", "p2")] }],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: held.map((itemId) => ({ itemId, since: 1, origin: "ordered" })) },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/shop/reports": { team: 0, organisations: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
  };
  ROUTES["/api/v1/shop/catalogs"] = [ROUTES["/api/v1/shop/catalog"]];
  window.fetch = (input) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const body = Object.prototype.hasOwnProperty.call(ROUTES, path) ? ROUTES[path] : null;
    return Promise.resolve(new Response(body === null ? "not in this fixture" : JSON.stringify(body), {
      status: body === null ? 404 : 200, headers: { "content-type": "application/json" },
    }));
  };
}

test("each held product carries a light and a count, and what is coming shows beside it", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installShop);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  const row = (name) => page.locator(".cascade > .col:nth-child(3) .cell", { hasText: name });

  // In the cascade too, each Marktleistung carries its catalogue icon as a pie.
  // Held three times: a full pie, ready, and the count.
  await expect(row("Functional account (F)").locator(".pie-icon")).toHaveAttribute("data-share", "100");
  await expect(row("Functional account (F)").locator(".meta")).toContainText("Ready");
  await expect(row("Functional account (F)").locator(".meta")).toContainText("3 held");

  // Held once with a second on its way: ready for the one there, and both counted.
  await expect(row("Monitor 24-inch").locator(".pie-icon")).toHaveAttribute("data-share", "100");
  await expect(row("Monitor 24-inch").locator(".meta")).toContainText("1 held · 1 being set up");

  // Ordered and not yet there: listed all the same, an empty pie.
  const phone = row("Android smartphone").locator(".pie-icon");
  await expect(phone).toHaveAttribute("data-share", "0");
  await expect(phone).toHaveAttribute("aria-label", "Being set up: 0 of 1 ready");
  await expect(row("Android smartphone").locator(".meta")).toContainText("1 being set up");

  // Opened, an entry shows the positions behind it with the order list's lights.
  await row("Monitor 24-inch").locator("button.label").click();
  await expect(page.locator(".svc-detail .lines .dot.running")).toHaveCount(1);
  await row("Android smartphone").locator("button.label").click();
  await expect(page.locator(".svc-detail .lines .dot.pending")).toHaveCount(1);
  expect(errors).toEqual([]);
});

test("the information button is a plain i, without a ring", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.addInitScript(installShop);
  await page.goto("/shop.html");
  const svg = page.locator('button.sq[data-icon="info"] svg').first();
  await expect(svg.locator("circle")).toHaveCount(1);
  expect(Number(await svg.locator("circle").getAttribute("r"))).toBeLessThan(4);
  await expect(svg.locator("path.stem")).toHaveCount(1);
});

// The tile layout: "My services" is a list, one row per Marktleistung, its
// catalogue icon a pie of how far it is provisioned.
function installTiles() {
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" } };
  const ids = ["notebook", "notebook-hw", "dock", "monitor"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("notebook", "Notebook Convertible", "Notebook Convertible", Object.assign({ icon: "device-laptop" }, work)),
      item("notebook-hw", "Notebook (Gerät)", "Notebook (device)"),
      item("dock", "Dockingstation", "Docking station"),
      item("monitor", "Monitor 24 Zoll", "Monitor 24-inch", Object.assign({ icon: "device-desktop", multipleAllowed: true }, work)),
    ],
    includes: { notebook: ["notebook-hw", "dock"] },
    waves: [ids], withoutApproval: ids,
  };
  const catalogue = { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" },
    items: ids, theme: { layout: "tiles" } };
  const line = (itemId, status, positionId) => ({ itemId, status, positionId, approval: { kind: "none" } });
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": catalogue,
    "/api/v1/shop/catalogs": [catalogue],
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": [{ id: "ord_7", recipient: "usr_1", orderer: "usr_1", createdAt: 1, releaseId: "rel_s",
      lines: [line("notebook", "done", "p1"), line("notebook-hw", "done", "p2"), line("dock", "running", "p3")] }],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [
      { itemId: "notebook", since: 1.7e18, origin: "ordered", orderId: "ord_7" },
      { itemId: "notebook-hw", since: 1.7e18, origin: "ordered", orderId: "ord_7" },
      { itemId: "monitor", since: 1.7e18, origin: "ordered", orderId: "ord_3" },
      { itemId: "monitor", since: 1.7e18, origin: "ordered", orderId: "ord_4" },
    ] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/shop/reports": { team: 0, organisations: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
  };
  window.fetch = (input) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const body = Object.prototype.hasOwnProperty.call(ROUTES, path) ? ROUTES[path] : null;
    return Promise.resolve(new Response(body === null ? "not in this fixture" : JSON.stringify(body), {
      status: body === null ? 404 : 200, headers: { "content-type": "application/json" },
    }));
  };
}

test("in the tile layout my services are a list of Marktleistungen with a pie for each", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installTiles);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await expect(page.locator(".svc-list")).toBeVisible();
  await expect(page.locator(".cascade")).toHaveCount(0);

  // Marktleistungen only, under their heading; their parts are not rows.
  await expect(page.locator(".svc-heading")).toHaveText(["Workplace"]);
  await expect(page.locator(".svc-row:not(.svc-head) .svc-name strong")).toHaveText(["Monitor 24-inch", "Notebook Convertible"]);

  // The notebook brings a device and a dock, and the dock is still on its way:
  // two of three ready, a pie two thirds full in the catalogue's colour.
  const notebook = page.locator(".svc-row", { hasText: "Notebook Convertible" });
  await expect(notebook.locator(".pie-icon")).toHaveAttribute("data-share", "67");
  await expect(notebook.locator(".pie-icon")).toHaveAttribute("aria-label", "Being set up: 2 of 3 ready");
  await expect(notebook.locator(".svc-state")).toHaveText("Being set up: 2 of 3 ready");
  await expect(notebook.locator(".svc-order")).toHaveText("ord_7");
  // The icon is the catalogue's, drawn twice: grey whole, and the share in colour.
  await expect(notebook.locator(".pie-icon svg")).toHaveCount(2);
  const colour = await notebook.locator(".pie-done").evaluate((e) => getComputedStyle(e).backgroundColor);
  const accent = await page.evaluate(() => {
    const p = document.createElement("span"); p.style.color = "var(--accent)"; document.body.append(p);
    const c = getComputedStyle(p).color; p.remove(); return c;
  });
  expect(colour).toBe(accent);

  // The monitors are all there, twice: a full pie, ready, and the count.
  const monitor = page.locator(".svc-row", { hasText: "Monitor 24-inch" });
  await expect(monitor.locator(".pie-icon")).toHaveAttribute("data-share", "100");
  await expect(monitor.locator(".svc-state")).toHaveText("Ready");
  await expect(monitor.locator(".svc-qty")).toHaveText("2×");
  expect(errors).toEqual([]);
});
