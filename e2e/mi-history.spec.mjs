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

test("a small instance keeps its replay and opens the explorer from the grouped row", async ({ page }) => {
  const explore = page.locator("#history-list .ops-mi-explore");
  await expect(explore).toHaveCount(1);
  await explore.click();
  const rows = page.locator(".mi-explorer .mi-row");
  await expect(rows).toHaveCount(3);
  await expect(page.locator("#rp-tabs")).toBeHidden();
  await rows.nth(1).click();
  // The iteration chosen in the explorer is the replay's selected element instance.
  await expect(page.locator('#history-list .ops-hrow[data-eik="1003"]')).toHaveClass(/sel/);
  await page.locator(".mi-ex-close").click();
  await expect(page.locator(".mi-explorer")).toHaveCount(0);
  await expect(page.locator("#rp-tabs")).toBeVisible();
  await expect(page.locator("#tab-details")).toBeVisible();
});

test("the token legend names a multi-instance activity once, not once per iteration", async ({ page }) => {
  // Frame 5 holds seven tokens on the multi-instance task: its body and the iterations
  // running under it. Two hundred chips of "Token … — same task" push the diagram off
  // the screen and say nothing the history row does not already say.
  await page.locator("#scrub").evaluate((el) => {
    el.value = "5";
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const chips = page.locator("#token-legend .token-chip");
  await expect(chips).toHaveCount(1);
  await expect(chips.first()).toHaveClass(/\bmi\b/);
  await expect(chips.first()).toContainText("rechne was");
  await expect(chips.first()).toContainText("7");

  // One token on the shape is still that token, named as before.
  await page.locator("#scrub").evaluate((el) => {
    el.value = "2";
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await expect(chips).toHaveCount(1);
  await expect(chips.first()).not.toHaveClass(/\bmi\b/);
  await expect(chips.first()).toContainText("Token 1");
});

test("folding the legend keeps other elements' tokens and their order", async ({ page }) => {
  const result = await page.evaluate(async () => {
    const { foldLegendTokens } = await import("/replay-history.js");
    const tokens = [
      { elementId: "A", tokenId: "1" },
      { elementId: "MI", tokenId: "2" },
      { elementId: "B", tokenId: "3" },
      { elementId: "MI", tokenId: "4" },
      { elementId: "MI2", tokenId: "5" },
    ];
    return foldLegendTokens(tokens, (id) => id.startsWith("MI")).map((e) =>
      e.token ? e.token.tokenId : `${e.elementId}x${e.tokens.length}`);
  });
  // MI2 holds one token only, so it stays an ordinary chip.
  expect(result).toEqual(["1", "MIx2", "3", "5"]);
});
