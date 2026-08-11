// End-to-end coverage for the company-colour theming in Console → Organization
// (api/web/theme.js + the Appearance card in app.js, viewConsoleOrg). The accent
// variables it sets are plain browser state (CSS custom properties on the document
// root + localStorage), so only a real DOM can verify the wiring: a pick re-tints
// the root, persists, and survives a reload without a flash of the default; Reset
// clears the override. Drives the REAL app shell against a mocked /api/v1 (the
// static harness runs no Go API), exactly like backup.spec.mjs.
import { test, expect } from "@playwright/test";

// installMock answers /auth/me as single-user and returns [] for every other
// /api/v1 call the shell makes on boot (users, connectors, secrets, info…), so the
// Organization view renders fully without a backend.
function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    return route.fulfill({ json: [] });
  });
}

async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
}

const gotoOrg = async (page) => {
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#view")).toContainText("Appearance");
};

const rootAccent = (page) =>
  page.evaluate(() => document.documentElement.style.getPropertyValue("--accent").trim());

const storedTheme = (page) =>
  page.evaluate(() => JSON.parse(localStorage.getItem("atlas.theme") || "null"));

test("the Organization view exposes an Appearance card with brand presets", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await expect(page.locator("#view h2", { hasText: "Appearance" })).toBeVisible();
  await expect(page.locator("#theme-presets .theme-swatch").first()).toBeVisible();
  await expect(page.locator("#theme-color")).toBeVisible();
});

test("picking a preset re-tints the root and persists the choice", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await page.locator('.theme-swatch[data-color="#4f46e5"]').click();

  // The accent variable is set on the document root (which recolours the UI)…
  expect(await rootAccent(page)).toBe("#4f46e5");
  // …a darker hover and a pale soft shade are derived from it…
  const hover = await page.evaluate(() =>
    document.documentElement.style.getPropertyValue("--accent-hover").trim());
  const soft = await page.evaluate(() =>
    document.documentElement.style.getPropertyValue("--accent-soft").trim());
  expect(hover).not.toBe("");
  expect(hover).not.toBe("#4f46e5");
  expect(soft).not.toBe("");
  // …and the choice is stored for next time.
  expect((await storedTheme(page)).color).toBe("#4f46e5");
  // The chosen swatch reads as selected.
  await expect(page.locator('.theme-swatch[data-color="#4f46e5"]')).toHaveClass(/active/);
});

test("a custom hex value applies the accent", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  const hex = page.locator("#theme-hex");
  await hex.fill("#ff8800");
  await hex.press("Enter");

  expect(await rootAccent(page)).toBe("#ff8800");
  expect((await storedTheme(page)).color).toBe("#ff8800");
});

test("the saved theme is applied on reload before the app renders", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);
  await page.locator('.theme-swatch[data-color="#0d7a63"]').click();
  expect(await rootAccent(page)).toBe("#0d7a63");

  // Reload: the inline bootstrap in index.html must re-apply the accent up front,
  // so there is no flash of the stock blue before app.js loads.
  await bootApp(page);
  expect(await rootAccent(page)).toBe("#0d7a63");
});

test("Reset clears the override and restores the default accent", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await page.locator('.theme-swatch[data-color="#be123c"]').click();
  expect(await rootAccent(page)).toBe("#be123c");

  await page.locator("#theme-reset").click();

  // The inline override is removed — the UI falls back to app.css's :root accent.
  expect(await rootAccent(page)).toBe("");
  expect(await storedTheme(page)).toBeNull();
});
