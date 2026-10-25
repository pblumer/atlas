// e2e for the bar of view names under the top bar (app.js setChrome, app.css .topnav).
//
// What was reported: with eleven Console entries for an administrator, a laptop's
// window no longer held the bar. Flexbox squeezed the entries, two-word names broke
// onto two lines ("AI access", "API tokens", "Audit log"), the organisation's name was
// cut to "A…" and the account button was clipped at the right edge. Nothing failed —
// the page was simply hard to read, at the widths most people use.
//
// Two changes answer it and both are held here: the bar never wraps a name and never
// pushes the icons off (it scrolls sideways within itself when it must, at every
// width), and the pages opened seldom sit behind one entry — Administration in the
// Console, Diagnostics in Operations, which had ten entries of its own — so both bars
// fit a laptop again. The layout half is measured rather than eyeballed
// — the line boxes of each name, the rectangles of the icons — because a wrapped name
// is a rendering fact no DOM query sees.
import { test, expect } from "@playwright/test";

const ADMIN = { id: "usr_root", username: "root", roles: ["admin"] };
const MODELLER = { id: "usr_mona", username: "mona", roles: ["modeler", "operator", "user"] };
const WORKER = { id: "usr_tina", username: "tina", roles: ["user"] };
const OPERATOR = { id: "usr_otto", username: "otto", roles: ["operator", "user"] };
const ADMIN_PAGES = ["Engine", "Logs", "Backup", "Organization", "API tokens", "Security", "Audit log"];

// stubAPI answers /auth/me as the given account and everything else benignly. The
// catch-all is registered first because Playwright matches the most recently
// registered route.
async function boot(page, user, { width = 1280, hash = "" } = {}) {
  await page.setViewportSize({ width, height: 700 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: user ? { authEnabled: true, user } : { authEnabled: false, user: null } }));
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#topnav")?.children.length > 0, null, { timeout: 15000 });
}

// The names on the bar itself: the leading text of each entry, without the group's
// caret or the incident count that rides on its entry.
const barNames = (page) => page.evaluate(() =>
  [...document.querySelectorAll("#topnav > a, #topnav .topnav-group-btn")]
    .map((el) => el.firstChild.textContent.trim()));

// layout measures what a reader sees: how many lines each name takes, whether every
// control on the top bar lies wholly inside the window, and whether the names need to
// scroll to be seen.
const layout = (page) => page.evaluate(() => {
  // Lines are told apart by height, not by their top edge alone: the group's smaller
  // caret sits on the same line as its name with a top of its own, and a box that
  // overlaps the line before it vertically is still on that line.
  const lines = (el) => {
    const r = document.createRange();
    r.selectNodeContents(el);
    const boxes = [...r.getClientRects()].filter((b) => b.width > 0).sort((a, b) => a.top - b.top);
    let count = 0, bottom = -Infinity;
    for (const b of boxes) {
      if (b.top >= bottom - 1) { count++; bottom = b.bottom; } else bottom = Math.max(bottom, b.bottom);
    }
    return count;
  };
  const nav = document.getElementById("topnav");
  const bar = document.querySelector(".topbar");
  const names = [...nav.querySelectorAll(":scope > a, .topnav-group-btn")];
  const controls = [...bar.querySelectorAll(":scope > .icon-btn:not([hidden]), :scope > .dropdown:not([hidden]) > .icon-btn")]
    .filter((el) => el.offsetParent !== null);
  return {
    wrapped: names.filter((el) => lines(el) > 1).map((el) => el.textContent.trim()),
    clipped: controls.filter((el) => {
      const b = el.getBoundingClientRect();
      return b.left < 0 || b.right > innerWidth + 0.5 || b.width < 20;
    }).map((el) => el.getAttribute("aria-label") || el.title || el.className),
    barOverflows: bar.scrollWidth > bar.clientWidth + 1,
    navScrolls: nav.scrollWidth > nav.clientWidth + 1,
    navWidth: nav.clientWidth,
    controls: controls.length,
  };
});

// The two longest bars, for the accounts that see the most of them: an administrator
// sees every page, an operator every Operations page but the two mock tools.
for (const [who, user, hash] of [["an administrator's Console", ADMIN, ""], ["an administrator's Operations", ADMIN, "#/operations"],
  ["an operator's Operations", OPERATOR, "#/operations"]]) {
  for (const width of [1440, 1280, 1024]) {
    test(`${who} bar fits a ${width}px window: no wrapped name, nothing clipped, nothing to scroll`, async ({ page }) => {
      await boot(page, user, { width, hash });
      const l = await layout(page);
      expect(l.controls, "the measurement found the top bar's buttons").toBeGreaterThan(3);
      expect(l.wrapped).toEqual([]);
      expect(l.clipped).toEqual([]);
      expect(l.barOverflows).toBe(false);
      expect(l.navScrolls, "every entry is visible without scrolling the bar").toBe(false);
    });
  }
}

