// e2e for the top bar staying in view while a page scrolls (app.css .topbar, body).
//
// What was reported: in Tasks (and every other app) the top bar disappeared once a
// long page was scrolled. The bar was declared `position: sticky`, but a sticky box
// only sticks inside its parent, and the parent — <body> — was held to the window's
// height by `html, body { height: 100% }`. The page's content overflowed the body, so
// after a little less than one window of scrolling the body's end carried the bar up
// and out of sight. Nothing threw; the app switcher, the view names and the account
// menu were simply gone until the reader scrolled back to the top.
//
// This scrolls well past one window in three apps and measures where the bar is.
import { test, expect } from "@playwright/test";

const USER = { id: "usr_tina", username: "tina", roles: ["admin", "modeler", "operator", "user"] };

async function boot(page, hash) {
  await page.setViewportSize({ width: 1280, height: 700 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user: USER } }));
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#topnav")?.children.length > 0, null, { timeout: 15000 });
  // A page several windows long, whatever the view rendered with the stubbed API.
  await page.evaluate(() => {
    const tall = document.createElement("div");
    tall.style.height = "4000px";
    document.getElementById("view").appendChild(tall);
  });
}

const barTop = (page, y) => page.evaluate((y) => {
  window.scrollTo(0, y);
  const r = document.querySelector(".topbar").getBoundingClientRect();
  return { top: Math.round(r.top), bottom: Math.round(r.bottom), scrolled: Math.round(window.scrollY) };
}, y);

for (const [app, hash] of [["Tasks", "#/tasks"], ["Console", "#/console"], ["Operations", "#/operations"]]) {
  test(`the top bar stays in view while ${app} scrolls`, async ({ page }) => {
    await boot(page, hash);
    for (const y of [300, 900, 2500]) {
      const bar = await barTop(page, y);
      expect(bar.scrolled, `the page scrolled to ${y}`).toBe(y);
      expect(bar.top, `the bar's top edge after scrolling ${y}px`).toBe(0);
      expect(bar.bottom, `the bar is drawn after scrolling ${y}px`).toBeGreaterThan(30);
    }
  });
}

test("the page still fills the window when its content is short", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 700 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user: USER } }));
  await page.goto("/index.html#/tasks");
  await page.waitForFunction(() => document.querySelector("#topnav")?.children.length > 0, null, { timeout: 15000 });
  // The body's background (the cockpit's starfield, the dark theme's ground) reaches
  // the bottom of the window even when there is little to show.
  const h = await page.evaluate(() => document.body.getBoundingClientRect().height);
  expect(h).toBeGreaterThanOrEqual(700);
});
