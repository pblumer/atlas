// e2e for the shared code editor's layers (api/web/code-editor.js): a transparent text
// area over a coloured copy of its text and a line-number gutter. The three only read
// as one surface while they scroll together. A backdrop that also spans the text
// area's scrollbars could not scroll as far as the text area, so at the end of a long
// script the colours and the line numbers stopped a scrollbar short of the text, and a
// selection was drawn on the line above the text it selected.
import { test, expect } from "@playwright/test";

// Playwright starts Chromium with --hide-scrollbars, and hidden scrollbars take no
// room: the defect needs the classic scrollbars a Windows desktop shows.
test.use({ launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/code-editor-scroll-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// layers scrolls the text area to (top, left) and reports where each layer ended up.
const layers = (page, id, top, left) => page.evaluate(([id, top, left]) => {
  const ta = document.getElementById(id);
  const wrap = ta.closest(".code-editor");
  const pre = wrap.querySelector(".ce-highlight:not(.ce-mirror)");
  const gutter = wrap.querySelector(".ce-gutter");
  ta.scrollTop = top;
  ta.scrollLeft = left;
  ta.dispatchEvent(new Event("scroll"));
  return {
    scrollbarH: ta.offsetHeight - ta.clientHeight,
    scrollbarW: ta.offsetWidth - ta.clientWidth,
    ta: { top: ta.scrollTop, left: ta.scrollLeft, width: ta.clientWidth },
    pre: { top: pre.scrollTop, left: pre.scrollLeft, width: pre.clientWidth },
    gutter: gutter && gutter.isConnected ? gutter.scrollTop : null,
  };
}, [id, top, left]);

test("the backdrop and the gutter follow the text area to its last line and column", async ({ page }) => {
  const end = await layers(page, "script", 1e6, 1e6);
  // The precondition: both scrollbars take room, as they do on Windows.
  expect(end.scrollbarH).toBeGreaterThan(0);
  expect(end.scrollbarW).toBeGreaterThan(0);
  expect(end.pre.top).toBe(end.ta.top);
  expect(end.gutter).toBe(end.ta.top);
  expect(end.pre.left).toBe(end.ta.left);

  // And in the middle, where they always agreed.
  const mid = await layers(page, "script", 120, 40);
  expect(mid.pre.top).toBe(mid.ta.top);
  expect(mid.gutter).toBe(mid.ta.top);
  expect(mid.pre.left).toBe(mid.ta.left);
});

test("a wrapping backdrop breaks its lines at the text area's width", async ({ page }) => {
  const end = await layers(page, "wrapped", 1e6, 0);
  expect(end.scrollbarW).toBeGreaterThan(0);
  expect(end.pre.width).toBe(end.ta.width);
  expect(end.pre.top).toBe(end.ta.top);
});

test("a resize that removes a scrollbar re-fits the backdrop", async ({ page }) => {
  const before = await layers(page, "wrapped", 0, 0);
  expect(before.scrollbarW).toBeGreaterThan(0);
  expect(before.pre.width).toBe(before.ta.width);
  // Tall enough for every line: the vertical scrollbar goes, with no edit to say so.
  await page.evaluate(() => { document.getElementById("wrapped").style.height = "900px"; });
  await expect.poll(() => layers(page, "wrapped", 0, 0)).toMatchObject({ scrollbarW: 0 });
  await expect.poll(async () => {
    const l = await layers(page, "wrapped", 0, 0);
    return l.pre.width === l.ta.width;
  }).toBe(true);
});

test("an edit that adds a scrollbar re-fits the backdrop", async ({ page }) => {
  await page.evaluate(() => { document.getElementById("wrapped").style.height = "900px"; });
  await expect.poll(() => layers(page, "wrapped", 0, 0)).toMatchObject({ scrollbarW: 0 });
  // Typing the field past its height brings the scrollbar back.
  await page.locator("#wrapped").focus();
  await page.evaluate(() => {
    const ta = document.getElementById("wrapped");
    ta.setSelectionRange(ta.value.length, ta.value.length);
  });
  await page.keyboard.insertText("\n" + Array.from({ length: 60 }, (_, i) => `$More${i} = ${i}`).join("\n"));
  const end = await layers(page, "wrapped", 1e6, 0);
  expect(end.scrollbarW).toBeGreaterThan(0);
  expect(end.pre.width).toBe(end.ta.width);
  expect(end.pre.top).toBe(end.ta.top);
});

test("a selection does not paint the text area's own text", async ({ page }) => {
  const sel = await page.evaluate(() => {
    const s = getComputedStyle(document.getElementById("script"), "::selection");
    return { color: s.color, background: s.backgroundColor };
  });
  expect(sel.color).toBe("rgba(0, 0, 0, 0)");
  // A tint, not an opaque band: the coloured text below has to read through it.
  const alpha = Number((/rgba\([^)]*,\s*([\d.]+)\)/.exec(sel.background) || [])[1]);
  expect(alpha).toBeGreaterThan(0);
  expect(alpha).toBeLessThan(1);
});
