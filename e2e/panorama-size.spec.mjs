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

// Sizing by load instead of by structure (ADR-0211 §8's instance weighting). Same
// argument as the band test above: this is a claim about every tally a node could
// carry, so it is checked as arithmetic over the whole range rather than sampled
// from whatever a rendered fixture happens to contain.
const TALLIES = [0, 1, 2, 5, 25, 100, 999, 50002];

// The property the whole weighting rests on, and the one a reader would notice first
// if it were missing: nothing disappears. An idle process is still unmistakably a
// node, so "nothing running here" is a thing the picture can say rather than a gap
// indistinguishable from "not on this server".
test("every node keeps a floor, whatever is running on it", async ({ page }) => {
  const sizes = await page.evaluate((tallies) => ({
    // Against the busiest tally the fixture has, so the smallest share is the
    // smallest one this scale can produce.
    weighted: tallies.map((running) => window.radiusForInstances(
      { kind: "process", runtime: { running } }, Math.max(...tallies))),
    // The kinds that run nothing at all — not zero of something, but nothing that
    // could be counted — and a node whose payload carries no tally at all.
    idle: ["worker", "decision", "target", "restricted", "unresolved", "draft"]
      .map((kind) => window.radiusForInstances({ kind }, 100)),
    // And a landscape where nothing at all is running: no reference to divide by,
    // and the picture must still be a picture.
    quiet: window.radiusForInstances({ kind: "process", runtime: { running: 0 } }, 0),
  }), TALLIES);

  const floor = sizes.weighted[0];
  expect(floor).toBeGreaterThan(8);
  for (const r of [...sizes.idle, sizes.quiet]) expect(r).toBe(floor);
  for (const r of sizes.weighted) expect(r).toBeGreaterThanOrEqual(floor);
});

// More running is never smaller, and the busiest node is visibly the busiest — the
// whole point of asking for this weighting is to find it without reading a number.
test("load moves a node up, and the busiest is unmistakable", async ({ page }) => {
  const band = await page.evaluate((tallies) => tallies.map((running) =>
    window.radiusForInstances({ kind: "process", runtime: { running } }, Math.max(...tallies))),
  TALLIES);

  for (let i = 1; i < band.length; i++) {
    expect(band[i], `at ${TALLIES[i]} running`).toBeGreaterThanOrEqual(band[i - 1]);
  }
  // Not larger by a rounding error: the peak is several times the floor, so the two
  // ends of the estate are told apart at a glance rather than by measurement.
  expect(band[band.length - 1]).toBeGreaterThan(band[0] * 3);
});

// Area carries the count, not radius. Doubling a radius quadruples the ink, so a
// radius taken straight from the number would draw four times the load it stands
// for — which is the encoding error that makes a bubble chart lie.
test("the area above the floor is the share of the peak", async ({ page }) => {
  const [floor, quarter, whole] = await page.evaluate(() => [0, 25, 100].map((running) =>
    window.radiusForInstances({ kind: "process", runtime: { running } }, 100)));

  // A quarter of the peak's load is half of the peak's span above the floor, which
  // is what "area is proportional" means once the floor is subtracted.
  expect((quarter - floor) / (whole - floor)).toBeCloseTo(0.5, 6);
});

// A tally past the reference cannot happen on a landscape whose reference is its own
// busiest node — but a stale saved reference, or a node arriving between two reads,
// must not draw a circle that swallows the picture.
test("nothing is drawn larger than the reference", async ({ page }) => {
  const [peak, beyond] = await page.evaluate(() => [100, 10000].map((running) =>
    window.radiusForInstances({ kind: "process", runtime: { running } }, 100)));
  expect(beyond).toBe(peak);
});

// The reference is the busiest node on the landscape. Taken from the whole of it
// rather than from what is on screen — see the call site — but the arithmetic is
// here: a payload with no tallies at all has no reference, and says so as zero
// rather than as one.
test("the reference is the busiest tally, and absent when there is none", async ({ page }) => {
  const peaks = await page.evaluate(() => ({
    busiest: window.instancePeak({ nodes: [
      { id: "p1", runtime: { running: 12 } },
      { id: "p2", runtime: { running: 50002 } },
      { id: "w", kind: "worker" },
    ] }),
    idle: window.instancePeak({ nodes: [
      { id: "p1", runtime: { running: 0, finished: 7 } },
      { id: "w", kind: "worker" },
    ] }),
    empty: window.instancePeak({ nodes: [] }),
    // A payload this build does not recognise is still a payload it has to survive.
    malformed: window.instancePeak(null),
  }));

  expect(peaks).toEqual({ busiest: 50002, idle: 0, empty: 0, malformed: 0 });
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
    // Every shape the picture can draw, the notation projections' rectangles
    // included: a projection that let a corner out of the reserved circle would
    // break the separation guarantee for a picture the reader only switched the
    // vocabulary of.
    for (const shape of ["circle", "square", "triangle", "hexagon", "diamond", "pentagon", "box", "rounded"]) {
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
  for (const shape of ["square", "triangle", "hexagon", "diamond", "pentagon", "box", "rounded"]) {
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
