// End-to-end coverage for how the step replay draws a deferred choice
// (api/web/editor.js mountInstanceReplay, ADR-0110/0046), driven through the real
// vendored bpmn-js against a mock `api`.
//
// The live view already stopped drawing a race literally (ADR-0249): an event-based
// gateway arms every branch at once, so the engine parks a token on each branch and none
// on the gateway, and drawn one-for-one that reads as N tokens racing each other rather
// than as the one wait it is. The replay said the same moment the other way — a token dot
// per branch and a chip per branch in the legend — so the two views described one thing
// differently. This is the same rule applied to a frame: the race on the gateway, the
// armed branches outlined, and the chip below saying once what that means.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/event-gateway-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();
});

const shape = (page, id) => page.locator(`#canvas g[data-element-id="${id}"]`);
const chips = (page) => page.locator("#token-legend .token-chip");

// The x of each token dot, in diagram coordinates — which is where on the diagram the
// replay says the tokens are.
const dotXs = (page) => page.evaluate(() =>
  [...document.querySelectorAll(".layer-atlas-tokens > g")]
    .map((g) => Number(/translate\(([-\d.]+)/.exec(g.getAttribute("transform"))[1])));

const atFrame = async (page, n) => {
  await page.locator("#scrub").evaluate((el, v) => {
    el.value = String(v);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  }, n);
};

test("draws the armed race once, on the gateway", async ({ page }) => {
  await atFrame(page, 5); // both branches armed, the gateway already completed

  // The wait is shown where an operator looks for it. The gateway's box is x 250..300,
  // so a single dot inside it is the whole of what this frame draws.
  await expect(shape(page, "gw")).toHaveClass(/atlas-active/);
  const xs = await dotXs(page);
  expect(xs.length, "one token dot for one race").toBe(1);
  expect(xs[0]).toBeGreaterThanOrEqual(250);
  expect(xs[0]).toBeLessThanOrEqual(300);

  // The branches are armed, not counted — the same dashed outline the live view uses.
  for (const id of ["reply", "timeout"]) {
    await expect(shape(page, id)).toHaveClass(/atlas-armed/);
    await expect(shape(page, id)).not.toHaveClass(/atlas-visited/);
  }
  expect(page.__errors).toEqual([]);
});

test("names the race once in the token legend, and says what it is waiting for", async ({ page }) => {
  await atFrame(page, 5);

  await expect(chips(page)).toHaveCount(1);
  const chip = chips(page).first();
  await expect(chip).toContainText("nächstes Ereignis");
  await expect(chip).toContainText("waiting for the first of 2 events");
  // What the dashed branches mean is said here, once, rather than beside each of them.
  await expect(chip).toHaveAttribute("title", /arms every branch at once/);
  expect(page.__errors).toEqual([]);
});

test("does not draw the gateway's own token twice while it is arming", async ({ page }) => {
  // Frame 4 is the moment between arming the branches and completing: the engine still
  // holds the gateway's token, and the branches already carry its forks. One token, so
  // one dot — the race is that token, not a second one beside it.
  await atFrame(page, 4);

  const xs = await dotXs(page);
  expect(xs.length, "the race and the gateway's token are the same token").toBe(1);
  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).toContainText("waiting for the first of 2 events");
  expect(page.__errors).toEqual([]);
});

test("a decided race is drawn literally again — the winner is a token running there", async ({ page }) => {
  // Frame 6: the message won and the timer branch was cancelled. What is left on `reply`
  // is not a wait to be counted on the gateway, it is the winner on its way out, so it
  // gets its own dot and its own chip.
  await atFrame(page, 6);

  await expect(shape(page, "reply")).toHaveClass(/atlas-active/);
  await expect(shape(page, "reply")).not.toHaveClass(/atlas-armed/);
  await expect(shape(page, "gw")).toHaveClass(/atlas-visited/);
  const xs = await dotXs(page);
  expect(xs.length).toBe(1);
  expect(xs[0], "the dot is on the branch, not on the gateway").toBeGreaterThanOrEqual(380);

  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).toContainText("Antwort");
  await expect(chips(page).first()).not.toContainText("waiting for the first");
  expect(page.__errors).toEqual([]);
});

test("a frame with no race is untouched by the rule", async ({ page }) => {
  await atFrame(page, 2); // the token on the gateway itself, before it armed anything

  await expect(shape(page, "gw")).toHaveClass(/atlas-active/);
  for (const id of ["reply", "timeout"]) {
    await expect(shape(page, id)).not.toHaveClass(/atlas-armed/);
  }
  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).not.toContainText("waiting for the first");
  expect(page.__errors).toEqual([]);
});
