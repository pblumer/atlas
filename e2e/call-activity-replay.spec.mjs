// End-to-end coverage for the call-activity drill-down in the Operations
// instance-replay view (api/web/editor.js mountInstanceReplay, ADR-0076): a call
// activity whose timeline step carries a childInstanceKey drills into the child two
// ways — clicking the call activity's "+" marker on the diagram (same window) and a
// "Called process" link in its Details panel. The replay is read-only: a
// double-click must not open bpmn-js label editing. Driven through the real
// vendored bpmn-js against a mock `api`.
import { test, expect } from "@playwright/test";

// A tall viewport so the diagram canvas is large and the call activity's bottom
// "+" marker is well clear of the replay's "no tokens" legend bar (which, in the
// default short viewport, overlaps the small canvas). This mirrors the real app,
// where the canvas fills the window.
test.use({ viewport: { width: 1000, height: 1000 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/call-activity-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForSelector('[data-element-id="CallActivity_1"]');
});

test("clicking a call activity's + marker drills into the child replay", async ({ page }) => {
  const child = await page.evaluate(() => window.__CHILD);
  const childHref = `#/operations/i/${child}`;

  // Click the bottom-centre "+" marker of the call activity (a real mouse click at
  // that point, as the operator would), not the element centre.
  const box = await page.locator('[data-element-id="CallActivity_1"]').boundingBox();
  await page.mouse.click(box.x + box.width / 2, box.y + box.height - 5);

  await expect.poll(() => page.evaluate(() => location.hash)).toBe(childHref);
  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("clicking the body of a call activity selects it (does not navigate)", async ({ page }) => {
  const box = await page.locator('[data-element-id="CallActivity_1"]').boundingBox();
  // Click the upper body (the name area), away from the bottom marker.
  await page.mouse.click(box.x + box.width / 2, box.y + box.height * 0.3);

  await expect(page.locator("#tab-details")).toContainText("Call activity"); // selected
  expect(await page.evaluate(() => location.hash)).toBe(""); // did not drill in
});

test("double-clicking a call activity does not open label editing (read-only replay)", async ({ page }) => {
  const box = await page.locator('[data-element-id="CallActivity_1"]').boundingBox();
  await page.mouse.dblclick(box.x + box.width / 2, box.y + box.height * 0.3);

  const editing = await page.evaluate(() =>
    !!document.querySelector('.djs-direct-editing-parent, .djs-direct-editing-content, [contenteditable="true"]'));
  expect(editing, "label editing must stay off in the read-only replay").toBe(false);
});

test("selecting a call activity lists the called process as a link in Details", async ({ page }) => {
  const child = await page.evaluate(() => window.__CHILD);
  const childHref = `#/operations/i/${child}`;

  // Select via its Instance History row (the same selectElement path as a diagram
  // click, without the replay chrome overlapping the small canvas).
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();

  const details = page.locator("#tab-details");
  await expect(details).toContainText("Called process");
  const link = details.locator(".ca-child-link");
  await expect(link).toHaveAttribute("href", childHref);
  await expect(link).toContainText(child);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("a plain element carries no called-process link", async ({ page }) => {
  await page.locator('#history-list .ops-hrow[data-eik="1000"]').click(); // Start_1
  await expect(page.locator("#tab-details")).toContainText("Start_1");
  await expect(page.locator("#tab-details")).not.toContainText("Called process");
});
