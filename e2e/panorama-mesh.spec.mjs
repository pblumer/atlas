import { test, expect } from "@playwright/test";

// The derived landscape mesh (ADR-0211). These tests drive the real view against a
// mocked mesh payload, so they cover what a browser actually renders: the graph, the
// drilldown link, and — the part that matters most — that a filtered or collapsed
// picture says so in words rather than merely looking smaller.

const graph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", application: "application:a1", processId: "invoice", version: 2 },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", application: "application:a1", processId: "dunning", version: 1 },
    { id: "restricted:1", kind: "restricted", provenance: "derived" },
    { id: "unresolved:process:archive", kind: "unresolved", name: "archive", provenance: "derived" },
    { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
    { id: "decision:credit", kind: "decision", name: "Credit score", provenance: "derived" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
    { from: "process:1", to: "process:2", kind: "calls" },
    { from: "process:1", to: "restricted:1", kind: "calls" },
    { from: "process:2", to: "unresolved:process:archive", kind: "calls" },
    { from: "process:1", to: "worker:c1", kind: "uses" },
    { from: "process:2", to: "decision:credit", kind: "uses" },
  ],
  restricted: 1,
  clustered: false,
};

function installMock(page, mesh = graph) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/panorama/mesh") return route.fulfill({ json: mesh });
    return route.fulfill({ json: [] });
  });
}

test("renders the derived landscape and drills into a process", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/panorama/landscape");

  // Every node is drawn, and the two placeholder kinds are visually distinct from
  // real ones rather than merged into them.
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator(".mesh-restricted")).toHaveCount(1);
  await expect(page.locator(".mesh-unresolved")).toHaveCount(1);
  await expect(page.locator(".mesh-edges line")).toHaveCount(7);
  await expect(page.locator(".mesh-canvas")).toContainText("Billing");
  await expect(page.locator(".mesh-canvas")).toContainText("Invoice");

  // Nothing here was drawn by a person, and the view says so — that is the
  // provenance rule, and in this slice everything is derived.
  await expect(page.locator(".mesh-meta")).toContainText("derived");

  // Panorama owns the landscape altitude and links into the existing Operations
  // view for a process, rather than rendering a second BPMN canvas. Since impact
  // analysis landed, a node click selects rather than navigates — a node cannot do
  // both — so the drilldown lives in the selection panel.
  await page.locator('[data-node-id="process:1"]').click();
  await page.getByRole("link", { name: "Open in Operations" }).click();
  await expect(page).toHaveURL(/#\/operations\/p\/1$/);

  expect(pageErrors).toEqual([]);
});

test("says in words that the picture is filtered", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  // The count is the point: a viewer must be able to tell a complete landscape
  // from one their access has cut down, without inferring it from an absence.
  await expect(page.locator(".mesh-note").first()).toContainText("1");
  await expect(page.locator(".mesh-note").first()).toContainText("access");

  // A restricted placeholder must not carry the hidden resource's identity.
  await expect(page.locator(".mesh-restricted")).not.toContainText("Dunning");
  const label = await page.locator(".mesh-restricted .mesh-label").textContent();
  expect(label?.trim() ?? "").toBe("");
});

test("repeats the server's collapse instead of hiding it", async ({ page }) => {
  installMock(page, {
    nodes: [{ id: "application:a1", kind: "application", name: "Billing", provenance: "derived", children: 42 }],
    edges: [],
    restricted: 0,
    clustered: true,
  });
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-note").first()).toContainText("size budget");
  await expect(page.locator(".mesh-canvas")).toContainText("42");
});

test("an empty landscape explains itself rather than showing a blank page", async ({ page }) => {
  installMock(page, { nodes: [], edges: [], restricted: 0, clustered: false });
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".card.empty")).toContainText("derived");
  await expect(page.locator(".card.empty")).toContainText("nothing to model first");
});

// The size budget is an acceptance criterion, not a hope (ADR-0211 §7): the mesh
// must actually paint at the size the server is willing to send. The ceiling here is
// deliberately loose — measured cost at this size is around a second, and CI runners
// are slower than a developer's machine. It is here to catch a change that makes the
// layout worse than quadratic, which would blow straight through it, not to police
// a few hundred milliseconds.
test("stays inside its size budget", async ({ page }) => {
  const nodes = [], edges = [];
  for (let a = 0; a < 20; a++) {
    nodes.push({ id: `application:a${a}`, kind: "application", name: `App ${a}`, provenance: "derived" });
    for (let p = 0; p < 19; p++) {
      const id = `process:${a}_${p}`;
      nodes.push({ id, kind: "process", name: `P${a}-${p}`, provenance: "derived", application: `application:a${a}`, processId: `p${a}_${p}`, version: 1 });
      edges.push({ from: `application:a${a}`, to: id, kind: "contains" });
      if (p > 0) edges.push({ from: `process:${a}_${p - 1}`, to: id, kind: "calls" });
    }
  }
  expect(nodes.length).toBe(400); // the budget in api/panoramamesh.go

  installMock(page, { nodes, edges, restricted: 0, clustered: false });
  const started = Date.now();
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).toBeVisible({ timeout: 30000 });
  await expect(page.locator(".mesh-node")).toHaveCount(400);
  expect(Date.now() - started).toBeLessThan(15000);
});

// Search is what makes a few hundred nodes navigable at all — without it the view
// is a picture rather than a tool. It matches on name, kind and BPMN process id,
// and it reports how much it hid: a filtered mesh looks exactly like a small one.
test("filters the landscape and says how much it is hiding", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-node")).toHaveCount(7);

  await page.getByLabel("Filter the landscape").fill("invoice");
  // One match, and the header says so — separately from the context around it.
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator("#mesh-count")).toContainText("1 of 7 node(s) match");
  await expect(page.locator(".mesh-canvas")).toContainText("Invoice");

  // Filtering by kind is the other half: "what does this instance talk to".
  await page.getByLabel("Filter the landscape").fill("worker");
  await expect(page.locator(".mesh-worker:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator(".mesh-canvas")).toContainText("ops-mail");

  await page.getByLabel("Filter the landscape").fill("nothing-matches-this");
  await expect(page.locator(".mesh-empty-filter")).toContainText("Nothing matches");

  await page.getByLabel("Filter the landscape").fill("");
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator("#mesh-count")).toContainText("7 node(s)");
});

// A match on its own is a circle in an empty field: it answers "does this exist" and
// nothing else, when the question somebody types a name to ask is nearly always "and
// what is it attached to". So the filter keeps one hop around every match — and says
// which of what is on screen is a result and which is only there to explain it.
test("a filtered node keeps the things it is attached to", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.getByLabel("Filter the landscape").fill("invoice");

  // Invoice calls Dunning and a restricted node, uses the mail worker, and sits in
  // Billing. All of them are on screen; none of them matched "invoice".
  const match = page.locator('[data-node-id="process:1"]');
  await expect(match).toHaveCount(1);
  await expect(match).not.toHaveClass(/mesh-context/);
  for (const id of ["process:2", "worker:c1", "application:a1", "restricted:1"]) {
    await expect(page.locator(`[data-node-id="${id}"]`)).toHaveClass(/mesh-context/);
  }
  // And it stops at one hop: the decision only Dunning uses is two away.
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(0);

  // The context is drawn as context rather than as a result — a search that
  // presented non-matches as matches would be a worse answer than an empty field.
  await expect(page.locator("#mesh-count")).toContainText("1 of 7 node(s) match");
  await expect(page.locator("#mesh-count")).toContainText("4 shown for context");
  const faded = await page.locator('[data-node-id="worker:c1"]').evaluate(
    (el) => Number(getComputedStyle(el).opacity));
  expect(faded).toBeGreaterThan(0);
  expect(faded).toBeLessThan(1);

  // The edges between what is left are drawn, so the attachment is visible and not
  // merely implied by two circles being on the same screen.
  const joined = await page.evaluate(() => [...document.querySelectorAll(".mesh-edge")]
    .some((e) => e.dataset.from === "process:1" && e.dataset.to === "worker:c1"));
  expect(joined).toBe(true);
});

