// End-to-end coverage for the shop badge on the canvas (api/web/editor.js, ADR-0429 §6 "The
// shop's tasks are marked, not redrawn"). A send task declaring <atlas:shopTask>, and a
// receive task whose message a product action owns, carry a small shop badge wherever the
// implementation badges are drawn — the Implement tab and the runtime views, never the
// Design view — and it sits beside the task's envelope rather than over it, because that
// envelope is the BPMN marker saying which way the message goes. The badge is derived, so
// nothing about it reaches the model. Driven through the real vendored bpmn-js against a
// mock `api` (e2e/message-sources-harness.html).
import { test, expect } from "@playwright/test";

async function open(page, { before } = {}) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/message-sources-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  if (before) await page.evaluate(before);
}

async function mount(page, opts) {
  await open(page, opts);
  await page.evaluate(() => window.__mount("mailbox-lifecycle"));
  await expect(page.locator('g.djs-element[data-element-id="Send_shop"]')).toHaveCount(1);
}

const tab = (page, name) => page.locator(`[data-tab="${name}"]`).click();
const shopBadge = (page, id) => page.locator(`.djs-overlays[data-container-id="${id}"] .shop-badge`);
const implBadge = (page, id) => page.locator(`.djs-overlays[data-container-id="${id}"] .impl-badge`);

// envelopeBox is the screen box of the marker bpmn-js draws in a send or receive task's
// top-left corner: the envelope, filled for a send and outlined for a receive. It is the
// one path in the shape's visual, the task body being a rect.
async function envelopeBox(page, id) {
  const marker = page.locator(`g.djs-element[data-element-id="${id}"] > .djs-visual > path`);
  await expect(marker).toHaveCount(1);
  return marker.boundingBox();
}

const overlap = (a, b) =>
  a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

test("a shop send task and a receive task waiting for a product's message carry the shop badge", async ({ page }) => {
  await mount(page);
  await tab(page, "implement");

  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  await expect(shopBadge(page, "Send_shop")).toHaveAttribute("title", "Shop: states how a product action ended");
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  await expect(shopBadge(page, "Receive_extend")).toHaveAttribute("title", "Shop: waits for product action storage-extend of Mailbox");

  // A receive task waiting for a watch's message is not the shop's, and a message send
  // keeps the implementation badge it always had.
  await expect(shopBadge(page, "Receive_plain")).toHaveCount(0);
  await expect(shopBadge(page, "Send_msg")).toHaveCount(0);
  await expect(implBadge(page, "Send_msg")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("the shop badge sits beside the envelope, which stays drawn and uncovered", async ({ page }) => {
  await mount(page);
  await tab(page, "implement");
  for (const id of ["Send_shop", "Receive_extend"]) {
    await expect(shopBadge(page, id)).toHaveCount(1);
    // Nothing covers the envelope: no implementation badge on the shop's tasks.
    await expect(implBadge(page, id)).toHaveCount(0);
    const envelope = await envelopeBox(page, id);
    const badge = await shopBadge(page, id).boundingBox();
    const shape = await page.locator(`g.djs-element[data-element-id="${id}"] > .djs-visual > rect`).first().boundingBox();
    expect(overlap(badge, envelope), `${id}: badge ${JSON.stringify(badge)} over envelope ${JSON.stringify(envelope)}`).toBe(false);
    // Beside it: to its right, on the same band, inside the task.
    expect(badge.x).toBeGreaterThanOrEqual(envelope.x + envelope.width);
    expect(badge.y).toBeLessThan(envelope.y + envelope.height);
    expect(badge.y + badge.height).toBeGreaterThan(envelope.y);
    expect(badge.x + badge.width).toBeLessThanOrEqual(shape.x + shape.width);
    expect(badge.y).toBeGreaterThanOrEqual(shape.y);
  }
  expect(page.__errors).toEqual([]);
});

test("the Design view draws no shop badge, as it draws no implementation badge", async ({ page }) => {
  await mount(page);
  // Opened on the Design tab: the plain BPMN symbols only.
  await expect(page.locator('[data-tab="design"]')).toHaveClass(/active/);
  await expect(page.locator(".shop-badge")).toHaveCount(0);
  await expect(page.locator(".impl-badge")).toHaveCount(0);

  await tab(page, "implement");
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  await tab(page, "design");
  await expect(page.locator(".shop-badge")).toHaveCount(0);
  // The envelope was there all along.
  await envelopeBox(page, "Send_shop");
  await envelopeBox(page, "Receive_extend");
  expect(page.__errors).toEqual([]);
});

test("a listing that fails badges no receive task; the shop send task keeps its badge", async ({ page }) => {
  await mount(page, { before: () => { window.__failSources = true; } });
  await tab(page, "implement");
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  // Give a failed answer every chance to have drawn something, then check it did not.
  await page.evaluate(() => new Promise((r) => setTimeout(r, 100)));
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a listing that arrives late badges the receive task when it arrives, once", async ({ page }) => {
  await mount(page, { before: () => { window.__holdSources = true; } });
  await tab(page, "implement");
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(0);

  await page.evaluate(() => window.__releaseSources());
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  // The canvas asks once for the diagram, however often it redraws.
  const calls = await page.evaluate(() => window.__sourceCalls);
  await tab(page, "design");
  await tab(page, "implement");
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  expect(await page.evaluate(() => window.__sourceCalls)).toBe(calls);
  expect(page.__errors).toEqual([]);
});

test("the badge is derived: it follows the declaration and never reaches the model", async ({ page }) => {
  await mount(page);
  const before = await page.evaluate(() => window.__xml());
  await tab(page, "implement");
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  expect(await page.evaluate(() => window.__xml())).toBe(before);

  // Choosing the message kind takes the shop task off, and the badge with it.
  await page.evaluate(() => window.__select("Send_shop"));
  const list = page.locator("#f-stkind-list");
  await expect(list).toHaveCount(1);
  if (!(await list.isVisible())) await page.locator(".pgroup-head .pgroup-title").filter({ hasText: /^Type$/ }).click();
  await page.locator(".stkind-row[data-kind='message']").click();
  await expect(shopBadge(page, "Send_shop")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the live view marks the same tasks, the receive task once the listing arrives", async ({ page }) => {
  await open(page, { before: () => { window.__holdSources = true; } });
  await page.evaluate(() => window.__mountLive());
  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(0);

  // The answer redraws the shop badges in place: the receive task gains one, and the send
  // task keeps exactly the one it had.
  await page.evaluate(() => window.__releaseSources());
  await expect(shopBadge(page, "Receive_extend")).toHaveCount(1);
  await expect(shopBadge(page, "Send_shop")).toHaveCount(1);
  await expect(implBadge(page, "Send_msg")).toHaveCount(1);
  await expect(shopBadge(page, "Receive_extend")).toHaveAttribute("title", "Shop: waits for product action storage-extend of Mailbox");
  await expect(shopBadge(page, "Receive_plain")).toHaveCount(0);
  await envelopeBox(page, "Send_shop");
  expect(page.__errors).toEqual([]);
});
