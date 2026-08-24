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
  },
  // Serve the repo's real web assets + the test-only harness; Playwright waits for it.
  webServer: {
    command: "node server.mjs",
    url: `${baseURL}/harness.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 30000,
  },
});
