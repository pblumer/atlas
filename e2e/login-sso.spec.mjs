// The login screen offers a federated login only when there is one
// (ADR-0210, measure M12).
//
// The button is the whole visible surface of the feature, and it is drawn from
// what the server says rather than from configuration the browser cannot see. An
// instance with no provider must look exactly as it did before.
import { test, expect } from "@playwright/test";

const json = (body, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

// stub answers the calls the login screen makes. The catch-all goes first because
// Playwright matches the most recently registered route.
async function stub(page, providers) {
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  await page.route("**/api/v1/auth/providers", (route) => route.fulfill(json(providers)));
  // 401 from /auth/me is how the app learns enforcement is on and nobody is signed in.
  await page.route("**/api/v1/auth/me", (route) => route.fulfill(json({ error: "authentication required" }, 401)));
}

test("with no provider the login screen is the password form and nothing else", async ({ page }) => {
  await stub(page, []);
  await page.goto("/index.html");

  await expect(page.locator("#login-form")).toBeVisible();
  await expect(page.locator("#sso-providers")).toBeHidden();
  await expect(page.locator("#sso-error")).toBeHidden();
});

test("a configured provider is offered above the password form", async ({ page }) => {
  await stub(page, [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }]);
  await page.goto("/index.html");

  const button = page.locator("#sso-providers a");
  await expect(button).toHaveText("Sign in with Contoso ID");
  await expect(button).toHaveAttribute("href", "/auth/oidc/start");
  // The password form stays: a provider that is down must not lock an operator out.
  await expect(page.locator("#login-form")).toBeVisible();
});

test("a failed federated login says so without saying why", async ({ page }) => {
  await stub(page, [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }]);
  await page.goto("/index.html?sso=failed");

  const err = page.locator("#sso-error");
  await expect(err).toBeVisible();
  await expect(err).toContainText("did not work");
  // Nothing about which check failed, which account it was, or whether one exists.
  await expect(err).not.toContainText("token");
  await expect(err).not.toContainText("nonce");
});
