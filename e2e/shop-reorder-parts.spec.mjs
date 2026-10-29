// e2e for ordering again what a held bundle is missing (api/web/shop.js toggle,
// renderServices; ADR-0312, amended 2026-10-28).
//
// What was asked for: a position of a bundle that was not chosen can be added
// later, although the catalogue offers it only beneath the bundle and not as a
// Marktleistung of its own; and a position given back out of a bundle can be
// ordered again. The server took both all along (api/order/reorder_test.go); the
// shop offered neither. A part that is always included showed a disabled "−" even
// after it had been given back, and "My services" listed only what is held.
import { test, expect } from "@playwright/test";

function installHeldWithGaps() {
  window.__unmatched = [];
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" },
    productGroup: "Telefonie", productGroupTexts: { de: "Telefonie", en: "Telephony" } };
  const identity = { category: "Identitaet", categoryTexts: { de: "Identität", en: "Identity" } };
  const ids = ["iphone", "iphone-hw", "android", "android-hw", "huelle", "intern", "u-account", "funktion"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("iphone", "Apple iPhone", "Apple iPhone", work),
      item("iphone-hw", "Hardware Apple iPhone", "Apple iPhone hardware"),
      item("android", "Android Smartphone", "Android smartphone", work),
      item("android-hw", "Hardware Android", "Android hardware"),
      item("huelle", "Smartphone-Hülle", "Smartphone case"),
      item("intern", "Benutzeraccount intern", "Internal user account",
        Object.assign({ productGroup: "persoenlich", productGroupTexts: { de: "Konto persönlich", en: "Personal account" } }, identity)),
      item("u-account", "Interner Account (U)", "Internal account (U)"),
      item("funktion", "Funktionsaccount (F)", "Functional account (F)",
        Object.assign({ productGroup: "unpersoenlich", productGroupTexts: { de: "Konto unpersönlich", en: "Non-personal account" } }, identity)),
    ],
    includes: { iphone: ["iphone-hw"], android: ["android-hw"], intern: ["u-account"] },
    // The case is offered by both phones, and the release lists the one this
    // person does not hold first.
    options: { android: ["huelle"], iphone: ["huelle"] },
    waves: [ids], withoutApproval: ids,
  };
  // The iPhone is held, but its hardware was given back and its case never taken;
  // the internal account is held whole.
  const held = ["iphone", "intern", "u-account", "funktion"];
  window.__posted = [];
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": { id: "cat_s", rank: 1, languages: ["de", "en"],
      texts: { de: "Katalog", en: "Catalogue" }, items: ids },
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": window.__openOrders || [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: held.map((itemId) => ({ itemId, since: 1, origin: "ordered" })) },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
  };
  window.fetch = (input, init) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    if (path === "/api/v1/orders" && init && init.method === "POST") {
      window.__posted.push(JSON.parse(init.body));
      return Promise.resolve(new Response(JSON.stringify({ id: "ord_new", lines: [] }), {
        status: 201, headers: { "content-type": "application/json" },
      }));
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) {
      return Promise.resolve(new Response(JSON.stringify(ROUTES[path]), {
        status: 200, headers: { "content-type": "application/json" },
      }));
    }
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}


async function open(page, view, openOrders) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  if (openOrders) await page.addInitScript((o) => { window.__openOrders = o; }, openOrders);
  await page.addInitScript(installHeldWithGaps);
  await page.goto("/shop.html");
  if (view) await page.getByRole("button", { name: view, exact: true }).click();
  await page.waitForSelector(".cascade", { timeout: 10000 });
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// serviceRow is the row of one service in the fourth column.
const serviceRow = (page, name) => page.locator(".cascade > .col:nth-child(4) .cell", { hasText: name });

