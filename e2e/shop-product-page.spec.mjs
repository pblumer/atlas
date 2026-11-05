// e2e for a product page of the tile layout (api/web/shop.js renderProductPage,
// renderActions).
//
// What was asked for: the page carried a button "Put in the basket" at its top
// right, beside the row along the bottom that every shop screen ends in. It is
// gone; the row's "Add to basket" now puts the product in and opens the basket,
// and the row's "Back" leads to the tiles rather than out of the shop. The page is
// two columns: the description open in the wide one, the folding facts — who it is
// for, what comes with it, approval, price — in the narrow one beside it.
import { test, expect } from "@playwright/test";

function installShop() {
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_s", state: "active", texts: { de, en },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const work = { category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" },
    productGroup: "Telefonie", productGroupTexts: { de: "Telefonie", en: "Telephony" } };
  const ids = ["android", "android-hw", "iphone"];
  const RELEASE = {
    id: "rel_s", catalogId: "cat_s",
    items: [
      item("android", "Android Smartphone", "Android smartphone",
        Object.assign({ descriptions: { de: "Diensttelefon mit Android.", en: "Company phone running Android." } }, work)),
      item("android-hw", "Hardware Android", "Android hardware",
        { descriptions: { de: "Das Gerät selbst.", en: "The device itself." } }),
      item("iphone", "Apple iPhone", "Apple iPhone", work),
    ],
    includes: { android: ["android-hw"] },
    waves: [ids], withoutApproval: ids,
  };
  const catalogue = { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" },
    items: ids, theme: { layout: "tiles" } };
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": catalogue,
    "/api/v1/shop/catalogs": [catalogue],
    "/api/v1/catalogs/cat_s/releases": [RELEASE],
    "/api/v1/orders": [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
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

async function openProduct(page, name) {
  await page.setViewportSize({ width: 1280, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installShop);
  await page.goto("/shop.html");
  await page.locator("button.tile", { hasText: name }).click();
  await expect(page.locator(".product-page h2")).toHaveText(name);
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("a product page has no basket button of its own; the bottom row puts the product in", async ({ page }) => {
  await openProduct(page, "Android smartphone");
  await expect(page.locator(".product-page button", { hasText: "Put in the basket" })).toHaveCount(0);
  await expect(page.locator(".product-page button", { hasText: "Go to the basket" })).toHaveCount(0);

  // The basket is empty, and the row's button is offered all the same: it is
  // what puts this product in.
  const toBasket = page.locator(".actions button.primary");
  await expect(toBasket).toHaveText("Add to basket >");
  await expect(toBasket).toBeEnabled();
  await toBasket.click();

  // On in the basket, with the product in it.
  await expect(page.locator(".product-page")).toHaveCount(0);
  await expect(page.locator(".actions button.primary")).toHaveText(/Place|Order|order/);
  await expect(page.getByText("Android smartphone").first()).toBeVisible();
});

test("the bottom row's back leads from a product page to the tiles", async ({ page }) => {
  await openProduct(page, "Apple iPhone");
  await page.locator(".actions button.secondary", { hasText: "Back" }).first().click();
  await expect(page.locator(".product-page")).toHaveCount(0);
  await expect(page.locator(".tiles")).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/shop.html");
});

test("the description fills the wide column and the folds sit in the narrow one", async ({ page }) => {
  await openProduct(page, "Android smartphone");
  const main = page.locator(".product-page .page-main");
  const side = page.locator(".product-page .page-side");
  await expect(main).toContainText("Company phone running Android.");
  await expect(main.locator("details")).toHaveCount(0);
  await expect(side.locator("details.page-section")).toHaveCount(4);
  // What comes with it is open; it is what a reader compares.
  await expect(side.locator("details[open] summary")).toHaveText(["Scope of service"]);
  await expect(side).toContainText("Android hardware");
  const [m, s] = await Promise.all([main.boundingBox(), side.boundingBox()]);
  expect(s.x).toBeGreaterThan(m.x + m.width - 1);
  expect(s.width).toBeLessThan(m.width);

  // A narrow screen stacks them.
  await page.setViewportSize({ width: 600, height: 900 });
  const [m2, s2] = await Promise.all([main.boundingBox(), side.boundingBox()]);
  expect(s2.y).toBeGreaterThan(m2.y + m2.height - 1);
});

test("the bottom row stays in reach while a long page scrolls", async ({ page }) => {
  await openProduct(page, "Android smartphone");
  await page.setViewportSize({ width: 1280, height: 420 });
  await page.evaluate(() => window.scrollTo(0, 0));
  const row = page.locator(".actions");
  const inView = async () => {
    const box = await row.boundingBox();
    return box.y >= 0 && box.y + box.height <= 420 + 1;
  };
  // The page is taller than the window, so the row would be below it.
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeGreaterThan(420);
  expect(await inView()).toBe(true);
  await page.mouse.wheel(0, 150);
  expect(await inView()).toBe(true);
  // And it still answers there.
  await row.locator("button.primary").click();
  await expect(page.locator(".product-page")).toHaveCount(0);
});

test("a part in the scope opens its own description in a panel", async ({ page }) => {
  await openProduct(page, "Android smartphone");
  await page.locator(".product-page .page-side button.page-part", { hasText: "Android hardware" }).click();
  const dialog = page.getByRole("dialog", { name: "Android hardware" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("The device itself.");
  // Closing it leaves the reader on the product page.
  await dialog.locator(".info-close").click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator(".product-page h2")).toHaveText("Android smartphone");
});

test("the way back to the catalogue is a button, not a link in the corner", async ({ page }) => {
  await openProduct(page, "Android smartphone");
  const back = page.locator(".product-page .page-top button.page-back");
  await expect(back).toHaveText("‹ Back to the catalogue");
  // Drawn as a button: a border and room around the words, not underlined text.
  const style = await back.evaluate((e) => {
    const s = getComputedStyle(e);
    return { border: s.borderTopStyle, padding: parseFloat(s.paddingLeft) };
  });
  expect(style.border).toBe("solid");
  expect(style.padding).toBeGreaterThanOrEqual(12);
  await back.click();
  await expect(page.locator(".product-page")).toHaveCount(0);
  await expect(page.locator("button.tile", { hasText: "Android smartphone" })).toBeVisible();
});
