// e2e for the shop on a narrow screen (api/web/shop.html, ADR-draft-the-shop-is-one-column-wide-on-a-narrow-screen), against the
// same fixtures the basket and the task specs use.
//
// What was asked for: the shop usable on a phone. At 390px the four-column
// cascade showed one and a half columns, the basket's services were cut off, and
// an order's positions, tasks and the form to answer one sat in a table column
// off the right edge of the screen.
//
// So below one breakpoint every view is one column wide: the catalogue steps
// through its columns with a way back, the basket stacks each offering over its
// services and options, and the orders table becomes a list of cards. What these
// tests hold is geometric — nothing wider than the screen, one column showing,
// a service under its offering — so it is read off the rendered boxes.
//
// A wide screen keeps its layout; the specs that measure it run at 900px and are
// unchanged. The last test here says so at 1280px as well.
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const FORM = JSON.parse(readFileSync(
  new URL("../api/systemprocesses/form-genehmigung.json", import.meta.url), "utf8"));

function installCatalogue() {
  window.__unmatched = [];
  const item = (id, de, en, extra) => Object.assign({
    id, homeCatalog: "cat_b", state: "active", texts: { de, en },
    category: "Arbeitsplatz", categoryTexts: { de: "Arbeitsplatz", en: "Workplace" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  }, extra || {});
  const ids = ["monitor", "monitor-hw", "monitor-kabel", "laptop", "laptop-hw",
    "laptop-os", "huelle", "tastatur"];
  const CAT = {
    id: "cat_b", rank: 1, languages: ["de", "en"],
    texts: { de: "Warenkorbkatalog", en: "Basket catalogue" }, items: ids,
  };
  const RELEASE = {
    id: "rel_b", catalogId: "cat_b",
    items: [
      item("monitor", "Monitor 24 Zoll", "Monitor 24-inch"),
      item("monitor-hw", "Hardware Monitor", "Monitor hardware"),
      item("monitor-kabel", "Anschlusskabel", "Connection cable"),
      item("laptop", "Apple MacBook Pro", "Apple MacBook Pro"),
      item("laptop-hw", "Hardware MacBook", "MacBook hardware"),
      item("laptop-os", "Betriebssystem macOS", "Operating system macOS"),
      item("huelle", "Laptop Hülle", "Laptop sleeve", { price: "CHF 45.-" }),
      item("tastatur", "Tastatur extern", "External keyboard"),
    ],
    includes: { monitor: ["monitor-hw", "monitor-kabel"], laptop: ["laptop-hw", "laptop-os"] },
    options: { laptop: ["huelle", "tastatur"] },
    waves: [ids],
    withoutApproval: ids,
  };
  const ROUTES = {
    // A user with an id, because ordering needs somebody to be: without one the
    // page draws every "+" disabled and nothing reaches the basket.
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_b/releases": [RELEASE],
    "/api/v1/orders": [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
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
    // Recorded rather than swallowed: a route the page reads and this fixture
    // does not serve would otherwise let a test pass against a page that never
    // loaded its catalogue.
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

function installOrders(form) {
  window.__unmatched = [];
  window.__completed = [];
  const line = (itemId, status) => ({ itemId, status });
  const OWN = {
    id: "ord_own", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
    createdAt: Date.UTC(2026, 8, 24, 10) * 1e6,
    lines: [line("vpn", "pending"), line("laptop", "done")],
  };
  const HELD = {
    id: "ord_held", orderer: "usr_2", recipient: "usr_2", releaseId: "rel_t",
    createdAt: Date.UTC(2026, 8, 24, 9) * 1e6,
    lines: [line("vpn", "pending")],
  };
  const task = (key, orderId, holder, mayWork) => ({
    key, orderId, positionId: "vpn", processInstanceKey: key + 1000, elementInstanceKey: key + 2000,
    processId: "atlas-genehmigung-fix", name: "Genehmigen", formId: "genehmigung",
    approval: true, holder, mayWork,
  });
  const TASKS = {
    tasks: [
      task(11, "ord_own", { kind: "fixed", name: "Bob Muster" }, false),
      task(22, "ord_held", { kind: "superior", name: "Anja" }, true),
    ],
    orders: [HELD],
    truncated: false,
  };
  const CAT = { id: "cat_t", rank: 1, languages: ["de", "en"],
    texts: { de: "Katalog", en: "Catalogue" }, items: [] };
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_t/releases": [],
    "/api/v1/orders": [OWN],
    "/api/v1/shop/tasks": TASKS,
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }, { id: "usr_2", name: "Carla" }],
    "/api/v1/forms/genehmigung": { id: "genehmigung", schema: form },
    "/api/v1/instances/2022/variables": {},
  };
  const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {
    status: 200, headers: { "content-type": "application/json" },
  }));
  const realFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const path = url.pathname;
    // The form runtime and its stylesheets are the page's own static files.
    if (!path.startsWith("/api/")) return realFetch(input, init);
    const done = path.match(/^\/api\/v1\/tasks\/(\d+)\/complete$/);
    if (done && init && init.method === "POST") {
      window.__completed.push({ key: Number(done[1]), body: JSON.parse(init.body) });
      TASKS.tasks = TASKS.tasks.filter((x) => x.key !== Number(done[1]));
      return json({ taskKey: Number(done[1]) });
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}


const PHONE = { width: 390, height: 844 };

