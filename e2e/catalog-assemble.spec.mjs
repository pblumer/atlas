// Assembling a product from the services that exist (#1022, first half).
//
// A catalogue is built out of services that each provision themselves. What a
// product adds is an *arrangement*: which of those services come with it and which
// are offered beside it. Until now that was said through a table of edges — pick a
// "from", pick a relationship, pick a "to" — which is the data as it is stored
// rather than the question somebody has.
//
// The kit asks the question once per service: not part, included, or optional.
//
// The API halves (the edge kinds, the cycle refusal at publish) are covered by the
// Go suite. This drives the REAL app shell against a mocked /api/v1 to check the
// wiring: what the kit shows, what one save sends, and what it must leave alone.
import { test, expect } from "@playwright/test";

const CATALOG = {
  id: "cat_1", revision: 7, rank: 1, languages: ["de"], texts: { de: "Mobile Geräte" },
  groups: ["grp_all"], items: ["paket", "phone", "huelle", "lader", "sim"],
  edges: [
    // The arrangement as it stands: the package carries the phone and offers a case.
    { from: "paket", to: "phone", kind: "composition" },
    { from: "paket", to: "huelle", kind: "aggregation" },
    // Another whole's arrangement, which this kit must never touch.
    { from: "sim", to: "lader", kind: "composition" },
    // One hop further down the package: the phone offers the charger too. It is here
    // so that "already contains" can be asked across two edges and not only one.
    { from: "phone", to: "lader", kind: "aggregation" },
    // And a precedence edge, which is a different question entirely.
    { from: "phone", to: "sim", kind: "requires" },
  ],
};

