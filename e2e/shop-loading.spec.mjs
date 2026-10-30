// e2e for the shop while it loads (api/web/shop.js load, renderLoading).
//
// What was asked for: while the shop loads — which can take a few seconds — it said
// "No catalogue is assigned to you", because the first paint happens before the
// catalogue is known and an unknown catalogue read as none. It now says it is
// loading, and says that none is assigned only once the server has answered so
// (404). A failure to read the catalogue is a failure, with the way to try again,
// and not "none assigned" either.
import { test, expect } from "@playwright/test";

function installSlowShop(catalogStatus) {
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
  // The catalogue answers only once the test lets it, with the status it names:
  // 200 for a catalogue, 404 for "none is assigned to you", 500 for a failure.
  let release;
  window.__answerCatalogue = new Promise((r) => { release = r; });
  window.__letCatalogueAnswer = release;
  window.fetch = (input) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    if (path === "/api/v1/shop/catalog") {
      return window.__answerCatalogue.then(() => catalogStatus === 200
        ? new Response(JSON.stringify(ROUTES[path]), { status: 200, headers: { "content-type": "application/json" } })
        : new Response(catalogStatus === 404 ? "no catalogue is assigned to you" : "resolve catalogue: disk", { status: catalogStatus }));
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


async function open(page, status) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installSlowShop, status);
  await page.goto("/shop.html");
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const NONE = "No catalogue is assigned to you.";

test("while the catalogue loads, the shop says it is loading — not that none is assigned", async ({ page }) => {
  await open(page, 200);
  await expect(page.getByText("Loading the shop …")).toBeVisible();
  await expect(page.getByText(NONE)).toHaveCount(0);
  // Switching to another screen while it loads says the same, not "you hold nothing".
  await page.getByRole("button", { name: "My services", exact: true }).click();
  await expect(page.getByText("Loading the shop …")).toBeVisible();
  await expect(page.getByText("You currently hold no services.")).toHaveCount(0);
  await page.getByRole("button", { name: "Catalogue", exact: true }).click();

  await page.evaluate(() => window.__letCatalogueAnswer());
  await expect(page.locator(".cascade .cell", { hasText: "Apple iPhone" }).first()).toBeVisible();
  await expect(page.getByText("Loading the shop …")).toHaveCount(0);
  await expect(page.getByText(NONE)).toHaveCount(0);
});

test("only the server's answer that none is assigned says so", async ({ page }) => {
  await open(page, 404);
  await expect(page.getByText("Loading the shop …")).toBeVisible();
  await page.evaluate(() => window.__letCatalogueAnswer());
  await expect(page.getByText(NONE)).toBeVisible();
  await expect(page.getByText("Loading the shop …")).toHaveCount(0);
});

test("a catalogue that cannot be read is a failure with a retry, not none assigned", async ({ page }) => {
  await open(page, 500);
  await page.evaluate(() => window.__letCatalogueAnswer());
  await expect(page.locator("p.error")).toContainText("That did not work.");
  await expect(page.locator("p.error").getByRole("button")).toBeVisible();
  await expect(page.getByText(NONE)).toHaveCount(0);
  await expect(page.getByText("Loading the shop …")).toHaveCount(0);
});