// An unresolved node must say what kind of thing is missing: a missing deployment
// and a missing worker are fixed in different places.
test("an unresolved dependency names what kind of thing is missing", async ({ page }) => {
  installMock(page, {
    nodes: [
      { id: "process:1", kind: "process", name: "Notifier", provenance: "derived", processId: "notifier", version: 1 },
      { id: "unresolved:worker:ops-mail", kind: "unresolved", name: "ops-mail", provenance: "derived" },
    ],
    edges: [{ from: "process:1", to: "unresolved:worker:ops-mail", kind: "uses" }],
    restricted: 0,
    clustered: false,
  });
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-unresolved title")).toContainText("worker");
  await expect(page.locator(".mesh-unresolved title")).toContainText("park");
});

// Impact analysis in the view (ADR-0211 §6). The traversal itself is covered case by
// case in panorama-impact.spec.mjs; these cover what the viewer actually gets.
test("selecting a node shows its blast radius and dims the rest", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-panel-empty")).toContainText("Nothing selected");

  // Dunning uses the credit decision, and Invoice calls Dunning. So at two hops the
  // decision's blast radius is both processes; at one hop, only Dunning.
  await page.locator('[data-node-id="decision:credit"]').click();
  await expect(page.locator(".mesh-impact-count")).toContainText("2");
  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-in-impact/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-in-impact/);
  // Context stays on screen, dimmed rather than removed, so the impact set reads as
  // part of the landscape instead of as the whole of it. The application is dimmed
  // even though it contains both: containment is not a dependency.
  await expect(page.locator('[data-node-id="application:a1"]')).toHaveClass(/mesh-dimmed/);

  // Depth is the chosen depth: one hop stops at the direct dependent.
  await page.locator("#mesh-depth").selectOption("1");
  await expect(page.locator(".mesh-impact-count")).toContainText("1");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-dimmed/);

  // Clicking the selection again clears it.
  await page.locator('[data-node-id="decision:credit"]').click();
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// The honesty rule, carried from the picture into the analysis over it: a walk that
// stopped at a permission boundary must not present its count as a total.
test("an impact answer that hits a restricted node says it is a lower bound", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  // The controls are addressed by id rather than by label: a node's aria-label is a
  // descriptive sentence, and getByLabel matches by substring — "Show" collides with
  // "…its identity is not shown" on a restricted node.
  await page.locator('[data-node-id="process:1"]').click();
  await page.locator("#mesh-direction").selectOption("dependencies");

  await expect(page.locator(".mesh-truncated")).toContainText("Incomplete");
  await expect(page.locator(".mesh-truncated")).toContainText("lower bound");
});

// A selection the filter removes cannot stay selected: the panel would describe a
// node that is no longer on screen.
test("filtering away the selection clears it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.locator('[data-node-id="worker:c1"]').click();
  await expect(page.locator(".mesh-impact-count")).toBeVisible();

  // Nothing matching the worker, and it is not within a hop of what does.
  await page.getByLabel("Filter the landscape").fill("dunning");
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// The model overlay (ADR-0211 §11, P2.5b): the mesh compared against the
// architecture models, in both directions.
const overlaidGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "both",
      modelElementId: "app-orders", modelElementType: "ApplicationComponent", modelName: "Order Service" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 1 },
    { id: "modeled:application:a-ghost", kind: "application", name: "Reporting", provenance: "modeled",
      modelElementId: "app-ghost", modelElementType: "ApplicationComponent", modelName: "Reporting" },
  ],
  edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
  restricted: 0, clustered: false, modeled: 1, unmodeled: 1, outOfScope: 2,
};

test("shows what is modeled, what is not, and what could not be compared", async ({ page }) => {
  installMock(page, overlaidGraph);
  await page.goto("/index.html#/panorama/landscape");

  // Provenance is on the node itself, not only in the legend.
  await expect(page.locator('[data-node-id="application:a1"]')).toHaveClass(/mesh-prov-both/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-prov-derived/);
  await expect(page.locator('[data-node-id="modeled:application:a-ghost"]')).toHaveClass(/mesh-prov-modeled/);

  const legend = page.locator(".mesh-legend");
  // Drift the drawing alone could not show.
  await expect(legend).toContainText("declared by a");
  await expect(legend).toContainText("not present here");
  // What exists and nobody wrote down.
  await expect(legend).toContainText("no");
  // Bindings at an altitude this picture does not draw are counted, not dropped —
  // calling them missing would invent drift that is not there.
  await expect(legend).toContainText("neither matched nor missing");

  // A node whose Atlas name and modeled name differ says both.
  await expect(page.locator('[data-node-id="application:a1"] title')).toContainText("Order Service");
});

// Without an overlay the legend must not imply the landscape was checked: "0
// unmodeled" would be a claim about a comparison nobody made.
test("says nothing about drift when no model was compared", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("nothing on this");
  await expect(legend).not.toContainText("neither matched nor missing");
});

// Severity on the mesh (ADR-0211 §4). A landscape with one process parked behind an
// incident, one worker that cannot serve work at all, and one node nothing observes
// — the three answers that must stay distinguishable from each other.
const statusGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived",
      state: "not-ready", severity: "critical", reason: "This worker cannot serve work: it is disabled.",
      severityFrom: "process:2" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived",
      application: "application:a1", processId: "invoice", version: 1,
      state: "degraded", severity: "attention", incidents: 3,
      reason: "3 token(s) are parked behind an unresolved incident.",
      sites: [
        { elementId: "charge-card", elementType: "ServiceTask", count: 2,
          message: "POST https://payments.example/charge: 502 Bad Gateway" },
        { elementId: "notify-customer", elementType: "SendTask", count: 1,
          message: "no worker holds the mail connector" },
      ] },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived",
      application: "application:a1", processId: "dunning", version: 1,
      state: "not-ready", severity: "critical", reason: "This worker cannot serve work: it is disabled." },
    { id: "decision:credit", kind: "decision", name: "Credit score", provenance: "derived",
      state: "unbound", severity: "unknown" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
  ],
  restricted: 0, clustered: false,
  status: {
    ok: 0, attention: 1, critical: 2, unknown: 1,
    unavailable: [
      { state: "unreachable", reason: "This view contacts no source outside the engine." },
      { state: "stale", reason: "Every fact here is read from this server's own state." },
    ],
  },
};

test("marks severity on the node itself, not only in the legend", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-sev-critical/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveClass(/mesh-sev-unknown/);

  // Colour is never the only channel: each class that is not "nothing to report"
  // carries a glyph, so the picture is readable without colour perception.
  await expect(page.locator('[data-node-id="process:2"] .mesh-badge-glyph')).toHaveText("!");
  await expect(page.locator('[data-node-id="process:1"] .mesh-badge-glyph')).toHaveText("•");

  // The state and the reason travel with the node. The three classes are a reading
  // aid for a zoomed-out picture, never a replacement for the state underneath.
  await expect(page.locator('[data-node-id="process:1"] title')).toContainText("degraded");
  await expect(page.locator('[data-node-id="process:1"] title')).toContainText("3 token(s)");
});

// The rule that keeps a red parent actionable: it says which descendant made it red.
// An unattributed finding at the top tells an operator that something is wrong
// somewhere, which is not something anybody can act on.
test("an inherited severity names the descendant it came from", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator('[data-node-id="application:a1"] title')).toContainText("inherited from process:2");

  await page.locator('[data-node-id="application:a1"]').click();
  const panel = page.locator(".mesh-panel");
  await expect(panel).toContainText("Critical");
  await expect(panel).toContainText("not ready");
  await expect(panel).toContainText("inherited from process:2");
});

