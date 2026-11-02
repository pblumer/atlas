// The Multi-Instance Explorer (ADR-0517, OHS2): an instance with 50,000 iterations
// opens without the full replay timeline, shows its loop as one collapsed group
// with the server's counts, and browses iterations a page at a time — the DOM never
// holds more than one page, and a jump to iteration 37,421 is a request for that
// position, not a scroll.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/mi-explorer-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const mount = async (page) => {
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow.mi-bounded")).toHaveCount(1);
};

test("a 50,000-iteration instance opens as a collapsed group without the timeline", async ({ page }) => {
  await mount(page);
  const group = page.locator("#history-list .ops-hrow.mi-bounded");
  await expect(group).toContainText("Calculate positions"); // the diagram's name, not the element id
  await expect(group.locator(".ops-mi-count")).toContainText("50");
  await expect(group.locator(".mi-summary")).toContainText("3");
  await expect(page.locator(".mi-bounded-note")).toBeVisible();
  const requests = await page.evaluate(() => window.__requests);
  expect(requests.some((u) => u.includes("/timeline"))).toBe(false);
  expect(requests.some((u) => u.includes("/iterations"))).toBe(false); // loaded only on demand
});

test("the explorer pages iterations and jumps to 37,421 by seeking", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  const rows = page.locator(".mi-explorer .mi-row");
  await expect(rows).toHaveCount(100);
  await expect(rows.first()).toContainText("#1");
  await expect(page.locator(".mi-stat.terminated b")).toHaveText("3");

  await page.locator(".mi-ex-next").click();
  await expect(rows.first()).toContainText("#101");
  await expect(rows).toHaveCount(100);
  await page.locator(".mi-ex-prev").click();
  await expect(rows.first()).toContainText("#1");

  await page.locator(".mi-ex-jump").fill("37421");
  await page.locator(".mi-ex-controls button[type=submit]").click();
  await expect(rows.first()).toContainText("37");
  await expect(rows.first()).toContainText("421");
  await expect(rows.first().locator(".mi-row-state")).toHaveClass(/terminated/);
  const asked = await page.evaluate(() => window.__requests.filter((u) => u.includes("/iterations?")));
  expect(asked.some((u) => u.includes("from=37421"))).toBe(true);
  // Never more than one page in the DOM, whatever the iteration count.
  expect(await page.locator(".mi-row").count()).toBeLessThanOrEqual(100);
  const requests = await page.evaluate(() => window.__requests);
  expect(requests.some((u) => u.includes("/timeline"))).toBe(false);
});

test("filters are sent to the server and kept while paging", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-ex-state").selectOption("terminated");
  const rows = page.locator(".mi-explorer .mi-row");
  await expect(rows).toHaveCount(3);
  await page.locator(".mi-ex-state").selectOption("");
  await page.locator(".mi-ex-inc").check();
  await expect(rows).toHaveCount(10);
  await expect(rows.first()).toContainText("5");
  const asked = await page.evaluate(() => window.__requests.filter((u) => u.includes("/iterations?")));
  expect(asked.some((u) => u.includes("state=terminated"))).toBe(true);
  expect(asked.some((u) => u.includes("incidents=true"))).toBe(true);
});

test("keyboard navigation opens an iteration's facts", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-ex-inc").check();
  await expect(page.locator(".mi-explorer .mi-row")).toHaveCount(10);
  await page.locator(".mi-ex-list").focus();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  const detail = page.locator(".mi-ex-detail");
  await expect(detail).toContainText("10");
  await expect(detail.locator(".mi-inc-list li.resolved")).toHaveCount(1);
  await expect(page.locator(".mi-explorer .mi-row.sel")).toHaveCount(1);
});

test("an unavailable history is explained, never shown as empty", async ({ page }) => {
  await page.evaluate(() => { window.__scenario.unavailable = true; });
  await page.evaluate(() => window.__mount());
  // Without a verified index the replay falls back to the classic timeline, and
  // the explorer is not offered as if there were nothing to explore.
  await expect(page.locator("#history-list")).toBeVisible();
  await expect(page.locator(".ops-mi-explore")).toHaveCount(0);
  const requests = await page.evaluate(() => window.__requests);
  expect(requests.some((u) => u.includes("/timeline"))).toBe(true);
});

test("a failed page shows an error with retry", async ({ page }) => {
  await mount(page);
  await page.evaluate(() => { window.__scenario.failNext = 1; });
  await page.locator("#history-list .ops-mi-explore").click();
  await expect(page.locator(".mi-ex-error")).toContainText("disk on fire");
  await page.locator(".mi-ex-retry").click();
  await expect(page.locator(".mi-explorer .mi-row")).toHaveCount(100);
});

