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

// Sizing by a quantity instead of by structure (ADR-0211 §8's heat weightings). Same
// argument as the band test above: this is a claim about every tally a node could
// carry, so it is checked as arithmetic over the whole range rather than sampled
// from whatever a rendered fixture happens to contain.
//
// Both weightings are checked, and by the same table rather than by two copies of
// one test: they differ only in what they read off a node, and a property that held
// for one of them and not the other would be a picture nobody could trust to mean
// the same thing twice.
const TALLIES = [0, 1, 2, 5, 25, 100, 999, 50002];
// NOW is the moment the duration weighting is measured against, fixed so the third
// row is arithmetic like the other two rather than a race with the wall clock.
const NOW = 1_800_000_000_000;
const HEATS = ["instances", "incidents", "incident-age"];
// The smallest tally each weighting tells apart from the next one up — the bottom of
// its scale, where the radius is the floor plus one step. Written out here rather
// than read off the module on purpose: it is the contract the key states to a reader,
// and a test that asked the code what the code does would check nothing.
const LEAST = { instances: 1, incidents: 1, "incident-age": 60_000 };
// And the step itself: what a node earns the moment it carries anything at all.
const STEP = 6;

// nodeWith builds a process carrying one weighting's tally, in the browser. Inlined
// into each evaluate() rather than passed across, because a function cannot cross
// into the page — so it is written once here and stringified there.
const NODE_FOR = `(h, n) => h === "instances" ? { kind: "process", runtime: { running: n } }
  : h === "incidents" ? { kind: "process", incidents: n }
  // A duration is a moment on the node read against a clock, so a tally of n
  // milliseconds is an incident raised n milliseconds ago — in nanoseconds, which is
  // what the server sends.
  : { kind: "process", oldestIncident: n > 0 ? (NOW_MS - n) * 1e6 : 0 }`;