// What the picture cannot see is stated beside what it can. Without this an instance
// nothing observes renders as uniformly well, and a green view with no way to go red
// is worse than no view at all.
test("says which observation states it cannot produce", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("Not watched here");
  await expect(legend).toContainText("unreachable");
  await expect(legend).toContainText("stale");
});

// Severity is a search axis, not only a colour: typing "critical" is how an operator
// gets from a few hundred nodes to the handful that are broken.
test("filters the landscape by severity", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  await page.locator("#mesh-search").fill("critical");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(2);
  await expect(page.locator('[data-node-id="process:1"]:not(.mesh-context)')).toHaveCount(0);
});

// The opening picture is the whole landscape, and it stays zoomable from there.
// Both halves matter: a view that opened zoomed in would hide the mesh, and one that
// could not zoom would be unreadable the moment the graph got interesting.
test("opens fitted to the content and zooms from there", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  const canvas = page.locator(".mesh-canvas");
  const fitted = await canvas.getAttribute("viewBox");
  const [, , fitW, fitH] = fitted.split(" ").map(Number);

  // The frame the view opens on is the drawing surface itself, so its aspect ratio
  // matches the element's — which is what leaves no letterboxed band around the
  // graph. Anything else and preserveAspectRatio pads one axis with empty space.
  const box = await canvas.boundingBox();
  expect(fitW / fitH).toBeCloseTo(box.width / box.height, 1);

  // The settled nodes reach the frame: the content fills the space it was given
  // rather than floating in a small disc in the middle of it.
  const spread = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")]
      .map((g) => /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform")));
    const xs = at.map((m) => Number(m[1])), ys = at.map((m) => Number(m[2]));
    return {
      x: Math.max(...xs) - Math.min(...xs),
      y: Math.max(...ys) - Math.min(...ys),
      left: Math.min(...xs), top: Math.min(...ys),
    };
  });
  // The fit scales both axes by one factor, so the content reaches the padding on
  // the axis that ran out first. Requiring both would mean stretching the axes
  // independently, which fills the last pixel by misreporting distance — and
  // distance is the only thing a force layout is trying to say.
  const reachesX = spread.x > fitW - 150;
  const reachesY = spread.y > fitH - 150;
  expect(reachesX || reachesY).toBe(true);
  // And it is centred in whatever is left over, so the leftover is a margin rather
  // than a blank half.
  expect(spread.left).toBeCloseTo(fitW - spread.left - spread.x, 0);

  await page.locator("#mesh-zoom-in").click();
  const zoomed = Number((await canvas.getAttribute("viewBox")).split(" ")[2]);
  expect(zoomed).toBeLessThan(fitW);

  await page.locator("#mesh-zoom-fit").click();
  expect(await canvas.getAttribute("viewBox")).toBe(fitted);
});

// Panning is gated on there being something off-screen. At the fitted frame the
// whole landscape is already visible, so a drag there could only push it out of
// view and hand back the empty space the fit exists to remove.
test("pans once zoomed in, and is inert at the fitted frame", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  const canvas = page.locator(".mesh-canvas");
  const fitted = await canvas.getAttribute("viewBox");
  // Measured per drag rather than once: a repaint can change the legend's height and
  // move the canvas under a coordinate captured earlier, which would silently drag
  // somewhere else and report the pan as broken.
  const drag = async () => {
    const box = await canvas.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 - 120, box.y + box.height / 2, { steps: 6 });
    await page.mouse.up();
  };

  await drag();
  expect(await canvas.getAttribute("viewBox")).toBe(fitted);

  await page.locator("#mesh-zoom-in").click();
  const zoomed = await canvas.getAttribute("viewBox");
  await drag();
  const panned = await canvas.getAttribute("viewBox");
  expect(panned).not.toBe(zoomed);
  // Only the origin moved: a pan is a translation, never a rescale.
  expect(panned.split(" ").slice(2)).toEqual(zoomed.split(" ").slice(2));

  // And the drag did not also select whatever it started on — panning and selecting
  // share the surface, and a pan that changed the answer beside the picture would
  // make the picture impossible to move.
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// meshOf builds a landscape of n processes under one application, for the label
// policy — which is a function of how crowded the picture is.
function meshOf(processes) {
  const graph = {
    nodes: [{ id: "application:a1", kind: "application", name: "Billing", provenance: "derived" }],
    edges: [], restricted: 0, clustered: false,
  };
  for (let i = 0; i < processes; i++) {
    graph.nodes.push({
      id: `process:${i}`, kind: "process", name: `Process ${i}`, provenance: "derived",
      application: "application:a1", processId: `p${i}`, version: 1,
    });
    graph.edges.push({ from: "application:a1", to: `process:${i}`, kind: "contains" });
  }
  return graph;
}

// Zoomed out over a crowded landscape a name would be a smear sitting on top of
// the structure the picture is carrying, so it is not painted. That is a statement
// about the magnification, not about the node: the name is in the DOM, it is the
// node's accessible label, and hovering or zooming brings it back.
test("a crowded landscape holds its names until they can be read", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/landscape");

  // Every node wants its name; the canvas decides which are legible right now.
  await expect(page.locator('[data-node-id="process:0"]')).toHaveClass(/mesh-named/);
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-names-all/);
  const ink = page.locator('[data-node-id="process:0"] .mesh-label-ink');
  await expect(ink).toHaveCSS("opacity", "0");

  // The name is in the DOM either way: a screen reader must not depend on a
  // pointer, or on a zoom level, to reach it.
  await expect(page.locator('[data-node-id="process:0"] .mesh-label')).toHaveText("Process 0");
  await expect(page.locator('[data-node-id="process:0"]'))
    .toHaveAttribute("aria-label", /Process 0/);

  // Hovering reveals it, without re-rendering the graph.
  await page.locator('[data-node-id="process:0"]').hover();
  await expect(ink).toHaveCSS("opacity", "1");
});

// And zooming in reveals them all — which is the whole bargain of laying the graph
// out in a world larger than the window. Nothing is re-rendered to do it: the
// canvas is told what magnification it is at and the stylesheet does the rest.
test("zooming in brings the names out", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/landscape");

  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).not.toHaveClass(/mesh-names-all/);
  const before = await canvas.getAttribute("viewBox");

  for (let i = 0; i < 6; i++) await page.locator("#mesh-zoom-in").click();

  await expect(canvas).toHaveClass(/mesh-names-all/);
  await expect(page.locator('[data-node-id="process:0"] .mesh-label-ink')).toHaveCSS("opacity", "1");
  // The graph did not move under the zoom: same nodes, same layout, new window.
  expect(await canvas.getAttribute("viewBox")).not.toBe(before);
  await expect(page.locator(".mesh-node")).toHaveCount(101);
});

// A small landscape needs no zoom: its world is the window, so every name is
// already large enough to read and hiding any would be pure loss.
test("a small landscape keeps every name on screen", async ({ page }) => {
  installMock(page, meshOf(4));
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-node")).toHaveCount(5);
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-names-all/);
  for (const id of ["application:a1", "process:0", "process:3"]) {
    await expect(page.locator(`[data-node-id="${id}"] .mesh-label-ink`)).toHaveCSS("opacity", "1");
  }
});

// Two things bring a name back in a crowded graph without hovering and without
// zooming: selecting the node, and filtering down to it. Both are how somebody
// actually looks for one. The graph has to be large enough that its names are
// genuinely held back, or this asserts nothing.
test("selecting or filtering brings a name back", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-names-all/);

  await page.locator('[data-node-id="process:7"]').click();
  await expect(page.locator('[data-node-id="process:7"] .mesh-label-ink')).toHaveCSS("opacity", "1");

  await page.locator("#mesh-search").fill("Process 12");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator('[data-node-id="process:12"] .mesh-label-ink')).toHaveCSS("opacity", "1");
});

