// End-to-end coverage for a menu's flyout — the "Move to" submenu on an artifact row
// (api/web/app.js, api/web/app.css).
//
// Two things were wrong with it. It opened to the *left*, which is not where a submenu
// opens anywhere else, so the hand went the wrong way first. And reaching it was a knack:
// the flyout is position:fixed (a card's overflow would otherwise clip it) and was shown
// by `.submenu:hover`, so a hand moving diagonally from its row to the flyout crossed the
// menu rows in between — each of them outside the pair — and the flyout closed under it
// before it arrived.
//
// It opens to the right now, flush against the parent menu, and it is held open for a
// moment after the pointer leaves. Settling anywhere else still closes it; dismissing the
// menu closes it at once.
//
// Driven through the real app shell against a mocked /api/v1, like
// router-reentrancy.spec.mjs — the machinery under test is the router's own menus.
import { test, expect } from "@playwright/test";

// Projects are served from /applications (ADR-0034 renamed them); the UI still calls
// them projects in the menu, which is what a reader of the flyout sees.
const PROJECTS = [
  { id: "p1", name: "Onboarding", myRole: "owner" },
  { id: "p2", name: "CSV_Test", myRole: "owner" },
  { id: "p3", name: "Executable Processes", myRole: "owner" },
  { id: "p4", name: "Entscheidungs-Tests", myRole: "owner" },
];
const DRAFTS = [{ processId: "alter_neu", name: "Alter neu", savedAt: 1, projectId: "" }];

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/applications") || path.endsWith("/projects")) {
      return route.fulfill({ json: PROJECTS });
    }
    if (path.endsWith("/drafts")) return route.fulfill({ json: DRAFTS });
    return route.fulfill({ json: [] });
  });
}

// openFlyout opens a row's ⋯ menu and hovers "Move to", leaving the pointer on that row.
// Returns the row's box, so a test can walk the pointer from there.
async function openFlyout(page) {
  await page.evaluate(() => { location.hash = "#/modeler/p/ungrouped"; });
  await expect(page.locator("#view")).toContainText("Not assigned");
  const dots = page.locator("tr", { hasText: "Alter neu" }).locator(".dropdown-toggle").first();
  await dots.click();
  const toggle = page.locator(".submenu-toggle").first();
  await expect(toggle).toBeVisible();
  const box = await toggle.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.locator(".submenu-menu")).toBeVisible();
  return box;
}

const geometry = (page) => page.evaluate(() => {
  const fly = document.querySelector(".submenu-menu");
  const parent = fly.closest(".dropdown-menu:not(.submenu-menu)");
  const f = fly.getBoundingClientRect(), p = parent.getBoundingClientRect();
  return {
    left: fly.classList.contains("sm-left"),
    gap: fly.classList.contains("sm-left") ? Math.round(p.left - f.right) : Math.round(f.left - p.right),
    insideViewport: f.left >= 0 && f.right <= window.innerWidth,
  };
});

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  installMock(page);
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null, { timeout: 15000 });
});

test.describe("on a wide window", () => {
  test.use({ viewport: { width: 1500, height: 900 } });

  test("the flyout opens to the right, flush against the menu", async ({ page }) => {
    await openFlyout(page);
    const g = await geometry(page);
    expect(g.left).toBe(false);
    // No gap at all: a gap is a strip belonging to neither box, and crossing it was
    // enough to close the flyout.
    expect(g.gap).toBe(0);
    expect(g.insideViewport).toBe(true);
    expect(page.__errors).toEqual([]);
  });

  test("reaching the flyout across the rows in between does not close it", async ({ page }) => {
    const row = await openFlyout(page);
    const target = await page.evaluate(() => {
      const f = document.querySelector(".submenu-menu").getBoundingClientRect();
      return { x: f.left + 40, y: f.top + 60 }; // a row well down the flyout, so the path slopes
    });
    const from = { x: row.x + row.width / 2, y: row.y + row.height / 2 };

    // Walk it in small steps, as a hand does, and check at every one — the old behaviour
    // closed partway across, which a single before/after assertion would have missed.
    for (let i = 1; i <= 14; i++) {
      await page.mouse.move(from.x + ((target.x - from.x) * i) / 14, from.y + ((target.y - from.y) * i) / 14);
      await expect(page.locator(".submenu-menu"), `closed at step ${i} of the reach`).toBeVisible();
    }
    await expect(page.locator(".submenu-menu button").filter({ hasText: "Onboarding" })).toBeVisible();
    expect(page.__errors).toEqual([]);
  });

  test("settling on another row closes it, a moment later", async ({ page }) => {
    await openFlyout(page);
    const other = await page.evaluate(() => {
      const p = document.querySelector(".dropdown-menu:not(.submenu-menu):not([hidden])").getBoundingClientRect();
      return { x: p.left + 20, y: p.top + 10 };
    });
    await page.mouse.move(other.x, other.y);
    // Still open right after — that grace is the whole point...
    await page.waitForTimeout(100);
    await expect(page.locator(".submenu-menu")).toBeVisible();
    // ...and gone once the pointer has settled there.
    await expect(page.locator(".submenu-menu")).toBeHidden({ timeout: 2000 });
    expect(page.__errors).toEqual([]);
  });

  test("dismissing the menu takes the flyout with it, at once", async ({ page }) => {
    await openFlyout(page);
    await page.mouse.click(20, 860); // outside every menu
    // No grace period here: a dismissed menu must not leave a flyout standing.
    await expect(page.locator(".submenu-menu")).toBeHidden();
    expect(page.__errors).toEqual([]);
  });

  test("picking a project from the flyout moves the artifact", async ({ page }) => {
    await openFlyout(page);
    const put = page.waitForRequest((r) =>
      r.method() !== "GET" && /\/drafts\/alter_neu/.test(new URL(r.url()).pathname));
    await page.locator(".submenu-menu button").filter({ hasText: "Onboarding" }).click();
    const req = await put;
    expect(req.postDataJSON()).toMatchObject({ projectId: "p1" });
    expect(page.__errors).toEqual([]);
  });
});

test.describe("when there is no room on the right", () => {
  test.use({ viewport: { width: 700, height: 900 } });

  test("the flyout flips to the left rather than running off screen", async ({ page }) => {
    await openFlyout(page);
    const g = await geometry(page);
    expect(g.left).toBe(true);
    expect(g.gap).toBe(0);
    expect(g.insideViewport).toBe(true);
    expect(page.__errors).toEqual([]);
  });
});
