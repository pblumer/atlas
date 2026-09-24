// The service portal when a sign-in is required and nobody has one yet
// (api/web/shop.js).
//
// The case behind it, reported from a running server: an instance started with
// --auth, a visitor who opens the portal directly without a session. Every route
// the page reads answers 401 — the catalogue read was swallowed and drawn as "no
// catalogue is assigned to you", which is a statement about entitlement and not
// about authentication, and the orders read was not swallowed at all. So what a
// visitor got was an error line with an HTTP status in it over an empty page,
// with nothing saying a sign-in was needed and nothing offering one.
//
// The portal boots itself against the real assets here, with only the API stubbed,
// so these drive the shipped shop.js rather than a copy of its logic.
import { test, expect } from "@playwright/test";

const json = (body, status = 200) => ({
  status,
  contentType: "application/json; charset=utf-8",
  body: JSON.stringify(body),
});

const REFUSED = json({ error: "authentication required" }, 401);

const CATALOG = {
  id: "demo",
  texts: { de: "Demokatalog", en: "Demo catalogue" },
  theme: {},
};
const RELEASE = {
  id: "r1",
  items: [{ id: "laptop", texts: { de: "Laptop", en: "Laptop" } }],
  includes: {},
  options: {},
};

// stub answers what the portal reads while booting. The catch-all is registered
// first on purpose: Playwright matches the most recently registered route, so
// everything named after it wins.
async function stub(page, { me, providers = [], registration = { enabled: false }, signedIn = false }) {
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  await page.route("**/api/v1/shop/catalog", (route) =>
    route.fulfill(signedIn ? json(CATALOG) : REFUSED));
  await page.route("**/api/v1/catalogs/*/releases", (route) =>
    route.fulfill(signedIn ? json([RELEASE]) : REFUSED));
  await page.route("**/api/v1/orders", (route) =>
    route.fulfill(signedIn ? json([]) : REFUSED));
  await page.route("**/api/v1/inventory", (route) =>
    route.fulfill(signedIn ? json({ items: [] }) : REFUSED));
  await page.route("**/api/v1/shop/favourites", (route) =>
    route.fulfill(signedIn ? json({ itemIds: [] }) : REFUSED));
  await page.route("**/api/v1/principals", (route) =>
    route.fulfill(signedIn ? json([]) : REFUSED));
  await page.route("**/api/v1/auth/providers", (route) => route.fulfill(json(providers)));
  await page.route("**/api/v1/settings/registration", (route) => route.fulfill(json(registration)));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill(me));
}

const signin = (page) => page.locator(".signin");

// Every test here watches for a page error. The sign-in is drawn by the same
// render() that draws the catalogue, and the half of it that is skipped — the nav,
// the cascade, the config forms — is exactly where a missing guard would throw.
test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
});
test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

test("a portal that requires a sign-in asks for one", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.goto("/shop.html?lang=en");

  await expect(signin(page)).toBeVisible();
  await expect(signin(page)).toContainText("Please sign in");
  await expect(page.locator('.signin input[name="username"]')).toBeVisible();
  await expect(page.locator('.signin input[name="password"]')).toBeVisible();

  // And nothing behind it. Every route below this screen answers 401, so a shell
  // drawn around empty lists would tell somebody they are the audience for
  // nothing when they are simply not signed in.
  await expect(page.locator("nav.nav")).toHaveCount(0);
  await expect(page.locator(".cascade")).toHaveCount(0);

  // A first visit, not an expired one. This is what the gate buys that arriving
  // here by way of a refused call does not: a page that reached the sign-in
  // through the first 401 it happened to hit would tell somebody who has never
  // had a session that theirs ran out.
  await expect(signin(page)).not.toContainText("run out");
});

test("the refusal is not reported as a broken portal", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.goto("/shop.html?lang=en");
  await expect(signin(page)).toBeVisible();

  // The two sentences the page used to show for this: the generic failure with an
  // HTTP status behind it, and the entitlement answer that is about a different
  // question entirely.
  await expect(page.locator("#app")).not.toContainText("401");
  await expect(page.locator("#app")).not.toContainText("No catalogue is assigned to you");
});

