// e2e for the decision graph window (api/web/decision-graph.js).
//
// Double-clicking a business rule task in Operations opens the decision behind it:
// its requirements graph with the case drawn on it, the rules that fired, and the
// answer. The window exists to be turned towards somebody from the business, so
// what these tests assert is not that it rendered — it is that what it says is
// true. A value on the wrong node, or a silence that reads like "no rules matched"
// when it means "no rules were recorded", is the failure mode that matters.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/decision-graph-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// nodeText returns what one node of the graph says, found by the name drawn in it.
const nodeText = (page, name) =>
  page.locator(".drg-canvas g", { has: page.locator(`text="${name}"`) }).first();

test("an evaluation is addressed by the server's exact key, not by a rounded number", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  const calls = await page.evaluate(() => window.__apiCalls);
  expect(calls).toHaveLength(1);
  // A nanosecond timestamp is past 2^53. Round-tripping it through a JavaScript
  // number turns …033700 into …033800, which addresses an evaluation that does not
  // exist — so the path must carry the digits the server served, unchanged.
  expect(calls[0].path).toBe("/api/v1/instances/281474999937230/decisions/1789824241612033700/graph");
  expect(calls[0].path).not.toContain("1789824241612033800");
});

test("the graph carries the case: what went in, what came of it, and the answer", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");

  // Every input datum the task handed over shows the value it handed over.
  await expect(nodeText(page, "betrag")).toContainText("60000");
  await expect(nodeText(page, "laufzeitMonate")).toContainText("48");
  await expect(nodeText(page, "einkommen")).toContainText("5000");
  // An intermediate decision shows what it worked out — read off the trace, which
  // records the value the table above it tested.
  await expect(nodeText(page, "Tragbarkeit")).toContainText("0.25");
  // And the decision that answered shows the answer, marked as the answer.
  const answer = nodeText(page, "Kreditentscheid");
  await expect(answer).toContainText("abgelehnt");
  await expect(answer).toHaveClass(/is-result/);
});

test("a decision the caller supplied is drawn as given, not as computed", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  // bonitaet arrived in the input context rather than being worked out, which is
  // what a decision service's boundary looks like from outside. The picture has to
  // say so, or the legend beside it is a lie — and "who decided the rating" is
  // exactly the question somebody accounting for a rejection will ask.
  const given = await nodeText(page, "Bonität").locator("rect").getAttribute("fill");
  const computed = await nodeText(page, "Tragbarkeit").locator("rect").getAttribute("fill");
  expect(given).toBe("#eff6ff");
  expect(computed).not.toBe(given);
  await expect(nodeText(page, "Bonität").locator("title"))
    .toHaveText(/supplied, not computed/);
});

test("a node the case never touched says so rather than showing nothing", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  const idle = nodeText(page, "zahlungsstoerungen");
  await expect(idle).toHaveClass(/is-idle/);
  await expect(idle).toContainText("not part of this case");
});

test("the rule that carried the answer is shown, with why the ones above it did not", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-rules .mgrid");
  await expect(page.locator(".drg-rules .mtable-head")).toContainText("Rule 6 fired");

  // The rule that fired, and the output it carried.
  const fired = page.locator(".drg-rules tr.mrule.is-hit");
  await expect(fired).toHaveCount(1);
  await expect(fired).toContainText("abgelehnt");

  // And the near miss: rule 5 matched the rating and failed on the ratio. That pair
  // — one condition green, the next red — is the whole explanation of the rejection,
  // so it is asserted rather than left to the eye.
  const nearMiss = page.locator(".drg-rules tr.mrule").nth(4);
  await expect(nearMiss.locator("td.mcell.is-ok")).toContainText('"C"');
  await expect(nearMiss.locator("td.mcell.is-no")).toContainText("<= 0.15");
});

