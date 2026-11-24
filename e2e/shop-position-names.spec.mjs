// e2e for what a held position is called in the shop (api/web/shop.js positionName),
// against a fixture.
//
// What was reported (2026-11-24): somebody holding the same Marktleistung twice — two
// SharePoint Team Sites — saw the actions of both under the same words, the product's
// name, and could not tell which site a button would act on. A position somebody
// answered an order form for is now named by the first answer given, in the form's
// own order, with a name field (one the form checks for availability) first.
//
// It loads the REAL shop.html with only the network replaced.
import { test, expect } from "@playwright/test";

// The order form: a cost centre first and the site's name second, and the name is
// the field the form checks for availability. Keys chosen so that the answers'
// sorted order ("costCentre" < "siteName") is the wrong one.
const SITE_FORM = {
  type: "default", schemaVersion: 4, id: "site-form",
  components: [
    { type: "select", key: "costCentre", label: "Cost centre",
      values: [{ value: "4711", label: "Marketing (4711)" }] },
    { type: "textfield", key: "siteName", label: "Site name", atlasAvailability: { minLength: 3 } },
    { type: "checkbox", key: "external", label: "External sharing" },
  ],
};
// A form without a name field: the first answer in the form's order names it.
const PLAIN_FORM = {
  type: "default", schemaVersion: 4, id: "plain-form",
  components: [
    { type: "checkbox", key: "agree", label: "Agree" },
    { type: "textfield", key: "zTitle", label: "Title" },
    { type: "textfield", key: "aNote", label: "Note" },
  ],
};

function installFixture(forms) {
  const line = (key, config) => ({
    itemId: "teamsite", status: "done", lifecycleProcess: "site-lifecycle",
    lifecycleForm: "per-position", configForm: "site-form", config,
    actions: [
      { key: "provision", message: "site.provision", effect: "provision" },
      { key: "deprovision", message: "site.deprovision", effect: "deprovision", triggers: ["customer"] },
      { key: "rename", message: "site.rename", effect: "service", triggers: ["customer"] },
    ],
    instances: [{ key, processId: "site-lifecycle", operation: "provision" }],
  });
  const ORDERS = [
    { id: "ord_b", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
      createdAt: Date.UTC(2026, 10, 24, 10) * 1e6,
      lines: [line(2, { costCentre: "4711", siteName: "Projekt Atlas", external: "true" })] },
    { id: "ord_a", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
      createdAt: Date.UTC(2026, 10, 20, 10) * 1e6,
      lines: [line(1, { costCentre: "4711", siteName: "Team Finanzen" })] },
    { id: "ord_c", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
      createdAt: Date.UTC(2026, 10, 18, 10) * 1e6,
      lines: [{ itemId: "wiki", status: "done", lifecycleProcess: "wiki-lifecycle",
        configForm: "plain-form", config: { agree: "true", aNote: "nur intern", zTitle: "Handbuch" },
        actions: [{ key: "provision", message: "w.p", effect: "provision" },
          { key: "archive", message: "w.a", effect: "service", triggers: ["customer"] }] }] },
  ];
  const ACTIONS = { actions: [
    { key: "rename", effect: "service", triggers: ["customer"],
      labels: { de: "Site umbenennen", en: "Rename site" }, available: true },
  ] };
  const CAT = { id: "cat_t", rank: 1, languages: ["de", "en"],
    texts: { de: "Katalog", en: "Catalogue" }, items: [] };
  const ROUTES = {
    "/api/v1/auth/me": { authEnabled: true, user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_t/releases": [],
    "/api/v1/orders": ORDERS,
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
    "/api/v1/forms/site-form": { id: "site-form", schema: forms.site },
    "/api/v1/forms/plain-form": { id: "plain-form", schema: forms.plain },
    "/api/v1/orders/ord_a/lines/teamsite/actions": ACTIONS,
    "/api/v1/orders/ord_b/lines/teamsite/actions": ACTIONS,
    "/api/v1/orders/ord_c/lines/wiki/actions": { actions: [
      { key: "archive", effect: "service", triggers: ["customer"], labels: { en: "Archive" }, available: true },
    ] },
  };
  // Every catalogue this person reaches (ADR-0522).
  ROUTES["/api/v1/shop/catalogs"] = [ROUTES["/api/v1/shop/catalog"]];
  const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {
    status: 200, headers: { "content-type": "application/json" },
  }));
  const realFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const path = url.pathname;
    if (!path.startsWith("/api/")) return realFetch(input, init);
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture, { site: SITE_FORM, plain: PLAIN_FORM });
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My services" }).click();
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("two positions of one product are told apart by the name their form gave them",
  async ({ page }) => {
    await page.locator(".cell button.label", { hasText: "teamsite" }).click();
    const actions = page.locator(".svc-detail .svc-actions");
    // The name field, not the cost centre that sorts before it.
    await expect(actions.locator(".svc-action-row", { hasText: "teamsite “Projekt Atlas”:" })
      .getByRole("button", { name: "Rename site" })).toBeVisible({ timeout: 10000 });
    await expect(actions.locator(".svc-action-row", { hasText: "teamsite “Team Finanzen”:" })
      .getByRole("button", { name: "Rename site" })).toBeVisible();
    await expect(actions).not.toContainText("4711");
    // And the position under its order says the same.
    await expect(page.locator(".svc-detail .lines > li").first()).toContainText("teamsite “Projekt Atlas”");
  });

test("without a name field the first answer in the form's order names the position",
  async ({ page }) => {
    await page.locator(".cell button.label", { hasText: "wiki" }).click();
    // "Handbuch" is the first typed answer in the form; the checkbox names nothing,
    // and "aNote" would come first by key.
    await expect(page.locator(".svc-detail .svc-actions")).toContainText("wiki “Handbuch”:",
      { timeout: 10000 });
  });

test("a position is found by its name", async ({ page }) => {
  await page.getByPlaceholder("Search service, person or order").fill("Finanzen");
  await expect(page.locator(".cell button.label", { hasText: "teamsite" })).toBeVisible({ timeout: 10000 });
  await expect(page.locator(".cell button.label", { hasText: "wiki" })).toHaveCount(0);
});