test("the sign-in says which installation is asking", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.goto("/shop.html?lang=en");
  await expect(signin(page)).toBeVisible();
  // The instance's own mark, from the endpoint that is public exactly so a screen
  // shown before any session can read it.
  await expect(page.locator("header img.mark")).toHaveAttribute("src", "/api/v1/settings/logo");
});

test("an identity provider is offered where one is configured", async ({ page }) => {
  await stub(page, {
    me: REFUSED,
    providers: [{ name: "Entra ID", start: "/api/v1/auth/sso/entra/start" }],
  });
  await page.goto("/shop.html?lang=en");

  const provider = page.locator(".signin .provider");
  await expect(provider).toHaveText("Sign in with Entra ID");
  // The page it must come back to travels with it. Without that the callback's
  // default applies and a portal visitor is signed in on the Console.
  await expect(provider).toHaveAttribute(
    "href", "/api/v1/auth/sso/entra/start?returnTo=%2Fshop.html");
  // The password form stands beside it: an installation may federate one login and
  // still keep local accounts.
  await expect(page.locator('.signin input[name="password"]')).toBeVisible();
});

test("no provider configured leaves the password form alone", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.goto("/shop.html?lang=en");
  await expect(signin(page)).toBeVisible();
  await expect(page.locator(".signin .provider")).toHaveCount(0);
});

test("signing in loads the catalogue and stays on the portal", async ({ page }) => {
  let session = false;
  await page.route("**/api/v1/**", (route) => route.fulfill(json({})));
  await page.route("**/api/v1/shop/catalog", (route) =>
    route.fulfill(session ? json(CATALOG) : REFUSED));
  await page.route("**/api/v1/catalogs/*/releases", (route) =>
    route.fulfill(session ? json([RELEASE]) : REFUSED));
  await page.route("**/api/v1/orders", (route) => route.fulfill(session ? json([]) : REFUSED));
  await page.route("**/api/v1/inventory", (route) =>
    route.fulfill(session ? json({ items: [] }) : REFUSED));
  await page.route("**/api/v1/shop/favourites", (route) =>
    route.fulfill(session ? json({ itemIds: [] }) : REFUSED));
  await page.route("**/api/v1/principals", (route) => route.fulfill(session ? json([]) : REFUSED));
  await page.route("**/api/v1/auth/providers", (route) => route.fulfill(json([])));
  await page.route("**/api/v1/settings/registration", (route) =>
    route.fulfill(json({ enabled: false })));
  await page.route("**/api/v1/auth/login", (route) => {
    session = true;
    route.fulfill(json({ ok: true }));
  });
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill(session
      ? json({ authEnabled: true, user: { id: "u1", username: "ada", displayName: "Ada", roles: ["user"] } })
      : REFUSED));

  await page.goto("/shop.html?lang=en");
  await expect(signin(page)).toBeVisible();

  await page.locator('.signin input[name="username"]').fill("ada");
  await page.locator('.signin input[name="password"]').fill("correct horse");
  await page.locator(".signin button.primary").click();

  // The catalogue, on the portal. Not the Console: somebody who followed a link to
  // order a laptop holds no Console role, and landing there is landing in a shell
  // whose every entry is missing.
  await expect(signin(page)).toHaveCount(0);
  await expect(page.locator("nav.nav")).toBeVisible();
  await expect(page.locator("header h1")).toHaveText("Demo catalogue");
  expect(new URL(page.url()).pathname).toBe("/shop.html");
});

test("a wrong password is named as one", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill(json({ error: "invalid credentials" }, 401)));
  await page.goto("/shop.html?lang=en");

  await page.locator('.signin input[name="username"]').fill("ada");
  await page.locator('.signin input[name="password"]').fill("wrong");
  await page.locator(".signin button.primary").click();

  await expect(page.locator(".signin .error")).toContainText("not right");
});

