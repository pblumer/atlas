// End-to-end coverage for the endpoints a worker reports on Operations → Workers
// (ADR-0471 S2b). A worker that keeps its own configuration names, beside each worker it
// holds, where that worker connects — scheme, host and port only. The page shows it next
// to the name, so an operator reads where a worker process reaches without asking the
// machine it runs on.
import { test, expect } from "@playwright/test";

const NOW = Date.UTC(2026, 9, 23, 12, 0, 0);

function installMock(page, workers) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/workers")) {
      return route.fulfill({
        json: { workers, supervised: [], unservedConnectors: [], jobTypeCollisions: [], breakers: [], types: [] },
      });
    }
    return route.fulfill({ json: [] });
  });
}

const row = (over) => ({
  worker: "extern-1", types: { "io.atlas.clio.query": 1 }, leased: 0, pulled: 4, completed: 4,
  failed: 0, firstSeen: (NOW - 120_000) * 1e6, lastSeen: (NOW - 5_000) * 1e6, ...over,
});

async function openWorkers(page) {
  await page.clock.setFixedTime(NOW);
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await page.evaluate(() => { location.hash = "#/operations/workers"; });
  await expect(page.locator('table[data-dt-key="wk-workers"]')).toBeVisible();
}

test("a held worker shows where it connects, and one without a report shows its name alone", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, [row({
    connectors: ["clio", "constructor", "mail"],
    endpoints: { clio: "https://clio.example.com:8443" },
  })]);
  await openWorkers(page);

  const held = page.locator('table[data-dt-key="wk-workers"] tbody tr td:nth-child(3) .pill-kv');
  await expect(held).toHaveCount(3);
  await expect(held.nth(0)).toHaveText("clio → https://clio.example.com:8443");
  // A name that is also a property every object inherits is still a name without a report.
  await expect(held.nth(1)).toHaveText("constructor");
  await expect(held.nth(2)).toHaveText("mail");
  expect(errors).toEqual([]);
});

test("a reported endpoint is shown as text, never as markup", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, [row({ connectors: ["erp"], endpoints: { erp: '<img src=x onerror="window.hit=1">' } })]);
  await openWorkers(page);

  const cell = page.locator('table[data-dt-key="wk-workers"] tbody tr td:nth-child(3)');
  await expect(cell).toContainText('erp → <img src=x onerror="window.hit=1">');
  await expect(cell.locator("img")).toHaveCount(0);
  expect(await page.evaluate(() => window.hit)).toBeUndefined();
  expect(errors).toEqual([]);
});
