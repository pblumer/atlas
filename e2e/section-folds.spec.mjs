// Every page with two or more headed sections folds each of them (fold.js, ADR-0505):
// the pages whose sections were shaped so the rule could not find them — a heading
// outside a card, a card filled after the route had folded the view, two cards headed
// by <h1>, <h3> headings over lists, a lazily loaded tab. Each test boots the real
// Console on mocked answers and checks that every section of one page has its toggle
// and that a click folds it.
import { test, expect } from "@playwright/test";

const NOW = Date.now();

// answers maps a path to the JSON the mock gives for it; everything else is [].
function installMock(page, answers, { auth = null } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: auth ? { authEnabled: true, user: auth } : { authEnabled: false, user: null } });
    }
    if (Object.hasOwn(answers, path)) return route.fulfill({ json: answers[path] });
    return route.fulfill({ json: [] });
  });
}

async function boot(page, answers, hash, opts) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page, answers, opts);
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await page.evaluate((h) => { location.hash = h; }, hash);
}

// expectFolds checks that each section has the toggle, and that clicking it hides what
// the section holds while its heading stays.
async function expectFolds(page, sections) {
  for (const { sel, body } of sections) {
    const toggle = page.locator(`${sel} [data-fold-toggle], ${sel} .section-toggle`).first();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(page.locator(body)).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(page.locator(body)).toBeHidden();
    await expect(toggle).toBeVisible();
  }
  expect(page.__errors).toEqual([]);
}

test("the dashboard folds a card its module fills after the route has run", async ({ page }) => {
  const soon = Math.floor(NOW / 1000) + 5 * 86400;
  await boot(page, {
    "/api/v1/api-tokens": [{ id: "tok-1", name: "ci", prefix: "atl_ci", expiresAt: soon, secretName: "CI_TOKEN" }],
  }, "#/console");
  await expect(page.locator("#api-tokens-expiring h2")).toBeVisible();
  await expectFolds(page, [
    { sel: "#api-tokens-expiring", body: "#api-tokens-expiring ul" },
    { sel: "#view .grid2 .card:first-child", body: "#dep-summary" },
  ]);
});

test("Connected accounts folds the list drawn under a heading outside a card", async ({ page }) => {
  await boot(page, {
    "/api/v1/me/connections": { identities: [], linkable: [], connections: [] },
  }, "#/console/connections");
  await expect(page.locator("#con-section h2")).toBeVisible();
  await expectFolds(page, [
    { sel: "#connections > .card:has(table)", body: '#connections table[data-dt-key="identities"]' },
    { sel: "#con-section", body: "#con-list" },
  ]);
});

test("Approvals folds each of its sections", async ({ page }) => {
  await boot(page, {
    "/api/v1/me/action-approvals": { items: [], total: 0, totalExact: true, truncated: false },
  }, "#/console/approvals", { auth: { id: "usr_bob", username: "bob", displayName: "Bob", roles: ["user"] } });
  await expect(page.locator("#apr-waiting h2")).toBeVisible();
  await expectFolds(page, [
    { sel: "#apr-waiting", body: "#apr-waiting [data-empty]" },
    { sel: "#apr-answered", body: "#apr-answered [data-empty]" },
  ]);
});

test("Backup folds its two topics under one page title", async ({ page }) => {
  await boot(page, {}, "#/console/backup");
  await expect(page.locator("#view h1").first()).toHaveText("Backup & restore");
  await expectFolds(page, [
    { sel: "#backup-design", body: "#restore-btn" },
    { sel: "#backup-full", body: "#restore-full-btn" },
  ]);
});

test("Display settings folds each choice, and keeps the preview in view", async ({ page }) => {
  await boot(page, {}, "#/console/settings");
  await expect(page.locator("#ds-sec-zone h2")).toBeVisible();
  await expectFolds(page, [
    { sel: "#ds-sec-numbers", body: "#ds-sec-numbers .ds-options" },
    { sel: "#ds-sec-zone", body: "#ds-zone" },
    { sel: "#ds-sec-language", body: "#ds-language" },
    { sel: "#ds-sec-appearance", body: "#ds-appearance" },
  ]);
  await expect(page.locator("#ds-sample")).toBeVisible();
  await expect(page.locator("#ds-save")).toBeVisible();
});

