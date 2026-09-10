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
  //	r = floor + span * sqrt(share)   so   ((r - floor) / span)^2 == share
  //
  // The square root is the point. A circle's area goes up with the square of its
  // radius, so a radius taken straight from the number would draw four times the
  // quantity at twice the count — the encoding error that makes a bubble chart lie.
  //
  // This is asserted over the whole range and not at one point, because it is what the
  // picture *tells the reader it is doing*: the legend and the export stamp both spell
  // the relation out, and a sentence a reader can check is a sentence a test has to
  // hold the code to.
  test(`the ${heat} radius rises with the square root of the share`, async ({ page }) => {
    const shares = [0.01, 0.04, 0.09, 0.25, 0.5, 0.64, 1];
    const radii = await page.evaluate(([h, now, src, ss]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      const at = (v) => window.radiusForHeat(nodeFor(h, v), 10000, h, now);
      return { floor: at(0), peak: at(10000), each: ss.map((x) => at(x * 10000)) };
    }, [heat, NOW, NODE_FOR, shares]);

    const span = radii.peak - radii.floor;
    shares.forEach((share, i) => {
      const rose = (radii.each[i] - radii.floor) / span;
      expect(rose * rose, `at a share of ${share}`).toBeCloseTo(share, 6);
    });
  });

  // The claim this once made instead, kept as a test so it cannot come back. "The area
  // above the floor is the node's share" reads well and is false: the floor offsets the
  // relation, so the ring above the floor at a quarter of the peak's tally is about
  // 0.36 of the ring at the peak, not 0.25. It went into the legend, the export stamp,
  // the record and the changelog before arithmetic caught it.
  test(`the ${heat} weighting does not claim the ring above the floor is the share`, async ({ page }) => {
    const ring = await page.evaluate(([h, now, src]) => {
      const NOW_MS = now;
      const nodeFor = eval(src);
      const area = (r) => Math.PI * r * r;
      const at = (v) => area(window.radiusForHeat(nodeFor(h, v), 100, h, now));
      const floor = at(0);
      return (at(25) - floor) / (at(100) - floor);
    }, [heat, NOW, NODE_FOR]);

    expect(ring).toBeGreaterThan(0.3);
    expect(ring, "if this is 0.25 the encoding changed and the key must be rewritten")
      .not.toBeCloseTo(0.25, 2);
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
