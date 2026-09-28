// The Modeler home's deployed-process table marks a definition deployed while latest
// was frozen at deploy time (api/web/app.js, api/web/frozen-decisions.js, ADR-0423).
//
// That table is where an owner opens and redeploys a process, and redeploying is what
// makes a frozen definition follow each new decision version — so the mark belongs
// on its row, and on no row whose definition follows latest.
import { test, expect } from "@playwright/test";

const PROCESSES = [
  // Frozen on v1 while v3 is deployed: warned.
  { key: 20, processId: "orders", name: "Orders", version: 2, deployedAt: 1789000000, active: true, executable: true,
    frozenDecisions: [{ decisionId: "eligibility", key: 11, version: 1, latestKey: 33, latestVersion: 3, behind: true }] },
  // Frozen, but on what is still the newest version: marked, not warned.
  { key: 21, processId: "audit", name: "Audit", version: 1, deployedAt: 1789000000, active: true, executable: true,
    frozenDecisions: [{ decisionId: "eligibility", key: 33, version: 3, latestKey: 33, latestVersion: 3, behind: false }] },
  // Follows latest: nothing to say.
  { key: 22, processId: "billing", name: "Billing", version: 1, deployedAt: 1789000000, active: true, executable: true },
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler");
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const row = (page, name) => page.locator("tr", { has: page.locator(`a[href^="#/modeler/d/"] b`, { hasText: name }) });

test("a frozen definition's row says so, and warns when a newer version is deployed", async ({ page }) => {
  const orders = row(page, "Orders").locator(".frozen-mark");
  await expect(orders).toHaveText("Decision frozen · newer deployed");
  await expect(orders).toHaveClass(/\bwarn\b/);
  await expect(orders).toHaveAttribute("title", /eligibility runs v1, the newest is v3/);

  const audit = row(page, "Audit").locator(".frozen-mark");
  await expect(audit).toHaveText("Decision frozen");
  await expect(audit).not.toHaveClass(/\bwarn\b/);

  await expect(row(page, "Billing")).toBeVisible();
  await expect(row(page, "Billing").locator(".frozen-mark")).toHaveCount(0);
});