// overflowing lists what ends past the right edge of the screen. The page itself
// may not scroll sideways, and neither may anything inside it: a table that
// scrolls in its own box hides its last column just as well.
const overflowing = (page) => page.evaluate(() => {
  const vw = document.documentElement.clientWidth;
  const out = [];
  for (const e of document.querySelectorAll("#app *")) {
    const b = e.getBoundingClientRect();
    if (b.width > 0 && b.right > vw + 1) out.push(`${e.tagName}.${e.className}`);
  }
  return out;
});

// shownColumns is the heads of the cascade's columns that are on screen.
const shownColumns = (page) => page.locator(".cascade[data-step] > .col > .colhead")
  .filter({ visible: true }).allTextContents();

const cell = (page, name) => page.locator(".cell").filter({
  has: page.getByText(name, { exact: true }),
});

async function openCatalogue(page, size) {
  await page.setViewportSize(size);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installCatalogue);
  await page.goto("/shop.html");
  await page.waitForSelector(".cascade", { timeout: 10000 });
}

test.afterEach(async ({ page }) => {
  expect(page._errors || [], "no uncaught page errors").toEqual([]);
});

test("the catalogue shows one column at a time on a phone, and steps back", async ({ page }) => {
  await openCatalogue(page, PHONE);
  expect(await shownColumns(page)).toEqual(["Category"]);
  await expect(page.locator(".stepper")).toBeVisible();

  await page.getByRole("button", { name: "Workplace", exact: true }).click();
  expect(await shownColumns(page)).toEqual(["Product group"]);
  await page.getByRole("button", { name: "All groups", exact: true }).click();
  expect(await shownColumns(page)).toEqual(["Offering"]);
  await page.getByRole("button", { name: "Apple MacBook Pro", exact: true }).click();
  expect(await shownColumns(page)).toEqual(["Service"]);
  // The path says where the column came from, in the reader's language.
  await expect(page.locator(".step-path")).toContainText("Workplace");
  await expect(page.locator(".step-path")).toContainText("Apple MacBook Pro");
  expect(await overflowing(page)).toEqual([]);

  await page.getByRole("button", { name: /Back/ }).click();
  expect(await shownColumns(page)).toEqual(["Offering"]);
});

test("the basket stacks each offering over its own services on a phone", async ({ page }) => {
  await openCatalogue(page, PHONE);
  await page.getByRole("button", { name: "Workplace", exact: true }).click();
  await page.getByRole("button", { name: "All groups", exact: true }).click();
  for (const name of ["Monitor 24-inch", "Apple MacBook Pro"]) {
    await cell(page, name).first().locator("button.sq", { hasText: "+" }).click();
  }
  await page.getByRole("button", { name: /Add to basket/ }).click();
  await expect(cell(page, "MacBook hardware")).toHaveCount(1);

  const top = async (name) => (await cell(page, name).first().boundingBox()).y;
  const monitor = await top("Monitor 24-inch");
  const cable = await top("Connection cable");
  const macbook = await top("Apple MacBook Pro");
  const hardware = await top("MacBook hardware");
  const sleeve = await top("Laptop sleeve");
  // Each offering's services below it and above the next offering.
  expect(cable).toBeGreaterThan(monitor);
  expect(cable).toBeLessThan(macbook);
  expect(hardware).toBeGreaterThan(macbook);
  expect(sleeve).toBeGreaterThan(hardware);
  // The column names no longer stand above the cells, so the cells say what they are.
  await expect(page.locator(".basket-groups .grp[data-label]").filter({ visible: true }).first())
    .toBeVisible();
  expect(await overflowing(page)).toEqual([]);
});

test("an order is a card on a phone, and its task is answered inside it", async ({ page }) => {
  await page.setViewportSize(PHONE);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.addInitScript(installOrders, FORM);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My orders" }).click();
  await expect(page.getByText("ord_own")).toBeVisible({ timeout: 10000 });

  // The column heads are gone and every cell names itself.
  await expect(page.locator(".table.orders thead tr:not(.filters)")).toBeHidden();
  expect(await overflowing(page)).toEqual([]);

  await page.locator('li[data-task="22"]').getByRole("button", { name: "Work on it" }).click();
  const panel = page.locator('li[data-task="22"] .cfg');
  await expect(panel.getByLabel("Genehmigen")).toBeVisible({ timeout: 10000 });
  // The checkbox is a checkbox: the table's field rule once stretched it across
  // the cell and pushed its label off the card.
  const box = await panel.getByLabel("Genehmigen").boundingBox();
  expect(box.width).toBeLessThan(40);
  const card = await page.locator("tr", { hasText: "ord_held" }).boundingBox();
  const label = await panel.locator("label", { hasText: "Genehmigen" }).boundingBox();
  expect(label.x + label.width).toBeLessThanOrEqual(card.x + card.width);
  expect(await overflowing(page)).toEqual([]);
});

test("a wide screen keeps every column side by side and no stepper", async ({ page }) => {
  await openCatalogue(page, { width: 1280, height: 800 });
  expect(await shownColumns(page)).toEqual(["Category", "Product group", "Offering", "Service"]);
  await expect(page.locator(".stepper")).toBeHidden();
  const heads = page.locator(".cascade[data-step] > .col > .colhead");
  const first = await heads.nth(0).boundingBox();
  const last = await heads.nth(3).boundingBox();
  expect(Math.abs(first.y - last.y)).toBeLessThan(3);
});
