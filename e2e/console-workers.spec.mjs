// End-to-end coverage for the Console's Workers page (ADR-0203).
import { test, expect } from "@playwright/test";

function installMock(page) {
  const state = { enabled: true };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (/\/connectors\/[^/]+$/.test(path) && req.method() === "PATCH") {
      state.enabled = (req.postDataJSON() || {}).enabled !== false;
      return route.fulfill({ json: { id: "c1", enabled: state.enabled } });
    }
    if (path.endsWith("/connectors")) {
      // `role` is what the caller may do with this worker (ADR-0205), and it is
      // what decides whether the row draws its actions at all. A server with auth
      // off answers "owner" for every worker, which is what this mock stands in
      // for; leaving it out would make the row read as somebody else's, and the
      // page would correctly draw no buttons.
      return route.fulfill({ json: [{ id: "c1", name: "clio-prod", kind: "clio", role: "owner", endpoint: "https://clio.example", credentialsRef: "clio_token", enabled: state.enabled }] });
    }
    return route.fulfill({ json: [] });
  });
}

async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

const goto = async (page, hash) => {
  await page.evaluate((h) => { location.hash = h; }, hash);
  await page.waitForTimeout(400);
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
  await bootApp(page);
});

test("Workers replaces Connectors in Console navigation", async ({ page }) => {
  const link = page.locator('.topnav a[href="#/console/workers"]');
  await expect(link).toHaveText("Workers");
  await link.click();
  await expect(page.locator("#view h1")).toHaveText("Workers");
  await expect(link).toHaveClass(/active/);
  await expect(page.locator('.topnav a[href="#/console/connectors"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("Workers presents catalog and configured workers over the compatibility APIs", async ({ page }) => {
  await goto(page, "#/console/workers");
  const view = page.locator("#view");
  await expect(view.locator("h2").filter({ hasText: "Worker catalog" })).toBeVisible();
  await expect(view.locator("h2").filter({ hasText: "Configured workers" })).toBeVisible();
  await expect(page.locator("#worker-rows")).toContainText("clio-prod");
  await expect(view.locator("h2").filter({ hasText: "Secrets" })).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("the legacy connectors route canonicalizes to Workers", async ({ page }) => {
  await goto(page, "#/console/connectors");
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/console/workers");
  await expect(page.locator("#view h1")).toHaveText("Workers");
  expect(page.__errors).toEqual([]);
});

test("a worker's actions live behind the row's ⋯ menu", async ({ page }) => {
  await goto(page, "#/console/workers");
  const row = page.locator("#worker-rows tr").first();
  // Nothing but the menu trigger is drawn on the row: the seven buttons that used to
  // stand there are what made the table unreadable.
  await expect(row.locator("td.row-actions .dropdown-toggle")).toHaveCount(1);
  await expect(row.locator("td.row-actions .dropdown-menu")).toBeHidden();

  await row.locator("td.row-actions .dropdown-toggle").click();
  const menu = row.locator("td.row-actions .dropdown-menu");
  await expect(menu).toBeVisible();
  // A clio worker's own actions lead, then the ones every worker has.
  await expect(menu).toContainText("Provision access");
  await expect(menu).toContainText("Events");
  await expect(menu).toContainText("Edit");
  await expect(menu).toContainText("Share");
  await expect(menu).toContainText("Disable");
  await expect(menu).toContainText("Delete");
  expect(page.__errors).toEqual([]);
});

test("editing a configured worker stays on Workers", async ({ page }) => {
  await goto(page, "#/console/workers");
  await expect(page.locator("#worker-rows")).toContainText("enabled");
  await page.click("#worker-rows .dropdown-toggle");
  await page.click('#worker-rows .dropdown-menu button[data-act="toggle"]');
  await expect(page.locator("#worker-rows")).toContainText("disabled");
  await expect(page.locator("#view h1")).toHaveText("Workers");
  expect(page.__errors).toEqual([]);
});

// The Console's create form is where somebody who has just installed Atlas stands, and
// the fields on it presuppose work at the provider that nothing on the screen used to
// mention (ADR-0289). The setup block follows the type
// picked in the form, so it is the steps for the worker actually being added.
test("the New worker form says how the picked type is set up, and links to the handbook", async ({ page }) => {
  await goto(page, "#/console/workers");
  await page.click("#new-worker");
  const doc = page.locator(".conn-setup .wtdoc");
  await expect(doc).toBeVisible();
  // The form opens on temis, the first option.
  await expect(doc.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-temis");

  await page.selectOption('.worker-form [name="kind"]', "googlesheets");
  await expect(doc.locator(".wtdoc-needs")).toContainText("service account key");
  await expect(doc.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-googlesheets");
  await page.locator(".conn-setup .wtdoc-more > summary").click();
  // The step that is forgotten more often than any other, on the screen where the
  // worker is created rather than in an incident an hour later.
  await expect(page.locator(".conn-setup .wtdoc-steps")).toContainText("Share the spreadsheet");
  expect(page.__errors).toEqual([]);
});

// The catalog lists every Worker Type this Atlas serves, which is two dozen rows of
// prose; the configured workers grow with the installation. Both are lists, so both
// get the table's own filter row (ADR-0286), and both fold away so an operator who
// came for the vault or a mockup switch scrolls past neither.
test("the Worker catalog is a filterable list", async ({ page }) => {
  await goto(page, "#/console/workers");
  const catalog = page.locator("#sec-workers-catalog table");
  await expect(catalog.locator(".dt-filter").first()).toBeVisible();
  const rows = catalog.locator("tbody tr:not([hidden])");
  expect(await rows.count()).toBeGreaterThan(5);

  await catalog.locator(".dt-filter").first().fill("atlassian jira");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("Jira");
  expect(page.__errors).toEqual([]);
});

test("both worker sections fold away, and stay folded", async ({ page }) => {
  await goto(page, "#/console/workers");
  for (const id of ["workers-catalog", "workers-configured"]) {
    const toggle = page.locator(`.section-toggle[data-section="${id}"]`);
    const body = page.locator(`#sec-${id}`);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(body).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(body).toBeHidden();
  }
  // A folded section still says what it holds.
  await expect(page.locator(".sec-head").last()).toContainText("1 Worker");

  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await expect(page.locator("#sec-workers-catalog")).toBeHidden();
  await expect(page.locator("#sec-workers-configured")).toBeHidden();
  // The vault below is not folded with them.
  await expect(page.locator("#view h2").filter({ hasText: "Secrets" })).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("New worker unfolds the folded section it opens its form in", async ({ page }) => {
  await goto(page, "#/console/workers");
  await page.click('.section-toggle[data-section="workers-configured"]');
  await expect(page.locator("#sec-workers-configured")).toBeHidden();
  await page.click("#new-worker");
  await expect(page.locator("#sec-workers-configured")).toBeVisible();
  await expect(page.locator(".worker-form")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

// Enabling or disabling a worker re-renders the page without the router, which is
// what used to give the tables their filter row; the list must keep it.
test("the configured workers keep their filter row after a worker is changed", async ({ page }) => {
  await goto(page, "#/console/workers");
  await page.click("#worker-rows .dropdown-toggle");
  await page.click('#worker-rows .dropdown-menu button[data-act="toggle"]');
  await expect(page.locator("#worker-rows")).toContainText("disabled");
  await expect(page.locator('table[data-dt-key="connectors"] .dt-filter').first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});
