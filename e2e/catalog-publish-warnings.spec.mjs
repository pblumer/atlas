// A publish that goes through says what it leaves exposed
// (ADR-0443).
//
// Publishing warns, and does not refuse, when an answer of a product's order form
// reaches one of its processes without that process declaring it personal data. The
// Go suite decides which answers those are. This drives the REAL catalogue page
// against a mocked /api/v1 to check the wiring: the release is reported as made, the
// warnings survive the reload every publish does, and a publish without warnings
// shows none.
import { test, expect } from "@playwright/test";

const CATALOG = {
  id: "cat_1", revision: 3, rank: 1, languages: ["de"], texts: { de: "Verwaltung" },
  groups: ["grp_all"], items: ["park"], edges: [],
};

const ITEMS = [
  { id: "park", homeCatalog: "cat_1", state: "active", texts: { de: "Parkplatz" },
    approval: { kind: "none" }, provisionProcess: "prov-park", deprovisionProcess: "prov-park",
    configForm: "park-form" },
];

const WARNING = {
  item: "park",
  message: "the answers fahrzeug of form park-form reach prov-park in the clear: declare the " +
    "personal ones in that process with atlas:personal and atlas:dataSubject=\"recipient\", and " +
    "mark a field that names nobody with the custom property personal=false",
};

function installMock(page, warnings) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "POST" && path.endsWith("/catalogs/cat_1/releases")) {
      return route.fulfill({ status: 201, json: { id: "rel_9", catalogId: "cat_1", items: ITEMS, warnings } });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/catalogs/cat_1")) return route.fulfill({ json: CATALOG });
    if (path.endsWith("/api/v1/catalog-products")) return route.fulfill({ json: ITEMS });
    if (path.endsWith("/releases")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
}

async function publish(page) {
  await page.goto("/index.html#/catalog/c/cat_1");
  const button = page.locator('[data-act="publish"]');
  await expect(button).toBeVisible({ timeout: 15000 });
  await button.click();
}

test("a publish that warns says the release stands and which answers are exposed", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, [WARNING]);
  await publish(page);

  const card = page.locator(".publish-report .publish-warnings");
  await expect(card).toBeVisible();
  await expect(card).toContainText("Published, with 1 warning.");
  await expect(card).toContainText("item park");
  await expect(card).toContainText("the answers fahrzeug of form park-form reach prov-park in the clear");
  expect(errors).toEqual([]);
});

test("a publish without warnings shows none", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, undefined);
  await publish(page);
  await expect(page.locator("#toast")).toContainText("Published rel_9");
  await expect(page.locator(".publish-warnings")).toHaveCount(0);
  expect(errors).toEqual([]);
});