// Size carries rank as well as kind: at a few hundred nodes the eye sorts by size
// before it reads anything, so an application has to be unmistakably the largest.
// mesh-body is the node's own outline: a group can also hold a provenance ring and a
// severity badge, and either would answer with the wrong radius.
//
// data-r rather than the `r` attribute, because a node's outline is a square, a
// hexagon or a triangle as often as it is a circle — and because data-r is the
// radius the *layout* reserved, which is what every claim about size and spacing on
// this view is actually about. Reading `r` would answer null for every shape that is
// not a circle, and `+null` is 0: the overlap tests below would have gone on passing
// while measuring nothing.
const radiusOf = async (page, id) => Number(
  await page.locator(`[data-node-id="${id}"] .mesh-body`).getAttribute("data-r"));

test("kinds are told apart by size, not only by colour", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  const application = await radiusOf(page, "application:a1");
  const process = await radiusOf(page, "process:1");
  const worker = await radiusOf(page, "worker:c1");
  expect(application).toBeGreaterThan(process);
  expect(process).toBeGreaterThan(worker);
});

// Size says two things at once — what kind of thing this is, and how much of the
// landscape hangs off it. The second is what makes a hub findable in a picture with
// four hundred circles in it, where the eye sorts by size before it reads anything.
// (That the two never overwrite each other is arithmetic, checked exhaustively in
// panorama-size.spec.mjs; this is the claim that the drawing actually uses it.)
test("a well-connected node is drawn larger than a lonely one of its kind", async ({ page }) => {
  installMock(page, {
    nodes: [
      { id: "process:hub", kind: "process", name: "Hub", provenance: "derived", processId: "hub", version: 1 },
      { id: "process:leaf", kind: "process", name: "Leaf", provenance: "derived", processId: "leaf", version: 1 },
      ...Array.from({ length: 6 }, (_, i) => ({
        id: `worker:w${i}`, kind: "worker", name: `w${i}`, provenance: "derived", workerType: "mail",
      })),
    ],
    edges: Array.from({ length: 6 }, (_, i) => (
      { from: "process:hub", to: `worker:w${i}`, kind: "uses" })),
    restricted: 0, clustered: false,
  });
  await page.goto("/index.html#/panorama/landscape");

  const hub = await radiusOf(page, "process:hub");
  const leaf = await radiusOf(page, "process:leaf");
  expect(hub).toBeGreaterThan(leaf);
});

// The separation pass exists because repulsion alone is a soft force a spring can
// overpower, and two circles sitting on top of each other is the one arrangement
// that makes the picture unreadable rather than merely tight.
test("nodes do not overlap each other", async ({ page }) => {
  installMock(page, meshOf(30));
  await page.goto("/index.html#/panorama/landscape");

  const overlaps = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      const circle = g.querySelector(".mesh-body");
      return { x: +m[1], y: +m[2], r: Number(circle.getAttribute("data-r")) };
    });
    let worst = 0;
    for (let i = 0; i < at.length; i++) {
      for (let j = i + 1; j < at.length; j++) {
        const gap = Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r;
        worst = Math.min(worst, gap);
      }
    }
    return worst;
  });
  // Negative means two circles intersect. A little slack for the fit's rescaling.
  expect(overlaps).toBeGreaterThan(-2);
});

// Which bubble is connected to which, answered by pointing at one. Impact analysis
// already answers the bigger question — what breaks if this goes down — but it
// needs a click and walks the whole chain. This is the question asked dozens of
// times while reading a landscape: what does *this* touch?
test("pointing at a node shows what it is connected to", async ({ page }) => {
  installMock(page, graph);
  await page.goto("/index.html#/panorama/landscape");
  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).not.toHaveClass(/mesh-relating/);

  await page.locator('[data-node-id="process:1"]').hover();
  await expect(canvas).toHaveClass(/mesh-relating/);

  // The node itself, its neighbours, and the edges between them are lifted.
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-relating-self/);
  const related = page.locator(".mesh-node.mesh-related");
  expect(await related.count()).toBeGreaterThan(0);
  await expect(page.locator(".mesh-edge.mesh-related-edge").first()).toBeVisible();

  // Every edge lifted actually touches the node — the highlight is the graph's own
  // adjacency, not a guess from where things happen to sit on screen.
  const honest = await page.evaluate(() => [...document.querySelectorAll(".mesh-related-edge")]
    .every((e) => e.dataset.from === "process:1" || e.dataset.to === "process:1"));
  expect(honest).toBe(true);

  // A related node's name comes out with it: knowing something is connected is not
  // much use without knowing what it is.
  await expect(related.first().locator(".mesh-label-ink")).toHaveCSS("opacity", "1");

  // And it is marked in colour, not only by being left un-faded: a ring in the
  // accent says "these are the ones" where merely staying visible says "these are
  // still here". The ring is outside the circle rather than a recolouring of it,
  // because the body's own stroke is carrying the severity.
  const ring = async (id) => page.locator(`[data-node-id="${id}"] .mesh-halo`)
    .evaluate((el) => ({
      opacity: Number(getComputedStyle(el).opacity),
      width: parseFloat(getComputedStyle(el).strokeWidth),
    }));
  const neighbour = await related.first().getAttribute("data-node-id");
  const lit = await ring(neighbour);
  expect(lit.opacity).toBeGreaterThan(0.5);
  expect(lit.width).toBeGreaterThan(1);
  // The node being asked about is never in doubt: its ring is the stronger one.
  expect((await ring("process:1")).width).toBeGreaterThan(lit.width);
  // And an unrelated node has no ring at all.
  expect((await ring("decision:credit")).opacity).toBe(0);
  // The severity stroke on a related node survives being highlighted.
  await expect(page.locator(`[data-node-id="${neighbour}"] .mesh-body`)).toBeVisible();

  // Unrelated ones fall back rather than disappearing: the question is "what does
  // this touch", not "what if the rest were gone".
  const dimmed = await page.evaluate(() => {
    const el = [...document.querySelectorAll(".mesh-node")]
      .find((n) => !n.classList.contains("mesh-related") && !n.classList.contains("mesh-relating-self"));
    return el ? getComputedStyle(el).opacity : null;
  });
  expect(Number(dimmed)).toBeGreaterThan(0);
  expect(Number(dimmed)).toBeLessThan(1);
});

// The layout is what the whole picture rests on: a graph compressed into the
// viewport puts its circles through each other, and no amount of colour or labelling
// recovers from that. The world grows with the content instead, so the guarantee
// holds at every size rather than only at the small ones.
test("no two nodes overlap, however many there are", async ({ page }) => {
  for (const size of [12, 60]) {
    installMock(page, meshOf(size));
    await page.goto("/index.html#/panorama/landscape");
    await page.locator(".mesh-node").first().waitFor();

    const worst = await page.evaluate(() => {
      const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
        const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
        return { x: +m[1], y: +m[2], r: +g.querySelector(".mesh-body").getAttribute("data-r") };
      });
      let gap = Infinity;
      for (let i = 0; i < at.length; i++) for (let j = i + 1; j < at.length; j++) {
        gap = Math.min(gap, Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r);
      }
      return gap;
    });
    // Not merely non-negative: there is room for a name between any two of them.
    expect(worst).toBeGreaterThan(30);
  }
});

// Picking a bubble up and putting it somewhere.
//
// The layout answers "where does this graph want to sit", which is the right first
// answer and never the last one: the person reading it knows things the simulation
// does not, and until now had no way to say so. These check the three things that
// make the gesture worth having — the node goes where it is dropped, the graph
// rearranges itself around it, and what was placed by hand survives the next thing
// that repaints the picture.