test("a part given back out of a held bundle can be ordered again from the catalogue", async ({ page }) => {
  await open(page);
  await page.locator(".cascade .cell", { hasText: "Apple iPhone" }).first().click();
  const hw = serviceRow(page, "Apple iPhone hardware").locator("button.sq").first();
  await expect(hw).toBeEnabled();
  await expect(hw).toHaveAttribute("title", "No longer held — order it again on its own.");
  await hw.click();
  await page.getByRole("button", { name: /Add to basket/ }).click();
  await page.getByRole("button", { name: /order >/ }).click();
  await expect.poll(() => page.evaluate(() => window.__posted.length)).toBe(1);
  const body = await page.evaluate(() => window.__posted[0]);
  expect(body.items).toEqual(["iphone-hw"]);
});

test("a part of a bundle that is held whole, or that a chosen bundle carries, stays included", async ({ page }) => {
  await open(page);
  // The internal account is held with its part: nothing is missing, nothing offered.
  await page.locator(".cascade .cell", { hasText: "Internal user account" }).first().click();
  const kept = serviceRow(page, "Internal account (U)").locator("button.sq").first();
  await expect(kept).toBeDisabled();
  await expect(kept).toHaveAttribute("title", "Always included — cannot be deselected.");
  // Ordering the iPhone again brings its hardware with it, so the part is not a
  // second, separate choice.
  await page.locator(".cascade .cell", { hasText: "Apple iPhone" }).first().click();
  await page.locator(".cascade > .col:nth-child(3) .cell", { hasText: "Apple iPhone" })
    .locator('button.sq[data-icon="cart"]').click();
  const hw = serviceRow(page, "Apple iPhone hardware").locator("button.sq").first();
  await expect(hw).toBeDisabled();
  await expect(hw).toHaveAttribute("title", "Always included — cannot be deselected.");
});

test("my services offers what a held bundle is missing, and the basket takes it", async ({ page }) => {
  await open(page, "My services");
  await page.locator(".cascade .cell", { hasText: "Apple iPhone" }).first().click();
  const col = page.locator(".cascade > .col:nth-child(4)");
  await expect(col.locator(".colhead")).toHaveText(["Service", "Not held"]);
  await expect(col.locator(".cell .label")).toHaveText(["Apple iPhone hardware", "Smartphone case"]);
  // Both are offers: the hardware because it was given back, the case because it
  // was never taken.
  await serviceRow(page, "Apple iPhone hardware").locator("button.sq").first().click();
  await serviceRow(page, "Smartphone case").locator("button.sq").first().click();
  await expect(serviceRow(page, "Smartphone case").locator("button.sq").first()).toHaveAttribute("aria-pressed", "true");
  // The way on to the basket is on this screen, not only in the catalogue.
  await page.getByRole("button", { name: /Add to basket.*\(2\)/ }).click();
  await page.getByRole("button", { name: /order >/ }).click();
  await expect.poll(() => page.evaluate(() => window.__posted.length)).toBe(1);
  const body = await page.evaluate(() => window.__posted[0]);
  expect([...body.items].sort()).toEqual(["huelle", "iphone-hw"]);
});

test("a bundle held whole offers nothing extra in my services", async ({ page }) => {
  await open(page, "My services");
  await page.locator(".cascade .cell", { hasText: "Internal user account" }).first().click();
  const col = page.locator(".cascade > .col:nth-child(4)");
  await expect(col.locator(".colhead")).toHaveText(["Service"]);
  await expect(col.locator(".cell .label")).toHaveText(["Internal account (U)"]);
});

// Ordered again and not yet provisioned is not held, but it is coming: offering it a
// second time would place a second order, because the server does not compare
// orders with each other.
test("a part already ordered again is not offered a second time", async ({ page }) => {
  const pending = [{ id: "ord_1", recipient: "usr_1", status: "running",
    lines: [{ itemId: "iphone-hw", variantId: "", status: "pending" }] }];
  await open(page, null, pending);
  await page.locator(".cascade .cell", { hasText: "Apple iPhone" }).first().click();
  const hw = serviceRow(page, "Apple iPhone hardware").locator("button.sq").first();
  await expect(hw).toBeDisabled();
  // "My services" opens on the product the catalogue stands at.
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await expect(page.locator(".cascade > .col:nth-child(4) .cell .label")).toHaveText(["Smartphone case"]);
});
