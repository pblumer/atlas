// MFE1: a compact mobile work shell plus passive, role-gated inbox/incident refresh.
// Real app DOM, mocked HTTP boundary. These tests precede the implementation.
import { test, expect } from "@playwright/test";

const task = (key, name) => ({
  key, name, processId: "safety", elementId: "inspect",
  processInstanceKey: 9001, elementInstanceKey: 9002, processDefKey: 1, priority: 50,
});

function stub(page, roles = ["operator", "user"]) {
  const calls = { tasks: 0, stats: 0 };
  let tasks = [task(101, "Original inspection")], incidents = 0;
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: {
        id: "u1", username: "inspector", roles,
      } } });
    }
    if (path === "/api/v1/tasks") {
      calls.tasks++;
      return route.fulfill({ json: { items: tasks, total: tasks.length, totalExact: true,
        truncated: false } });
    }
    if (path === "/api/v1/stats") {
      calls.stats++;
      return route.fulfill({ json: { unresolvedIncidents: incidents } });
    }
    if (path === "/api/v1/tasks/folders" || path.endsWith("/users/assignable")) {
      return route.fulfill({ json: [] });
    }
    return route.fulfill({ json: [] });
  });
  return { calls, setTasks: (value) => { tasks = value; },
    setIncidents: (value) => { incidents = value; } };
}

test("mobile shell prioritises the app, moves secondary actions behind More and keeps role-scoped destinations", async ({ page }) => {
  stub(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator("#task-list")).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".topbar #app-name")).toBeVisible();
  await expect(page.locator("#topnav")).toBeHidden();
  await expect(page.locator("#global-search")).toBeHidden();
  await expect(page.locator("#mobile-more-btn")).toBeVisible();
  await page.locator("#mobile-more-btn").click();
  const menu = page.locator("#mobile-more-menu");
  await expect(menu).toBeVisible();
  await expect(menu.locator('a[href="#/tasks/start"]')).toBeVisible();
  await expect(menu.locator('a[href="#/operations/incidents"]')).toBeVisible();
  await expect(menu.locator("[data-mobile-action=search]")).toBeVisible();
  await menu.locator("[data-mobile-action=search]").click();
  await expect(page.locator("#sp-input")).toBeVisible();
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});

test("mobile worker does not see privileged Start or Incidents and a single My tasks destination is not duplicated", async ({ page }) => {
  stub(page, ["user"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator("#task-list")).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".mfe-entry")).toBeHidden();
  await page.locator("#mobile-more-btn").click();
  const menu = page.locator("#mobile-more-menu");
  await expect(menu.locator('a[href="#/tasks/start"]')).toHaveCount(0);
  await expect(menu.locator('a[href="#/operations/incidents"]')).toHaveCount(0);
});

test("desktop keeps its existing chrome and no mobile More button", async ({ page }) => {
  stub(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator("#task-list")).toBeVisible({ timeout: 15000 });
  await expect(page.locator("#topnav")).toBeVisible();
  await expect(page.locator("#mobile-more-btn")).toBeHidden();
  await expect(page.locator("#global-search")).toBeVisible();
});

test("10-second foreground polling notices new work and incidents without touching an opened task", async ({ page }) => {
  await page.clock.install();
  const api = stub(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  api.setTasks([task(101, "Original inspection"), task(102, "New inspection")]);
  api.setIncidents(3);
  await page.clock.fastForward(11000);
  await expect(page.locator('.tasks-item[data-key="102"]')).toBeVisible();
  await page.locator("#mobile-more-btn").click();
  await expect(page.locator('#mobile-more-menu .nav-badge[data-badge="incidents"]')).toHaveText("3");
  await expect(page.locator("#mobile-more-count")).toHaveText("3");
  await expect(page.locator("#mobile-more-btn")).toHaveAttribute("aria-expanded", "true");
  await expect(page.locator('#mobile-more-menu .nav-badge[data-badge="tasks"]')).toHaveText("2");
  await page.locator("#mobile-more-btn").click();
  await expect(page.locator("#mobile-more-btn")).toHaveAttribute("aria-expanded", "false");

  await page.locator('.tasks-item[data-key="101"]').click();
  await expect(page.locator("#task-detail")).toBeVisible();
  const detail = await page.locator(".tasks-detail-head h1").textContent();
  api.setTasks([task(102, "New inspection")]);
  await page.clock.fastForward(11000);
  await expect(page.locator(".tasks-detail-head h1")).toHaveText(detail);
  await expect(page.locator("#task-detail")).toBeVisible();
  expect(api.calls.tasks).toBeGreaterThanOrEqual(2);
  expect(api.calls.stats).toBeGreaterThanOrEqual(2);
});

test("background tabs do not poll and foreground reactivation fetches immediately", async ({ page }) => {
  await page.clock.install();
  const api = stub(page, ["user"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  await expect.poll(() => api.calls.tasks).toBeGreaterThan(0);
  const before = api.calls.tasks;
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(31000);
  expect(api.calls.tasks).toBe(before);
  expect(api.calls.stats).toBe(0); // the user role has no operator incident visibility
  api.setTasks([task(102, "After foreground")]);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(page.locator('.tasks-item[data-key="102"]')).toBeVisible();
  expect(api.calls.tasks).toBeGreaterThan(before);
});

test("task-only role never polls incident counts", async ({ page }) => {
  await page.clock.install();
  const api = stub(page, ["user"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  await page.clock.fastForward(31000);
  expect(api.calls.tasks).toBeGreaterThanOrEqual(2);
  expect(api.calls.stats).toBe(0);
});
