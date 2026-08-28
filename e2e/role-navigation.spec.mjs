// The Console offers only what the signed-in person's roles reach
// (ADR-draft-roles-per-endpoint-group, measure M9).
//
// The server refuses regardless — this is a courtesy, not a boundary. But without
// it a task worker's Console is a menu of screens whose every call comes back 403,
// which is how an operator concludes the product is broken rather than that their
// account is narrow.
import { test, expect } from "@playwright/test";

// stubAPI answers /auth/me as the given account and everything else benignly. The
// catch-all is registered first because Playwright matches the most recently
// registered route.
function stubAPI(page, user) {
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user } }));
}

const drawer = (page) => page.locator("#drawer-apps a");
const topnav = (page) => page.locator("#topnav a");

async function boot(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#drawer-apps")?.children.length > 0,
    null, { timeout: 15000 });
}

test("a task worker is offered Tasks and the Console, not the Modeler", async ({ page }) => {
  stubAPI(page, { username: "tina", roles: ["user"] });
  await boot(page);

  // Panorama is a "soon" placeholder with nothing behind it, so it is offered to
  // everyone; its label carries the badge's text.
  await expect(drawer(page)).toHaveText(["Console", "Tasks", "Panoramasoon"]);
  // And inside the Console, the administrator's screens are not offered either.
  const names = await topnav(page).allTextContents();
  expect(names).toContain("Dashboard");
  expect(names).not.toContain("Organization");
  expect(names).not.toContain("Audit log");
});

test("a modeller who also operates is offered both", async ({ page }) => {
  stubAPI(page, { username: "mona", roles: ["modeler", "operator", "user"] });
  await boot(page);

  await expect(drawer(page)).toHaveText(["Console", "Modeler", "Tasks", "Operations", "Panoramasoon"]);
});

test("an administrator is offered everything, Organization included", async ({ page }) => {
  stubAPI(page, { username: "root", roles: ["admin"] });
  await boot(page);

  // Panorama is in the list as "soon", so an administrator sees all five entries.
  await expect(drawer(page)).toHaveCount(5);
  expect(await topnav(page).allTextContents()).toContain("Organization");
});

test("with enforcement off there is nobody to have a role, so nothing is hidden", async ({ page }) => {
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: false, user: null } }));
  await boot(page);

  await expect(drawer(page)).toHaveCount(5);
  expect(await topnav(page).allTextContents()).toContain("Organization");
});

// The other half of M9 in the Console: granting the roles. Before this the account
// dialog had one checkbox, "Administrator", and there was nothing else to give.
test("the account dialog grants the four roles by name", async ({ page }) => {
  let patched = null;
  page.route("**/api/v1/**", (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/users") && req.method() === "GET") {
      return route.fulfill({
        json: [{ id: "u1", username: "tina", displayName: "Tina", roles: ["user"], disabled: false, source: "local" }],
      });
    }
    if (path.endsWith("/users/u1") && req.method() === "PATCH") {
      patched = req.postDataJSON();
      return route.fulfill({ json: { id: "u1", username: "tina", roles: patched.roles } });
    }
    return route.fulfill({ json: [] });
  });
  page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user: { id: "root", username: "root", roles: ["admin"] } } }));

  await boot(page);
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await page.locator("#user-rows button[data-act=edit]").first().click();

  const form = page.locator(".user-form");
  await expect(form.locator('input[name="role-admin"]')).not.toBeChecked();
  await expect(form.locator('input[name="role-modeler"]')).not.toBeChecked();
  await expect(form.locator('input[name="role-operator"]')).not.toBeChecked();
  await expect(form.locator('input[name="role-user"]')).toBeChecked();

  await form.locator('input[name="role-modeler"]').check();
  await form.locator('button[type=submit]').click();

  await expect.poll(() => patched && patched.roles).toEqual(["modeler", "user"]);
});
