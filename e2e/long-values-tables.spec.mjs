// A value the process data decides — a Graph delta link, a token, a URL used as a
// correlation key — is often one unbroken line. Shown in a list in a chip, a pill or a
// one-line preview that does not wrap, such a value set its column's minimum to its
// whole length, and the table ran thousands of pixels past its panel: the Variables
// tab's type and Copy columns, a decision's result and its "How this was decided"
// link, a message's payload all slid off screen. Each list here must stay inside the
// box it was given, with the long value still on it.
import { test, expect } from "@playwright/test";

const LINK = "https://graph.microsoft.com/v1.0/groups/delta?$deltatoken=" + "IQpFqblJdaIs4Udx_lqiiaKTV5Odsm_zOm04QBXj3kw2U4Yu".repeat(8);
const TAIL = LINK.slice(-24);

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

// Measures a table against the box that clips or scrolls it (its nearest scrolling
// ancestor, or its card): how far the table reaches past that box, how far the page
// reaches past the window, and — when an element of the table is named — whether that
// element ends inside the box, i.e. is on screen sideways wherever its row sits. The
// table's own width is what it was laid out at, so it is compared with the box rather
// than read from the box's scroll width, which a scrolling box would absorb.
const measure = (table, el = null) => table.evaluate((t, e) => {
  let box = t.parentElement;
  while (box && !/(auto|scroll|hidden)/.test(getComputedStyle(box).overflowX) && !box.classList.contains("card")) box = box.parentElement;
  box = box || document.documentElement;
  return {
    table: t.offsetWidth - box.clientWidth,
    page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    inside: e ? e.getBoundingClientRect().right <= box.getBoundingClientRect().right + 1 : null,
  };
}, el);

test("the Variables tab keeps a structure with a long line inside its panel", async ({ page }) => {
  await page.goto("/long-values-tables-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
  await page.locator("#rp-tabs button[data-tab='variables']").click();
  const table = page.locator("#tab-variables table.vt");
  await expect(table.locator(".v-open", { hasText: "fields" })).toBeVisible();

  // Closed: the one-line preview is cut, and the type and Copy columns stay in view.
  expect((await measure(table)).table).toBeLessThanOrEqual(1);
  const type = await table.locator("td.c-type", { hasText: "object" }).elementHandle();
  expect((await measure(table, type)).inside).toBe(true);

  // Opened: the long line scrolls inside the structure's own box, not the table.
  await table.locator(".v-open", { hasText: "fields" }).click();
  const body = table.locator(".v-struct .vj-body");
  await expect(body).toBeVisible();
  await expect(body).toContainText(TAIL);
  expect((await measure(table)).table).toBeLessThanOrEqual(1);
  const scrolls = await body.evaluate((b) => b.scrollWidth > b.clientWidth + 1);
  expect(scrolls).toBe(true);
  expect(page.__errors).toEqual([]);
});

// The Console pages below are the real ones, against a mocked /api/v1, on a desktop
// window. At the suite's 900px the decision table is at its own minimum width with short
// values too (six columns; about 825px with this machine's fonts, more on CI's), and its
// card then scrolls it as every card does (ADR-0163) — which would measure the fonts,
// not the long value. A wide window leaves room, so only the value can overreach.
async function openConsole(page, hash, answers) {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    for (const [suffix, json] of answers) if (path.endsWith(suffix)) return route.fulfill({ json });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html" + hash);
}

test("a decision's evaluations keep a long input and result inside the card", async ({ page }) => {
  await openConsole(page, "#/operations/decisions/Probe", [["/decisions/Probe/evaluations", [
    { at: 1794395600000000000, atKey: "1794395600000000000", instanceKey: 281474976710657, elementId: "decide",
      inputs: { link: LINK, n: 1 }, outputs: { next: LINK } },
  ]]]);
  const row = page.locator("tr", { hasText: "281474976710657" });
  await expect(row).toBeVisible();
  const table = row.locator("xpath=ancestor::table[1]");

  const reach = await measure(table);
  expect(reach.table).toBeLessThanOrEqual(1);
  expect(reach.page).toBeLessThanOrEqual(0);
  // Both values are there in full, broken across lines rather than cut.
  await expect(row.locator(".pill-kv", { hasText: "link" })).toContainText(TAIL);
  await expect(row.locator(".res-val")).toContainText(TAIL);
  // The way to the full account stays on screen beside them.
  expect((await measure(table, await row.locator(".dec-open").elementHandle())).inside).toBe(true);
  // A short value still reads on one line.
  const short = await row.locator(".pill-kv", { hasText: "n = 1" }).evaluate((e) => e.getClientRects().length === 1 && e.offsetHeight < 30);
  expect(short).toBe(true);
});

test("a buffered message with a long correlation key, id and payload stays inside the card", async ({ page }) => {
  await openConsole(page, "#/operations/messages", [["/messages/buffered", {
    messages: [{ key: "281474976710677", name: "probe.delta", correlationKey: LINK, messageId: "msg-" + LINK.slice(60),
      bufferedAt: 1794395600000000000, expiresAt: 1794399200000000000, variables: [{ name: "deltaLink", value: LINK }] }],
    total: 1,
  }]]);
  const row = page.locator("#bm-list tbody tr", { hasText: "probe.delta" });
  await expect(row).toBeVisible();
  const table = page.locator("#bm-list table");

  const reach = await measure(table);
  expect(reach.table).toBeLessThanOrEqual(1);
  expect(reach.page).toBeLessThanOrEqual(0);
  await expect(row.locator(".chip")).toContainText(TAIL);
});

test("the event log keeps a long correlation key inside the card", async ({ page }) => {
  await openConsole(page, "#/operations/event-log", [["/event-log", {
    rows: [{ position: "40", cause: "39", at: "2026-10-22T10:00:04Z", kind: "message.sent", channel: "message", name: "probe.delta",
      correlationKey: LINK, source: "publish", reached: 0, started: 0 }],
    next: "40", latest: "40", more: false, prunedThrough: "0",
  }]]);
  const row = page.locator('#el-rows tr[data-pos="40"]');
  await expect(row).toBeVisible();
  const table = row.locator("xpath=ancestor::table[1]");

  const reach = await measure(table);
  expect(reach.table).toBeLessThanOrEqual(1);
  expect(reach.page).toBeLessThanOrEqual(0);
  await expect(row.locator('.chip[title="Correlation key"]')).toContainText(TAIL);
  // The last column is still on screen.
  expect((await measure(table, await row.locator("td").last().elementHandle())).inside).toBe(true);
});