test("a decision service says why there is no rule matrix instead of showing none", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__atService));
  await page.waitForSelector(".drg-canvas svg");

  await expect(page.locator(".drg-tag")).toHaveText(/decision service/i);
  // The distinction the window turns on: the values are exact, and only the
  // rule-level detail is missing. Reading this as "nothing matched" would be a
  // different, and false, account of the case.
  const rules = page.locator(".drg-rules");
  await expect(rules).toContainText("records no rule-by-rule trace");
  await expect(rules).toContainText("exactly what the case carried");
  await expect(page.locator(".drg-rules .mgrid")).toHaveCount(0);

  // The graph still carries the case, which is the point of saying it that way.
  await expect(nodeText(page, "betrag")).toContainText("60000");
  await expect(nodeText(page, "Kreditentscheid")).toContainText("abgelehnt");
  // With no trace there is nothing that can speak for the encapsulated decision, so
  // it is left blank rather than guessed at.
  await expect(nodeText(page, "Tragbarkeit")).toContainText("not part of this case");
});

test("a decision whose model is gone says so rather than drawing an empty frame", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__atGone));
  await page.waitForSelector(".drg-canvas");
  await expect(page.locator(".drg-canvas")).toContainText("no graph to draw");
  // The record itself still stands, and still answers the first question asked of it.
  await expect(page.locator(".drg-result")).toContainText("abgelehnt");
});

test("an evaluation that cannot be loaded reports the reason in the window", async ({ page }) => {
  await page.evaluate(() => window.__open("1789824241612039999"));
  await expect(page.locator(".drg-body")).toContainText("no decision evaluation recorded at that time");
});

test("the window closes by Escape, by the backdrop and by its button, and gives focus back", async ({ page }) => {
  for (const close of [
    async () => page.keyboard.press("Escape"),
    async () => page.locator(".drg-x").click(),
    async () => page.locator(".drg-ov").click({ position: { x: 5, y: 5 } }),
  ]) {
    await page.locator("#opener").focus();
    await page.evaluate(() => window.__open(window.__at));
    await page.waitForSelector(".drg-canvas svg");
    await close();
    await expect(page.locator(".drg-ov")).toHaveCount(0);
    // Focus goes back where the gesture came from, so a keyboard reader is not
    // dropped at the top of the page each time they look at a decision.
    expect(await page.evaluate(() => document.activeElement && document.activeElement.id)).toBe("opener");
  }
});

test("opening a second decision replaces the first rather than stacking windows", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  await page.evaluate(() => window.__open(window.__atService));
  await page.waitForSelector(".drg-tag");
  await expect(page.locator(".drg-ov")).toHaveCount(1);
  await expect(page.locator("#drg-title")).toHaveText("Kreditfreigabe");
});

test("navigating away takes the window with it", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  // It is appended to the body, so nothing else removes it — and a fixed overlay left
  // standing over the next screen is the worst kind of bug to reproduce.
  await page.evaluate(() => { location.hash = "#/operations/incidents"; });
  await expect(page.locator(".drg-ov")).toHaveCount(0);
});

test("the plain renderer the Modeler shares still draws a model with no case on it", async ({ page }) => {
  // app.js imports this same function for the read-only DMN view, so it has to keep
  // drawing a graph that carries no evaluation at all.
  const svg = await page.evaluate(() => window.__plain());
  expect(svg).toContain("<svg");
  expect(svg).toContain("Kreditentscheid");
  expect(svg).toContain("decision table");
  expect(svg).not.toContain("not part of this case");
});

// The DRD notation, held as one table.
//
// In a DRD the shape carries the meaning: it is the only thing telling a reader a
// decision from an input datum, and the line style plus the arrowhead are the only
// things telling one requirement from another. None of it is styling, and a test
// that only says "it rendered" cannot see the difference between the right shape and
// the wrong one — which is how a business knowledge model came to be drawn as a
// parallelogram, a shape the notation does not have.
//
// This is the same table, read the same way, that the modeler's own renderer is held
// against in the vendored fork (dmn-js, packages/dmn-js-drd/test/spec/draw/
// DrdNotationSpec.js). The two renderers draw the same models in two places, so they
// answer to the same rows.
//
// Atlas draws three of the notation's shapes and two of its lines, and that is all of
// it: a graph carries decisions, input data and business knowledge models, joined by
// information and knowledge requirements, and nothing else — dmn/graph_test.go pins
// that, so covering five rows here is covering everything that can arrive. A
// knowledge source, a text annotation, an authority requirement, an association and
// the decision service box never reach this renderer; the modeler's draws them, and
// is held to them there.
//
// Source: DMN 1.5, §5.3.3, Table 5-2.

