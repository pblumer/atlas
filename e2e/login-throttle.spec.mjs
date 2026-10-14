// What the login screen says when the *attempt* was refused rather than the
// password (api/web/app.js, viewLogin; the throttle itself is ADR-0197).
//
// The case behind it: an operator seeded an instance, mistyped the generated
// admin password a few times, and from the sixth attempt on met "Invalid username
// or password." for a password that was by then correct — the throttle refuses an
// attempt before the password is ever checked, and the screen reported that as a
// credential failure. They went hunting for a wrong password for a quarter of an
// hour, and the thing that finally "fixed" it was restarting the server, which is
// exactly the one action that clears the throttle's in-memory buckets.
//
// So the screen must tell the two apart. It may: the server already answers 429
// with a message, and saying so leaks nothing the 401 is careful to withhold —
// the throttle counts attempts against names that do not exist too, which is what
// keeps it from becoming a directory of the ones that do.
//
// The app boots against the real assets with only the API stubbed, so these
// assert the shipped app.js rather than a copy of its logic.
import { test, expect } from "@playwright/test";

const json = (body, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

// signIn brings up the login screen, answers POST /auth/login with the given
// response, submits the form and hands back the error line.
async function signIn(page, answer) {
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  // 401 from /auth/me is how the app learns enforcement is on and nobody is signed in.
  await page.route("**/api/v1/auth/me", (route) => route.fulfill(json({ error: "authentication required" }, 401)));
  await page.route("**/api/v1/auth/login", (route) => route.fulfill(answer));
  await page.goto("/index.html");

  await expect(page.locator("#login-form")).toBeVisible();
  await page.fill("#login-form input[name=username]", "admin");
  await page.fill("#login-form input[name=password]", "2829547fc4c5040328cb94ac");
  await page.click("#login-form button[type=submit]");
  return page.locator("#login-error");
}

test("a refused password is still called a refused password", async ({ page }) => {
  const err = await signIn(page, json({ error: "invalid credentials" }, 401));

  await expect(err).toBeVisible();
  await expect(err).toHaveText("Invalid username or password.");
});

test("a throttled attempt does not blame the password", async ({ page }) => {
  const err = await signIn(page, json({ error: "too many login attempts; try again shortly" }, 429));

  await expect(err).toBeVisible();
  await expect(err).toContainText(/too many/i);
  // The whole point: the password may well be right, and the screen must not send
  // somebody looking for a new one.
  await expect(err).not.toContainText("Invalid username or password.");
});

test("a server that could not answer is not reported as a wrong password", async ({ page }) => {
  const err = await signIn(page, json({ error: "login: userstore: read dir: permission denied" }, 500));

  await expect(err).toBeVisible();
  await expect(err).not.toContainText("Invalid username or password.");
  // The server's own wording stays in its log: a pre-auth screen is not the place
  // to publish what broke inside the instance.
  await expect(err).not.toContainText("read dir");
});

// The OAuth consent screen (api/web/oauth-consent.html) carries its own sign-in
// form and, by its own comment, deliberately the same words. So it inherits the
// same defect and the same fix: the words that are deliberate are the vague ones a
// 401 gets, not the ones a throttled attempt was getting by accident.
async function consentSignIn(page, answer) {
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  await page.route("**/api/v1/oauth/authorize-context*", (route) =>
    route.fulfill(json({ clientName: "Contoso Reports", signedInAs: null })));
  await page.route("**/api/v1/auth/login", (route) => route.fulfill(answer));
  await page.goto("/oauth-consent.html?client_id=abc&response_type=code");

  await expect(page.locator("#login")).toBeVisible();
  await page.fill("#login input[name=username]", "admin");
  await page.fill("#login input[name=password]", "2829547fc4c5040328cb94ac");
  await page.click("#login button[type=submit]");
  return page.locator("#login-error");
}

test("the consent screen still calls a refused password a refused password", async ({ page }) => {
  const err = await consentSignIn(page, json({ error: "invalid credentials" }, 401));

  await expect(err).toBeVisible();
  await expect(err).toHaveText("Invalid username or password.");
});

test("the consent screen does not blame the password for a throttled attempt", async ({ page }) => {
  const err = await consentSignIn(page, json({ error: "too many login attempts; try again shortly" }, 429));

  await expect(err).toBeVisible();
  await expect(err).toContainText(/too many/i);
  await expect(err).not.toContainText("Invalid username or password.");
});