test("an application's Constants tab folds both of its lists", async ({ page }) => {
  await boot(page, {
    "/api/v1/applications": [{ id: "app-1", name: "Rechnung", visibility: "private" }],
    "/api/v1/applications/app-1/constants": {
      constants: [{ name: "TENANT", type: "string", value: "t1" }],
      required: [{ name: "TENANT", type: "string", processes: ["p"], state: "set" }],
    },
  }, "#/modeler/p/app-1");
  await page.locator('#pd-tabs button[data-pane="constants"]').click();
  await expect(page.locator("#ac-table")).toBeVisible();
  await expect(page.locator("#pd-constants .sec-head").first()).toContainText("1 Konstante");
  await expectFolds(page, [
    { sel: '#pd-constants .sec-head:has([data-section="ac-sec-required"])', body: "#ac-required" },
    { sel: '#pd-constants .sec-head:has([data-section="ac-sec-held"])', body: "#ac-table" },
  ]);
});

test("a business object's page folds each of its sections", async ({ page }) => {
  await boot(page, {
    "/api/v1/infomodel/models/m1/usage": {
      modelId: "m1", modelName: "Orders", applicationId: "app-1", applicationName: "Shop",
      class: { name: "Order", attributes: [{ name: "id", type: "string" }] },
      summary: {}, processes: [], model: [],
    },
  }, "#/data/objects/m1/Order");
  await expect(page.locator('[data-fold="structure"] h2')).toBeVisible();
  await expectFolds(page, [
    { sel: '[data-fold="structure"]', body: '[data-fold="structure"] .card' },
    { sel: '[data-fold="processes"]', body: '[data-fold="processes"] .card' },
    { sel: '[data-fold="model"]', body: '[data-fold="model"] .card' },
  ]);
});

test("Planned against built folds both sides and what it never compared", async ({ page }) => {
  await boot(page, {
    "/api/v1/applications": [{ id: "app-1", name: "Shop", visibility: "private" }],
    "/api/v1/infomodel/difference": { modeled: true, planned: [], built: [], excluded: ["documentation"] },
  }, "#/data/difference/app-1");
  await expect(page.locator('[data-fold="planned"] h2')).toBeVisible();
  await expectFolds(page, [
    { sel: '[data-fold="planned"]', body: '[data-fold="planned"] .md-meaning' },
    { sel: '[data-fold="built"]', body: '[data-fold="built"] .md-meaning' },
    { sel: '[data-fold="excluded"]', body: '[data-fold="excluded"] ul' },
  ]);
});

test("Data objects folds each type's card", async ({ page }) => {
  const row = (itemType, name) => ({ itemType, name, key: "", instanceKey: 1, processId: "p", value: "1" });
  await boot(page, {
    "/api/v1/data-objects": { objects: [row("Order", "order"), row("Invoice", "invoice")] },
  }, "#/data/instances");
  await expect(page.locator('[data-fold="Order"] h2')).toBeVisible();
  await expectFolds(page, [
    { sel: '[data-fold="Invoice"]', body: '[data-fold="Invoice"] .im-groups' },
    { sel: '[data-fold="Order"]', body: '[data-fold="Order"] .im-groups' },
  ]);
});

test("the catalogue list folds every section below the list", async ({ page }) => {
  await boot(page, {
    "/api/v1/catalog-products/approver-report": { problems: [], checked: 3 },
    "/api/v1/catalog-products/translation-gaps": { gaps: [], checked: 3, catalogs: ["c1"] },
  }, "#/catalog");
  await expect(page.locator('[data-fold="import"] h2')).toBeVisible();
  await expectFolds(page, [
    { sel: '[data-fold="import"]', body: ".cat-import" },
    { sel: '[data-fold="mappings"]', body: '[data-fold="mappings"] .card' },
    { sel: '[data-fold="approvers"]', body: '[data-fold="approvers"] p' },
    { sel: '[data-fold="translations"]', body: '[data-fold="translations"] p' },
  ]);
});
