// End-to-end coverage for the org-wide company-colour theming in Console →
// Organization (api/web/theme.js + the Appearance card in app.js, viewConsoleOrg,
// ADR-0112). The accent is stored on the server (GET/PUT/DELETE
// /api/v1/settings/theme) and applied for every browser; localStorage is only a
// no-flash cache. Only a real DOM can verify the wiring: a pick re-tints the root
// and persists to the server, the server value drives a fresh load (even with the
// cache cleared), and Reset clears the override. Drives the REAL app shell against
// a STATEFUL mock of the theme endpoint (the static harness runs no Go API), like
// backup.spec.mjs.
import { test, expect } from "@playwright/test";

// installMock answers /auth/me as single-user and models the theme endpoint in
// memory: GET returns the current accent, PUT stores it, DELETE clears it. Every
// other /api/v1 call returns []. It returns a handle whose .accent reflects the
// server's stored value, so a test can assert what was persisted.
function installMock(page, { putStatus } = {}) {
  const state = { accent: "" };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (path.endsWith("/settings/theme")) {
      if (req.method() === "GET") return route.fulfill({ json: { accent: state.accent } });
      if (req.method() === "PUT") {
        if (putStatus && putStatus !== 200) {
          return route.fulfill({ status: putStatus, json: { error: "admin role required" } });
        }
        state.accent = (req.postDataJSON() || {}).accent || "";
        return route.fulfill({ json: { accent: state.accent } });
      }
      if (req.method() === "DELETE") { state.accent = ""; return route.fulfill({ status: 204 }); }
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

const gotoOrg = async (page) => {
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#view")).toContainText("Appearance");
};

const rootAccent = (page) =>
  page.evaluate(() => document.documentElement.style.getPropertyValue("--accent").trim());

test("the Organization view exposes an Appearance card with brand presets", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await expect(page.locator("#view h2", { hasText: "Appearance" })).toBeVisible();
  await expect(page.locator("#theme-presets .theme-swatch").first()).toBeVisible();
  await expect(page.locator("#theme-color")).toBeVisible();
  await expect(page.locator("#view")).toContainText("everyone on this instance");
});

test("picking a preset re-tints the root and persists to the server", async ({ page }) => {
  const server = installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await page.locator('.theme-swatch[data-color="#4f46e5"]').click();

  // The accent variable is set on the document root (which recolours the UI)…
  await expect.poll(() => rootAccent(page)).toBe("#4f46e5");
  // …a darker hover and a pale soft shade are derived from it…
  expect(await page.evaluate(() =>
    document.documentElement.style.getPropertyValue("--accent-hover").trim())).not.toBe("#4f46e5");
  // …and the choice is persisted org-wide on the server.
  await expect.poll(() => server.accent).toBe("#4f46e5");
  // The chosen swatch reads as selected.
  await expect(page.locator('.theme-swatch[data-color="#4f46e5"]')).toHaveClass(/active/);
  await expect(page.locator("#toast")).toContainText("everyone");
});

test("a custom hex value applies and persists the accent", async ({ page }) => {
  const server = installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  const hex = page.locator("#theme-hex");
  await hex.fill("#ff8800");
  await hex.press("Enter");

  await expect.poll(() => rootAccent(page)).toBe("#ff8800");
  await expect.poll(() => server.accent).toBe("#ff8800");
});

test("the server's accent drives a fresh load even with no local cache", async ({ page }) => {
  const server = installMock(page);
  await bootApp(page);
  await gotoOrg(page);
  await page.locator('.theme-swatch[data-color="#0d7a63"]').click();
  await expect.poll(() => server.accent).toBe("#0d7a63");

  // Wipe the local no-flash cache, then reload: the accent must come back purely
  // from the server (syncFromServer), proving the setting is org-wide, not
  // per-browser.
  await page.evaluate(() => localStorage.clear());
  await bootApp(page);
  await expect.poll(() => rootAccent(page)).toBe("#0d7a63");
});

test("Reset clears the override on the server and restores the default accent", async ({ page }) => {
  const server = installMock(page);
  await bootApp(page);
  await gotoOrg(page);

  await page.locator('.theme-swatch[data-color="#be123c"]').click();
  await expect.poll(() => server.accent).toBe("#be123c");

  await page.locator("#theme-reset").click();

  await expect.poll(() => server.accent).toBe("");
  // The override is removed — the UI falls back to app.css's :root accent.
  await expect.poll(() => rootAccent(page)).toBe("");
});

test("a non-admin's rejected write is surfaced and the preview reverts", async ({ page }) => {
  installMock(page, { putStatus: 403 });
  await bootApp(page);
  await gotoOrg(page);

  await page.locator('.theme-swatch[data-color="#7c3aed"]').click();

  // The optimistic preview is rolled back to the default, and the error is shown.
  await expect(page.locator("#toast")).toContainText("admin");
  await expect.poll(() => rootAccent(page)).toBe("");
});
