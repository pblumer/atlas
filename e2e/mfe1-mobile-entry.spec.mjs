// MFE1 / ADR-0514: a single prominent mobile path between the two existing
// human-work screens. The Start route remains operator-only under ADR-0209;
// these links never grant permission or introduce another command path.
import { test, expect } from "@playwright/test";

const TASK = {
  key: 101, processInstanceKey: 9001, processDefKey: 1,
  processId: "inspection", elementId: "Check_1", name: "Safety check", priority: 50,
};
const PROCESS = {
  key: 7, processId: "inspection", name: "Safety inspection",
  startFormId: "inspection-start", version: 1, executable: true,
};

function installMock(page, roles = ["operator", "user"], authEnabled = true) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url()).pathname;
    if (url.endsWith("/auth/me")) return route.fulfill({ json: {
      authEnabled, user: authEnabled ? { id: "u1", username: "inspector", roles } : null,
    } });
    if (url.endsWith("/api/v1/tasks")) return route.fulfill({ json: {
      items: [TASK], total: 1, totalExact: true, truncated: false,
    } });
    if (url.endsWith("/api/v1/processes")) return route.fulfill({ json: [PROCESS] });
    if (url.endsWith("/api/v1/me/start-favourites")) return route.fulfill({ json: { processIds: [] } });
    if (url.endsWith("/api/v1/public-links")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
}

const mobileNav = (page) => page.locator(".mfe-entry");
const navLinks = (page) => mobileNav(page).locator("a");

test("MFE1 operator phone: Tasks and Start are equal, reachable destinations", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });

  await expect(mobileNav(page)).toBeVisible();
  await expect(navLinks(page)).toHaveText(["My tasks", "Start process"]);
  await expect(navLinks(page).first()).toHaveAttribute("aria-current", "page");
  await expect(navLinks(page).last()).not.toHaveAttribute("aria-current", "page");
  const rectangles = await navLinks(page).evaluateAll((links) =>
    links.map((a) => ({ width: a.getBoundingClientRect().width, height: a.getBoundingClientRect().height })));
  expect(rectangles[0].height).toBeGreaterThanOrEqual(44);
  expect(rectangles[1].height).toBeGreaterThanOrEqual(44);
  expect(Math.abs(rectangles[0].width - rectangles[1].width)).toBeLessThan(2);

  await navLinks(page).last().click();
  await expect(page.locator("#start-list .tasks-item")).toContainText("Safety inspection");
  await expect(navLinks(page).last()).toHaveAttribute("aria-current", "page");
  await navLinks(page).first().click();
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible();
  await expect(navLinks(page).first()).toHaveAttribute("aria-current", "page");
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});

test("MFE1 user phone: navigation does not offer operator-only Start", async ({ page }) => {
  installMock(page, ["user"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  await expect(mobileNav(page)).toBeVisible();
  await expect(navLinks(page)).toHaveText(["My tasks"]);
  await expect(mobileNav(page).locator('a[href="#/tasks/start"]')).toHaveCount(0);
  await expect(page.locator('#topnav a[href="#/tasks/start"]')).toHaveCount(0);
});

test("MFE1 phone: the mobile destinations do not cover the active task detail", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  const item = page.locator('.tasks-item[data-key="101"]');
  await expect(item).toBeVisible({ timeout: 15000 });
  await expect(mobileNav(page)).toBeVisible();
  await item.click();
  await expect(page.locator("#task-detail")).toBeVisible();
  await expect(mobileNav(page)).toBeHidden();
  await page.locator("#task-back").click();
  await expect(item).toBeVisible();
  await expect(mobileNav(page)).toBeVisible();
});

test("MFE1 desktop: retain existing chrome and no duplicated mobile navigation", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  await expect(mobileNav(page)).toBeHidden();
  await expect(page.locator('#topnav a[href="#/tasks/start"]')).toBeVisible();
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator("#start-list .tasks-item")).toBeVisible({ timeout: 15000 });
  await expect(mobileNav(page)).toBeHidden();
});

test("MFE1 auth-off phone: both destinations follow existing public-mode navigation", async ({ page }) => {
  installMock(page, [], false);
  await page.setViewportSize({ width: 360, height: 740 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="101"]')).toBeVisible({ timeout: 15000 });
  await expect(navLinks(page)).toHaveText(["My tasks", "Start process"]);
});

// The reverse role boundary matters too: an operator without user role may start
// an instance, but is not automatically entitled to see somebody's task inbox.
test("MFE1 operator-only phone: Start remains available without a task inbox shortcut", async ({ page }) => {
  installMock(page, ["operator"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator("#start-list .tasks-item")).toBeVisible({ timeout: 15000 });
  await expect(navLinks(page)).toHaveText(["Start process"]);
  await expect(mobileNav(page).locator('a[href="#/tasks"]')).toHaveCount(0);
});