const ITEMS = [
  { id: "paket", homeCatalog: "cat_1", state: "active", texts: { de: "iPhone Paket" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" },
  { id: "phone", homeCatalog: "cat_1", state: "active", texts: { de: "Apple iPhone 18 Pro" },
    approval: { kind: "none" }, provisionProcess: "prov-phone", deprovisionProcess: "d" },
  { id: "huelle", homeCatalog: "cat_1", state: "active", texts: { de: "Schutzhülle" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" },
  { id: "lader", homeCatalog: "cat_1", state: "active", texts: { de: "Ladegerät" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" },
  { id: "sim", homeCatalog: "cat_1", state: "active", texts: { de: "SIM-Karte" },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" },
];

// installMock answers what the page asks on boot and records every write.
function installMock(page, sent) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() !== "GET") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { /* not JSON */ }
      sent.push({ method: req.method(), path, body });
      return route.fulfill({ json: CATALOG });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/catalogs/cat_1")) return route.fulfill({ json: CATALOG });
    if (path.endsWith("/api/v1/catalog-products")) return route.fulfill({ json: ITEMS });
    if (path.endsWith("/releases")) return route.fulfill({ json: [] });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: [{ processId: "prov-phone" }] });
    return route.fulfill({ json: [] });
  });
}

async function bootCatalogue(page) {
  await page.goto("/index.html#/catalog/c/cat_1");
  await expect(page.locator(".product-form, table.table").first()).toBeVisible({ timeout: 15000 });
}

// openKit opens the assembly panel for one product.
async function openKit(page, id) {
  await page.locator(`[data-act="assemble"][data-id="${id}"]`).click();
  await expect(page.locator(".assemble")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__sent = [];
  installMock(page, page.__sent);
});

test("the kit asks one question per service, and shows how it stands", async ({ page }) => {
  await bootCatalogue(page);
  await openKit(page, "paket");

  const kit = page.locator(".assemble");
  // Every other product this catalogue offers is a row, and the product being
  // assembled is not among them: a thing cannot be part of itself. Asserted on the
  // rows and not on the panel, because the panel says whose arrangement this is.
  await expect(kit.locator("tbody tr")).toHaveCount(4);
  await expect(kit.locator("tbody")).not.toContainText("iPhone Paket");

  // And each row shows where it stands today, read from the edges.
  await expect(kit.locator('input[name="part-phone"][value="composition"]')).toBeChecked();
  await expect(kit.locator('input[name="part-huelle"][value="aggregation"]')).toBeChecked();
  await expect(kit.locator('input[name="part-lader"][value="none"]')).toBeChecked();
  await expect(kit.locator('input[name="part-sim"][value="none"]')).toBeChecked();
  expect(page.__errors).toEqual([]);
});

test("one save writes the whole arrangement and leaves the rest alone", async ({ page }) => {
  await bootCatalogue(page);
  await openKit(page, "paket");

  // Take the case out, put the charger in, and make the SIM an option.
  await page.locator('input[name="part-huelle"][value="none"]').check();
  await page.locator('input[name="part-lader"][value="composition"]').check();
  await page.locator('input[name="part-sim"][value="aggregation"]').check();
  await page.locator('[data-act="assemble-save"]').click();

  await expect.poll(() => page.__sent.length).toBeGreaterThan(0);
  const write = page.__sent[0];
  expect(write.method).toBe("PATCH");
  expect(write.path).toBe("/api/v1/catalogs/cat_1");
  // The revision the page was rendered at rides along, because `edges` is replaced
  // whole and a write without it is somebody else's arrangement disappearing.
  expect(write.body.revision).toBe(7);

  const edges = write.body.edges;
  const has = (from, kind, to) => edges.some((e) => e.from === from && e.kind === kind && e.to === to);
  expect(has("paket", "composition", "phone"), "the phone stays included").toBe(true);
  expect(has("paket", "composition", "lader"), "the charger is now included").toBe(true);
  expect(has("paket", "aggregation", "sim"), "the SIM is now optional").toBe(true);
  expect(edges.some((e) => e.from === "paket" && e.to === "huelle"),
    "the case was taken out").toBe(false);

  // What the kit was not asked about is untouched: another whole's arrangement, and
  // every precedence edge — which is a different question and not this screen's.
  expect(has("sim", "composition", "lader"), "another product's parts").toBe(true);
  expect(has("phone", "requires", "sim"), "precedence").toBe(true);
  expect(page.__errors).toEqual([]);
});

test("a product cannot be put inside something it already contains", async ({ page }) => {
  await bootCatalogue(page);
  await openKit(page, "phone");

  // The package already carries the phone. Making the package part of the phone
  // closes a loop, and a catalogue with one cannot be published — so the kit says
  // so instead of writing it and letting publish explain three screens later.
  await page.locator('input[name="part-paket"][value="composition"]').check();
  await page.locator('[data-act="assemble-save"]').click();

  await expect(page.locator("#toast")).toBeVisible();
  await expect(page.locator("#toast")).toContainText("iPhone Paket");
  expect(page.__sent).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("a loop two edges long is refused the same way", async ({ page }) => {
  await bootCatalogue(page);
  await openKit(page, "lader");

  // The package contains the phone, and the phone offers the charger. So putting the
  // package inside the charger closes a loop nobody can see on one row — which is the
  // case a refusal that only compared two products at a time would wave through.
  await page.locator('input[name="part-paket"][value="composition"]').check();
  await page.locator('[data-act="assemble-save"]').click();

  await expect(page.locator("#toast")).toBeVisible();
  await expect(page.locator("#toast")).toContainText("iPhone Paket");
  expect(page.__sent).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("the pairwise form is left for precedence only", async ({ page }) => {
  await bootCatalogue(page);

  // Structure is assembled per product now. A second way to say the same thing is
  // how two surfaces drift, and the one that drifts here is the one that decides
  // what somebody is actually ordering.
  // Read the values, not the labels: the labels are prose ("contains", "optionally
  // contains") and a guard reading those would pass on the day somebody renames one.
  const kinds = await page.locator('.edge-new select[name="kind"] option')
    .evaluateAll((os) => os.map((o) => o.value));
  expect(kinds).toEqual(["requires"]);
  expect(page.__errors).toEqual([]);
});
