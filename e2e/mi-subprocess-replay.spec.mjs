// The token row under an instance replay while a multi-instance subprocess runs. The
// subprocess is named once with its count (as a multi-instance task is, see
// mi-history.spec.mjs), and so is every inner element that the iterations' tokens share:
// twelve iterations waiting on one inner task are one chip, not twelve.
import { test, expect } from "@playwright/test";

const scrub = (page, frame) => page.locator("#scrub").evaluate((el, v) => {
  el.value = String(v);
  el.dispatchEvent(new Event("input", { bubbles: true }));
}, frame);

test.beforeEach(async ({ page }) => {
  await page.goto("/mi-subprocess-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#token-legend")).toBeVisible();
});

test("the iterations' tokens on an inner task are one chip under the subprocess", async ({ page }) => {
  await scrub(page, 2);
  const chips = page.locator("#token-legend .token-chip");
  await expect(chips).toHaveCount(2);
  await expect(chips.nth(0)).toHaveClass(/\bmi\b/);
  await expect(chips.nth(0)).not.toHaveClass(/\bmi-inner\b/);
  await expect(chips.nth(0)).toContainText("Datensatz korrigieren");
  await expect(chips.nth(0)).toContainText("4");
  await expect(chips.nth(1)).toHaveClass(/\bmi-inner\b/);
  await expect(chips.nth(1)).toContainText("Datensatz bearbeiten");
  await expect(chips.nth(1)).toContainText("3");
  await expect(chips.nth(1)).toContainText("Datensatz korrigieren");
});

test("an inner element holding one token keeps its ordinary chip", async ({ page }) => {
  await scrub(page, 1);
  const chips = page.locator("#token-legend .token-chip");
  // The subprocess, the one iteration on the task, the two still on the inner start.
  await expect(chips).toHaveCount(3);
  await expect(chips.nth(1)).not.toHaveClass(/\bmi\b/);
  await expect(chips.nth(1)).toContainText("Token 5");
  await expect(chips.nth(2)).toHaveClass(/\bmi-inner\b/);
  await expect(chips.nth(2)).toContainText("2");
});

test("the scope of an element is its own or its nearest multi-instance ancestor", async ({ page }) => {
  const result = await page.evaluate(async () => {
    const { multiInstanceScope } = await import("/replay-history.js");
    const tree = {
      P: { mode: "", parentId: undefined },
      MI: { mode: "parallel", parentId: "P" },
      Sub: { mode: "", parentId: "MI" },
      Inner: { mode: "", parentId: "Sub" },
      InnerMI: { mode: "sequential", parentId: "Sub" },
      Plain: { mode: "", parentId: "P" },
    };
    const describe = (id) => tree[id] || null;
    return ["MI", "Inner", "InnerMI", "Plain", "Unknown"].map((id) => multiInstanceScope(id, describe));
  });
  expect(result).toEqual([
    { scopeId: "MI", mode: "parallel", inside: false },
    { scopeId: "MI", mode: "parallel", inside: true },
    { scopeId: "InnerMI", mode: "sequential", inside: false },
    null,
    null,
  ]);
});

// The instance history nests what runs inside an iteration under it. A parallel
// subprocess's iterations run interleaved, so in log order every iteration's inner start
// comes before any inner task and nothing says which belongs to which.
const historyRows = (page) => page.locator("#history-list .ops-hrow[data-i]:not(.root)");
const rowOf = (page, eik) => page.locator(`#history-list .ops-hrow[data-eik="${eik}"]`);

test("the history folds a multi-instance subprocess into one row", async ({ page }) => {
  await expect(historyRows(page)).toHaveCount(2); // Start, the body — no iteration, no inner step
  await expect(rowOf(page, "1001")).toHaveClass(/\bmi-group\b/);
  await expect(rowOf(page, "1001").locator(".ops-mi-count")).toContainText("3");
});

test("an opened body lists its iterations, each saying how far it got", async ({ page }) => {
  await rowOf(page, "1001").locator(".ops-mi-toggle").click();
  await expect(historyRows(page)).toHaveCount(5);
  const second = rowOf(page, "1003");
  await expect(second).toHaveClass(/\bmi-iter\b/);
  await expect(second.locator(".ops-iter-steps")).toContainText("2");
  await expect(second.locator(".ops-iter-at")).toContainText("Datensatz bearbeiten");
  // Inner steps are not rows until their iteration is opened.
  await expect(rowOf(page, "1012")).toHaveCount(0);
});

test("an opened iteration shows its own steps directly under it, one level deeper", async ({ page }) => {
  await rowOf(page, "1001").locator(".ops-mi-toggle").click();
  await rowOf(page, "1003").locator(".ops-mi-toggle").click();
  const eiks = await historyRows(page).evaluateAll((rows) => rows.map((r) => `${r.dataset.eik}@${r.dataset.depth}`));
  expect(eiks).toEqual(["1000@0", "1001@0", "1002@1", "1003@1", "1012@2", "1022@2", "1004@1"]);
  const indent = (eik) => rowOf(page, eik).evaluate((r) => parseFloat(getComputedStyle(r).paddingLeft));
  expect(await indent("1012")).toBeGreaterThan(await indent("1003"));
  expect(await indent("1003")).toBeGreaterThan(await indent("1001"));
});

test("the playhead on a folded inner step marks the outermost row holding it", async ({ page }) => {
  await scrub(page, 2); // frame 2 is at the third iteration's inner task
  await expect(rowOf(page, "1001")).toHaveClass(/\bcur\b/);
  await rowOf(page, "1001").locator(".ops-mi-toggle").click();
  await expect(rowOf(page, "1004")).toHaveClass(/\bcur\b/);
  await expect(rowOf(page, "1001")).not.toHaveClass(/\bcur\b/);
  await rowOf(page, "1004").locator(".ops-mi-toggle").click();
  await expect(rowOf(page, "1023")).toHaveClass(/\bcur\b/);
});
