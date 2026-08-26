// End-to-end coverage for the Console's Connectors page (api/web/app.js,
// viewConsoleConnectors).
//
// The connector catalog, the connectors this instance has configured, and the vault
// their credentials resolve from used to be the last three cards of Organization —
// under the user roster, the groups and the colour picker. Organization answers "who
// uses this instance"; a connector is not a person, and an operator comes back to the
// integrations far more often than to the roster they were filed behind. They are
// their own top-level Console page now.
//
// These drive the REAL app shell (index.html → app.js) against a mocked /api/v1, like
// router-reentrancy.spec.mjs and theme.spec.mjs, because what is under test is the
// routing and the page split — not markup a harness could stand in for.
import { test, expect } from "@playwright/test";

// installMock answers /auth/me as single-user, serves one configured connector whose
// enabled flag it actually stores (so a toggle has something to re-render from), and
// answers every other call with an empty list.
function installMock(page) {
  const state = { enabled: true };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (/\/connectors\/[^/]+$/.test(path) && req.method() === "PATCH") {
      state.enabled = (req.postDataJSON() || {}).enabled !== false;
      return route.fulfill({ json: { id: "c1", enabled: state.enabled } });
    }
    if (path.endsWith("/connectors")) {
      return route.fulfill({
        json: [{
          id: "c1", name: "clio-prod", kind: "clio", endpoint: "https://clio.example",
          credentialsRef: "clio_token", enabled: state.enabled,
        }],
      });
    }
    return route.fulfill({ json: [] });
  });
  return state;
}

async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
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

test("Connectors is a Console nav item of its own", async ({ page }) => {
  const link = page.locator('.topnav a[href="#/console/connectors"]');
  await expect(link).toHaveText("Connectors");
  await link.click();
  await expect(page.locator("#view h1")).toHaveText("Connectors");
  // Marked as where you are, so the nav says which of the seven pages this is.
  await expect(link).toHaveClass(/active/);
  expect(page.__errors).toEqual([]);
});

test("the page carries the catalog, the configured connectors and the vault", async ({ page }) => {
  await goto(page, "#/console/connectors");
  const view = page.locator("#view");
  // The catalog of kinds Atlas can delegate to...
  await expect(view).toContainText("Sibling engines Atlas delegates to");
  await expect(view).toContainText("Decision engine");
  // ...what this instance has actually pointed somewhere...
  await expect(view.locator("h2").filter({ hasText: "Configured connectors" })).toBeVisible();
  await expect(page.locator("#connector-rows")).toContainText("clio-prod");
  // ...and the vault its credential reference resolves from.
  await expect(view.locator("h2").filter({ hasText: "Secrets" })).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("Organization keeps the people and the branding, and none of the connectors", async ({ page }) => {
  await goto(page, "#/console/org");
  const view = page.locator("#view");
  await expect(view.locator("h1")).toHaveText("Organization");
  await expect(view).toContainText("Users");
  await expect(view).toContainText("Groups");
  await expect(view).toContainText("Appearance");
  await expect(view).not.toContainText("Configured connectors");
  await expect(view).not.toContainText("Sibling engines Atlas delegates to");
  await expect(page.locator("#new-connector")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("editing a connector re-renders the page it lives on", async ({ page }) => {
  // The row handlers re-render by calling a view function by name. Pointed at the old
  // one, a Disable would have thrown the operator back to Organization — and with the
  // connector cards gone from it, to a page without the row they just changed.
  await goto(page, "#/console/connectors");
  await expect(page.locator("#connector-rows")).toContainText("enabled");
  await page.click('#connector-rows button[data-cact="toggle"]');
  await expect(page.locator("#connector-rows")).toContainText("disabled");
  await expect(page.locator("#view h1")).toHaveText("Connectors");
  expect(page.__errors).toEqual([]);
});