// The property the whole weighting rests on, and the one a reader would notice first
// if it were missing: nothing disappears. A quiet process is still unmistakably a
// node, so "nothing here" is a thing the picture can say rather than a gap
// indistinguishable from "not on this server".
for (const heat of HEATS) {
  test(`every node keeps a floor on the ${heat} weighting`, async ({ page }) => {
    const sizes = await page.evaluate(([tallies, h, now, src]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      return {
        // Against the largest tally the fixture has, so the smallest share is the
        // smallest one this scale can produce.
        weighted: tallies.map((n) => window.radiusForHeat(
          nodeFor(h, n), Math.max(...tallies), h, now)),
        // The kinds that count nothing at all — not zero of something, but nothing
        // that could be counted — and a node whose payload carries no tally at all.
        quiet: ["worker", "decision", "target", "restricted", "unresolved", "draft"]
          .map((kind) => window.radiusForHeat({ kind }, 100, h, now)),
        // And a landscape where nothing at all is counted: no reference to divide by,
        // and the picture must still be a picture.
        flat: window.radiusForHeat({ kind: "process" }, 0, h, now),
        // A weighting this build has never heard of — a saved view from a later one —
        // falls back rather than drawing every node at nothing.
        unknown: window.radiusForHeat({ kind: "process" }, 100, "something-new", now),
      };
    }, [TALLIES, heat, NOW, NODE_FOR]);

    const floor = sizes.weighted[0];
    expect(floor).toBeGreaterThan(8);
    for (const r of [...sizes.quiet, sizes.flat, sizes.unknown]) expect(r).toBe(floor);
    for (const r of sizes.weighted) expect(r).toBeGreaterThanOrEqual(floor);

    // A node measured by *another* weighting's field sits at the floor too: the radii
    // on one picture answer one question, and never half of another.
    const crossed = await page.evaluate(([h, others, now, src]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      return others.map((other) => window.radiusForHeat(nodeFor(other, 500), 500, h, now));
    }, [heat, HEATS.filter((h) => h !== heat), NOW, NODE_FOR]);
    for (const r of crossed) expect(r).toBe(floor);
  });

  // More is never smaller, and the largest node is visibly the largest — the whole
  // point of asking for a weighting is to find it without reading a number.
  test(`${heat} moves a node up, and the worst is unmistakable`, async ({ page }) => {
    const band = await page.evaluate(([tallies, h, now, src]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      return tallies.map((n) =>
        window.radiusForHeat(nodeFor(h, n), Math.max(...tallies), h, now));
    }, [TALLIES, heat, NOW, NODE_FOR]);

    for (let i = 1; i < band.length; i++) {
      expect(band[i], `at ${TALLIES[i]}`).toBeGreaterThanOrEqual(band[i - 1]);
    }
    // Not larger by a rounding error: the peak is several times the floor, so the two
    // ends of the estate are told apart at a glance rather than by measurement.
    expect(band[band.length - 1]).toBeGreaterThan(band[0] * 3);
  });

  // The law the key states, checked as the law rather than as a sample of it:
  //
  //	r = floor + step + (span - step) · ln(value/least) / ln(peak/least)
  //
  // which is a *ratio* scale: equal steps of radius are equal multiples of the tally.
  // A process running ten instances stands as far above one running one as one
  // running a hundred stands above it. That is the sentence the key and the export
  // stamp both put in front of a reader, and a sentence a reader can check is one a
  // test has to hold the code to.
  test(`the ${heat} radius rises with the ratio, in equal steps per tenfold`, async ({ page }) => {
    const least = LEAST[heat];
    const decades = [1, 10, 100, 1000, 10000];
    const radii = await page.evaluate(([h, now, src, ds, l]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      const peak = l * 10000;
      const at = (v) => window.radiusForHeat(nodeFor(h, v), peak, h, now);
      return { floor: at(0), each: ds.map((d) => at(l * d)) };
    }, [heat, NOW, NODE_FOR, decades, least]);

    // Four tenfolds, four equal steps. This is the whole claim.
    const steps = radii.each.slice(1).map((r, i) => r - radii.each[i]);
    for (const step of steps) expect(step, "one tenfold").toBeCloseTo(steps[0], 6);
    // And the ends are where the key says they are: the smallest tally the weighting
    // distinguishes is one step above the floor, the peak is the top of the scale.
    expect(radii.each[0] - radii.floor).toBeCloseTo(STEP, 6);
    expect(radii.each[decades.length - 1] - radii.floor).toBeCloseTo(30, 6);
  });

  // What the reported defect actually was, stated as the property that fixes it: a
  // node carrying the least this weighting can count is already unmistakably bigger
  // than a node carrying nothing — whatever the busiest node on the landscape is
  // doing. Under the square-root law it was not: against a peak of four thousand, one
  // running instance was drawn half a unit above idle on a span of thirty.
  test(`one ${heat} is drawn clear of none, however busy the landscape`, async ({ page }) => {
    const least = LEAST[heat];
    const drawn = await page.evaluate(([h, now, src, l, peaks]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      return peaks.map((peak) => ({
        peak,
        none: window.radiusForHeat(nodeFor(h, 0), l * peak, h, now),
        one: window.radiusForHeat(nodeFor(h, l), l * peak, h, now),
      }));
    }, [heat, NOW, NODE_FOR, least, [1, 2, 10, 4000, 1e6]]);

    for (const row of drawn) {
      // Visible rather than technically larger: half again the radius, and more than
      // twice the area, at every peak.
      expect(row.one / row.none, `against a peak of ${row.peak}`).toBeGreaterThan(1.5);
      expect((row.one * row.one) / (row.none * row.none)).toBeGreaterThan(2);
      // And exactly one step, so "this one is doing something" does not get quieter
      // as the landscape gets busier. The exception is a landscape with no range at
      // all — every process running one, every node holding its only incident — where
      // the smallest tally is also the largest and is drawn as the largest, because
      // being the worst is what it is.
      const step = row.one - row.none;
      if (row.peak > 1) expect(step, `against a peak of ${row.peak}`).toBeCloseTo(STEP, 6);
      else expect(step, "a landscape with no range").toBeCloseTo(30, 6);
    }
  });

  // Two claims this picture used to make, kept as tests so neither can come back.
  //
  // It said the radius rose with the square root of the share, which made a circle's
  // *area* proportional to the tally. That was arithmetically right and answered the
  // wrong question — see radiusForHeat — and the scale is a ratio scale now. It also
  // once said the ring above the floor was the node's share, which was never true at
  // all: the floor offsets the relation.
  //
  // Both are pinned negatively, because the sentence in the key is what a reader
  // takes away and a stale one is worse than none.
  test(`the ${heat} weighting is not a square-root scale, and not a share of area`, async ({ page }) => {
    const least = LEAST[heat];
    const read = await page.evaluate(([h, now, src, l]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      const peak = l * 100;
      const at = (v) => window.radiusForHeat(nodeFor(h, v), peak, h, now);
      const area = (r) => Math.PI * r * r;
      const floor = at(0), top = at(peak);
      return {
        // Where a square-root scale would put a quarter of the peak, against where
        // this one does.
        rose: (at(l * 25) - floor) / (top - floor),
        ring: (area(at(l * 25)) - area(floor)) / (area(top) - area(floor)),
      };
    }, [heat, NOW, NODE_FOR, least]);

    // sqrt(0.25) is 0.5. If this comes back to 0.5 the encoding changed and every
    // sentence naming it — the key, the export stamp, ADR-0211 §8 — must change too.
    expect(read.rose, "if this is 0.5 the scale is a square-root one again")
      .not.toBeCloseTo(0.5, 2);
    expect(read.ring, "if this is 0.25 the ring above the floor is the share again")
      .not.toBeCloseTo(0.25, 2);
  });

  // The scale in the key, as arithmetic: which tallies get a circle.
  //
  // Powers of ten from the weighting's least upward and then the peak, because the law
  // is logarithmic and the marks a reader can interpolate between on a logarithmic
  // scale are the decades. A linear set of marks would be four of them crowded at one
  // end, saying nothing about the range the picture actually spans.
  test(`the ${heat} scale is marked by decades, and always names both ends`, async ({ page }) => {
    const least = LEAST[heat];
    const read = await page.evaluate(([h, l]) => ({
      quiet: window.heatTicks(0, h),
      flat: window.heatTicks(l, h),
      narrow: window.heatTicks(l * 2, h),
      decade: window.heatTicks(l * 40, h),
      wide: window.heatTicks(l * 4200, h),
      vast: window.heatTicks(l * 1e7, h),
    }), [heat, least]);

    // Nothing counted anywhere: no scale, because there is nothing to scale against.
    expect(read.quiet).toEqual([]);
    // Every other landscape names its own ends — the smallest tally that counts, and
    // the largest there is — whatever happens to the rungs in between.
    for (const [name, ticks] of Object.entries(read)) {
      if (name === "quiet") continue;
      expect(ticks[0], `${name} starts at the least`).toBe(least);
      expect(ticks[ticks.length - 1], `${name} ends at the peak`).toBe(
        name === "flat" ? least : { narrow: least * 2, decade: least * 40, wide: least * 4200, vast: least * 1e7 }[name]);
      // Short enough to stay on one line beside a legend that already carries the
      // kinds, the edges, the severities and the provenances. The nothing-at-all
      // circle takes a place of its own, so this counts to one less than the row does.
      expect(ticks.length, `${name} fits the row`).toBeLessThanOrEqual(4);
    }
    // And the rungs between the ends are a constant multiple rather than a different
    // one each time: a ladder of ten, then a hundredfold, then fourfold is three rules
    // on one line and a reader carries none of them.
    const rungs = read.vast.slice(0, -1);
    const stride = rungs.slice(1).map((v, i) => v / rungs[i]);
    for (const each of stride) expect(each).toBeCloseTo(stride[0], 6);
  });

  // The circles in the key are the law, not a drawing of it. The scale is sized by
  // radiusForTally, which is what sized the nodes — so a reader holding a node against
  // a reference circle is comparing like with like, and neither can be changed without
  // the other following.
  test(`the ${heat} scale is sized by the same law as the picture`, async ({ page }) => {
    const least = LEAST[heat];
    const same = await page.evaluate(([h, now, src, l]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      const peak = l * 4200;
      return window.heatTicks(peak, h).map((value) => ({
        value,
        onScale: window.radiusForTally(value, peak, h),
        onCanvas: window.radiusForHeat(nodeFor(h, value), peak, h, now),
      }));
    }, [heat, NOW, NODE_FOR, least]);

    expect(same.length).toBeGreaterThan(1);
    for (const row of same) expect(row.onScale, `at ${row.value}`).toBeCloseTo(row.onCanvas, 9);
  });

  // A tally past the reference — a stale saved reference, or a node arriving between
  // two reads — must not draw a circle that swallows the picture.
  test(`nothing on the ${heat} weighting is drawn larger than the reference`, async ({ page }) => {
    const [whole, beyond] = await page.evaluate(([h, now, src]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      return [100, 10000].map((v) => window.radiusForHeat(nodeFor(h, v), 100, h, now));
    }, [heat, NOW, NODE_FOR]);
    expect(beyond).toBe(whole);
  });
}