// screenAt reads a node's rendered centre in page coordinates, which is what a drag
// is expressed in — the transform is in world units and the two differ by whatever
// the current viewBox is.
async function screenAt(page, id) {
  const box = await page.locator(`[data-node-id="${id}"] .mesh-body`).boundingBox();
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

// worldAt reads the settled coordinates the layout is actually working in.
async function worldAt(page, id) {
  return page.evaluate((nodeId) => {
    const g = document.querySelector(`[data-node-id="${nodeId}"]`);
    const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
    return { x: +m[1], y: +m[2] };
  }, id);
}

// dragBy picks a node up and puts it down dx/dy away. hover() rather than a raw
// mouse.move, because it scrolls the node into view first: a press at coordinates
// below the fold is a gesture the browser never sees, and the test would be
// reporting on the scroll position rather than on the drag.
async function dragBy(page, id, dx, dy) {
  const target = page.locator(`[data-node-id="${id}"] .mesh-body`);
  await target.hover();
  const box = await target.boundingBox();
  const stage = await page.locator("#mesh-surface").boundingBox();
  const view = page.viewportSize();
  const from = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  // Into the part of the stage that is actually on screen. hover() may have scrolled
  // the stage so that its top is above the viewport, and a drop at a negative
  // coordinate is a gesture the browser never sees.
  const within = (value, low, high) => Math.min(Math.max(value, low), high);
  const to = {
    x: within(from.x + dx, Math.max(stage.x, 0) + 40,
      Math.min(stage.x + stage.width, view.width) - 40),
    y: within(from.y + dy, Math.max(stage.y, 0) + 40,
      Math.min(stage.y + stage.height, view.height) - 40),
  };
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
  return { from, to };
}

test("a node goes where it is dropped, and the graph settles around it", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  const others = await page.evaluate(() => Object.fromEntries(
    [...document.querySelectorAll(".mesh-node")].map((g) => [g.dataset.nodeId, g.getAttribute("transform")])));

  const { to } = await dragBy(page, "process:3", 120, -70);
  const landed = await screenAt(page, "process:3");
  // Where it was put, not where the layout wanted it: within a couple of pixels of
  // the pointer, which is all the rounding in the transform allows for.
  expect(Math.abs(landed.x - to.x)).toBeLessThan(6);
  expect(Math.abs(landed.y - to.y)).toBeLessThan(6);

  // And it says so. A node that is not where the layout put it is a fact about the
  // picture; hiding it would make the arrangement look like the simulation's answer.
  await expect(page.locator('[data-node-id="process:3"]')).toHaveClass(/mesh-pinned/);

  // The rest of the landscape moved out of the way rather than staying put and
  // being overlapped — which is the difference between dragging a node and dragging
  // a sticker across a picture of one.
  const moved = await page.evaluate((before) => [...document.querySelectorAll(".mesh-node")]
    .filter((g) => g.dataset.nodeId !== "process:3" && g.getAttribute("transform") !== before[g.dataset.nodeId])
    .length, others);
  expect(moved).toBeGreaterThan(0);

  // Still no two circles through each other, which is the guarantee the whole
  // picture rests on and the one a drag could most easily break.
  const worst = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      return { x: +m[1], y: +m[2], r: +g.querySelector(".mesh-body").getAttribute("data-r") };
    });
    let gap = Infinity;
    for (let i = 0; i < at.length; i++) for (let j = i + 1; j < at.length; j++) {
      gap = Math.min(gap, Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r);
    }
    return gap;
  });
  expect(worst).toBeGreaterThan(0);

  // Dragging is not selecting. A gesture that also selected whatever it started on
  // would make the picture impossible to arrange without changing the answer beside it.
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// A drag moves what it is joined to, and leaves the rest of the landscape exactly
// where the reader last saw it.
//
// This is the property, not a nicety. Resuming the layout's own physics from the
// picture on screen looks reasonable and is not: the picture has been fitted since
// it was simulated, so it sits nowhere near the simulation's equilibrium, and
// restarting it there reorganises the whole landscape the moment a node is touched.
// The reader loses the arrangement they were reading in order to move one node in it.
test("a drag moves the neighbourhood, not the landscape", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  const positions = () => page.evaluate(() => Object.fromEntries(
    [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      return [g.dataset.nodeId, { x: +m[1], y: +m[2] }];
    })));

  const before = await positions();
  await dragBy(page, "process:3", 130, -60);
  const after = await positions();
  const moved = (id) => Math.hypot(after[id].x - before[id].x, after[id].y - before[id].y);

  // The node went where it was put, and the application it sits in came after it.
  expect(moved("process:3")).toBeGreaterThan(60);
  expect(moved("application:a1")).toBeGreaterThan(5);

  // Everything else is untouched, except whatever was actually in the way. In this
  // landscape the processes are joined to their application and to nothing else, so
  // there is nothing to pull them along — only a collision could move one.
  const others = Object.keys(before)
    .filter((id) => id !== "process:3" && id !== "application:a1");
  const still = others.filter((id) => moved(id) < 1);
  expect(still.length).toBeGreaterThanOrEqual(others.length - 2);
});

test("what you placed by hand survives a repaint", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  await dragBy(page, "process:5", -110, 90);
  const dropped = await worldAt(page, "process:5");

  // Anything that re-lays-out the graph. A depth change is the cheapest of them and
  // has nothing to do with where things sit, which is the point: an arrangement that
  // only survived until the next unrelated click would not be worth making.
  await page.selectOption("#mesh-depth", "1");
  await expect(page.locator('[data-node-id="process:5"]')).toHaveClass(/mesh-pinned/);
  const after = await worldAt(page, "process:5");
  expect(Math.hypot(after.x - dropped.x, after.y - dropped.y)).toBeLessThan(1);

  // A filter is a temporary question, and asking one must not cost the arrangement.
  await page.fill("#mesh-search", "Process 5");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await page.fill("#mesh-search", "");
  await expect(page.locator(".mesh-node")).toHaveCount(13);
  await expect(page.locator('[data-node-id="process:5"]')).toHaveClass(/mesh-pinned/);
});

test("release puts everything back where the layout wants it", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  const release = page.locator("#mesh-release");
  await expect(release).toBeDisabled();
  const original = await worldAt(page, "process:7");

  await dragBy(page, "process:7", 140, 60);
  await expect(release).toBeEnabled();

  await release.click();
  await expect(release).toBeDisabled();
  await expect(page.locator('[data-node-id="process:7"]')).not.toHaveClass(/mesh-pinned/);
  // Back where it started: the layout is deterministic, so releasing everything
  // reproduces the picture the graph opened with rather than some third arrangement.
  const back = await worldAt(page, "process:7");
  expect(Math.hypot(back.x - original.x, back.y - original.y)).toBeLessThan(1);
});

// Releasing one node lives in the panel beside the node it is about. It used to be
// a double-click — which is a thing you have to be told, and which the drilldown
// now wants — so it became a button on the thing itself, which is a thing you can
// see.
test("one node can be released without disturbing the arrangement", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  await dragBy(page, "process:2", 100, 40);
  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-pinned/);
  await dragBy(page, "process:9", -90, -60);
  await expect(page.locator('[data-node-id="process:9"]')).toHaveClass(/mesh-pinned/);
  const kept = await worldAt(page, "process:9");

  // Nothing selected: the panel offers no release, because there is no node it
  // would be about.
  await expect(page.locator(".mesh-unpin")).toHaveCount(0);

  await page.locator('[data-node-id="process:2"]').click();
  await page.locator(".mesh-unpin").click();
  await expect(page.locator('[data-node-id="process:2"]')).not.toHaveClass(/mesh-pinned/);

  // The other one is untouched — both its pin and the place it was put.
  await expect(page.locator('[data-node-id="process:9"]')).toHaveClass(/mesh-pinned/);
  await expect(page.locator("#mesh-release")).toBeEnabled();
  const still = await worldAt(page, "process:9");
  expect(Math.hypot(still.x - kept.x, still.y - kept.y)).toBeLessThan(1);
});

