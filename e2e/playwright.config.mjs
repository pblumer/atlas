import { defineConfig } from "@playwright/test";

const PORT = Number(process.env.PORT) || 8199;
const baseURL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: ".",
  testMatch: "**/*.spec.mjs",
  timeout: 30000,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"]],
  use: {
    baseURL,
    headless: true,
    browserName: "chromium",
    viewport: { width: 900, height: 520 },
    trace: "retain-on-failure",
    // The catalogue page folds every section until a browser opens it, and remembers
    // which ones it did (catalog-admin.js pageSection). Every spec but the one about
    // that fold drives controls inside the sections, so each starts as a maintainer
    // who keeps them all open; catalog-sections.spec.mjs starts from nothing.
    storageState: {
      cookies: [],
      origins: [{
        origin: baseURL,
        localStorage: [{
          name: "atlas.catalog.pageSections",
          value: JSON.stringify(Object.fromEntries(["products", "content", "relations", "publish",
            "settings", "appearance", "sharing", "transfer", "purge"].map((k) => [k, true]))),
        }],
      }],
    },
  },
  // Serve the repo's real web assets + the test-only harness; Playwright waits for it.
  webServer: {
    command: "node server.mjs",
    url: `${baseURL}/harness.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 30000,
  },
});