// A duration is the one weighting measured against a clock rather than read off the
// node, and every node on one picture has to be measured against the same reading:
// taken per node, the reference would be a few milliseconds older than the node that
// set it, and the largest node would come out larger than the whole it is a share of.
test("a duration weighting is measured against one moment, not a running clock", async ({ page }) => {
  const sizes = await page.evaluate((now) => {
    const oldest = { kind: "process", oldestIncident: (now - 60000) * 1e6 };
    const peak = window.heatPeak({ nodes: [oldest] }, "incident-age", now);
    return {
      peak,
      // The node that set the reference, measured against it.
      atPeak: window.radiusForHeat(oldest, peak, "incident-age", now),
      // The same node a second later, against a reference taken a second ago: still
      // clamped rather than swelling past the whole.
      later: window.radiusForHeat(oldest, peak, "incident-age", now + 1000),
      // A moment the server never dated is not an incident raised in 1970.
      undated: window.radiusForHeat({ kind: "process", incidents: 9 }, peak, "incident-age", now),
      floor: window.radiusForHeat({ kind: "process" }, peak, "incident-age", now),
    };
  }, NOW);

  expect(sizes.peak).toBe(60000);
  expect(sizes.later).toBe(sizes.atPeak);
  expect(sizes.undated).toBe(sizes.floor);
  expect(sizes.atPeak).toBeGreaterThan(sizes.floor * 3);
});