// Below the width the names fit in, they still never wrap and never cost the icons
// their place: the bar scrolls within itself. A phone has the narrowest window, and
// there the bar must keep room of its own: logo, app name and six buttons once left it
// none at all, and in Operations pushed the account button off the screen. At 360px
// the room is about one short name, partly shown and scrolled to the open one; more
// would mean dropping buttons on a phone, which is a decision of its own.
for (const [width, hash] of [[900, ""], [700, ""], [390, ""], [360, ""], [900, "#/operations"], [390, "#/operations"], [360, "#/operations"]]) {
  test(`at ${width}px ${hash || "#/console"} wraps no name and clips no button`, async ({ page }) => {
    await boot(page, ADMIN, { width, hash });
    const l = await layout(page);
    expect(l.wrapped).toEqual([]);
    expect(l.clipped).toEqual([]);
    expect(l.barOverflows).toBe(false);
    expect(l.navWidth, "the bar of view names keeps room of its own rather than none").toBeGreaterThan(40);
  });
}

test("the administrator's pages sit behind Administration, and the bar says when one is open", async ({ page }) => {
  await boot(page, ADMIN);
  expect(await barNames(page)).toEqual(["Dashboard", "Workers", "Events", "AI access", "Display", "Administration"]);

  const group = page.locator("#topnav .topnav-group-btn");
  const menu = page.locator("#topnav .topnav-group .dropdown-menu");
  await expect(menu).toBeHidden();
  await group.click();
  await expect(menu).toBeVisible();
  await expect(menu.locator("a")).toHaveText(ADMIN_PAGES);
  // The bar scrolls sideways and so clips what overflows it; the menu must not be.
  const box = await menu.boundingBox();
  const viewport = page.viewportSize();
  expect(box.y).toBeGreaterThan(30);
  expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
  expect(box.y + box.height).toBeLessThanOrEqual(viewport.height);

  await menu.locator("a", { hasText: "API tokens" }).click();
  await expect(page).toHaveURL(/#\/console\/api-tokens$/);
  await expect(menu).toBeHidden();
  // Open on one of its pages, the group is the entry marked as where one is, and its
  // menu marks the page.
  await expect(page.locator("#topnav .topnav-group-btn")).toHaveClass(/active/);
  await expect(page.locator("#topnav > a.active")).toHaveCount(0);
  await page.locator("#topnav .topnav-group-btn").click();
  await expect(page.locator('#topnav .topnav-group .dropdown-menu a[aria-current="page"]')).toHaveText("API tokens");

  // Leaving for a page on the bar moves the mark back to it.
  await page.keyboard.press("Escape");
  await page.locator("#topnav > a", { hasText: "Workers" }).click();
  await expect(page.locator("#topnav .topnav-group-btn")).not.toHaveClass(/active/);
  await expect(page.locator("#topnav > a.active")).toHaveText("Workers");
});

test("a modeller and a task worker see no Administration at all", async ({ page }) => {
  await boot(page, MODELLER);
  expect(await barNames(page)).toEqual(["Dashboard", "Workers", "Events", "AI access", "Display"]);
  await expect(page.locator("#topnav .topnav-group")).toHaveCount(0);
});

test("a task worker's bar has what the user role reaches", async ({ page }) => {
  await boot(page, WORKER);
  expect(await barNames(page)).toEqual(["Dashboard", "Workers", "AI access", "Display"]);
});

test("with enforcement off nobody has a role, so Administration is offered too", async ({ page }) => {
  await boot(page, null);
  expect(await barNames(page)).toContain("Administration");
});

test("the Operations pages for looking into something sit behind Diagnostics", async ({ page }) => {
  await boot(page, ADMIN, { hash: "#/operations" });
  expect(await barNames(page)).toEqual(["Instances", "Incidents", "Workers", "Decisions", "Reconciliation", "Diagnostics"]);
  await page.locator("#topnav .topnav-group-btn", { hasText: "Diagnostics" }).click();
  const menu = page.locator("#topnav .topnav-group .dropdown-menu");
  await expect(menu).toBeVisible();
  await expect(menu.locator("a")).toHaveText(["Messages", "Event log", "Outbox", "Call activities", "Mock directory", "Mock database", "Mockups"]);
  await menu.locator("a", { hasText: "Outbox" }).click();
  await expect(page).toHaveURL(/#\/operations\/outbox$/);
  await expect(page.locator("#topnav .topnav-group-btn")).toHaveClass(/active/);
  // The incident count stays on the bar, where an operator sees it without opening anything.
  await expect(page.locator('#topnav > a .nav-badge[data-badge="incidents"]')).toHaveCount(1);
});

test("an operator's Diagnostics has no mock tools, which are an administrator's", async ({ page }) => {
  await boot(page, OPERATOR, { hash: "#/operations" });
  expect(await barNames(page)).toEqual(["Instances", "Incidents", "Workers", "Decisions", "Reconciliation", "Diagnostics"]);
  await page.locator("#topnav .topnav-group-btn").click();
  await expect(page.locator("#topnav .topnav-group .dropdown-menu a")).toHaveText(["Messages", "Event log", "Outbox", "Call activities"]);
});