test("a failed attempt keeps the name that was typed", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill(json({ error: "invalid credentials" }, 401)));
  await page.goto("/shop.html?lang=en");

  await page.locator('.signin input[name="username"]').fill("ada");
  await page.locator('.signin input[name="password"]').fill("wrong");
  await page.locator(".signin button.primary").click();
  await expect(page.locator(".signin .error")).toBeVisible();

  // The name survives, the password does not. An attempt redraws the page twice,
  // and a field rebuilt from nothing makes somebody who mistyped one thing retype
  // both.
  await expect(page.locator('.signin input[name="username"]')).toHaveValue("ada");
  await expect(page.locator('.signin input[name="password"]')).toHaveValue("");
});

test("the throttle is not reported as a wrong password", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill(json({ error: "too many attempts" }, 429)));
  await page.goto("/shop.html?lang=en");

  await page.locator('.signin input[name="username"]').fill("ada");
  await page.locator('.signin input[name="password"]').fill("correct horse");
  await page.locator(".signin button.primary").click();

  // The one wait that cannot be shortened by typing. Told it was the password,
  // somebody spends a quarter of an hour retyping a password that is already right.
  const err = page.locator(".signin .error");
  await expect(err).toContainText("was not checked");
  await expect(err).not.toContainText("not right");
});

test("the language switch works before the sign-in, not only after it", async ({ page }) => {
  await stub(page, { me: REFUSED });
  await page.goto("/shop.html?lang=en");
  await expect(signin(page)).toContainText("Please sign in");

  await page.locator(".langs button", { hasText: "DE" }).click();
  await expect(signin(page)).toContainText("Bitte melden Sie sich an");
});

test("a portal that enforces no sign-in is unchanged", async ({ page }) => {
  await stub(page, { me: json({ authEnabled: false, user: null }), signedIn: true });
  await page.goto("/shop.html?lang=en");

  // Single-user mode: the catalogue is readable and an order is not, which is the
  // page's existing answer and not this screen's business.
  await expect(signin(page)).toHaveCount(0);
  await expect(page.locator("nav.nav")).toBeVisible();
  await expect(page.locator("header h1")).toHaveText("Demo catalogue");
});

test("a server that cannot answer is not read as nobody being signed in", async ({ page }) => {
  // Unreadable is not forbidden. An instance whose session store is broken answers
  // 500, and a page that read that as "you are anonymous" would show every one of
  // its customers a login form that cannot possibly work — while the catalogue
  // behind it is answering perfectly well.
  await stub(page, { me: json({ error: "boom" }, 500), signedIn: true });
  await page.goto("/shop.html?lang=en");

  await expect(signin(page)).toHaveCount(0);
  await expect(page.locator("nav.nav")).toBeVisible();
  await expect(page.locator("header h1")).toHaveText("Demo catalogue");
});

test("a session that runs out while the page is open asks again", async ({ page }) => {
  // The gate passes — /auth/me still answers — and the session dies before the
  // orders are read. That is the same refusal one step later, and reporting it as
  // a failure is the original defect with a different trigger.
  await stub(page, { me: json({ authEnabled: true, user: { id: "u1", username: "ada", roles: ["user"] } }) });
  await page.route("**/api/v1/shop/catalog", (route) => route.fulfill(json(CATALOG)));
  await page.route("**/api/v1/catalogs/*/releases", (route) => route.fulfill(json([RELEASE])));
  await page.route("**/api/v1/orders", (route) => route.fulfill(REFUSED));
  await page.goto("/shop.html?lang=en");

  await expect(signin(page)).toBeVisible();
  await expect(signin(page)).toContainText("session has run out");
  await expect(page.locator("#app")).not.toContainText("401");

  // And the catalogue that was resolved for the session that ended is let go.
  // A sign-in drawn over it would ask who you are under somebody's catalogue name
  // and brand — the page saying two things at once.
  await expect(page.locator("header h1")).toHaveText("Shop");
  await expect(page.locator("header img.mark")).toHaveAttribute("src", "/api/v1/settings/logo");
});
