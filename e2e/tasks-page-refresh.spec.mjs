// End-to-end coverage for what the Tasks inbox knows about its own page after a task is
// completed (api/web/app.js).
//
// GET /api/v1/tasks returns at most one page of open tasks; whether more exist, and where
// the next page starts, come back with the rows (truncated, nextCursor — headers before
// ADR-draft-a-capped-listing-answers-with-a-page). The first load reads them. Completing
// a task then re-read only the rows, so
// both were left at whatever the first load had seen: the "more exist" banner stayed up
// after the backlog was gone, and "Load older" went on paging from a cursor that had
// moved.
//
// This drives the REAL app shell against a mocked /api/v1 in which the queue drains
// between the two reads — the case that tells a stale flag from a fresh one.
import { test, expect } from "@playwright/test";

// listing is how every capped list endpoint answers since
// ADR-draft-a-capped-listing-answers-with-a-page: the rows under
// .items, beside the count of what is really there and whether the cap bit.
const listing = (items, extra = {}) => ({
  items, total: items.length, totalExact: true, truncated: false, ...extra,
});

const task = (key, name) => ({
  key, processInstanceKey: 9001, elementInstanceKey: 9100 + key, processDefKey: 1,
  processId: "service-desk-ticket", elementId: "ut_ersatz", name, priority: 50,
});

// The first page is full and flagged; after the completion the queue fits in one page,
// so the second read is not truncated at all.
const FIRST = [task(101, "Ersatzgerät beschaffen"), task(102, "Ersatzgerät beschaffen"), task(103, "Ersatzgerät beschaffen")];
const AFTER = [task(101, "Ersatzgerät beschaffen"), task(103, "Ersatzgerät beschaffen")];

function installMock(page, seen) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    seen.push(route.request().method() + " " + path + url.search);
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/complete")) return route.fulfill({ json: {} });
    if (path === "/api/v1/instances") return route.fulfill({ json: listing([]) });
    if (path === "/api/v1/tasks") {
      const done = seen.some((s) => s.includes("/complete"));
      return route.fulfill({
        json: done
          ? listing(AFTER)
          : listing(FIRST, { totalExact: false, truncated: true, nextCursor: "101" }),
      });
    }
    return route.fulfill({ json: [] });
  });
}

test("completing a task re-reads what the page says about itself, not just its rows", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const seen = [];
  installMock(page, seen);

  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });

  // The capped first page says so, and offers the cursor page.
  const banner = page.locator("#task-trunc");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("more exist");

  await page.locator('.tasks-item[data-key="102"]').click();
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Ersatzgerät beschaffen");
  await page.locator("#task-complete").click();

  // Two tasks left and nothing beyond them: the banner goes, rather than standing on a
  // flag from the load before.
  await expect(page.locator(".tasks-item")).toHaveCount(2);
  await expect(banner).toBeHidden();

  // And it took one request to find that out — the completed task is not chased down by
  // key first, which is what clearing the selection before the reload buys.
  expect(seen.filter((s) => /^GET \/api\/v1\/tasks\/\d+$/.test(s))).toEqual([]);
  expect(errors).toEqual([]);
});
