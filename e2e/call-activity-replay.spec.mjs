// End-to-end coverage for the call-activity drill-down in the Operations
// instance-replay view (api/web/editor.js mountInstanceReplay, ADR-0076): a call
// activity whose timeline step carries a childInstanceKey exposes the child two
// ways — a clickable "child" badge on the diagram element (single click → the
// child's replay, same window) and a "Called process" link in its Details panel.
// Driven through the real vendored bpmn-js against a mock `api`.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/call-activity-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
});

test("a call activity shows a clickable child badge that drills into the child replay", async ({ page }) => {
  const child = await page.evaluate(() => window.__CHILD);
  const childHref = `#/operations/i/${child}`;

  // The diagram overlay: a "child" badge on the call activity, linking to the child.
  const badge = page.locator(`#canvas .ca-child-link`);
  await expect(badge).toBeVisible();
  await expect(badge).toHaveAttribute("href", childHref);

  // Clicking the badge navigates to the child instance (same window — a hash change).
  await badge.click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(childHref);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("selecting a call activity lists the called process as a link in Details", async ({ page }) => {
  const child = await page.evaluate(() => window.__CHILD);
  const childHref = `#/operations/i/${child}`;

  // Select the call activity via its Instance History row (the same selectElement
  // path as a diagram click, without the replay chrome overlapping the small canvas).
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();

  const details = page.locator("#tab-details");
  await expect(details).toContainText("Called process");
  const link = details.locator(".ca-child-link");
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", childHref);
  await expect(link).toContainText(child);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("a plain element carries no child link", async ({ page }) => {
  // The start event is not a call activity, so it has neither a badge nor a
  // Called-process row.
  await expect(page.locator("#canvas .ca-child-link")).toHaveCount(1); // only the call activity's
  await page.locator('#history-list .ops-hrow[data-eik="1000"]').click(); // Start_1
  await expect(page.locator("#tab-details")).toContainText("Start_1");
  await expect(page.locator("#tab-details")).not.toContainText("Called process");
});
