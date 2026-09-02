import { test, expect } from "@playwright/test";

// How big a node is drawn (ADR-0211 §7). Size carries two things at once — what
// kind of thing this is, and how much of the landscape hangs off it — and the whole
// reading depends on neither overwriting the other. That is a claim about every
// degree a node could have, so it is checked as arithmetic over all of them rather
// than sampled from whatever a rendered fixture happens to contain.

// The bands, in the rank the picture is read in: an application is unmistakably the
// largest thing on screen, a leaf unmistakably the smallest.
const KINDS = ["application", "process", "worker", "decision", "restricted", "unresolved"];
const DEGREES = [0, 1, 2, 3, 5, 8, 12, 13, 40, 200];

async function radius(page, kind, degree) {
  return page.evaluate(([k, d]) => window.radiusFor({ kind: k }, d), [kind, degree]);
}

test.beforeEach(async ({ page }) => {
  await page.goto("/panorama-size-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
});

// The property everything else rests on. Without it, a much-used worker could be
// drawn the size of a process and the picture would be saying two things with one
// channel — which is worse than saying only one.
test("connectivity moves a node up its own band and never out of it", async ({ page }) => {
  const sizes = await page.evaluate(([kinds, degrees]) => {
    const out = {};
    for (const kind of kinds) out[kind] = degrees.map((d) => window.radiusFor({ kind }, d));
    return out;
  }, [KINDS, DEGREES]);

  for (const kind of KINDS) {
    const band = sizes[kind];
    // More connections is never smaller: the size has to be readable as a quantity.
    for (let i = 1; i < band.length; i++) {
      expect(band[i], `${kind} at degree ${DEGREES[i]}`).toBeGreaterThanOrEqual(band[i - 1]);
    }
    // And an unconnected node of a kind is the smallest that kind is ever drawn.
    expect(band[0]).toBe(Math.min(...band));
  }

  // The bands are closed. The largest process ever drawn is smaller than the
  // smallest application, and the largest worker smaller than the smallest process —
  // so rank survives connectivity rather than competing with it.
  const largest = (kind) => Math.max(...sizes[kind]);
  const smallest = (kind) => Math.min(...sizes[kind]);
  expect(largest("process")).toBeLessThan(smallest("application"));
  for (const leaf of ["worker", "decision", "restricted", "unresolved"]) {
    expect(largest(leaf), `${leaf} against process`).toBeLessThan(smallest("process"));
  }
});

// A hub is worth seeing at a glance, and past a dozen dependencies there is nothing
// left for a radius to say that the panel does not say better.
test("growth is bounded rather than unbounded", async ({ page }) => {
  const busy = await radius(page, "process", 12);
  const absurd = await radius(page, "process", 5000);
  expect(absurd).toBe(busy);
  // And it is worth seeing: a well-connected process is visibly larger than a lonely
  // one, not larger by a rounding error.
  expect(busy).toBeGreaterThan((await radius(page, "process", 0)) * 1.2);
});

// An unknown kind is drawn as a process rather than as nothing: a node with no size
// is a node that is not on the picture, and a payload this view does not recognise
// is still a payload it has to draw.
test("an unfamiliar kind still has a size", async ({ page }) => {
  expect(await radius(page, "something-new", 3)).toBeGreaterThan(0);
});

// Degree is what would actually propagate. Counting containment would make every
// application a hub by construction, which is a size that says nothing.
test("degree counts dependencies, not containment", async ({ page }) => {
  const degrees = await page.evaluate(() => Object.fromEntries(window.degreesOf({
    nodes: [{ id: "a" }, { id: "p1" }, { id: "p2" }, { id: "w" }],
    edges: [
      { from: "a", to: "p1", kind: "contains" },
      { from: "a", to: "p2", kind: "contains" },
      { from: "p1", to: "p2", kind: "calls" },
      { from: "p1", to: "w", kind: "uses" },
      { from: "p1", to: "p1", kind: "calls" },
      // An edge to something not on screen — a filter can leave these behind — must
      // not be counted for an end that is not there, nor blow up on lookup.
      { from: "p2", to: "gone", kind: "calls" },
    ],
  })));

  expect(degrees).toEqual({ a: 0, p1: 3, p2: 2, w: 1 });
});

// Shape is the third channel after colour and size, and the one that survives what
// they do not: a printout, a projector, and a reader who does not separate the hues.

// The property the whole change rests on. The layout's separation guarantee is
// stated in circles — every node keeps a clear radius around it — so a shape that
// never leaves that circle cannot break it, and the guarantee transfers for free.
// A shape that poked out would put corners through neighbours at exactly the sizes
// where the picture is already tightest.
test("no shape leaves the circle the layout reserved for it", async ({ page }) => {
  const worst = await page.evaluate(() => {
    const out = {};
    for (const shape of ["circle", "square", "triangle", "hexagon", "diamond"]) {
      let far = 0;
      for (const r of [1, 11, 12, 17, 30, 42]) {
        for (const [x, y] of window.shapeVertices(shape, r)) {
          far = Math.max(far, Math.hypot(x, y) / r);
        }
      }
      out[shape] = far;
    }
    return out;
  });

  for (const [shape, reach] of Object.entries(worst)) {
    // A circle has no vertices, so its reach is 0 and it trivially fits; every
    // polygon touches the circle exactly and never crosses it.
    expect(reach, shape).toBeLessThanOrEqual(1.0001);
  }
  expect(worst.circle).toBe(0);
  for (const shape of ["square", "triangle", "hexagon", "diamond"]) {
    expect(worst[shape], shape).toBeCloseTo(1, 5);
  }
});

// Each kind gets its own outline, and no two kinds share one — a shape that stood
// for two things would be a channel spent on nothing.
test("every kind is a different shape", async ({ page }) => {
  const shapes = await page.evaluate(() => Object.fromEntries(
    ["application", "process", "worker", "decision", "restricted"]
      .map((kind) => [kind, window.shapeForNode({ kind, id: `${kind}:1` })])));

  expect(shapes).toEqual({
    application: "circle", process: "square", worker: "hexagon",
    decision: "triangle", restricted: "diamond",
  });
  expect(new Set(Object.values(shapes)).size).toBe(Object.keys(shapes).length);
});

// An unresolved dependency is drawn in the silhouette of the thing that is *missing*
// rather than in one that means "missing": its id names the kind, the dashes already
// say it is not there, and the shape says what should have been.
test("an unresolved dependency wears the shape of what is missing", async ({ page }) => {
  const shapes = await page.evaluate(() => ({
    process: window.shapeForNode({ kind: "unresolved", id: "unresolved:process:archive" }),
    worker: window.shapeForNode({ kind: "unresolved", id: "unresolved:worker:mail" }),
    decision: window.shapeForNode({ kind: "unresolved", id: "unresolved:decision:credit" }),
    // A kind this build does not draw, and an id that names none: both fall back to
    // the shape that is not any kind's, rather than borrowing one that is.
    unknown: window.shapeForNode({ kind: "unresolved", id: "unresolved:something:new" }),
    malformed: window.shapeForNode({ kind: "unresolved", id: "unresolved" }),
  }));

  expect(shapes).toEqual({
    process: "square", worker: "hexagon", decision: "triangle",
    unknown: "diamond", malformed: "diamond",
  });
});

// A kind this build does not know still gets drawn: a node with no shape is a node
// that is not on the picture, and a payload this view does not recognise is still a
// payload it has to render.
test("an unfamiliar kind still has a shape", async ({ page }) => {
  const shape = await page.evaluate(() =>
    window.shapeForNode({ kind: "something-new", id: "something-new:1" }));
  expect(shape).toBe("square");
});
