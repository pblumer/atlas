// End-to-end coverage for the Console's Workers page (ADR-0203).
//
// This is the first vertical migration slice: product terminology moves from
// Connector Kind / Connector Instance to Worker Type / Worker while the existing
// connector APIs and stores remain the compatibility layer underneath.
import { test, expect } from "@playwright/test";

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

test("Workers replaces Connectors in the Console navigation", async ({ page }) => {
  const link = page.locator('.topnav a[href="#/console/workers"]');
  await expect(link).toHaveText("Workers");
  await link.click();
  await expect(page.locator("#view h1")).toHaveText("Workers");
  await expect(link).toHaveClass(/active/);
  await expect(page.locator('.topnav a[href="#/console/connectors"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("Workers presents catalog and configured workers while reusing connector APIs", async ({ page }) => {
  await goto(page, "#/console/workers");
  const view = page.locator("#view");
  await expect(view.locator("h2").filter({ hasText: "Worker catalog" })).toBeVisible();
  await expect(view.locator("h2").filter({ hasText: "Configured workers" })).toBeVisible();
  await expect(page.locator("#connector-rows")).toContainText("clio-prod");
  await expect(view.locator("h2").filter({ hasText: "Secrets" })).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("legacy connector route redirects to Workers", async ({ page }) => {
  await goto(page, "#/console/connectors");
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/console/workers");
  await expect(page.locator("#view h1")).toHaveText("Workers");
  expect(page.__errors).toEqual([]);
});

test("editing a configured worker stays on Workers", async ({ page }) => {
  await goto(page, "#/console/workers");
  await expect(page.locator("#connector-rows")).toContainText("enabled");
  await page.click('#connector-rows button[data-cact="toggle"]');
  await expect(page.locator("#connector-rows")).toContainText("disabled");
  await expect(page.locator("#view h1")).toHaveText("Workers");
  expect(page.__errors).toEqual([]);
});
