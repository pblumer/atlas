// e2e for the shop's icon buttons (api/web/shop.js iconButton, shop.html .sq).
//
// What was asked for: the star that marks a favourite becomes a heart, the "+" that
// puts something in the basket a shopping cart, and the "i" that opens a product's
// information the drawn info sign — each in the shop's main colour. They are inline
// SVG drawn with currentColor, so the colour is the shop's accent, and a catalogue
// theme that sets another accent recolours them with nothing else to change.
import { test, expect } from "@playwright/test";

function installShop() {
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


async function open(page, after) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installShop);
  if (after) await page.addInitScript(after);
  await page.goto("/shop.html");
  await page.waitForSelector(".cascade", { timeout: 10000 });
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const product = (page, name) => page.locator(".cascade > .col:nth-child(3) .cell", { hasText: name });

test("favourite, basket and information are a heart, a cart and an info sign", async ({ page }) => {
  await open(page);
  const row = product(page, "Android Smartphone");
  const heart = row.locator('button.sq[data-icon="heart"]');
  const cart = row.locator('button.sq[data-icon="cart"]');
  const info = row.locator('button.sq[data-icon="info"]');
  await expect(heart.locator("svg")).toHaveCount(1);
  await expect(cart.locator("svg")).toHaveCount(1);
  await expect(info.locator("svg")).toHaveCount(1);
  // No glyph is left beside the drawing.
  for (const b of [heart, cart, info]) await expect(b).toHaveText("");
  // An icon says nothing to a screen reader, so every button is named.
  await expect(heart).toHaveAttribute("aria-label", "Mark as favourite");
  await expect(cart).toHaveAttribute("aria-label", "Put in the basket");
  await expect(info).toHaveAttribute("aria-label", /.+/);
});

test("a favourite's heart is filled, and a cart adds to the basket", async ({ page }) => {
  // The server answers a mark with the list it holds; this fixture has no route for
  // it, so the mark is answered here, wrapped around the fixture's fetch.
  await open(page, () => {
    const inner = window.fetch;
    window.fetch = (input, init) => String(input).includes("/api/v1/shop/favourites/")
      ? Promise.resolve(new Response(JSON.stringify({ itemIds: ["android"] }), {
        status: 200, headers: { "content-type": "application/json" } }))
      : inner(input, init);
  });
  const row = product(page, "Android Smartphone");
  const fill = () => row.locator('button.sq[data-icon="heart"] svg path').evaluate((p) => getComputedStyle(p).fill);
  expect(await fill()).toBe("none");
  await row.locator('button.sq[data-icon="heart"]').click();
  await expect(row.locator('button.sq[data-icon="heart"]')).toHaveAttribute("aria-pressed", "true");
  expect(await fill()).not.toBe("none");

  await row.locator('button.sq[data-icon="cart"]').click();
  await expect(page.getByRole("button", { name: /Add to basket.*\(\d\)/ })).toBeVisible();
});

test("the icons take the shop's main colour, and follow a catalogue's accent", async ({ page }) => {
  await open(page);
  const row = product(page, "Android Smartphone");
  const colours = () => row.locator("button.sq[data-icon]").evaluateAll((bs) =>
    bs.map((b) => getComputedStyle(b.querySelector("svg")).stroke));
  const accent = () => page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.color = "var(--accent)";
    document.body.append(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  });
  expect(new Set(await colours())).toEqual(new Set([await accent()]));
  await page.evaluate(() => document.documentElement.style.setProperty("--accent", "rgb(200, 0, 100)"));
  expect(new Set(await colours())).toEqual(new Set(["rgb(200, 0, 100)"]));
});
