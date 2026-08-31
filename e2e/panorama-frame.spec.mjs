import { test, expect } from "@playwright/test";

// How the landscape is framed: the opening picture must be the whole mesh with as
// little empty space as the content allows, and it must be zoomable from there.
// Both are arithmetic over the settled coordinates, so they are tested as
// arithmetic — a screenshot could only say "it looks about right".

const FRAME = { width: 1000, height: 600 };
const PAD = 60;

async function fit(page, nodes, frame = FRAME, pad = PAD) {
  return page.evaluate(
    ([n, f, p]) => window.fitToFrame(n, f.width, f.height, p),
    [nodes, frame, pad],
  );
}

function extent(nodes) {
  const xs = nodes.map((n) => n.x), ys = nodes.map((n) => n.y);
  return {
    minX: Math.min(...xs), maxX: Math.max(...xs),
    minY: Math.min(...ys), maxY: Math.max(...ys),
  };
}

test.beforeEach(async ({ page }) => {
  await page.goto("/panorama-frame-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
});

// The complaint this exists to answer: a graph settled into a small disc in the
// middle of a wide frame, surrounded by nothing. After the fit the content reaches
// the padding on at least one axis, so the space is used rather than decorated.
test("fills the frame instead of floating in the middle of it", async ({ page }) => {
  const settled = await fit(page, [
    { id: "a", x: 480, y: 290 },
    { id: "b", x: 520, y: 310 },
    { id: "c", x: 500, y: 330 },
  ]);
  const box = extent(settled);

  const spansX = box.maxX - box.minX, spansY = box.maxY - box.minY;
  const touchesX = Math.abs(spansX - (FRAME.width - PAD * 2)) < 1;
  const touchesY = Math.abs(spansY - (FRAME.height - PAD * 2)) < 1;
  expect(touchesX || touchesY).toBe(true);

  // Nothing is pushed outside the frame doing it, and what is left over is even on
  // both sides: an off-centre fit reads as a broken layout rather than a tight one.
  expect(box.minX).toBeGreaterThanOrEqual(PAD - 1);
  expect(box.maxX).toBeLessThanOrEqual(FRAME.width - PAD + 1);
  expect(box.minY).toBeGreaterThanOrEqual(PAD - 1);
  expect(box.maxY).toBeLessThanOrEqual(FRAME.height - PAD + 1);
  expect(Math.abs(box.minX - (FRAME.width - box.maxX))).toBeLessThan(1);
  expect(Math.abs(box.minY - (FRAME.height - box.maxY))).toBeLessThan(1);
});

// The scale is uniform on purpose. Stretching the axes independently would fill the
// last pixel of the frame and misreport distance — and distance is the only thing a
// force layout is trying to say.
test("scales both axes by the same factor", async ({ page }) => {
  const settled = await fit(page, [
    { id: "a", x: 0, y: 0 },
    { id: "b", x: 100, y: 10 },
    { id: "c", x: 50, y: 5 },
  ]);
  const box = extent(settled);
  const scaleX = (box.maxX - box.minX) / 100;
  const scaleY = (box.maxY - box.minY) / 10;
  expect(Math.abs(scaleX - scaleY)).toBeLessThan(0.001);
});

// A landscape of one node, or of nodes that settled on a single line, has no extent
// on some axis. Dividing by it would produce NaN coordinates and a blank canvas —
// the empty picture the whole view exists to avoid.
test("a degenerate graph is centred rather than divided by zero", async ({ page }) => {
  for (const nodes of [
    [{ id: "only", x: 17, y: 42 }],
    [{ id: "a", x: 0, y: 200 }, { id: "b", x: 400, y: 200 }],
    [{ id: "a", x: 5, y: 5 }, { id: "b", x: 5, y: 5 }],
  ]) {
    const settled = await fit(page, nodes);
    for (const n of settled) {
      expect(Number.isFinite(n.x)).toBe(true);
      expect(Number.isFinite(n.y)).toBe(true);
      expect(n.x).toBeGreaterThanOrEqual(0);
      expect(n.x).toBeLessThanOrEqual(FRAME.width);
    }
  }
});

// Zooming about the pointer is what makes a wheel feel like a map: whatever is under
// the cursor has to stay under it. Stated as the invariant rather than as a
// coordinate, because that is the property, not the arithmetic behind it.
test("zoom keeps the point it is anchored on where it was", async ({ page }) => {
  const base = { x: 0, y: 0, w: 1000, h: 600 };
  const focus = { x: 250, y: 480 };
  const zoomed = await page.evaluate(
    ([v, f, b]) => window.zoomView(v, 0.5, f, b),
    [base, focus, base],
  );
  const fractionBefore = (focus.x - base.x) / base.w;
  const fractionAfter = (focus.x - zoomed.x) / zoomed.w;
  expect(Math.abs(fractionAfter - fractionBefore)).toBeLessThan(0.001);
  expect(zoomed.w).toBeCloseTo(500, 5);
  // The aspect ratio is preserved, or the frame would stop matching the element and
  // the letterboxing the fit removes would come straight back.
  expect(zoomed.w / zoomed.h).toBeCloseTo(base.w / base.h, 5);
});

// Zooming out past the content only restores the empty space the fit exists to
// remove, so it is bounded; zooming in stops where a node fills the frame.
test("zoom is bounded in both directions", async ({ page }) => {
  const base = { x: 0, y: 0, w: 1000, h: 600 };
  const focus = { x: 500, y: 300 };
  const out = await page.evaluate(
    ([b, f]) => {
      let view = b;
      for (let i = 0; i < 30; i++) view = window.zoomView(view, 1.3, f, b);
      return view;
    },
    [base, focus],
  );
  expect(out.w).toBeLessThanOrEqual(base.w * 1.61);

  const inward = await page.evaluate(
    ([b, f]) => {
      let view = b;
      for (let i = 0; i < 60; i++) view = window.zoomView(view, 1 / 1.3, f, b);
      return view;
    },
    [base, focus],
  );
  expect(inward.w).toBeGreaterThan(0);
  expect(inward.w).toBeCloseTo(base.w / 24, 3);
});