// Arranging without a mouse. Everything this view *says* is already reachable from
// the keyboard — focusing a node lifts its neighbours, Enter selects it — so this is
// a convenience rather than information. But a convenience only some people can have
// is not one.
test("a focused node can be moved with the arrow keys", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  const node = page.locator('[data-node-id="process:4"]');
  await node.focus();
  const before = await worldAt(page, "process:4");

  await page.keyboard.press("ArrowRight");
  const stepped = await worldAt(page, "process:4");
  expect(stepped.x).toBeGreaterThan(before.x);
  expect(Math.abs(stepped.y - before.y)).toBeLessThan(1);
  await expect(node).toHaveClass(/mesh-pinned/);

  // Shift is the coarse step: crossing a large landscape a pixel at a time is not a
  // keyboard equivalent of a drag.
  const fine = stepped.x - before.x;
  await page.keyboard.press("Shift+ArrowRight");
  const coarse = (await worldAt(page, "process:4")).x - stepped.x;
  expect(coarse).toBeGreaterThan(fine * 2);

  // And it is the same arrangement a drag makes, so Release puts it back.
  await page.locator("#mesh-release").click();
  await expect(node).not.toHaveClass(/mesh-pinned/);
});

// Saved views. The complaint they answer: watching one node means filtering down to
// it and zooming in, and a reload puts you back at the whole landscape with all of
// it to do again.
test("a saved view brings the whole setup back", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();

  // Set the landscape up: filter to one node, watch it, zoom in, place it by hand.
  await page.fill("#mesh-search", "invoice");
  await expect(page.locator("#mesh-count")).toContainText("1 of 7");
  await page.locator('[data-node-id="process:1"]').click();
  await expect(page.locator(".mesh-impact-count")).toBeVisible();
  await page.locator("#mesh-zoom-in").click();
  await page.locator("#mesh-zoom-in").click();
  await dragBy(page, "process:1", 60, 40);
  const framed = await page.locator(".mesh-canvas").getAttribute("viewBox");

  await page.fill("#mesh-view-name", "Billing watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("Saved “Billing watch”");
  await expect(page.locator(".mesh-view-open")).toHaveText("Billing watch");

  // Everything back to where a reload leaves it.
  await page.locator("#mesh-release").click();
  await page.fill("#mesh-search", "");
  await page.locator("#mesh-zoom-fit").click();
  await expect(page.locator(".mesh-node")).toHaveCount(7);

  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-count")).toContainText("1 of 7");
  await expect(page.locator(".mesh-impact-count")).toBeVisible();
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-pinned/);
  // The same magnification, framed on the node it was watching.
  const reopened = await page.locator(".mesh-canvas").getAttribute("viewBox");
  expect(Number(reopened.split(" ")[2])).toBeCloseTo(Number(framed.split(" ")[2]), 0);
});

// A view is a way of looking, not a snapshot of what was there: the landscape is
// derived and changes as things are deployed. So a view that was watching something
// which has since gone must say so rather than describing a node that is not on
// screen.
test("a view whose node is gone opens and says so", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.locator('[data-node-id="worker:c1"]').click();
  await page.fill("#mesh-view-name", "Mail worker");
  await page.locator("#mesh-view-save button").click();

  // The same view, against a landscape that no longer has that worker.
  const smaller = { ...graph, nodes: graph.nodes.filter((n) => n.id !== "worker:c1"),
    edges: graph.edges.filter((e) => e.from !== "worker:c1" && e.to !== "worker:c1") };
  installMock(page, smaller);
  await page.reload();
  await page.locator(".mesh-node").first().waitFor();

  // It survived the reload — that is the whole point of saving it.
  await expect(page.locator(".mesh-view-open")).toHaveText("Mail worker");
  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-view-note")).toContainText("no longer in this landscape");
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

test("views can be renamed over and forgotten", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-node").first().waitFor();

  await page.fill("#mesh-search", "invoice");
  await page.fill("#mesh-view-name", "Billing watch");
  await page.locator("#mesh-view-save button").click();

  // Saving the same name again updates it in place rather than making a second entry.
  await page.fill("#mesh-search", "dunning");
  await page.fill("#mesh-view-name", "billing watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("Updated");
  await expect(page.locator(".mesh-view-open")).toHaveCount(1);

  await page.fill("#mesh-search", "");
  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-search")).toHaveValue("dunning");

  // An empty name is refused with a sentence, not by doing nothing.
  await page.fill("#mesh-view-name", "   ");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("name");
  await expect(page.locator(".mesh-view-open")).toHaveCount(1);

  await page.locator(".mesh-view-drop").click();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();
  await page.reload();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();
});

// The findings list. The picture already marks which nodes have something wrong with
// them, and on four hundred circles that is not the same as being able to read them:
// finding three red dots means hunting, and hunting is what somebody does instead of
// noticing.
test("lists the findings beside the picture, worst first", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  const findings = page.locator(".mesh-finding-go");
  await expect(findings).toHaveCount(3);

  // Worst first, and within a class the one with more incidents behind it.
  const names = await page.locator(".mesh-finding-name").allTextContents();
  expect(names.slice(0, 2).sort()).toEqual(["Billing", "Dunning"]);
  expect(names[2]).toBe("Invoice");

  // Nothing well or unwatched is in the list: it is the findings, not the inventory.
  expect(names).not.toContain("Credit score");

  // The count is carried where there is one to carry. An incident belongs to a
  // token and only a process has tokens, so a node without a count is one that
  // cannot have one — never one reported as having none.
  await expect(page.locator('[data-finding="process:1"]')).toContainText("3 incident(s)");
  await expect(page.locator('[data-finding="process:1"]')).toContainText("degraded");
  await expect(page.locator('[data-finding="decision:credit"]')).toHaveCount(0);
  await expect(page.locator(".mesh-findings-head")).toContainText("3 node(s), 3 incident(s)");

  // The sentence behind the finding is there: the class says what kind of trouble,
  // only the reason says what to do about it.
  await expect(page.locator('[data-finding="process:2"]'))
    .toContainText("This worker cannot serve work");
});

// Going *to* a finding is the whole reason the list is worth having.
test("clicking a finding selects it and puts it on screen", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-zoomed/);

  await page.locator('[data-finding="process:1"]').click();

  // Selected, so the panel above it explains the finding.
  await expect(page.locator(".mesh-panel-head")).toContainText("Invoice");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-in-impact/);

  // And framed on it, rather than left somewhere in the landscape.
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-zoomed/);
  const centred = await page.evaluate(() => {
    const svg = document.querySelector(".mesh-canvas");
    const [x, y, w, h] = svg.getAttribute("viewBox").split(" ").map(Number);
    const g = document.querySelector('[data-node-id="process:1"]');
    const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
    return { dx: Math.abs(x + w / 2 - +m[1]), dy: Math.abs(y + h / 2 - +m[2]) };
  });
  expect(centred.dx).toBeLessThan(1);
  expect(centred.dy).toBeLessThan(1);
});

// An empty findings list is not a claim that everything is well: most nodes in a
// young landscape are unobserved, and a status view that cannot go red is worse than
// no status view.
test("an empty findings list does not claim everything is fine", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-finding-go")).toHaveCount(0);
  await expect(page.locator(".mesh-findings")).toContainText("not the same as everything being well");
});

