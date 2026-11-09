// Regression for #1480: the Live view must say which process an operator is
// watching. The toolbar can fill with controls even on desktop, so the process
// name must remain legible outside the shrinking breadcrumb.
import { test, expect } from "@playwright/test";

const LONG_NAME = "Identity and access lifecycle governance, fulfilment, recertification and complex approval workflow for distributed teams";

async function open(page, width, processName) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.setViewportSize({ width, height: 900 });
  await page.goto("/live-instance-search-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((name) => { window.__processName = name; }, processName);
  await page.evaluate(() => window.__mountLive());
  const title = page.locator(".live-process-heading h1");
  await expect(title).toBeVisible();
  return { title, errors };
}

for (const width of [1440, 900, 375]) {
  test(`shows the full process name and version at ${width}px`, async ({ page }) => {
    const { title, errors } = await open(page, width, LONG_NAME);
    await expect(title).toHaveText(LONG_NAME);
    await expect(title).toHaveAttribute("title", LONG_NAME);
    await expect(page.locator(".live-process-version")).toHaveText("v2");

    // This measures the rendered heading rather than just the DOM: the old
    // breadcrumb held the full string but clipped nearly all of it.
    const layout = await title.evaluate((el) => {
      const rect = el.getBoundingClientRect();
      const heading = el.closest(".live-process-heading").getBoundingClientRect();
      const style = getComputedStyle(el);
      return {
        left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom,
        headingTop: heading.top, headingBottom: heading.bottom,
        clipped: style.overflow === "hidden" || style.textOverflow === "ellipsis",
      };
    });
    expect(layout.left).toBeGreaterThanOrEqual(0);
    expect(layout.right).toBeLessThanOrEqual(width + 1);
    expect(layout.top).toBeGreaterThanOrEqual(layout.headingTop);
    expect(layout.bottom).toBeLessThanOrEqual(layout.headingBottom + 1);
    expect(layout.clipped).toBe(false);
    expect(errors).toEqual([]);
  });
}

test("process identity survives selecting an individual instance", async ({ page }) => {
  const { title, errors } = await open(page, 900, LONG_NAME);
  // The instance picker is filled asynchronously; pick a key on the first page.
  // 900001 is deliberately off-page in this harness and is only found via search.
  await expect(page.locator('#instance-sel option[value="900119"]')).toHaveCount(1);
  await page.locator("#instance-sel").selectOption("900119");
  await expect(title).toHaveText(LONG_NAME);
  await expect(page.locator(".live-process-version")).toHaveText("v2");
  expect(errors).toEqual([]);
});

test("falls back to the process ID when there is no name", async ({ page }) => {
  const { title, errors } = await open(page, 900, "");
  await expect(title).toHaveText("identitaet");
  expect(errors).toEqual([]);
});

// Pausing automatic starts (ADR-0119) is offered where an operator watches the process
// (ADR-0541): the heading says when the version is
// paused, and the newest version carries the switch for an operator.
test("an operator pauses and resumes the newest version from the heading", async ({ page }) => {
  const { errors } = await open(page, 1440, "Identitäts-Lebenszyklus");
  const host = page.locator("#live-active");
  await expect(host.locator(".pill")).toHaveCount(0);
  await host.locator("#live-pause").click();
  await expect(host.locator(".pill.warn")).toHaveText("pausiert");
  await expect(host.locator("#live-pause")).toHaveText("Aktivieren");
  const calls = await page.evaluate(() => window.__calls);
  expect(calls).toContainEqual(expect.objectContaining({ method: "PUT", body: { active: false } }));
  await host.locator("#live-pause").click();
  await expect(host.locator(".pill")).toHaveCount(0);
  await expect(host.locator("#live-pause")).toHaveText("Deaktivieren");
  expect(errors).toEqual([]);
});

test("a paused version says so, and only an operator gets the switch", async ({ page }) => {
  await page.goto("/live-instance-search-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => { window.__active = false; window.__mayUse = (role) => role !== "operator"; });
  await page.evaluate(() => window.__mountLive());
  const host = page.locator("#live-active");
  await expect(host.locator(".pill.warn")).toHaveText("pausiert");
  await expect(host.locator("#live-pause")).toHaveCount(0);
});
