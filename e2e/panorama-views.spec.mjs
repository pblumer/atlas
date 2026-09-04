import { test, expect } from "@playwright/test";

// Saved landscape views: what a view captures, and where opening one puts you.

const WORLD = { width: 2000, height: 1000 };

test.beforeEach(async ({ page }) => {
  await page.goto("/panorama-views-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
});

// Storage is shared with the rest of the app and is written by an older version of
// this code as readily as by this one. Half-understood storage is how a view opens
// onto a filter nobody saved, so anything unrecognised reads as *absent*.
test("unrecognised storage reads as no views at all", async ({ page }) => {
  const results = await page.evaluate(() => {
    const at = (raw) => window.views.readViews({ getItem: () => raw });
    return {
      missing: at(null),
      garbage: at("{"),
      wrongVersion: at(JSON.stringify({ version: 99, views: [{ id: "a", name: "A" }] })),
      notAList: at(JSON.stringify({ version: 1, views: { id: "a" } })),
      // An array body is the trap: `parsed.views` on it is undefined, and a laxer
      // check would have taken the array itself for the list.
      arrayBody: at(JSON.stringify([{ id: "a", name: "A" }])),
      halfEntries: at(JSON.stringify({ version: 1, views: [{ id: "a", name: "A" }, { id: 7 }, null] })),
      blocked: window.views.readViews({ getItem: () => { throw new Error("off"); } }),
      noStorage: window.views.readViews(null),
    };
  });
  for (const [key, value] of Object.entries(results)) {
    if (key === "halfEntries") continue;
    expect(value, key).toEqual([]);
  }
  // A list with one usable entry keeps that entry and drops the rest.
  expect(results.halfEntries).toEqual([{ id: "a", name: "A" }]);
});

// A save the browser refused has to be said out loud: the reader would otherwise
// find out by coming back for a view that is not there.
test("a refused save is reported rather than swallowed", async ({ page }) => {
  const outcome = await page.evaluate(() => ({
    ok: window.views.writeViews(window.fakeStore(false), [{ id: "a", name: "A" }]),
    refused: window.views.writeViews(window.fakeStore(true), [{ id: "a", name: "A" }]),
  }));
  expect(outcome).toEqual({ ok: true, refused: false });
});

// "Billing watch" is a thing somebody keeps up to date. Two entries with one name is
// a list you have to read twice to use once.
test("saving a name that exists replaces it, keeping its identity", async ({ page }) => {
  const result = await page.evaluate(() => {
    const first = { id: "v1", name: "Billing watch", term: "billing" };
    let { views } = window.views.saveView([], first);
    const again = window.views.saveView(views, { id: "v2", name: "  billing WATCH ", term: "invoice" });
    return { count: again.views.length, kept: again.views[0].id, term: again.views[0].term };
  });
  // The name is matched past case and surrounding space — that is not what somebody
  // means by a different view — and the entry keeps the id it already had.
  expect(result).toEqual({ count: 1, kept: "v1", term: "invoice" });
});

// The bound refuses rather than dropping the oldest: silently discarding something
// somebody deliberately kept is the worse failure.
test("the list is bounded, and says so instead of forgetting", async ({ page }) => {
  const result = await page.evaluate(() => {
    let views = [];
    for (let i = 0; i < window.views.MAX_VIEWS; i++) {
      views = window.views.saveView(views, { id: `v${i}`, name: `View ${i}` }).views;
    }
    const full = window.views.saveView(views, { id: "extra", name: "One more" });
    const unnamed = window.views.saveView([], { id: "x", name: "   " });
    return {
      held: views.length,
      refused: full.views.length,
      error: full.error,
      unnamed: unnamed.error,
      // Replacing still works at the limit: it does not add anything.
      replaced: window.views.saveView(views, { id: "z", name: "View 3" }).views.length,
    };
  });
  expect(result.held).toBe(24);
  expect(result.refused).toBe(24);
  expect(result.error).toContain("limit");
  expect(result.unnamed).toContain("name");
  expect(result.replaced).toBe(24);
});

// The world is sized from the graph and the shape of the window, so a coordinate
// captured on one screen means somewhere else on another. Fractions travel.
test("a view captures where it was looking, not the pixels it was looking at", async ({ page }) => {
  const captured = await page.evaluate((world) => window.views.captureView({
    name: "Billing watch",
    term: "billing", direction: "dependencies", depth: "1", selected: "process:1",
    frameView: { x: 800, y: 300, w: 400, h: 200 },
    world,
    pinned: new Map([["process:1", { x: 1000, y: 500 }]]),
    at: 1700000000000,
  }), WORLD);

  expect(captured.zoom).toBeCloseTo(0.2, 5);
  expect(captured.centre.fx).toBeCloseTo(0.5, 5);
  expect(captured.centre.fy).toBeCloseTo(0.4, 5);
  expect(captured.pins).toEqual([["process:1", 0.5, 0.5]]);
  expect(captured.term).toBe("billing");
  expect(captured.selected).toBe("process:1");
  expect(captured.id).toBeTruthy();

  // On a differently shaped screen the same view frames the same *place*.
  const elsewhere = await page.evaluate((view) =>
    window.views.frameFor(view, { width: 1000, height: 800 }, () => null), captured);
  expect(elsewhere.w).toBeCloseTo(200, 5);
  expect(elsewhere.x + elsewhere.w / 2).toBeCloseTo(500, 5);
  expect(elsewhere.y + elsewhere.h / 2).toBeCloseTo(320, 5);

  const pins = await page.evaluate((view) =>
    [...window.views.pinsFor(view, { width: 1000, height: 800 })], captured);
  expect(pins).toEqual([["process:1", { x: 500, y: 400 }]]);
});

// The correction that matters. A saved view is nearly always somebody watching one
// node, and the landscape it sits in is *derived* — it changes as things are
// deployed, so the coordinates that framed that node last week frame empty space
// today.
test("a view watching a node follows the node, not its old coordinates", async ({ page }) => {
  const framed = await page.evaluate((world) => {
    const view = window.views.captureView({
      name: "watch", selected: "process:1", world,
      frameView: { x: 0, y: 0, w: 200, h: 100 },
      pinned: new Map(),
    });
    return {
      moved: window.views.frameFor(view, world, () => ({ x: 1500, y: 900 })),
      // And when the node is gone, the stored centre is what is left to go on.
      gone: window.views.frameFor(view, world, () => null),
    };
  }, WORLD);

  expect(framed.moved.x + framed.moved.w / 2).toBeCloseTo(1500, 5);
  expect(framed.moved.y + framed.moved.h / 2).toBeCloseTo(900, 5);
  expect(framed.gone.x + framed.gone.w / 2).toBeCloseTo(100, 5);
});

// A view saved at the fitted frame asked for the whole landscape, and must not come
// back as a zoom of 100% that happens to look like one.
test("a view saved un-zoomed reopens on the whole landscape", async ({ page }) => {
  const frames = await page.evaluate((world) => {
    const whole = window.views.captureView({ name: "all", world, frameView: null, pinned: new Map() });
    return {
      captured: whole.zoom,
      frame: window.views.frameFor(whole, world, () => null),
      nonsense: window.views.frameFor({ zoom: -3 }, world, () => null),
      absent: window.views.frameFor({}, world, () => null),
    };
  }, WORLD);
  expect(frames.captured).toBe(1);
  expect(frames.frame).toBeNull();
  expect(frames.nonsense).toBeNull();
  expect(frames.absent).toBeNull();
});

// Forgetting one leaves the others alone, and an id the list does not carry is
// already forgotten rather than an error.
test("removing a view is exact", async ({ page }) => {
  const left = await page.evaluate(() => {
    const views = [{ id: "a", name: "A" }, { id: "b", name: "B" }];
    return {
      after: window.views.removeView(views, "a").map((v) => v.id),
      unknown: window.views.removeView(views, "nope").map((v) => v.id),
    };
  });
  expect(left).toEqual({ after: ["b"], unknown: ["a", "b"] });
});

// A saved view is the whole question somebody saved, and whether the picture was
// carrying instance counts is part of it: the counts change what the nodes say and
// how much room the layout reserves for them, so a view that reopened without them
// would reopen a different picture.
test("a view remembers whether it was showing instance counts", async ({ page }) => {
  const on = await page.evaluate((world) => window.views.captureView({
    name: "Busy", term: "", direction: "dependents", depth: "2",
    instances: true, world, at: 1700000000000,
  }), WORLD);
  expect(on.instances).toBe(true);

  // And a view saved before the counts existed carries false rather than undefined —
  // off is the picture it was looking at, and the reader gets that picture back.
  const before = await page.evaluate((world) => window.views.captureView({
    name: "Old", term: "", direction: "dependents", depth: "2", world, at: 1700000000000,
  }), WORLD);
  expect(before.instances).toBe(false);
});

// The path into the picture is the narrowing a saved view is most likely to be
// about: somebody who followed a dependency four deep and saved it saved the walk,
// not the last node.
test("a view remembers the path it was standing on", async ({ page }) => {
  const walked = await page.evaluate((world) => window.views.captureView({
    name: "Down the mail path", term: "", direction: "dependents", depth: "2",
    trail: ["application:a1", "process:1", "worker:c1"], world, at: 1700000000000,
  }), WORLD);
  expect(walked.trail).toEqual(["application:a1", "process:1", "worker:c1"]);

  // No walk is null rather than an empty array: the whole starmap is not a path of
  // length zero, it is the absence of one, and the two read differently on reopening.
  const whole = await page.evaluate((world) => window.views.captureView({
    name: "All", term: "", direction: "dependents", depth: "2", world, at: 1700000000000,
  }), WORLD);
  expect(whole.trail).toBeNull();
});