// The heartbeat. Motion is the one channel left once colour, size, shape and a glyph
// are all carrying something — and it is the channel the eye finds without being
// pointed at it, which is what a view somebody glances at needs.
test("nodes with a finding beat, and the worse ones beat slower", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-beating/);

  const beat = (id) => page.locator(`[data-node-id="${id}"] .mesh-beat`).evaluate((el) => {
    const style = getComputedStyle(el);
    return { name: style.animationName, seconds: parseFloat(style.animationDuration), stroke: style.stroke };
  });

  // The worse the state, the *less* pulse: a degraded process is still working and
  // beats quickly and twice; one that cannot do work beats once, slowly and heavily.
  const attention = await beat("process:1");
  const critical = await beat("process:2");
  expect(attention.name).toBe("mesh-beat-quick");
  expect(critical.name).toBe("mesh-beat-slow");
  expect(critical.seconds).toBeGreaterThan(attention.seconds * 2);

  // Each keeps its own severity colour rather than both going red: "it is broken"
  // and "something inside it went wrong" are the two findings this view exists to
  // tell apart.
  expect(critical.stroke).not.toBe(attention.stroke);

  // Nothing well or unwatched beats at all — a landscape that pulsed everywhere
  // would be saying nothing, loudly.
  await expect(page.locator('[data-node-id="decision:credit"] .mesh-beat')).toHaveCount(0);
});

// A landscape where three things are wrong should draw the eye to those three. One
// where two hundred are wrong is a picture of an outage, and two hundred animations
// say less than a still frame while costing far more to paint.
test("past its budget the beat stops rather than swamping the picture", async ({ page }) => {
  const many = {
    nodes: Array.from({ length: 90 }, (_, i) => ({
      id: `process:${i}`, kind: "process", name: `Process ${i}`, provenance: "derived",
      processId: `p${i}`, version: 1, state: "degraded", severity: "attention",
      incidents: 1, reason: "1 token(s) are parked behind an unresolved incident.",
    })),
    edges: [], restricted: 0, clustered: false,
  };
  installMock(page, many);
  await page.goto("/index.html#/panorama/landscape");

  // The rings are still there, and still marking the findings — they have simply
  // stopped competing for attention they no longer need to win.
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-beating/);
  const ring = await page.locator('[data-node-id="process:1"] .mesh-beat')
    .evaluate((el) => ({ name: getComputedStyle(el).animationName, opacity: Number(getComputedStyle(el).opacity) }));
  expect(ring.name).toBe("none");
  expect(ring.opacity).toBeGreaterThan(0);
});

// A filtered picture gets a filtered list — and then has to say so. "Findings" over a
// landscape showing one node in four would otherwise read as *the* findings, which is
// a claim about the three that are not there.
test("a filtered findings list says it is filtered", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-findings-head")).toContainText("3 node(s)");
  await expect(page.locator(".mesh-findings-head")).not.toContainText("filtered");

  await page.fill("#mesh-search", "dunning");
  await expect(page.locator(".mesh-findings-head")).toContainText("in the filtered landscape");
});

// Drilling into a node: the landscape reduced to it and what it touches.
//
// The complaint it answers is the one every large graph has: you find the thing you
// came for and it is still sitting in four hundred circles of everything else.
test("double-clicking a node goes into it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator("#mesh-drill-out")).toBeHidden();

  await page.locator('[data-node-id="process:1"] .mesh-body').dblclick();

  // Invoice, and what it touches at the depth already on screen (2 hops): its
  // application, both processes, the restricted placeholder, the mail worker, and
  // the decision Dunning uses. Not the unresolved archive dependency's siblings.
  await expect(page.locator("#mesh-drill-out")).toBeVisible();
  await expect(page.locator("#mesh-drill-out")).toContainText("Inside Invoice");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveCount(1);
  await expect(page.locator('[data-node-id="worker:c1"]')).toHaveCount(1);

  // The node it went into is the subject; everything else is there to explain it,
  // and is drawn as such — the same language a filtered picture uses.
  await expect(page.locator('[data-node-id="process:1"]')).not.toHaveClass(/mesh-context/);
  await expect(page.locator('[data-node-id="worker:c1"]')).toHaveClass(/mesh-context/);

  // And it is selected, so the panel explains it without a second click.
  await expect(page.locator(".mesh-panel-head")).toContainText("Invoice");
  await expect(page.locator("#mesh-count")).toContainText("hop(s)");
});

// The depth control decides the reach, so "just this" and "this and its whole chain"
// are the same gesture at two settings.
test("the drilldown reaches as far as the depth says", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.selectOption("#mesh-depth", "1");
  await page.locator('[data-node-id="process:1"] .mesh-body').dblclick();
  const oneHop = await page.locator(".mesh-node").count();
  // Invoice's own neighbours only: the decision that only Dunning uses is two away.
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(0);

  await page.selectOption("#mesh-depth", "2");
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(1);
  expect(await page.locator(".mesh-node").count()).toBeGreaterThan(oneHop);
});

// A drilldown is a place you are standing, so there has to be a way back — and
// coming back must not mean finding the node again.
test("leaving a drilldown restores the landscape with the node still marked", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.locator('[data-node-id="worker:c1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-out")).toBeVisible();

  await page.locator("#mesh-drill-out").click();
  await expect(page.locator("#mesh-drill-out")).toBeHidden();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator(".mesh-panel-head")).toContainText("ops-mail");

  // Escape is the other way out, the one it is everywhere else.
  await page.locator('[data-node-id="process:2"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-out")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#mesh-drill-out")).toBeHidden();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
});

// The search box and the drilldown ask the same kind of question, so only one is
// ever in force. Two narrowings compounding invisibly is how a picture ends up
// showing something nobody asked for and nobody can undo.
test("a search leaves the drilldown rather than compounding with it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  await page.fill("#mesh-search", "invoice");
  await expect(page.locator("#mesh-count")).toContainText("match");

  // Drilling in clears the box, so the header is never describing one narrowing
  // while the picture shows another.
  await page.locator('[data-node-id="process:1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-search")).toHaveValue("");
  await expect(page.locator("#mesh-count")).toContainText("hop(s)");

  // And typing goes back to asking about the whole landscape.
  await page.fill("#mesh-search", "dunning");
  await expect(page.locator("#mesh-drill-out")).toBeHidden();
  await expect(page.locator("#mesh-count")).toContainText("match");
});

// Where exactly the work is parked. "Three tokens are parked" says there is a
// problem; naming the task and quoting what it said says where to go — which is the
// difference between a status view somebody glances at and one they act on.
test("a finding says which task the work is parked on", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  const finding = page.locator(".mesh-findings");
  await expect(finding).toContainText("charge-card");
  await expect(finding).toContainText("ServiceTask");
  await expect(finding).toContainText("502 Bad Gateway");

  // Worst first, and a repeated element is one entry with a count rather than one
  // entry each.
  const rows = await page.locator(".mesh-findings .mesh-sites li").allTextContents();
  expect(rows[0]).toContain("charge-card");
  expect(rows[0]).toContain("2×");
  expect(rows[1]).toContain("notify-customer");
  // One incident on an element is not decorated with "1×" — a count that is always
  // there stops being read.
  expect(rows[1]).not.toContain("1×");

  // The selected node repeats it beside the picture, so acting on a finding does not
  // mean reading it in one panel and selecting it in another.
  await page.locator('[data-node-id="process:1"]').click();
  await expect(page.locator(".mesh-finding")).toContainText("charge-card");
});

// A node with a finding and no sites is the ordinary case for everything that is not
// a process: an incident belongs to a token, and only a process has tokens.
test("a finding with nowhere to point does not invent a place", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  await page.locator('[data-node-id="process:2"]').click();
  await expect(page.locator(".mesh-finding")).toContainText("This worker cannot serve work");
  await expect(page.locator(".mesh-finding .mesh-sites")).toHaveCount(0);
});

