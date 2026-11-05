// e2e for an entry of "My services" whose parts were given back or withdrawn
// (api/web/shop.js progressOf, entryParts, entryOrders), against a fixture built
// after the demo catalogue's Notebook Convertible.
//
// What was reported: the entry said "being set up: 4 of 7 ready" although nothing
// was on its way — three of its parts had been given back or never delivered. And
// the opened entry was a jumble of orders. Now an entry with parts missing and
// nothing coming is "incomplete"; opened, it names the product, lists its parts with
// where each stands, its events, and the orders behind it with the settled older
// ones behind a button.
import { test, expect } from "@playwright/test";

function installShop() {
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" } };
  const ids = ["nb", "nb-dock", "nb-hw", "nb-maus", "nb-netz", "nb-pen", "nb-tast", "nb-finish"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("nb", "Notebook Convertible", "Notebook Convertible", Object.assign({ icon: "device-laptop" }, work)),
      item("nb-dock", "Dockingstation", "Docking station"),
      item("nb-hw", "Notebook (Gerät)", "Notebook (device)"),
      item("nb-maus", "Maus", "Mouse"),
      item("nb-netz", "Netzteil", "Power supply"),
      item("nb-pen", "Active Pen", "Active pen"),
      item("nb-tast", "Tastatur", "Keyboard"),
      item("nb-finish", "Userfinish", "User finish"),
    ],
    includes: { nb: ["nb-dock", "nb-hw", "nb-maus", "nb-netz"] },
    options: { nb: ["nb-pen", "nb-tast", "nb-finish"] },
    waves: [ids], withoutApproval: ids,
  };
  const catalogue = { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" },
    items: ids, theme: { layout: "tiles" } };
  const line = (itemId, status) => ({ itemId, status, approval: { kind: "none" } });
  const ORDERS = [
    // Newest: the notebook and its keyboard delivered; the rest was already there.
    { id: "ord_new", recipient: "usr_1", orderer: "usr_1", createdAt: 3e18, releaseId: "rel_s",
      lines: [line("nb", "done"), line("nb-pen", "skipped"), line("nb-dock", "skipped"),
        line("nb-maus", "skipped"), line("nb-netz", "skipped"), line("nb-tast", "done")] },
    { id: "ord_pen", recipient: "usr_1", orderer: "usr_1", createdAt: 2e18, releaseId: "rel_s",
      lines: [line("nb-pen", "done")] },
    // Oldest: withdrawn, most of it given back since.
    { id: "ord_old", recipient: "usr_1", orderer: "usr_1", createdAt: 1e18, releaseId: "rel_s",
      lines: [line("nb", "cancelled"), line("nb-pen", "returned"), line("nb-dock", "returned"),
        line("nb-maus", "done"), line("nb-netz", "returned"), line("nb-tast", "returned")] },
  ];
  const held = ["nb", "nb-maus", "nb-pen", "nb-tast"];
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": catalogue,
    "/api/v1/shop/catalogs": [catalogue],
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": ORDERS,
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: held.map((itemId) => ({ itemId, since: 3e18, origin: "ordered" })) },
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

test("parts given back make an entry incomplete, not being set up", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installShop);
  await page.goto("/shop.html#/services");
  const row = page.locator(".svc-entry", { hasText: "Notebook Convertible" });
  // The notebook, the mouse, the pen and the keyboard are there; the device, the
  // dock and the power supply are not, and nothing is on its way: 4 of 7.
  await expect(row.locator(".svc-state")).toHaveText("Incomplete: 4 of 7 held");
  await expect(row.locator(".pie-icon")).toHaveAttribute("data-share", "57");
  await expect(row).not.toContainText("Being set up");
  // The status filter finds it under its own word.
  await page.getByLabel("Status").selectOption("incomplete");
  await expect(page.locator(".svc-entry")).toHaveCount(1);
  await page.getByLabel("Status").selectOption("building");
  await expect(page.locator(".svc-entry")).toHaveCount(0);
  await page.getByLabel("Status").selectOption("");

  await row.locator("button.svc-open").click();
  const detail = row.locator(".svc-detail");
  // The product's name first, with how it stands.
  await expect(detail.locator(".svc-detail-head h3")).toHaveText("Notebook Convertible");
  await expect(detail.locator(".svc-detail-head")).toContainText("Incomplete: 4 of 7 held");
  // Every part with where it stands, and only the missing ones offered.
  const state = (id) => detail.locator(`.svc-part[data-part="${id}"] .svc-part-state`);
  await expect(state("nb")).toHaveText("Held");
  await expect(state("nb-maus")).toHaveText("Held");
  await expect(state("nb-pen")).toHaveText("Held");
  await expect(state("nb-tast")).toHaveText("Held");
  await expect(state("nb-dock")).toHaveText("No longer held");
  await expect(state("nb-netz")).toHaveText("No longer held");
  await expect(state("nb-hw")).toHaveText("Not held");
  await expect(state("nb-finish")).toHaveText("Not chosen");
  await expect(detail.locator(".svc-part.offerable .svc-part-name"))
    .toHaveText(["Docking station", "Notebook (device)", "Power supply", "User finish"]);
  // "Already held" when ordered, and said that it is no longer.
  await expect(detail.locator(".lines > li", { hasText: "Docking station" })).toContainText("no longer held now");
  await expect(detail.locator(".lines > li", { hasText: "Mouse" })).not.toContainText("no longer held now");
  // The newest order is shown; the settled older ones wait behind a button.
  await expect(detail.locator(".svc-order-head")).toHaveCount(1);
  await expect(detail.locator(".svc-order-head")).toContainText("ord_new");
  await detail.getByRole("button", { name: "Show earlier orders (2)" }).click();
  await expect(detail.locator(".svc-order-head")).toHaveCount(3);
  expect(errors).toEqual([]);
});