// measure draws the picture into the page and reads the geometry of every node and
// every edge back off it. Geometry rather than markup is the point: a shape may move
// from a <rect> to a <polygon> and still be the right shape, and a row still says so.
const measure = (page, selector) => page.evaluate((sel) => {
  let host = null, svg;
  if (sel) {
    svg = document.querySelector(sel);
  } else {
    host = document.createElement("div");
    host.innerHTML = window.__notation();
    document.body.appendChild(host);
    svg = host.querySelector("svg");
  }

  const SAMPLES = 2000;
  const sample = (shape) => {
    const length = shape.getTotalLength(), points = [];
    for (let i = 0; i < SAMPLES; i++) points.push(shape.getPointAtLength((length * i) / SAMPLES));
    return points;
  };

  // The radius the drawn edge turns each corner of its own bounding box with,
  // clockwise from the top left. A square corner leaves no gap and reports 0; a
  // circular corner of radius r leaves the corner a gap of r * (sqrt(2) - 1), which
  // is what this undoes — so what comes back is the radius a reader sees.
  const cornerRadii = (shape) => {
    const b = shape.getBBox(), points = sample(shape);
    return [
      [b.x, b.y], [b.x + b.width, b.y],
      [b.x + b.width, b.y + b.height], [b.x, b.y + b.height],
    ].map(([cx, cy]) =>
      Math.min(...points.map((p) => Math.hypot(p.x - cx, p.y - cy))) / (Math.SQRT2 - 1));
  };

  // The straight run the drawn edge keeps on the left and on the right side. This is
  // what tells a rectangle with its corners taken off from a box sheared into a
  // parallelogram: the first keeps most of both sides, the second keeps none of
  // either.
  const sideRuns = (shape) => {
    const b = shape.getBBox(), points = sample(shape);
    const run = (x) => {
      const ys = points.filter((p) => Math.abs(p.x - x) < 0.5).map((p) => p.y);
      return ys.length ? Math.max(...ys) - Math.min(...ys) : 0;
    };
    return { left: run(b.x), right: run(b.x + b.width) };
  };

  // What a drawn property resolves to. Written as an attribute here and as an inline
  // style by the modeler, and a stylesheet may still have the last word, so all three
  // are asked — in that order, because a marker parked in <defs> is never laid out
  // and has no computed style worth reading.
  const styleOf = (el, property) =>
    el.style.getPropertyValue(property) || el.getAttribute(property) ||
    window.getComputedStyle(el).getPropertyValue(property);

  // What a line is drawn as: a dot is a dash no longer than the line is thick.
  const lineStyle = (line) => {
    const dashes = (styleOf(line, "stroke-dasharray") || "").trim();
    if (!dashes || dashes === "none") return "solid";
    const on = parseFloat(dashes.split(/[\s,]+/)[0]);
    return on <= (parseFloat(styleOf(line, "stroke-width")) || 1) ? "dotted" : "dashed";
  };

  // What a line ends in, read off the marker it points at.
  const headOf = (line) => {
    const ref = (styleOf(line, "marker-end") || "").match(/url\(["']?#(.+?)["']?\)/);
    if (!ref) return "none";
    const head = svg.querySelector(`#${ref[1]}`).firstElementChild;
    const filled = styleOf(head, "fill") !== "none" ? "filled" : "open";
    return head.tagName.toLowerCase() === "circle" ? `${filled}-circle` : `${filled}-arrow`;
  };

  const nodes = {};
  for (const group of svg.querySelectorAll("g")) {
    const name = group.querySelector("text"), shape = group.querySelector("rect, polygon, path");
    if (!name || !shape) continue;
    nodes[name.textContent] = {
      cornerRadii: cornerRadii(shape),
      sideRuns: sideRuns(shape),
      height: shape.getBBox().height,
    };
  }

  const edges = {};
  for (const line of svg.querySelectorAll("line[data-type]")) {
    edges[line.dataset.type] = { line: lineStyle(line), head: headOf(line) };
  }

  if (host) host.remove();
  return { nodes, edges };
}, selector);

// What each kind of node is drawn as, and what that is recognised by.
const SHAPES = [
  {
    element: "a decision",
    node: "Kreditentscheid",
    drawnAs: "a plain rectangle",
    // Rounding its corners makes it read as an input datum or as a decision service,
    // which are the two things it is not.
    check: (drawn) => drawn.cornerRadii.forEach((radius) => expect(radius).toBeLessThan(1)),
  },
  {
    element: "an input datum",
    node: "betrag",
    drawnAs: "a stadium, a rectangle with fully rounded ends",
    // Fully rounded means the round runs the whole height — half the height, not a
    // radius that happens to look like one at a particular size.
    check: (drawn) => drawn.cornerRadii.forEach((radius) =>
      expect(Math.abs(radius - drawn.height / 2)).toBeLessThan(1.5)),
  },
  {
    element: "a business knowledge model",
    node: "Ratenrechner",
    drawnAs: "a rectangle with two opposite corners cut off",
    // Two corners square, two not, on a diagonal — no other shape in the table has
    // that signature. The sides are the second half of it: what is drawn is still a
    // rectangle with two corners taken off, and slanting both whole sides instead
    // satisfies the corners alone while drawing a parallelogram.
    check: (drawn) => {
      const [topLeft, topRight, bottomRight, bottomLeft] = drawn.cornerRadii;
      expect(topLeft).toBeGreaterThan(4);
      expect(bottomRight).toBeGreaterThan(4);
      expect(topRight).toBeLessThan(1);
      expect(bottomLeft).toBeLessThan(1);
      expect(drawn.sideRuns.left).toBeGreaterThan(drawn.height * 0.5);
      expect(drawn.sideRuns.right).toBeGreaterThan(drawn.height * 0.5);
    },
  },
];

// What each kind of requirement is drawn as: the line, and what the line ends in.
// Both halves carry meaning — "this decision needs that value" against "this
// decision invokes that logic" — so both are held.
const LINES = [
  {
    element: "an information requirement",
    edge: "informationRequirement",
    line: "solid",
    head: "filled-arrow",
  },
  {
    element: "a knowledge requirement",
    edge: "knowledgeRequirement",
    line: "dashed",
    head: "open-arrow",
  },
];

for (const row of SHAPES) {
  test(`the notation: ${row.element} is drawn as ${row.drawnAs}`, async ({ page }) => {
    const { nodes } = await measure(page);
    expect(nodes[row.node], `${row.node} is drawn`).toBeTruthy();
    row.check(nodes[row.node]);
  });
}

for (const row of LINES) {
  test(`the notation: ${row.element} is drawn as a ${row.line} line ending in ${row.head}`,
    async ({ page }) => {
      const { edges } = await measure(page);
      expect(edges[row.edge], `${row.edge} is drawn`).toBeTruthy();
      expect(edges[row.edge]).toEqual({ line: row.line, head: row.head });
    });
}

test("the case view draws the shapes the plain view draws", async ({ page }) => {
  // Two call sites, one notation: the window that draws a case on the graph and the
  // read-only view the Modeler shares both go through the same shape function. A fix
  // that reaches only one of them is what this catches.
  const plain = (await measure(page)).nodes;
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  const drawn = (await measure(page, ".drg-canvas svg")).nodes;

  const rounded = (node) => node.cornerRadii.map((radius) => Math.round(radius));
  expect(rounded(drawn["Kreditentscheid"])).toEqual(rounded(plain["Kreditentscheid"]));
  expect(rounded(drawn["betrag"])).toEqual(rounded(plain["betrag"]));
});