// Attention has to look like a finding, not like a hint.
//
// It used to be drawn as a pale wash inside a thin ochre outline while critical was
// a solid disc — so the two classes differed in *form* as well as in colour, and the
// form was doing most of the work. A tinted outline beside a filled badge does not
// read as "less urgent", it reads as "not really a finding".
test("an attention finding is drawn as one, not as a hint", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/landscape");

  const badge = (id) => page.locator(`[data-node-id="${id}"] .mesh-badge-dot`)
    .evaluate((el) => {
      const style = getComputedStyle(el);
      return { fill: style.fill, stroke: style.stroke };
    });

  // Both classes are a filled badge in their own colour — the fill is the colour,
  // not a wash of it, so neither is a ring around nothing.
  const attention = await badge("process:1");
  const critical = await badge("process:2");
  expect(attention.fill).toBe(attention.stroke);
  expect(critical.fill).toBe(critical.stroke);
  // And they are still two different findings: "it is broken" and "something inside
  // it went wrong" are what this view exists to tell apart.
  expect(attention.fill).not.toBe(critical.fill);

  // The glyph on each is legible against the badge it sits on, which is why they do
  // not share one: white on the red, near-black on the amber.
  const ink = (id) => page.locator(`[data-node-id="${id}"] .mesh-badge-glyph`)
    .evaluate((el) => getComputedStyle(el).fill);
  expect(await ink("process:1")).not.toBe(await ink("process:2"));

  // Colour is never the only channel (ADR-0211 §4): each class keeps its own glyph,
  // so the picture is readable without separating red from amber.
  const glyphs = await page.evaluate(() => ({
    attention: document.querySelector('[data-node-id="process:1"] .mesh-badge-glyph').textContent,
    critical: document.querySelector('[data-node-id="process:2"] .mesh-badge-glyph').textContent,
  }));
  expect(glyphs.attention).not.toBe(glyphs.critical);

  // Weight is the third channel: a node with a finding wears a heavier ring than one
  // without, which survives being small, being zoomed out, and being looked at by
  // somebody who does not separate the two hues at all.
  const ring = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => parseFloat(getComputedStyle(el).strokeWidth));
  const quiet = await ring("decision:credit");
  expect(await ring("process:1")).toBeGreaterThan(quiet);
  expect(await ring("process:2")).toBeGreaterThan(quiet);
});

// Kinds are told apart by shape, which is the channel that survives what colour and
// size do not: a printout, a projector, and a reader who does not separate the hues.
test("each kind is drawn with its own outline", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

  const outline = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.tagName.toLowerCase());

  // A rect for the process, polygons for the rest, a circle for the application.
  expect(await outline("application:a1")).toBe("circle");
  expect(await outline("process:1")).toBe("rect");
  expect(await outline("worker:c1")).toBe("polygon");
  expect(await outline("decision:credit")).toBe("polygon");

  // The polygons are told apart by their corner count: three for the decision, six
  // for the worker, four for the placeholder that stands for an unknown kind.
  const corners = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.getAttribute("points").trim().split(/\s+/).length);
  expect(await corners("decision:credit")).toBe(3);
  expect(await corners("worker:c1")).toBe(6);
  expect(await corners("restricted:1")).toBe(4);

  // An unresolved process is drawn in the silhouette of the deployment that should
  // have been there — the dashes say it is missing, the shape says what is.
  expect(await outline("unresolved:process:archive")).toBe("rect");
  await expect(page.locator('[data-node-id="unresolved:process:archive"] .mesh-body'))
    .toHaveAttribute("stroke-dasharray", "4 3");
});

// The legend is drawn by the same function the nodes are, so it cannot come to
// disagree with the picture it explains.
test("the legend shows the shapes it is explaining", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await page.locator(".mesh-swatch").first().waitFor();

  const shapes = await page.evaluate(() => [...document.querySelectorAll(".mesh-swatch svg")]
    .map((svg) => {
      const body = svg.querySelector(".mesh-body");
      return body ? body.tagName.toLowerCase() : null;
    })
    .filter(Boolean));

  // One swatch per kind on screen, and between them every outline the canvas uses.
  expect(shapes).toContain("circle");
  expect(shapes).toContain("rect");
  expect(shapes.filter((tag) => tag === "polygon").length).toBeGreaterThan(1);
});

// A peer that stopped answering, on the landscape.
//
// Until now this view contacted nothing, so it declared *unreachable* and *stale*
// unproducible — which was true and useless: an operator scanning the landscape for
// trouble could not see that a whole server had gone away. Deployment targets are
// drawn now, and they are the only nodes here whose state comes from outside this
// process.
const peeredGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived",
      state: "healthy", severity: "ok", reason: "No work is parked." },
    { id: "target:t1", kind: "target", name: "Production", provenance: "derived",
      state: "unreachable", severity: "attention",
      reason: "This peer could not be reached." },
    { id: "target:t2", kind: "target", name: "Staging", provenance: "derived",
      state: "stale", severity: "attention",
      reason: "Last answered 240s ago and the refresh failed, so this is history rather than status." },
    { id: "target:t3", kind: "target", name: "Sandbox", provenance: "derived",
      state: "healthy", severity: "ok", reason: "This peer answered and identified itself." },
  ],
  edges: [],
  restricted: 0, clustered: false,
  status: { ok: 2, attention: 2, critical: 0, unknown: 0, unavailable: [] },
};

test("a peer that stopped answering is on the landscape", async ({ page }) => {
  installMock(page, peeredGraph);
  await page.goto("/index.html#/panorama/landscape");

  // Its own outline, so it is told from everything else without reading a word.
  const outline = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => ({
      tag: el.tagName.toLowerCase(),
      corners: el.getAttribute("points")?.trim().split(/\s+/).length ?? 0,
    }));
  expect(await outline("target:t1")).toEqual({ tag: "polygon", corners: 5 });

  // Both failures are *attention*, never critical: "I could not reach it" and "it is
  // broken" are different findings, and a view that painted them alike loses its
  // credibility on the first network fault.
  await expect(page.locator('[data-node-id="target:t1"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="target:t2"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="target:t3"]')).toHaveClass(/mesh-sev-ok/);

  // And they beat, so a server going away is noticed rather than looked for.
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-beating/);
  await expect(page.locator('[data-node-id="target:t1"] .mesh-beat')).toHaveCount(1);
  await expect(page.locator('[data-node-id="target:t3"] .mesh-beat')).toHaveCount(0);

  // The findings list tells the two apart in words, which is the half a colour
  // cannot carry: nothing is known, versus something is known and may be wrong.
  const findings = page.locator(".mesh-findings");
  await expect(findings).toContainText("unreachable");
  await expect(findings).toContainText("stale");
  await expect(findings).toContainText("history rather than status");

  // Never the peer's address. That is this operator's map of where their
  // infrastructure lives, and a landscape is opened by anybody with modeler access.
  const drawn = await page.locator("#mesh-root").innerHTML();
  for (const leak of ["http://", "https://", ".test", "credential"]) {
    expect(drawn.includes(leak), leak).toBe(false);
  }
});

// The declaration is a property of the response, not of the build. A payload that
// went on saying "unreachable cannot happen here" beside a target reporting exactly
// that would be a contract nobody could rely on again.
test("a landscape with a peer stops claiming it cannot see", async ({ page }) => {
  installMock(page, peeredGraph);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-legend")).toBeVisible();
  await expect(page.locator(".mesh-legend")).not.toContainText("Not watched here");

  // And the other way round: a landscape with no peer says so, and says what would
  // change it rather than only that it cannot.
  installMock(page, {
    ...peeredGraph,
    nodes: peeredGraph.nodes.filter((n) => n.kind !== "target"),
    status: {
      ok: 1, attention: 0, critical: 0, unknown: 0,
      unavailable: [
        { state: "unreachable", reason: "No deployment target is drawn here. Configure a deployment target and this landscape reports it." },
        { state: "stale", reason: "Only a peer's answer holds a freshness contract: configure a deployment target and this landscape reports it." },
      ],
    },
  });
  await page.reload();
  await expect(page.locator(".mesh-legend")).toContainText("Not watched here");
  await expect(page.locator(".mesh-legend")).toContainText("deployment target");
});
