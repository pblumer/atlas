// The account menu on a server that requires a login (api/web/app.js,
// updateAccount).
//
// The case behind it: an operator upgraded an authenticated instance, met the login
// screen, opened the account menu and read "Single-user mode" — the label a server
// with *no* login wears. They concluded auth had turned itself off and went looking
// for the deploy that had done it, while the server was in fact refusing them
// exactly as configured. The menu's `else` branch fired on "nobody is signed in" and
// said the one thing that is only true when nobody *can* sign in; the tooltip beside
// it had told the two apart all along.
//
// The app boots itself against the real assets here, with only the API stubbed, so
// what these assert is the shipped app.js and not a copy of its logic.
import { test, expect } from "@playwright/test";

const json = (body, status = 200) => ({
  status,
  contentType: "application/json; charset=utf-8",
  body: JSON.stringify(body),
});

// stubAPI answers the calls the shell makes while booting. Everything but /auth/me
// is incidental to these tests — the brand accent, the logo flag, the registration
// link — and the app already treats each of them as optional.
// The catch-all goes first on purpose: Playwright matches the most recently
// registered route, so /auth/me must be registered after it to win.
async function stubAPI(page, me) {
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill(me));
}

const menuText = (page) => page.evaluate(() => window.__acctMenu?.textContent ?? "");
const avatarTitle = (page) => page.locator(".topbar .avatar").getAttribute("title");

test("an enforced login is not called single-user mode", async ({ page }) => {
  // 401 from /auth/me is how the app learns enforcement is on and nobody is signed
  // in — the same answer the server's own middleware gives an anonymous caller.
  await stubAPI(page, json({ error: "authentication required" }, 401));
  await page.goto("/index.html");

  await expect(page.locator("#login-form")).toBeVisible();
  expect(await menuText(page)).toBe("Not signed in");
  expect(await avatarTitle(page)).toBe("Account");
});

test("a server with no login still says single-user mode", async ({ page }) => {
  // The other half: with enforcement off the label is the truth, and the app must
  // keep saying it — the fix is a distinction, not a removal.
  await stubAPI(page, json({ authEnabled: false, user: null }));
  await page.goto("/index.html");

  await expect(page.locator("#login-form")).toHaveCount(0);
  expect(await menuText(page)).toBe("Single-user mode");
  expect(await avatarTitle(page)).toBe("Single-user mode");
});

test("a signed-in user is named in the menu", async ({ page }) => {
  await stubAPI(page, json({ authEnabled: true, user: { username: "patrick", displayName: "Patrick Blumer" } }));
  await page.goto("/index.html");

  await expect(page.locator("#login-form")).toHaveCount(0);
  const text = await menuText(page);
  expect(text).toContain("Signed in as");
  expect(text).toContain("patrick");
  expect(await avatarTitle(page)).toBe("Patrick Blumer");
});
