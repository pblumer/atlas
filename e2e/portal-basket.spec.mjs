// e2e for the portal's basket layout (api/web/shop.js, against a fixture).
//
// What was reported: with two offerings in the basket, nothing said which service
// and which option belonged to which of them. The basket drew three columns, each a
// flat list of its own, so a row's height in one column had nothing to do with its
// height in the next. A service landed beside whichever offering happened to share
// its line, and the reader had to work out the relation the page never drew.
//
// The property under test is geometric — "on the same line as" — so it is read off
// the rendered boxes rather than the markup. A test that looked for a wrapping
// element would pass against a page that wrapped and still misaligned.
//
// And so it loads the REAL shop.html, with only the network replaced. The grid
// that makes a line is declared in that page's own stylesheet, not in app.css: a
// harness page that borrowed shop.js and not shop.html drew every cell at the
// left edge, one under the other, and no alignment could be measured in it at all.
import { test, expect } from "@playwright/test";

// The fixture is the shape the basket was reported against: two offerings, each
// with services behind it, one with optional parts. The monitor carries TWO
// services on purpose — with one, the laptop's first service could land on the
// laptop's line in a layout that never related them, and pass for the wrong reason.
function installFixture() {
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

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture);
  await page.goto("/shop.html");
  await page.waitForSelector(".cascade", { timeout: 10000 });
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
  const missed = await page.evaluate(() => window.__unmatched);
  expect(missed, "every route the page reads is served by the fixture").toEqual([]);
});

// cell is the one row naming this product. Exact text, because "Monitor 24-inch"
// and "Monitor hardware" share a word.
const cell = (page, name) => page.locator(".cell").filter({
  has: page.getByText(name, { exact: true }),
});

// top is where a row starts on the page.
const top = async (page, name) => {
  const box = await cell(page, name).first().boundingBox();
  expect(box, `${name} is drawn`).not.toBeNull();
  return box.y;
};

// fill puts both offerings into the basket through the page's own controls and
// opens it.
const fill = async (page) => {
  for (const name of ["Monitor 24-inch", "Apple MacBook Pro"]) {
    await cell(page, name).first().locator("button.sq", { hasText: "+" }).click();
  }
  await page.getByRole("button", { name: /Add to basket/ }).click();
  await expect(cell(page, "MacBook hardware")).toHaveCount(1);
};

// Within a pixel or two: rows share a line when their tops agree, and sub-pixel
// layout differs between engines.
const sameLine = (a, b) => Math.abs(a - b) < 3;

test("a service starts on the line of the offering it belongs to", async ({ page }) => {
  await fill(page);
  const monitor = await top(page, "Monitor 24-inch");
  const laptop = await top(page, "Apple MacBook Pro");

  expect(sameLine(await top(page, "Monitor hardware"), monitor),
    "the monitor's first service is beside the monitor").toBe(true);
  expect(sameLine(await top(page, "MacBook hardware"), laptop),
    "the laptop's first service is beside the laptop, not beside whatever the " +
    "monitor's second service pushed it to").toBe(true);
});

test("an option starts on the line of the offering that offers it", async ({ page }) => {
  await fill(page);
  const laptop = await top(page, "Apple MacBook Pro");
  expect(sameLine(await top(page, "Laptop sleeve"), laptop),
    "the laptop's first option is beside the laptop, not on the monitor's line")
    .toBe(true);
});

test("one offering's group ends before the next begins", async ({ page }) => {
  await fill(page);
  // The monitor's second service is the tallest thing in its group. The laptop's
  // group may not start above it, or the two groups overlap and the line that
  // tells them apart is gone.
  const cable = await cell(page, "Connection cable").first().boundingBox();
  const laptop = await top(page, "Apple MacBook Pro");
  expect(laptop, "the laptop's line starts below the monitor's last service")
    .toBeGreaterThanOrEqual(cable.y + cable.height - 1);
});

test("the columns are still named once, at the top", async ({ page }) => {
  await fill(page);
  // Grouping by offering must not turn into a header per group: the three names
  // are the grid's, and repeating them per group would be a table of tables.
  await expect(page.locator(".colhead", { hasText: "Offering" })).toHaveCount(1);
  await expect(page.locator(".colhead", { hasText: "Service" })).toHaveCount(1);
  await expect(page.locator(".colhead", { hasText: "Optional" })).toHaveCount(1);
});
