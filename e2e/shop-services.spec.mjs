// e2e for "My services" in the shop (api/web/shop.js renderServices).
//
// What was asked for: the screen read like the catalogue. It drew four
// independent lists — every heading, every group, every product and every service
// somebody holds — so a service sat beside whichever product happened to share its
// line and a heading beside a product filed under another. Now it is the
// catalogue's cascade over what this person holds: choosing a heading narrows the
// groups and products, and choosing a product shows what is behind it.
import { test, expect } from "@playwright/test";

function installHeld() {
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
  const held = ["iphone", "iphone-hw", "huelle", "intern", "u-account", "funktion"];
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": { id: "cat_s", rank: 1, languages: ["de", "en"],
      texts: { de: "Katalog", en: "Catalogue" }, items: ids },
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: held.map((itemId) => ({ itemId, since: 1, origin: "ordered" })) },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
  };
  window.fetch = (input) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) {
      return Promise.resolve(new Response(JSON.stringify(ROUTES[path]), {
        status: 200, headers: { "content-type": "application/json" },
      }));
    }
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

// column is the names one of the four columns shows, top to bottom.
const column = (page, index) => page
  .locator(`.cascade > .col:nth-child(${index}) .cell .label`).allTextContents();

async function openServices(page) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installHeld);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await page.waitForSelector(".cascade", { timeout: 10000 });
}

test.afterEach(async ({ page }) => {
  expect(page._errors || [], "no uncaught page errors").toEqual([]);
});

test("my services are a cascade, read like the catalogue", async ({ page }) => {
  await openServices(page);

  // Nothing chosen: every heading and product held, and no services — a service
  // is shown only beside the product it belongs to.
  expect(await column(page, 1)).toEqual(["All", "Identity", "Workplace"]);
  expect(await column(page, 3)).toEqual(["Apple iPhone", "Functional account (F)", "Internal user account"]);
  expect(await column(page, 4)).toEqual([]);

  // A heading narrows the groups and the products to what is filed under it.
  await page.getByRole("button", { name: "Identity", exact: true }).click();
  expect(await column(page, 2)).toEqual(["All groups", "Non-personal account", "Personal account"]);
  expect(await column(page, 3)).toEqual(["Functional account (F)", "Internal user account"]);

  await page.getByRole("button", { name: "Personal account", exact: true }).click();
  expect(await column(page, 3)).toEqual(["Internal user account"]);

  // A product shows what is behind it, and only that.
  await page.getByRole("button", { name: "Internal user account", exact: true }).click();
  expect(await column(page, 4)).toEqual(["Internal account (U)"]);

  // The case hangs under the phone this person holds, not the one the release
  // happens to list first.
  await page.getByRole("button", { name: "All", exact: true }).click();
  await page.getByRole("button", { name: "Apple iPhone", exact: true }).click();
  expect(await column(page, 4)).toEqual(["Apple iPhone hardware", "Smartphone case"]);
  expect(await page.evaluate(() => window.__unmatched)).toEqual([]);
});
