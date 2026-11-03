// e2e for the layers of the code editors (api/web/code-editor.js and json-editor.js):
// a transparent text area over a coloured copy of its text, and a line-number gutter.
// They only read as one surface while they scroll together and break their lines at
// the same width. A backdrop that also spans the text area's scrollbars could not
// scroll as far as the text area, so at the end of a long script the colours and the
// line numbers stopped a scrollbar short of the text, and a selection was drawn on the
// line above the text it selected. And the browser painted the selected text itself.
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

// contentWidth is the width a layer breaks its lines at: its client box less padding.
const contentWidths = (page, id) => page.evaluate((id) => {
  const ta = document.getElementById(id);
  const pre = ta.parentElement.querySelector(".ce-highlight:not(.ce-mirror), .json-highlight");
  const inner = (el) => {
    const cs = getComputedStyle(el);
    return el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
  };
  return { scrollbarW: ta.offsetWidth - ta.clientWidth, ta: inner(ta), pre: inner(pre) };
}, id);

test("the JSON editor's backdrop breaks its lines at the text area's width", async ({ page }) => {
  const w = await contentWidths(page, "json");
  expect(w.scrollbarW).toBeGreaterThan(0);
  expect(w.pre).toBe(w.ta);
  const end = await page.evaluate(() => {
    const ta = document.getElementById("json");
    const pre = ta.parentElement.querySelector(".json-highlight");
    ta.scrollTop = 1e6;
    ta.dispatchEvent(new Event("scroll"));
    return { ta: ta.scrollTop, pre: pre.scrollTop };
  });
  expect(end.pre).toBe(end.ta);
});

test("a compact JSON field's backdrop leaves the toolbar's room too", async ({ page }) => {
  // Focus expands the field. The ResizeObserver fits the backdrop on the next
  // layout cycle, not synchronously with focus; wait for that actual alignment.
  await page.locator("#json-compact").focus();
  await expect.poll(async () => {
    const w = await contentWidths(page, "json-compact");
    return w.pre - w.ta;
  }).toBe(0);
});

// selectionInk selects line `n` of a field with its coloured backdrop hidden, so what
// is left on screen is what the text area itself paints for the selection, and reports
// how many pixels in the middle of the selected text differ from the selection's tint.
// The line must start at the box's left edge and sit below no wrapped line.
async function selectionInk(page, id, n) {
  const clip = await page.evaluate(([id, n]) => {
    const ta = document.getElementById(id);
    ta.parentElement.querySelector(".ce-highlight:not(.ce-mirror), .json-highlight").style.visibility = "hidden";
    ta.focus();
    const lines = ta.value.split("\n");
    const start = lines.slice(0, n).reduce((a, l) => a + l.length + 1, 0);
    ta.setSelectionRange(start, start + lines[n].length);
    ta.scrollTop = 0;
    ta.scrollLeft = 0;
    const cs = getComputedStyle(ta);
    const lh = parseFloat(cs.lineHeight);
    const ctx = document.createElement("canvas").getContext("2d");
    ctx.font = cs.font;
    const cw = ctx.measureText("0").width;
    const lead = lines[n].length - lines[n].trimStart().length;
    const r = ta.getBoundingClientRect();
    const x = r.left + parseFloat(cs.paddingLeft) + lead * cw;
    const y = r.top + parseFloat(cs.paddingTop) + n * lh;
    // Six characters of text, inside the line box so the band's own edges stay out.
    return { x: Math.ceil(x) + 1, y: Math.ceil(y) + 4, width: Math.floor(6 * cw) - 2, height: Math.floor(lh) - 8 };
  }, [id, n]);
  const png = await page.screenshot({ clip });
  return page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const bmp = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
    const g = new OffscreenCanvas(bmp.width, bmp.height).getContext("2d");
    g.drawImage(bmp, 0, 0);
    const d = g.getImageData(0, 0, bmp.width, bmp.height).data;
    const tint = [d[0], d[1], d[2]];
    let ink = 0;
    for (let i = 0; i < d.length; i += 4) {
      if (Math.max(Math.abs(d[i] - tint[0]), Math.abs(d[i + 1] - tint[1]), Math.abs(d[i + 2] - tint[2])) > 24) ink++;
    }
    return { ink, tint };
  }, png.toString("base64"));
}

for (const [label, id, n] of [["a script", "script", 1], ["a JSON", "json", 2]]) {
  test(`a selection in ${label} field paints a tint, not the text area's own text`, async ({ page }) => {
    const { ink, tint } = await selectionInk(page, id, n);
    expect(ink, "pixels of selected text the text area drew itself").toBe(0);
    // The selection still shows: the band is tinted, not the white of the field.
    expect(Math.min(...tint)).toBeLessThan(245);
  });
}
