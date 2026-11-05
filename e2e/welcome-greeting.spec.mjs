// e2e for the Console's welcome heading (app.js viewConsoleDashboard).
//
// The welcome card greets the person who is signed in by name — their display name
// where they have one, as the account menu shows them, else their username. Without
// sign-in (auth off, the default) there is nobody to name, and the card keeps
// welcoming to atlas. A name is the person's own text, so it is shown as text: a
// display name holding markup must not become markup.
import { test, expect } from "@playwright/test";

async function boot(page, me) {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: me }));
  await page.goto("/index.html#/console");
  await page.waitForSelector(".welcome-head h1");
}

const heading = (page) => page.locator(".welcome-head h1");

test("the welcome card greets a signed-in person by their display name", async ({ page }) => {
  await boot(page, { authEnabled: true, user: { id: "usr_tina", username: "tina", displayName: "Tina Muster", roles: ["user"] } });
  await expect(heading(page)).toHaveText("Welcome, Tina Muster");
});

test("without a display name the welcome card uses the username", async ({ page }) => {
  await boot(page, { authEnabled: true, user: { id: "usr_root", username: "root", roles: ["admin"] } });
  await expect(heading(page)).toHaveText("Welcome, root");
});

test("with sign-in off the welcome card still welcomes to atlas", async ({ page }) => {
  await boot(page, { authEnabled: false, user: null });
  await expect(heading(page)).toHaveText("Welcome to atlas");
});

test("a display name is shown as text, never as markup", async ({ page }) => {
  await boot(page, { authEnabled: true, user: { id: "usr_x", username: "x", displayName: "<img src=x onerror=alert(1)>Eve", roles: ["user"] } });
  await expect(heading(page)).toHaveText("Welcome, <img src=x onerror=alert(1)>Eve");
  expect(await heading(page).locator("img").count()).toBe(0);
});
