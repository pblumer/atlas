// End-to-end coverage for what a looping activity puts on the Operations replay
// (api/web/editor.js mountInstanceReplay, ADR-0077/0133), driven through the real
// vendored bpmn-js against a mock `api`:
//
//   - the loop explanation on a round — the condition as the author wrote it, the values
//     it read, and what the loop then decided, which is the only way to tell a loop that
//     did what it was told from one that did not;
//   - the token markers of the several element instances a loop puts on one shape. A
//     round runs under the loop's body, so two of them sit on the task at all times, and
//     they used to be drawn on top of each other.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/loop-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();
});

// The x each token marker was translated to, in document order — the geometry the fan-out
// produces, read back off the layer the replay draws them into.
const dotXs = (page) => page.evaluate(() =>
  [...document.querySelectorAll(".layer-atlas-tokens > g")]
    .map((g) => Number(/translate\(([-\d.]+)/.exec(g.getAttribute("transform"))[1])));

test("a round explains the loop's condition, what it read, and what it decided", async ({ page }) => {
  await page.locator('#history-list .ops-hrow[data-eik="1004"]').click(); // the third round

  const loop = page.locator("#tab-details .ops-loop");
  await expect(loop).toBeVisible();
  await expect(loop).toContainText("Round 3 of at most 4");
  await expect(loop).toContainText("Repeat while");
  await expect(loop).toContainText("result >= 500");
  // The value the condition read is the thing that explains the decision.
  await expect(loop).toContainText("result");
  await expect(loop).toContainText("100");
  await expect(loop).toContainText("max iterations (4)");

  // A round that led to another says so instead.
  await page.locator('#history-list .ops-hrow[data-eik="1002"]').click();
  await expect(page.locator("#tab-details .ops-loop")).toContainText("ran the activity again");

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("the loop's body reports the loop itself, not a round", async ({ page }) => {
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();

  const loop = page.locator("#tab-details .ops-loop");
  await expect(loop).toContainText("3 rounds ran");
  await expect(loop).toContainText("Max iterations");
  await expect(loop).not.toContainText("Round 1");
});

test("two tokens on one shape are drawn apart, not on top of each other", async ({ page }) => {
  // Frame 3 is the body plus the round running under it — what a loop looks like at any
  // moment. A marker is 20px across, so anything closer than that overlaps.
  await page.locator("#scrub").evaluate((el) => {
    el.value = "3";
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const xs = await dotXs(page);
  expect(xs.length, "one marker per token on the shape").toBe(2);
  expect(Math.abs(xs[1] - xs[0]), "gap between two token markers").toBeGreaterThanOrEqual(20);
});

test("more tokens than the shape holds collapse into a count", async ({ page }) => {
  await page.locator("#scrub").evaluate((el) => {
    el.value = "5";
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const xs = await dotXs(page);
  // Seven tokens on a 100px task: as many markers as fit, the rest as one "+n".
  expect(xs.length).toBeLessThan(7);
  for (let i = 1; i < xs.length; i++) {
    expect(xs[i] - xs[i - 1], "markers stay clear of each other").toBeGreaterThanOrEqual(20);
  }
  const label = page.locator(".layer-atlas-tokens text");
  await expect(label).toHaveCount(1);
  await expect(label).toHaveText(`+${7 - (xs.length - 1)}`);

  // And every token is still named below the diagram, so nothing is lost by not drawing
  // them all.
  await expect(page.locator("#token-legend .token-chip")).toHaveCount(7);
});

test("a loop's badge counts its rounds, not its activations", async ({ page }) => {
  // The runtime endpoint reports 4 visits on the looping task — the loop's body plus its
  // three rounds. What a reader of a loop wants is how often it ran.
  const badge = page.locator("#canvas .ops-badge.loop");
  await expect(badge).toHaveCount(1);
  await expect(badge).toHaveText("↻ 3");
  await expect(badge).toHaveAttribute("title", "3 runs of this loop");
});
