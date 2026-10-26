// End-to-end coverage for the Operations heatmap (ADR-draft-operations-heatmap): the
// strip over the live view that shades the diagram by how much each element and sequence
// flow was used, or by where the time went, and the figures behind the colours.
//
// What has to hold, and is asserted here:
//   - Live stays the default and stays as it was: nothing is shaded until somebody asks.
//   - Runs shades shapes and arrows alike, and draws what nobody ever reached dashed —
//     "never" is a different statement from "rarely", in both styles.
//   - Glow and Outline are two drawings of the same values; switching between them does
//     not change what is counted.
//   - The time modes read the statistics endpoint, a window adds `from=` to it, and the
//     panel ranks the tasks by what the diagram shows.
//   - The heatmap is about the version: picking one instance goes back to Live.
//   - The choice survives a reload, like the live view's badge switches.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/heatmap-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => { try { localStorage.removeItem("atlas.live.heat"); } catch {} });
  await page.evaluate(() => window.__mountLive());
  await expect(page.locator("#heat-strip button[data-heat-mode='live']")).toHaveAttribute("aria-pressed", "true");
};

const mode = (page, key) => page.locator(`#heat-strip button[data-heat-mode='${key}']`);
const style = (page, key) => page.locator(`#heat-strip button[data-heat-style='${key}']`);
const element = (page, id) => page.locator(`.djs-element[data-element-id='${id}']`);
const heatBadge = (page, id) => page.locator(`.djs-overlays[data-container-id='${id}'] .token-badge.heat`);

test("live stays the default and shades nothing", async ({ page }) => {
  await open(page);
  await expect(page.locator(".djs-container.heat-glow")).toHaveCount(0);
  await expect(page.locator(".layer-atlas-heat [data-heat]")).toHaveCount(0);
  await expect(page.locator("#heat-strip #heat-period")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("runs glows over shapes and arrows and draws the unused branch dashed", async ({ page }) => {
  await open(page);
  await mode(page, "runs").click();

  await expect(page.locator(".djs-container.heat-glow")).toHaveCount(1);
  await expect(page.locator(".layer-atlas-heat [data-heat='fLow']")).toHaveCount(1);
  await expect(page.locator(".layer-atlas-heat [data-heat='review']")).toHaveCount(1);
  // Nothing went the high-risk way: no halo, a dashed outline and a zero instead.
  await expect(page.locator(".layer-atlas-heat [data-heat='fHigh']")).toHaveCount(0);
  await expect(element(page, "fHigh")).toHaveClass(/pg-heat-0/);
  await expect(element(page, "iso")).toHaveClass(/pg-heat-0/);
  await expect(heatBadge(page, "iso")).toHaveText("0");
  await expect(heatBadge(page, "review")).toHaveText("877");

  const panel = page.locator("#var-panel .heat-panel");
  await expect(panel).toContainText("Since deployment");
  await expect(panel.locator(".heat-never")).toContainText("Prüfung durch ISO");
  await expect(panel.locator(".heat-branch")).toContainText("96% · 836");
  expect(page.__errors).toEqual([]);
});

test("outline draws the same values as shaded strokes", async ({ page }) => {
  await open(page);
  await mode(page, "runs").click();
  await style(page, "outline").click();

  await expect(page.locator(".djs-container.heat-glow")).toHaveCount(0);
  await expect(page.locator(".layer-atlas-heat [data-heat]")).toHaveCount(0);
  await expect(element(page, "review")).toHaveClass(/pg-heat-5/);
  await expect(element(page, "decline")).toHaveClass(/pg-heat-1/);
  await expect(element(page, "iso")).toHaveClass(/pg-heat-0/);
  expect(page.__errors).toEqual([]);
});

test("the time modes read the statistics and a window asks for one", async ({ page }) => {
  await open(page);
  await mode(page, "waiting").click();
  await expect(heatBadge(page, "review")).toHaveText("18h 55m");
  await expect(heatBadge(page, "grant")).toHaveText("300ms");
  // The panel ranks by what the diagram shows: the slowest wait first.
  await expect(page.locator("#var-panel .heat-table tbody tr").first()).toContainText("Freigabe");

  await page.locator("#heat-period").selectOption("7d");
  await expect.poll(() => page.evaluate(() =>
    window.__requests.some((u) => u.includes("/statistics?from=")))).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("picking one instance goes back to live, and the choice survives a reload", async ({ page }) => {
  await open(page);
  await mode(page, "duration").click();
  await expect(mode(page, "duration")).toHaveAttribute("aria-pressed", "true");

  await page.locator("#instance-sel").selectOption("900001");
  await expect(mode(page, "live")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".djs-container.heat-glow")).toHaveCount(0);

  await mode(page, "runs").click();
  await style(page, "outline").click();
  await page.reload();
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
  await expect(mode(page, "runs")).toHaveAttribute("aria-pressed", "true");
  await expect(style(page, "outline")).toHaveAttribute("aria-pressed", "true");
  expect(page.__errors).toEqual([]);
});
