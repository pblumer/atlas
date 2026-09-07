// End-to-end coverage for the Live view's count badges as switches: the legend's three
// samples — completed here and moved on (gray), cancelled here (amber), tokens here now
// (green) — each turn their own number off and on across the whole diagram.
//
// The point is that up to three counts share one corner of a shape, and which of them an
// operator wants depends on the question: "where is work sitting right now" reads the
// green ones, "which branch does this process actually take" reads the gray history and
// is only crowded by the rest. Two things therefore have to hold, and both are asserted
// here: the switches are independent of one another, and a count stays switched off
// across the 1.5s poll that rebuilds every overlay from scratch.
//
// A switch is also remembered per browser, so the third thing asserted here is that a
// reload — and a remount onto another definition — comes back the way it was left.
//
// It drives the event-gateway harness because that one diagram carries all three colours
// at once: the gateway holds a gray and a green count, the losing branch an amber one.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/event-gateway-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
};

const badges = (page, id, cls = "") =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge${cls}`);
const green = (page, id) =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge:not(.history):not(.cancelled)`);
const toggle = (page, kind) => page.locator(`.legend-toggle[data-badge="${kind}"]`);

test("each count is switched off and back on from its own legend badge", async ({ page }) => {
  await open(page);

  // All three are on to begin with: the gateway carries its history and its live race,
  // the losing branch its cancelled token.
  await expect(badges(page, "gw", ".history")).toHaveText("1");
  await expect(green(page, "gw")).toHaveText("2");
  await expect(badges(page, "timeout", ".cancelled")).toHaveText("1");

  // Switching one off takes that number off the diagram and leaves the other two alone.
  await toggle(page, "passed").click();
  await expect(badges(page, "gw", ".history")).toBeHidden();
  await expect(badges(page, "reply", ".history")).toBeHidden();
  await expect(green(page, "gw")).toBeVisible();
  await expect(badges(page, "timeout", ".cancelled")).toBeVisible();

  // And the other two switch independently of it — three separate decisions, not a cycle
  // through three modes.
  await toggle(page, "live").click();
  await toggle(page, "cancelled").click();
  await expect(green(page, "gw")).toBeHidden();
  await expect(badges(page, "timeout", ".cancelled")).toBeHidden();

  // Clicking again puts each number back.
  await toggle(page, "passed").click();
  await toggle(page, "live").click();
  await toggle(page, "cancelled").click();
  await expect(badges(page, "gw", ".history")).toBeVisible();
  await expect(green(page, "gw")).toBeVisible();
  await expect(badges(page, "timeout", ".cancelled")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a switched-off count stays off across the poll that redraws the overlays", async ({ page }) => {
  await open(page);
  await expect(green(page, "gw")).toBeVisible();

  await toggle(page, "live").click();
  await expect(green(page, "gw")).toBeHidden();

  // The view polls the runtime every 1.5s and rebuilds every badge from the response, so
  // a hidden count that the renderer had to remember would blink back on the next one.
  await page.waitForTimeout(2000);
  await expect(green(page, "gw")).toBeHidden();
  await expect(badges(page, "gw", ".history")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("the legend keeps its own samples, and says which way each switch is thrown", async ({ page }) => {
  await open(page);

  // The legend is still the key: its badges are what you click, so they stay lit and
  // coloured whatever the diagram is showing. Only the pressed state changes.
  for (const kind of ["passed", "cancelled", "live"]) {
    await expect(toggle(page, kind)).toHaveAttribute("aria-pressed", "true");
  }
  await toggle(page, "cancelled").click();
  await expect(toggle(page, "cancelled")).toHaveAttribute("aria-pressed", "false");
  await expect(toggle(page, "cancelled").locator(".token-badge")).toBeVisible();
  await expect(toggle(page, "passed")).toHaveAttribute("aria-pressed", "true");
  await expect(toggle(page, "live")).toHaveAttribute("aria-pressed", "true");

  // The words stay where they were — the legend still reads as a legend.
  await expect(page.locator(".problems")).toContainText("completed here and moved on");
  await expect(page.locator(".problems")).toContainText("cancelled here");
  await expect(page.locator(".problems")).toContainText("tokens here now");
  expect(page.__errors).toEqual([]);
});

test("the switches come back the way they were left, after a reload", async ({ page }) => {
  await open(page);
  await toggle(page, "passed").click();
  await toggle(page, "live").click();
  await expect(badges(page, "gw", ".history")).toBeHidden();

  // Reload and mount again, as an operator refreshing the view: which counts you read is
  // about how you read a diagram, so it is not a choice to make twice.
  await open(page);
  await expect(toggle(page, "passed")).toHaveAttribute("aria-pressed", "false");
  await expect(toggle(page, "live")).toHaveAttribute("aria-pressed", "false");
  await expect(toggle(page, "cancelled")).toHaveAttribute("aria-pressed", "true");
  await expect(badges(page, "gw", ".history")).toBeHidden();
  await expect(green(page, "gw")).toBeHidden();
  await expect(badges(page, "timeout", ".cancelled")).toBeVisible();

  // And switching one back on is remembered just as well as switching it off.
  await toggle(page, "passed").click();
  await open(page);
  await expect(badges(page, "gw", ".history")).toBeVisible();
  await expect(green(page, "gw")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("the choice follows the operator to the next definition", async ({ page }) => {
  await open(page);
  await toggle(page, "live").click();

  // Mounting another deployed definition is what a version switch does. The preference is
  // about reading diagrams, not about the process being read, so it survives the remount
  // — and it is applied before the first poll draws, not after it.
  await page.evaluate(() => window.__mountPlain());
  await expect(page.locator('.legend-toggle[data-badge="live"]')).toHaveAttribute("aria-pressed", "false");
  await expect(badges(page, "p_start", ".history")).toBeVisible();
  await expect(green(page, "p_task")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