// The reference is the largest node on the landscape, per weighting. Taken from the
// whole of it rather than from what is on screen — see the call site — but the
// arithmetic is here: a payload with no tallies at all has no reference, and says so
// as zero rather than as one.
test("the reference is the largest tally of whichever quantity is being drawn", async ({ page }) => {
  const peaks = await page.evaluate((now) => {
    const graph = { nodes: [
      { id: "p1", runtime: { running: 12 }, incidents: 3, oldestIncident: (now - 5000) * 1e6 },
      { id: "p2", runtime: { running: 50002 } },
      { id: "p3", incidents: 41, oldestIncident: (now - 90000) * 1e6 },
      { id: "w", kind: "worker" },
    ] };
    return {
      running: window.heatPeak(graph, "instances", now),
      parked: window.heatPeak(graph, "incidents", now),
      // The oldest, which is not the one holding the most: two orderings of one
      // estate, which is the whole reason both weightings exist.
      stuck: window.heatPeak(graph, "incident-age", now),
      // A healthy estate has no incidents, and zero is the answer rather than a
      // missing one — the picture it produces is flat, and flat is the finding.
      healthy: window.heatPeak({ nodes: [{ id: "p1", runtime: { running: 9 } }] }, "incidents", now),
      // As is an estate whose incidents this engine never dated: absent, not 1970.
      undated: window.heatPeak({ nodes: [{ id: "p1", incidents: 9 }] }, "incident-age", now),
      empty: window.heatPeak({ nodes: [] }, "instances", now),
      // A payload this build does not recognise, and a weighting it does not know:
      // both are survivable rather than fatal.
      malformed: window.heatPeak(null, "instances", now),
      unknown: window.heatPeak(graph, "something-new", now),
      none: window.heatPeak(graph, null, now),
    };
  }, NOW);

  expect(peaks).toEqual({
    running: 50002, parked: 41, stuck: 90000, healthy: 0, undated: 0,
    empty: 0, malformed: 0, unknown: 0, none: 0,
  });
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

// How often the view re-reads the landscape it is drawing (ADR-0211 §7). The mesh is
// derived on the run loop and the size budget exists because that is not free, so the
// cadence is a fraction of what the last derive actually cost rather than a constant
// somebody guessed — and a claim about every cost is arithmetic rather than a wait.
test("the re-read cadence is a fraction of what the derive costs", async ({ page }) => {
  const paced = await page.evaluate(() => ({
    floor: window.REFRESH_FLOOR,
    ceiling: window.REFRESH_CEILING,
    // A landscape that derives in no time is re-read on the floor: there is nothing
    // to protect, and asking faster would buy nothing a reader could see.
    quick: window.refreshEvery(40),
    // The floor holds until the cost would push past it, which is where the two rules
    // cross: at a twentieth of the cadence, a derive has to reach 1.5 s before the
    // pacing has anything to say. Pinned from both sides, because a crossover nobody
    // states is one that moves the next time a constant is touched.
    justUnder: window.refreshEvery(1400),
    justOver: window.refreshEvery(1600),
    // Past it the cadence is the cost, and backs off on its own without anybody
    // configuring anything.
    heavy: window.refreshEvery(4000),
    // And an estate whose derive is measured in tens of seconds settles at the
    // ceiling rather than growing without bound.
    vast: window.refreshEvery(60_000),
    // A cost that could not be measured must not read as "free".
    unmeasured: [window.refreshEvery(0), window.refreshEvery(NaN), window.refreshEvery()],
    // A server that would not answer is asked at the ceiling, whatever the last good
    // derive cost: it does not want thirty requests a minute from every open tab, and
    // the freshness line is already saying the picture is not being kept up.
    failing: window.refreshEvery(40, { failing: true }),
  }));

  expect(paced.quick).toBe(paced.floor);
  for (const ms of paced.unmeasured) expect(ms).toBe(paced.floor);
  expect(paced.justUnder).toBe(paced.floor);
  expect(paced.justOver).toBeGreaterThan(paced.floor);
  expect(paced.heavy).toBeGreaterThan(paced.justOver);
  expect(paced.vast).toBe(paced.ceiling);
  expect(paced.failing).toBe(paced.ceiling);
  // Never faster than the floor and never slower than the ceiling, at any cost.
  for (const ms of [0, 1, 100, 1e4, 1e9]) {
    const every = await page.evaluate((c) => window.refreshEvery(c), ms);
    expect(every, `at ${ms} ms`).toBeGreaterThanOrEqual(paced.floor);
    expect(every, `at ${ms} ms`).toBeLessThanOrEqual(paced.ceiling);
  }
});
