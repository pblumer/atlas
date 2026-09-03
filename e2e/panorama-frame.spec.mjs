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

// What "Fit" frames (ADR-0211 §7). The world is an area budget the layout settles
// in; the picture is where the nodes ended up. They coincide until something is
// pinned — the fit is skipped then, to keep pins under the spots they were dropped
// on — and from that moment framing the world shows the picture in a corner of a
// mostly empty sheet.

const FIT_FRAME = { width: 1000, height: 500 };

// The box is the nodes plus what each of them draws: a circle has a radius, and a
// name hangs below it. A box drawn through the centres would frame the picture with
// half of every edge node cut off.
test("the content box carries each node's own footprint", async ({ page }) => {
  const box = await page.evaluate(() => window.contentBox(
    [{ x: 100, y: 100, r: 10 }, { x: 300, y: 200, r: 30 }],
    { top: 5, right: 7, bottom: 9, left: 11 }));

  expect(box).toEqual({ x: 100 - 10 - 11, y: 100 - 10 - 5, width: (330 + 7) - 79, height: (230 + 9) - 85 });
});

// Nothing to frame is not an error and not a guess: an empty box, for the caller to
// fall back from.
test("an empty graph has an empty box", async ({ page }) => {
  expect(await page.evaluate(() => window.contentBox([]))).toEqual({ x: 0, y: 0, width: 0, height: 0 });
});

// The view has to carry the viewport's aspect ratio or preserveAspectRatio pads one
// axis with a band of nothing — which is the "content in the corner, empty space
// above" this framing exists to remove.
test("the fitted view has the viewport's shape, whatever shape the content is", async ({ page }) => {
  const views = await page.evaluate((frame) => ({
    wide: window.fitView({ x: 0, y: 0, width: 800, height: 100 }, frame),
    tall: window.fitView({ x: 0, y: 0, width: 100, height: 800 }, frame),
    square: window.fitView({ x: -50, y: -50, width: 400, height: 400 }, frame),
  }), FIT_FRAME);

  for (const [name, v] of Object.entries(views)) {
    expect(v.w / v.h, name).toBeCloseTo(FIT_FRAME.width / FIT_FRAME.height, 5);
  }
});

// Centred in what is left over, so the leftover reads as a margin rather than as a
// blank half of the sheet.
test("the content is centred in the view", async ({ page }) => {
  const v = await page.evaluate((frame) =>
    window.fitView({ x: 200, y: 400, width: 100, height: 100 }, frame), FIT_FRAME);

  expect(200 - v.x).toBeCloseTo((v.w - 100) / 2, 5);
  expect(400 - v.y).toBeCloseTo((v.h - 100) / 2, 5);
});

// The corner the zoom controls float over is not drawable: a node under them takes
// no pointer, so it can be neither selected nor dragged. Reserving it is what makes
// every node in the fitted picture reachable — and the fit pushes content to the
// edges by construction, so without this a node lands there routinely.
test("the reserved corner stays clear of content", async ({ page }) => {
  const reserve = { width: 200, height: 60 };
  const cases = await page.evaluate(([frame, res]) => ["wide", "tall", "square"].map((shape) => {
    const box = shape === "wide" ? { x: 0, y: 0, width: 800, height: 120 }
      : shape === "tall" ? { x: 0, y: 0, width: 120, height: 800 }
      : { x: 0, y: 0, width: 400, height: 400 };
    const view = window.fitView(box, frame, res);
    return { shape, box, view, scale: frame.width / view.w };
  }), [FIT_FRAME, reserve]);

  for (const { shape, box, view, scale } of cases) {
    // The corner the chrome occupies, in the view's own units.
    const corner = {
      left: view.x + view.w - reserve.width / scale,
      top: view.y + view.h - reserve.height / scale,
    };
    // The content's box does not reach into it. Avoiding a corner means clearing one
    // of its two sides — not both, which would be reserving two full strips and
    // giving away a quarter of a wide canvas to a panel a few hundred pixels across.
    const clearsRight = box.x + box.width <= corner.left + 0.001;
    const clearsBottom = box.y + box.height <= corner.top + 0.001;
    expect(clearsRight || clearsBottom, shape).toBe(true);
  }
});

// Reserving the corner must not be paid for twice. A short panel on a wide canvas
// costs the height it occupies, never the width as well.
test("the corner costs one dimension, not both", async ({ page }) => {
  const { free, reserved } = await page.evaluate((frame) => {
    const box = { x: 0, y: 0, width: 900, height: 400 };
    return {
      free: window.fitView(box, frame),
      reserved: window.fitView(box, frame, { width: 200, height: 60 }),
    };
  }, FIT_FRAME);

  // Scale is frame width over view width, so a wider view is a smaller picture.
  const cost = reserved.w / free.w;
  expect(cost).toBeGreaterThan(1);      // it does cost something
  expect(cost).toBeLessThan(1.2);       // and not the 1.25+ two strips would cost
});

// A reserve wider than the viewport would fit the content into nothing, or invert
// the scale. It is chrome, so it can never take more than half the picture.
test("an absurd reserve cannot swallow the picture", async ({ page }) => {
  const v = await page.evaluate((frame) =>
    window.fitView({ x: 0, y: 0, width: 100, height: 100 }, frame,
      { width: frame.width * 3, height: frame.height * 3 }), FIT_FRAME);

  expect(v.w).toBeGreaterThan(0);
  expect(Number.isFinite(v.w) && Number.isFinite(v.h)).toBe(true);
  expect(v.w / v.h).toBeCloseTo(FIT_FRAME.width / FIT_FRAME.height, 5);
});

// Degenerate content — one node, or a set that settled on a line — has no extent on
// some axis. It must frame, not divide by zero.
test("content with no extent still frames", async ({ page }) => {
  const v = await page.evaluate((frame) =>
    window.fitView({ x: 50, y: 50, width: 0, height: 0 }, frame), FIT_FRAME);
  expect(Number.isFinite(v.x) && Number.isFinite(v.w)).toBe(true);
  expect(v.w).toBeGreaterThan(0);
});
