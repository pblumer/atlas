// Multi-instance group is a collapsible *history presentation*, never a second
// execution model. The server's durable replay facts remain selectable individually.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/mi-history-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow.root")).toBeVisible();
});

test("multi-instance body collapses its own iterations by default", async ({ page }) => {
  const rows = page.locator("#history-list .ops-hrow[data-i]");
  await expect(rows).toHaveCount(4); // root, start, body, end — not its three rounds
  const toggle = page.locator("#history-list .ops-mi-toggle");
  await expect(toggle).toHaveCount(1);
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#history-list .ops-mi-count")).toContainText("3");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(rows).toHaveCount(7);
  await expect(page.locator('#history-list .ops-hrow[data-eik="1004"]')).toBeVisible();
  await page.locator('#history-list .ops-hrow[data-eik="1004"]').click();
  await expect(page.locator('#history-list .ops-hrow[data-eik="1004"]')).toHaveClass(/sel/);
  await toggle.click();
  await expect(rows).toHaveCount(4);
  await expect(page.locator("#tab-details")).toBeVisible();
});

test("grouping requires real recorded parent token lineage", async ({ page }) => {
  const result = await page.evaluate(async () => {
    const { groupReplayHistory } = await import("/replay-history.js");
    const steps = [
      { elementId: "A", tokenId: "9", position: 1, loop: {kind: "multi-instance"} },
      { elementId: "A", tokenId: "10", parentTokenId: "9", position: 2, iteration: 1, loop: {kind: "multi-instance"} },
      { elementId: "A", tokenId: "11", parentTokenId: "other", position: 3, iteration: 2, loop: {kind: "multi-instance"} },
      { elementId: "A", tokenId: "9", position: 4, loop: {kind: "multi-instance"} },
      { elementId: "A", tokenId: "12", parentTokenId: "9", position: 5, iteration: 1, loop: {kind: "multi-instance"} },
    ];
    const groups = groupReplayHistory(steps);
    return { first: groups.children.get(0), second: groups.children.get(3), orphan: groups.parents.has(2) };
  });
  expect(result).toEqual({ first: [1], second: [4], orphan: false });
});
