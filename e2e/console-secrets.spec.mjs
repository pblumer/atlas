// Console › Administration › Secrets: the encrypted vault (ADR-0069) on a page of its
// own. It was the last card of the Workers page; the handbook has linked
// "Console → Secrets" (#/console/secrets) all along, and that route used to fall
// through to the dashboard.
import { test, expect } from "@playwright/test";

const ADMIN = { id: "usr_root", username: "root", roles: ["admin"] };

const WORKERS = [{ id: "c1", name: "clio-prod", kind: "clio", role: "owner", endpoint: "https://clio.example", credentialsRef: "clio_token", enabled: true }];
const SECRETS = [{ name: "clio_token", keyId: "c9ee8679a00c1777", updatedAt: 1792000000 }];

// vault is what GET /api/v1/secrets answers: "ok", "denied" or "unconfigured".
function installMock(page, { vault = "ok" } = {}) {
  const puts = [];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: true, user: ADMIN } });
    if (path === "/api/v1/connectors") return route.fulfill({ json: WORKERS });
    if (path === "/api/v1/secrets") {
      if (vault === "denied") return route.fulfill({ status: 403, json: { error: "admin role required" } });
      if (vault === "unconfigured") return route.fulfill({ status: 503, json: { error: "vault not configured" } });
      return route.fulfill({ json: SECRETS });
    }
    if (path.startsWith("/api/v1/secrets/") && req.method() === "PUT") {
      puts.push({ name: decodeURIComponent(path.slice("/api/v1/secrets/".length)), body: req.postDataJSON() });
      return route.fulfill({ json: { name: "risk_token", keyId: "c9ee8679a00c1777", updatedAt: 1792000001 } });
    }
    return route.fulfill({ json: [] });
  });
  return puts;
}

async function boot(page, hash) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

test("Administration leads to Secrets, and the page lists the vault with what uses each secret", async ({ page }) => {
  installMock(page);
  await boot(page, "#/console");
  await page.locator("#topnav .topnav-group-btn").click();
  await page.locator("#topnav .topnav-group .dropdown-menu a", { hasText: "Secrets" }).click();
  await expect(page).toHaveURL(/#\/console\/secrets$/);
  await expect(page.locator("#view h1")).toHaveText("Secrets");
  const row = page.locator('#secret-rows tr[data-name="clio_token"]');
  await expect(row).toContainText("used by clio-prod");
  await expect(row).toContainText("c9ee8679a00c1777");
  expect(page.__errors).toEqual([]);
});

test("the handbook's link lands on the vault, not on the dashboard", async ({ page }) => {
  installMock(page);
  await boot(page, "#/console/secrets");
  await expect(page.locator("#view h1")).toHaveText("Secrets");
  expect(await page.evaluate(() => location.hash)).toBe("#/console/secrets");
  expect(page.__errors).toEqual([]);
});

test("a new secret is saved, and the page it is saved from stays the vault", async ({ page }) => {
  const puts = installMock(page);
  await boot(page, "#/console/secrets");
  await page.click("#new-secret");
  await page.fill('.secret-form [name="name"]', "risk_token");
  await page.fill('.secret-form [name="value"]', "s3cr3t");
  await page.click('.secret-form button[type="submit"]');
  await expect.poll(() => puts.length).toBe(1);
  expect(puts[0]).toEqual({ name: "risk_token", body: { value: "s3cr3t" } });
  await expect(page.locator("#view h1")).toHaveText("Secrets");
  await expect(page.locator("#secret-rows")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("the vault says so when it may not be used, and when it is not configured", async ({ page }) => {
  installMock(page, { vault: "denied" });
  await boot(page, "#/console/secrets");
  await expect(page.locator("#view h1")).toHaveText("Secrets");
  await expect(page.locator("#view")).toContainText("Managing secrets requires the admin role.");
  await page.unrouteAll({ behavior: "ignoreErrors" });

  installMock(page, { vault: "unconfigured" });
  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view h1")?.textContent === "Secrets", null, { timeout: 15000 });
  await expect(page.locator("#view")).toContainText("ATLAS_VAULT_KEY");
  expect(page.__errors).toEqual([]);
});
