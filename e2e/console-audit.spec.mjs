// End-to-end coverage for the Console's global audit log (ADR-0184), and specifically
// for the number it puts on screen.
//
// The view had no browser test at all, which is how it was still reading
// GET /api/v1/audit as a bare array after that endpoint started answering a page
// (ADR-draft-a-capped-listing-answers-with-a-page). The listing counts every event
// matching the filters and then returns a window of it, so `total` is the number of
// changes and `items.length` is the number on screen. Those differ exactly when an
// administrator is looking at a busy installation — which is the only time this page is
// worth opening.
import { test, expect } from "@playwright/test";

const event = (n, action) => ({
  at: 1_756_900_000_000 + n,
  action,
  applicationId: "app-" + n,
  applicationName: "Anwendung " + n,
  subjectId: "usr-" + n,
  role: "editor",
  actorId: "usr-admin",
  actorName: "admin",
});

// listing is how every capped list endpoint answers: rows under .items, beside what the
// server knows about the population they came out of.
const listing = (items, extra = {}) => ({
  items, total: items.length, totalExact: true, truncated: false, ...extra,
});

function installMock(page, audit) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/principals")) return route.fulfill({ json: [] });
    if (path.endsWith("/api/v1/audit")) return route.fulfill({ json: audit });
    return route.fulfill({ json: [] });
  });
}

async function openAudit(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await page.evaluate(() => { location.hash = "#/console/audit"; });
  await expect(page.locator("#view h1").first()).toHaveText("Audit log");
}

test("a complete log shows its rows and claims nothing about a cap", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, listing([event(1, "share"), event(2, "transfer")]));
  await openAudit(page);

  await expect(page.locator('[data-dt-key="audit"] tbody tr')).toHaveCount(2);
  // Nothing on the page talks about a window, because there is no window.
  await expect(page.locator("#audit-out")).not.toContainText("Showing the newest");
  expect(errors).toEqual([]);
});

test("a windowed log says how many changes there are, not how many are on screen", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // Two rows returned out of fifty-seven that matched — the shape a busy installation
  // answers with, and the one the old code rendered as "two changes ever".
  installMock(page, listing([event(1, "share"), event(2, "unshare")], {
    total: 57, truncated: true,
  }));
  await openAudit(page);

  await expect(page.locator('[data-dt-key="audit"] tbody tr')).toHaveCount(2);
  await expect(page.locator("#audit-out")).toContainText("Showing the newest 2 of 57");
  expect(errors).toEqual([]);
});

test("an empty log says so rather than rendering an empty table", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, listing([]));
  await openAudit(page);

  await expect(page.locator("#audit-out")).toContainText("No access-control changes recorded yet");
  expect(errors).toEqual([]);
});
