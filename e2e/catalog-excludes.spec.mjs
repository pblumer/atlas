// Declaring an incompatibility in the Console (ADR-0342).
//
// The clerk who may create a supplier must not also approve payments to it.
// Neither right is wrong; the combination is, and an order that would produce it is
// refused rather than reported afterwards.
//
// The record has carried `excludes` since that decision, Publish freezes it into a
// release in BOTH directions, and the conflicts surface reads it. The Console knew
// three edge kinds of four: the incompatibility could not be declared there, and one
// that existed appeared in no table and could not be removed, because the only
// remove button is on a row that is drawn.
//
// What this drives is the half a Go guard cannot see: the symmetry. One fact is one
// row however it was stored, removing it takes both directions, and the mirror of
// one already recorded is refused rather than stored as a second fact.
import { test, expect } from "@playwright/test";

const CATALOG = {
  id: "cat_1", revision: 3, rank: 1, languages: ["de"], texts: { de: "Kreditoren" },
  groups: ["grp_all"], items: ["kreditor-erfassen", "zahlung-freigeben", "einsicht"],
  edges: [
    // Authored one way round. The release carries both; the screen must draw one row.
    { from: "kreditor-erfassen", to: "zahlung-freigeben", kind: "excludes" },
    // A precedence edge, so the sections cannot be told apart by luck.
    { from: "zahlung-freigeben", to: "einsicht", kind: "requires" },
  ],
};

const ITEMS = [
  { id: "kreditor-erfassen", homeCatalog: "cat_1", state: "active",
    texts: { de: "Kreditor erfassen" }, approval: { kind: "none" },
    provisionProcess: "p", deprovisionProcess: "d" },
  { id: "zahlung-freigeben", homeCatalog: "cat_1", state: "active",
    texts: { de: "Zahlung freigeben" }, approval: { kind: "none" },
    provisionProcess: "p", deprovisionProcess: "d" },
  { id: "einsicht", homeCatalog: "cat_1", state: "active",
    texts: { de: "Einsicht Kreditoren" }, approval: { kind: "none" },
    provisionProcess: "p", deprovisionProcess: "d" },
];

// installMock answers what the page asks on boot and records every write. The
// catalogue it answers a write with is the one that was sent, so the page reloads
// onto what it just saved.
function installMock(page, sent, state) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() !== "GET") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { /* not JSON */ }
      sent.push({ method: req.method(), path, body });
      if (body && body.edges) state.edges = body.edges;
      return route.fulfill({ json: { ...CATALOG, ...state } });
    }
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (path.endsWith("/api/v1/catalogs/cat_1")) {
      return route.fulfill({ json: { ...CATALOG, ...state } });
    }
    if (path.endsWith("/api/v1/catalog-products")) return route.fulfill({ json: ITEMS });
    if (path.endsWith("/releases")) return route.fulfill({ json: [] });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
}

async function bootCatalogue(page) {
  await page.goto("/index.html#/catalog/c/cat_1");
  await expect(page.locator("table.table").first()).toBeVisible({ timeout: 15000 });
}

// theIncompatibilityTable is the section this change adds, found by its heading so
// the guard does not depend on how many tables the page happens to draw.
const theIncompatibilityTable = (page) =>
  page.locator("h4", { hasText: "Incompatibility" }).locator("xpath=following-sibling::*[1]");

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__sent = [];
  page.__state = {};
  installMock(page, page.__sent, page.__state);
});

test("an incompatibility that exists is visible, and once", async ({ page }) => {
  await bootCatalogue(page);

  const table = theIncompatibilityTable(page);
  await expect(table.locator("tbody tr")).toHaveCount(1);
  await expect(table).toContainText("Kreditor erfassen");
  await expect(table).toContainText("Zahlung freigeben");
  // By its name in the vocabulary, not by the stored kind.
  await expect(table).toContainText("must not be held with");
  expect(page.__errors).toEqual([]);
});

test("the mirror of one already recorded is refused rather than stored twice",
  async ({ page }) => {
    await bootCatalogue(page);

    // The same fact said backwards. Stored, it would draw one row, be removed as a
    // pair, and leave whoever authored it wondering where the second went.
    await page.selectOption('.edge-new select[name="from"]', "zahlung-freigeben");
    await page.selectOption('.edge-new select[name="kind"]', "excludes");
    await page.selectOption('.edge-new select[name="to"]', "kreditor-erfassen");
    await page.locator('.edge-new button[type="submit"]').click();

    await expect(page.locator(".toast, .err").first()).toBeVisible({ timeout: 5000 });
    expect(page.__sent.filter((w) => w.method === "PATCH"),
      "nothing may be written for a fact that is already recorded").toEqual([]);
    expect(page.__errors).toEqual([]);
  });

test("a new incompatibility is written, and a product cannot exclude itself",
  async ({ page }) => {
    await bootCatalogue(page);

    await page.selectOption('.edge-new select[name="from"]', "einsicht");
    await page.selectOption('.edge-new select[name="kind"]', "excludes");
    await page.selectOption('.edge-new select[name="to"]', "einsicht");
    await page.locator('.edge-new button[type="submit"]').click();
    expect(page.__sent.filter((w) => w.method === "PATCH"),
      "a product cannot be incompatible with itself").toEqual([]);

    await page.selectOption('.edge-new select[name="to"]', "zahlung-freigeben");
    await page.locator('.edge-new button[type="submit"]').click();

    await expect(theIncompatibilityTable(page).locator("tbody tr")).toHaveCount(2);
    const written = page.__sent.filter((w) => w.method === "PATCH");
    expect(written).toHaveLength(1);
    // One direction is written, because the release records both from either one.
    expect(written[0].body.edges).toContainEqual(
      { from: "einsicht", to: "zahlung-freigeben", kind: "excludes" });
    expect(page.__errors).toEqual([]);
  });

test("removing an incompatibility removes both directions", async ({ page }) => {
  // Stored both ways round, which a catalogue edited over REST legitimately is.
  page.__state.edges = [
    { from: "kreditor-erfassen", to: "zahlung-freigeben", kind: "excludes" },
    { from: "zahlung-freigeben", to: "kreditor-erfassen", kind: "excludes" },
    { from: "zahlung-freigeben", to: "einsicht", kind: "requires" },
  ];
  await bootCatalogue(page);

  // Still one row: it is one fact, however many ways it was written down.
  const table = theIncompatibilityTable(page);
  await expect(table.locator("tbody tr")).toHaveCount(1);

  await table.locator('button[data-act="unexclude"]').click();

  const written = page.__sent.filter((w) => w.method === "PATCH");
  expect(written).toHaveLength(1);
  // Both directions gone, and the precedence edge untouched: removing one would
  // leave the other, and the row would come straight back with nothing to say why.
  expect(written[0].body.edges).toEqual([
    { from: "zahlung-freigeben", to: "einsicht", kind: "requires" },
  ]);
  expect(page.__errors).toEqual([]);
});
