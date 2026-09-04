// End-to-end coverage for the call-activity drill-down in the Operations
// instance-replay view (api/web/editor.js mountInstanceReplay, ADR-0076): a call
// activity whose timeline step carries a childInstanceKey exposes the child two
// ways — an always-visible drill-in badge on the diagram element (single click → the
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

test("a call activity carries a visible badge that drills into the child replay", async ({ page }) => {
  const child = await page.evaluate(() => window.__CHILD);
  const childHref = `#/operations/i/${child}`;

  const badge = page.locator(`#canvas .ca-child-btn`);
  await expect(badge).toBeVisible();
  await expect(badge).toHaveAttribute("href", childHref);

  // The badge must be visible without being hovered first: its predecessor was a
  // transparent hotspot that only revealed itself under the pointer, which meant
  // operators never discovered the drill-in. Painted, sized and opaque is the fix, so
  // that is what the test holds on to.
  const paint = await badge.evaluate((el) => {
    const cs = getComputedStyle(el);
    const box = el.getBoundingClientRect();
    return { bg: cs.backgroundColor, opacity: Number(cs.opacity), w: box.width, h: box.height };
  });
  expect(paint.opacity).toBe(1);
  expect(paint.bg).not.toBe("rgba(0, 0, 0, 0)");
  expect(paint.w).toBeGreaterThanOrEqual(16);
  expect(paint.h).toBeGreaterThanOrEqual(16);

  // Clicking it navigates to the child instance (same window — a hash change).
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
  await expect(page.locator("#canvas .ca-child-btn")).toHaveCount(1); // only the call activity's
  await page.locator('#history-list .ops-hrow[data-eik="1000"]').click(); // Start_1
  await expect(page.locator("#tab-details")).toContainText("Start_1");
  await expect(page.locator("#tab-details")).not.toContainText("Called process");
});

// --- The gesture on the marker itself --------------------------------------------
// The badge above is the always-visible affordance; the "+" is the one the marker
// itself suggests, and the one that has to work on every surface a call activity is
// drawn on. The replay's canvas is small next to the transport bar and the history
// tree, so these get a taller window than the file's default.
test.describe("drilling in through the + marker", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  // markerCenter is the screen position of the "+" bpmn-js drew on a shape — located
  // by the `data-marker` attribute its own renderer writes, so the gesture's hit test
  // and the drawing cannot drift apart silently.
  async function markerCenter(page, elementId) {
    const box = await page
      .locator(`[data-element-id="${elementId}"] path[data-marker="sub-process"]`)
      .boundingBox();
    if (!box) throw new Error(`no sub-process marker on ${elementId}`);
    return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  }

  test("double-clicking the + drills into the child replay", async ({ page }) => {
    const child = await page.evaluate(() => window.__CHILD);
    const at = await markerCenter(page, "CallActivity_1");
    await page.mouse.dblclick(at.x, at.y);

    await expect.poll(() => page.evaluate(() => location.hash)).toBe(`#/operations/i/${child}`);
    expect(page.__errors, "page errors during replay").toEqual([]);
  });

  test("a call activity that started no child opens the called process instead", async ({ page }) => {
    // CallActivity_2 ran but never created a child, so there is no instance to drill
    // into — and a dead gesture on the one element whose contents are elsewhere is
    // worse than landing one level out, on the process it calls.
    const at = await markerCenter(page, "CallActivity_2");
    await page.mouse.dblclick(at.x, at.y);

    await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/operations/p/55");
    expect(page.__errors, "page errors during replay").toEqual([]);
  });

  test("double-clicking the shape away from the + navigates nowhere", async ({ page }) => {
    const shape = await page.locator('[data-element-id="CallActivity_1"] .djs-hit').first().boundingBox();
    await page.mouse.dblclick(shape.x + shape.width * 0.25, shape.y + shape.height * 0.25);

    await page.waitForTimeout(300); // give a navigation that must not happen time to
    expect(await page.evaluate(() => location.hash)).toBe("");
    expect(page.__errors, "page errors during replay").toEqual([]);
  });
});