test("the explorer stacks its controls on a narrow screen", async ({ page }) => {
  await page.setViewportSize({ width: 420, height: 800 });
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await expect(page.locator(".mi-explorer .mi-row")).toHaveCount(100);
  const overflow = await page.evaluate(() => {
    const ex = document.querySelector(".mi-explorer");
    return ex.scrollWidth - ex.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
});

test("an iteration shows its own variables and decisions, technical detail on request", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-ex-jump").fill("37421");
  await page.locator(".mi-ex-controls button[type=submit]").click();
  await page.locator(".mi-explorer .mi-row").first().click();
  const detail = page.locator(".mi-ex-detail");
  await expect(detail.locator(".mi-vars tbody tr")).toHaveCount(2);
  await expect(detail.locator(".mi-vars")).toContainText("37421");
  await expect(detail.locator(".mi-dec-list li")).toHaveCount(1);
  await expect(detail.locator(".mi-dec-list")).toContainText("position-rule");
  await expect(detail.locator(".mi-dec-trace")).toHaveCount(0);
  await detail.locator(".mi-ex-tech").check();
  await expect(page.locator(".mi-ex-detail .mi-dec-trace")).toHaveCount(1);
  const asked = await page.evaluate(() => window.__requests.filter((u) => u.includes("technical=true")));
  expect(asked.length).toBe(1);
});

test("the bounded view shows the instance's own steps around its collapsed loop", async ({ page }) => {
  await mount(page);
  const rows = page.locator("#history-list .ops-hrow");
  await expect(rows).toHaveCount(3); // Start, the loop, End — not 50,000 iterations
  await expect(rows.nth(0)).toContainText("Start");
  await expect(rows.nth(1)).toHaveClass(/mi-group/);
  await expect(rows.nth(1)).toContainText("Calculate positions");
  await expect(rows.nth(2)).toContainText("End");
  await rows.nth(2).click();
  await expect(rows.nth(2)).toHaveClass(/sel/);
  const asked = await page.evaluate(() => window.__requests);
  expect(asked.some((u) => u.includes("/outline"))).toBe(true);
  expect(asked.some((u) => u.includes("/timeline"))).toBe(false);
});

test("without an outline the bounded view says so and lists the executions", async ({ page }) => {
  await page.evaluate(() => { window.__scenario.noOutline = true; });
  await mount(page);
  await expect(page.locator(".mi-bounded-hint")).toBeVisible();
  await expect(page.locator("#history-list .mi-outline-step")).toHaveCount(0);
});

test("an iteration replays step by step on the diagram", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-explorer .mi-row").first().click();
  const replay = page.locator(".mi-ex-detail .mi-replay");
  await replay.locator(".mi-rp-load").click();
  const steps = replay.locator(".mi-rp-step");
  await expect(steps).toHaveCount(4);
  await expect(steps.first()).toHaveClass(/cur/);
  await expect(steps.first()).toContainText("Calculate positions"); // the diagram's name
  const calc = page.locator('.djs-element[data-element-id="Calc_1"]');
  await expect(calc).toHaveClass(/atlas-active/);
  await replay.locator(".mi-rp-next").click();
  await expect(steps.nth(1)).toHaveClass(/cur/);
  await expect(replay.locator(".mi-rp-pos")).toContainText("2");
  await steps.nth(3).click();
  await expect(replay.locator(".mi-rp-next")).toBeDisabled();
  await replay.locator(".mi-rp-prev").click();
  await expect(steps.nth(2)).toHaveClass(/cur/);
  // Play runs to the end and stops there.
  await replay.locator(".mi-rp-play").click();
  await expect(replay.locator(".mi-rp-play")).toContainText("\u275A"); // ❚❚ while playing
  await expect(steps.nth(3)).toHaveClass(/cur/, { timeout: 5000 });
  await expect(replay.locator(".mi-rp-play")).toContainText("\u25B6", { timeout: 5000 }); // ▶ again, not ❚❚
  const asked = await page.evaluate(() => window.__requests.filter((u) => u.includes("/steps")));
  expect(asked.length).toBe(1);
  expect(asked[0]).toContain("/iterations/1000001/steps");
  // Closing the explorer leaves no replay marks on the diagram.
  await page.locator(".mi-ex-close").click();
  await expect(page.locator(".djs-element.atlas-active, .djs-element.atlas-visited")).toHaveCount(0);
});

test("a long replay window loads its next page, and another iteration starts afresh", async ({ page }) => {
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-ex-jump").fill("37421");
  await page.locator(".mi-ex-controls button[type=submit]").click();
  await expect(page.locator(".mi-explorer .mi-row").first()).toContainText("421");
  await page.locator(".mi-explorer .mi-row").first().click();
  const replay = page.locator(".mi-ex-detail .mi-replay");
  await replay.locator(".mi-rp-load").click();
  await expect(replay.locator(".mi-rp-step")).toHaveCount(3);
  await expect(replay.locator(".mi-rp-pos")).toContainText("+");
  await replay.locator(".mi-rp-more").click();
  await expect(replay.locator(".mi-rp-step")).toHaveCount(4);
  await expect(replay.locator(".mi-rp-step").last()).toHaveClass(/terminated/);
  await expect(replay.locator(".mi-rp-more")).toHaveCount(0);
  await page.locator(".mi-explorer .mi-row").nth(1).click();
  await expect(page.locator(".mi-ex-detail .mi-rp-load")).toBeVisible();
  await expect(page.locator(".djs-element.atlas-active")).toHaveCount(0);
});

test("unavailable iteration steps are explained, not shown as empty", async ({ page }) => {
  await page.evaluate(() => { window.__scenario.noSteps = true; });
  await mount(page);
  await page.locator("#history-list .ops-mi-explore").click();
  await page.locator(".mi-explorer .mi-row").first().click();
  await page.locator(".mi-ex-detail .mi-rp-load").click();
  // The explanation, in the harness's locale — not an empty list.
  await expect(page.locator(".mi-ex-detail .mi-replay .mi-ex-error")).toContainText(/Iterationsschritte|iteration steps existed/);
  await expect(page.locator(".mi-ex-detail .mi-rp-step")).toHaveCount(0);
});
